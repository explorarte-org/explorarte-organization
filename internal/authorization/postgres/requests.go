package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateRequest(ctx context.Context, request authorization.ApprovalRequest, outboxMax int) (result authorization.RequestApprovalResult, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, mapError(err)
	}
	defer func() {
		if err != nil {
			rollback(tx)
		}
	}()
	organization, err := getOrganizationTx(ctx, tx, request.OrganizationID)
	if err != nil {
		return result, err
	}
	if organization.RetiredAt != nil || organization.CurrentRevision != request.OrganizationRevisionID {
		return result, authorization.ErrPolicyRevisionMismatch
	}
	revision, err := getRevisionTx(ctx, tx, request.OrganizationID)
	if err != nil {
		return result, err
	}
	if revision.ID != request.OrganizationRevisionID || revision.DocumentHashes["capability-matrix.yaml"] != request.CapabilityMatrixHash {
		return result, authorization.ErrPolicyRevisionMismatch
	}
	role, err := getRoleTx(ctx, tx, request.OrganizationID, request.RequesterRoleID)
	if err != nil {
		return result, err
	}
	if role.RetiredAt != nil {
		return result, authorization.ErrRoleRetired
	}
	if !role.Enabled {
		return result, authorization.ErrRoleDisabled
	}
	if !role.Executable && role.ID != organization.OwnerRoleID {
		return result, authorization.ErrRoleNotExecutable
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO authorization_requests(
			organization_id,organization_revision_id,capability_matrix_hash,requester_role_id,
			capability_id,risk,approval_mode,resource_type,resource_id,action_digest,
			idempotency_key,request_hash,status,reason,version,correlation_id,causation_id,
			created_at,updated_at,expires_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending',$13,1,$14,$15,$16,$16,$17)
		ON CONFLICT (organization_id,idempotency_key) DO NOTHING
		RETURNING `+requestColumns,
		request.OrganizationID, request.OrganizationRevisionID, request.CapabilityMatrixHash, request.RequesterRoleID,
		request.CapabilityID, request.Risk, request.ApprovalMode, request.ResourceType, request.ResourceID, request.ActionDigest,
		request.IdempotencyKey, request.RequestHash, request.Reason, request.CorrelationID, request.CausationID,
		request.CreatedAt, request.ExpiresAt,
	)
	created, scanErr := scanRequest(row)
	if scanErr == nil {
		if err = appendEvent(ctx, tx, created, "authorization.request_created", authorization.EffectApprovalRequired, created.Status, authorization.ReasonApprovalPending, created.RequesterRoleID, outboxMax); err != nil {
			return result, err
		}
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return authorization.RequestApprovalResult{Request: created}, nil
	}
	if !errors.Is(scanErr, authorization.ErrRequestNotFound) {
		return result, scanErr
	}
	existing, err := scanRequest(tx.QueryRow(ctx, `SELECT `+requestColumns+` FROM authorization_requests WHERE organization_id=$1 AND idempotency_key=$2 FOR UPDATE`, request.OrganizationID, request.IdempotencyKey))
	if err != nil {
		return result, err
	}
	if existing.RequestHash != request.RequestHash {
		return result, authorization.ErrIdempotencyConflict
	}
	already, err := eventExists(ctx, tx, existing.ID, "authorization.request_reused")
	if err != nil {
		return result, err
	}
	if !already {
		if err = appendEvent(ctx, tx, existing, "authorization.request_reused", authorization.EffectApprovalRequired, existing.Status, authorization.ReasonApprovalPending, existing.RequesterRoleID, outboxMax); err != nil {
			return result, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return result, mapError(err)
	}
	return authorization.RequestApprovalResult{Request: existing, Reused: true}, nil
}

func (s *Store) GetRequest(ctx context.Context, id int64) (authorization.ApprovalRequest, error) {
	return scanRequest(s.pool.QueryRow(ctx, `SELECT `+requestColumns+` FROM authorization_requests WHERE id=$1`, id))
}

func (s *Store) ListRequests(ctx context.Context, filter authorization.ListRequestsFilter) ([]authorization.ApprovalRequest, error) {
	clauses := []string{"organization_id=$1"}
	args := []any{filter.OrganizationID}
	if filter.Status != "" {
		args = append(args, filter.Status)
		clauses = append(clauses, fmt.Sprintf("status=$%d", len(args)))
	}
	if strings.TrimSpace(filter.RequesterRoleID) != "" {
		args = append(args, strings.TrimSpace(filter.RequesterRoleID))
		clauses = append(clauses, fmt.Sprintf("requester_role_id=$%d", len(args)))
	}
	if strings.TrimSpace(filter.CapabilityID) != "" {
		args = append(args, strings.TrimSpace(filter.CapabilityID))
		clauses = append(clauses, fmt.Sprintf("capability_id=$%d", len(args)))
	}
	args = append(args, filter.Limit)
	query := `SELECT ` + requestColumns + ` FROM authorization_requests WHERE ` + strings.Join(clauses, " AND ") + fmt.Sprintf(" ORDER BY created_at DESC,id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var values []authorization.ApprovalRequest
	for rows.Next() {
		value, scanErr := scanRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func (s *Store) DecideRequest(ctx context.Context, command authorization.DecideRequestCommand, now time.Time, validator authorization.DecisionValidator, outboxMax int) (result authorization.ApprovalRequest, err error) {
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
	if request.Status != authorization.RequestPending {
		return result, authorization.ErrInvalidTransition
	}
	if !now.Before(request.ExpiresAt) {
		request, err = transitionExpired(ctx, tx, request, now, outboxMax)
		if err != nil {
			return result, err
		}
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return request, authorization.ErrApprovalExpired
	}
	organization, err := getOrganizationTx(ctx, tx, request.OrganizationID)
	if err != nil {
		return result, err
	}
	actor, err := getRoleTx(ctx, tx, request.OrganizationID, command.ActorRoleID)
	if err != nil {
		return result, err
	}
	if actor.ID != organization.OwnerRoleID || actor.RetiredAt != nil || !actor.Enabled {
		return result, authorization.ErrApproverNotOwner
	}
	if command.Decision == authorization.DecisionApprove {
		revision, revisionErr := getRevisionTx(ctx, tx, request.OrganizationID)
		if revisionErr != nil {
			return result, revisionErr
		}
		requester, roleErr := getRoleTx(ctx, tx, request.OrganizationID, request.RequesterRoleID)
		if roleErr != nil && !errors.Is(roleErr, authorization.ErrRoleNotFound) {
			return result, roleErr
		}
		reason := validator(authorization.ApprovalValidationContext{Request: request, Organization: organization, Revision: *revision, Role: requester})
		if reason != "" {
			if err = appendEvent(ctx, tx, request, "authorization.policy_drift_denied", authorization.EffectDeny, request.Status, reason, command.ActorRoleID, outboxMax); err != nil {
				return result, err
			}
			if err = tx.Commit(ctx); err != nil {
				return result, mapError(err)
			}
			return request, authorizationErrorForReason(reason)
		}
	}
	status := authorization.RequestApproved
	eventType := "authorization.decision_approved"
	reasonCode := authorization.ReasonAllowedByApproval
	if command.Decision == authorization.DecisionReject {
		status, eventType, reasonCode = authorization.RequestRejected, "authorization.decision_rejected", authorization.ReasonApprovalRejected
	}
	if _, err = tx.Exec(ctx, `INSERT INTO authorization_decisions(request_id,organization_id,decision,decided_by_role_id,reason,reference,digest,created_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8)`, request.ID, request.OrganizationID, command.Decision, command.ActorRoleID, strings.TrimSpace(command.Reason), strings.TrimSpace(command.Reference), strings.TrimSpace(command.Digest), now); err != nil {
		return result, mapError(err)
	}
	request, err = scanRequest(tx.QueryRow(ctx, `UPDATE authorization_requests SET status=$2,version=version+1,updated_at=$3,approved_at=CASE WHEN $2='approved' THEN $3 ELSE NULL END,rejected_at=CASE WHEN $2='rejected' THEN $3 ELSE NULL END WHERE id=$1 AND status='pending' RETURNING `+requestColumns, request.ID, status, now))
	if err != nil {
		return result, authorization.ErrInvalidTransition
	}
	if err = appendEvent(ctx, tx, request, eventType, map[authorization.RequestStatus]authorization.Effect{authorization.RequestApproved: authorization.EffectAllow, authorization.RequestRejected: authorization.EffectDeny}[status], status, reasonCode, command.ActorRoleID, outboxMax); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, mapError(err)
	}
	return request, nil
}

func transitionExpired(ctx context.Context, tx pgx.Tx, request authorization.ApprovalRequest, now time.Time, outboxMax int) (authorization.ApprovalRequest, error) {
	if !authorization.CanTransition(request.Status, authorization.RequestExpired) {
		return authorization.ApprovalRequest{}, authorization.ErrInvalidTransition
	}
	updated, err := scanRequest(tx.QueryRow(ctx, `UPDATE authorization_requests SET status='expired',version=version+1,updated_at=$2,expired_at=$2 WHERE id=$1 AND status=$3 RETURNING `+requestColumns, request.ID, now, request.Status))
	if err != nil {
		return authorization.ApprovalRequest{}, authorization.ErrInvalidTransition
	}
	if err = appendEvent(ctx, tx, updated, "authorization.request_expired", authorization.EffectDeny, updated.Status, authorization.ReasonApprovalExpired, "", outboxMax); err != nil {
		return authorization.ApprovalRequest{}, err
	}
	return updated, nil
}
