package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ConsumeApproval(ctx context.Context, command authorization.ConsumeApprovalCommand, now time.Time, validator authorization.ApprovalValidator, outboxMax int) (result authorization.ConsumeApprovalResult, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, mapError(err)
	}
	defer func() {
		if err != nil {
			rollback(tx)
		}
	}()
	request, err := lockRequest(ctx, tx, command.RequestID)
	if err != nil {
		return result, err
	}
	if request.OrganizationID != command.OrganizationID {
		return result, authorization.ErrOrganizationMismatch
	}
	result.Request = request
	if request.Status == authorization.RequestConsumed {
		use, useErr := scanUse(tx.QueryRow(ctx, `SELECT id,request_id,organization_id,consumed_by_role_id,action_digest,correlation_id,causation_id,created_at FROM authorization_uses WHERE request_id=$1`, request.ID))
		if useErr != nil {
			return result, useErr
		}
		if use.ConsumedByRoleID == command.ActorRoleID && use.ActionDigest == command.ActionDigest && commandScopeMatches(request, command) {
			result.Use, result.Reused = &use, true
			if err = tx.Commit(ctx); err != nil {
				return result, mapError(err)
			}
			return result, nil
		}
		result.ReasonCode = authorization.ReasonApprovalConsumed
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return result, nil
	}
	switch request.Status {
	case authorization.RequestPending:
		result.ReasonCode = authorization.ReasonApprovalPending
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return result, nil
	case authorization.RequestRejected:
		result.ReasonCode = authorization.ReasonApprovalRejected
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return result, nil
	case authorization.RequestCancelled:
		result.ReasonCode = authorization.ReasonApprovalCancelled
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return result, nil
	case authorization.RequestExpired:
		result.ReasonCode = authorization.ReasonApprovalExpired
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return result, nil
	case authorization.RequestApproved:
	default:
		return result, authorization.ErrInvalidTransition
	}
	if !now.Before(request.ExpiresAt) {
		request, err = transitionExpired(ctx, tx, request, now, outboxMax)
		if err != nil {
			return result, err
		}
		result.Request, result.ReasonCode = request, authorization.ReasonApprovalExpired
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return result, nil
	}
	organization, err := getOrganizationTx(ctx, tx, request.OrganizationID)
	if err != nil {
		return result, err
	}
	revision, err := getRevisionTx(ctx, tx, request.OrganizationID)
	if err != nil {
		return result, err
	}
	role, roleErr := getRoleTx(ctx, tx, request.OrganizationID, request.RequesterRoleID)
	if roleErr != nil && !errors.Is(roleErr, authorization.ErrRoleNotFound) {
		return result, roleErr
	}
	validation := authorization.ApprovalValidationContext{Request: request, Organization: organization, Revision: *revision, Role: role}
	reason := validator(validation, command)
	if reason != "" {
		eventType := "authorization.scope_mismatch"
		if reason != authorization.ReasonApprovalScopeMismatch {
			eventType = "authorization.policy_drift_denied"
		}
		eventRequest := withEventCorrelation(request, command.CorrelationID, command.CausationID)
		if err = appendEvent(ctx, tx, eventRequest, eventType, authorization.EffectDeny, request.Status, reason, command.ActorRoleID, outboxMax); err != nil {
			return result, err
		}
		result.ReasonCode = reason
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return result, nil
	}
	var use authorization.ApprovalUse
	use, err = scanUse(tx.QueryRow(ctx, `INSERT INTO authorization_uses(request_id,organization_id,consumed_by_role_id,action_digest,correlation_id,causation_id,created_at) VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7) RETURNING id,request_id,organization_id,consumed_by_role_id,action_digest,correlation_id,causation_id,created_at`, request.ID, request.OrganizationID, command.ActorRoleID, command.ActionDigest, command.CorrelationID, command.CausationID, now))
	if err != nil {
		return result, err
	}
	request, err = scanRequest(tx.QueryRow(ctx, `UPDATE authorization_requests SET status='consumed',version=version+1,updated_at=$2,consumed_at=$2 WHERE id=$1 AND status='approved' RETURNING `+requestColumns, request.ID, now))
	if err != nil {
		return result, authorization.ErrInvalidTransition
	}
	eventRequest := withEventCorrelation(request, command.CorrelationID, command.CausationID)
	if err = appendEvent(ctx, tx, eventRequest, "authorization.approval_consumed", authorization.EffectAllow, request.Status, authorization.ReasonAllowedByApproval, command.ActorRoleID, outboxMax); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, mapError(err)
	}
	result.Request, result.Use = request, &use
	return result, nil
}

func (s *Store) CancelRequest(ctx context.Context, command authorization.CancelRequestCommand, now time.Time, outboxMax int) (result authorization.ApprovalRequest, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, mapError(err)
	}
	defer func() {
		if err != nil {
			rollback(tx)
		}
	}()
	request, err := lockRequest(ctx, tx, command.RequestID)
	if err != nil {
		return result, err
	}
	if request.OrganizationID != command.OrganizationID {
		return result, authorization.ErrOrganizationMismatch
	}
	if request.Status == authorization.RequestCancelled {
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return request, nil
	}
	if !authorization.CanTransition(request.Status, authorization.RequestCancelled) {
		return result, authorization.ErrInvalidTransition
	}
	organization, err := getOrganizationTx(ctx, tx, request.OrganizationID)
	if err != nil {
		return result, err
	}
	actor, err := getRoleTx(ctx, tx, request.OrganizationID, command.ActorRoleID)
	if err != nil {
		return result, err
	}
	if actor.RetiredAt != nil || !actor.Enabled || (actor.ID != request.RequesterRoleID && actor.ID != organization.OwnerRoleID) {
		return result, authorization.ErrCapabilityDenied
	}
	request, err = scanRequest(tx.QueryRow(ctx, `UPDATE authorization_requests SET status='cancelled',version=version+1,updated_at=$2,cancelled_at=$2 WHERE id=$1 AND status IN ('pending','approved') RETURNING `+requestColumns, request.ID, now))
	if err != nil {
		return result, authorization.ErrInvalidTransition
	}
	if err = appendEvent(ctx, tx, request, "authorization.request_cancelled", authorization.EffectDeny, request.Status, authorization.ReasonApprovalCancelled, command.ActorRoleID, outboxMax); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, mapError(err)
	}
	return request, nil
}

func (s *Store) ExpireRequests(ctx context.Context, organizationID string, now time.Time, batch int, outboxMax int) (result authorization.ExpireResult, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, mapError(err)
	}
	defer func() {
		if err != nil {
			rollback(tx)
		}
	}()
	rows, err := tx.Query(ctx, `SELECT `+requestColumns+` FROM authorization_requests WHERE organization_id=$1 AND status IN ('pending','approved') AND expires_at <= $2 ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $3`, organizationID, now, batch)
	if err != nil {
		return result, mapError(err)
	}
	var requests []authorization.ApprovalRequest
	for rows.Next() {
		request, scanErr := scanRequest(rows)
		if scanErr != nil {
			rows.Close()
			return result, scanErr
		}
		requests = append(requests, request)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, mapError(err)
	}
	rows.Close()
	for _, request := range requests {
		if _, err = transitionExpired(ctx, tx, request, now, outboxMax); err != nil {
			return result, err
		}
		result.Expired++
	}
	if err = tx.Commit(ctx); err != nil {
		return result, mapError(err)
	}
	return result, nil
}

func commandScopeMatches(request authorization.ApprovalRequest, command authorization.ConsumeApprovalCommand) bool {
	return (command.OrganizationID == "" || command.OrganizationID == request.OrganizationID) &&
		(command.OrganizationRevisionID == 0 || command.OrganizationRevisionID == request.OrganizationRevisionID) &&
		(command.CapabilityMatrixHash == "" || command.CapabilityMatrixHash == request.CapabilityMatrixHash) &&
		(command.CapabilityID == "" || command.CapabilityID == request.CapabilityID) &&
		(command.ResourceType == "" || command.ResourceType == request.ResourceType) &&
		(command.ResourceID == "" || command.ResourceID == request.ResourceID) &&
		(command.ApprovalMode == "" || command.ApprovalMode == request.ApprovalMode)
}
