package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/jackc/pgx/v5"
)

const requestColumns = `id,organization_id,organization_revision_id,capability_matrix_hash,requester_role_id,capability_id,risk,approval_mode,resource_type,resource_id,action_digest,idempotency_key,request_hash,status,reason,version,correlation_id,causation_id,created_at,updated_at,expires_at,approved_at,rejected_at,cancelled_at,expired_at,consumed_at`

func scanRequest(row scanner) (authorization.ApprovalRequest, error) {
	var value authorization.ApprovalRequest
	err := row.Scan(&value.ID, &value.OrganizationID, &value.OrganizationRevisionID, &value.CapabilityMatrixHash, &value.RequesterRoleID, &value.CapabilityID, &value.Risk, &value.ApprovalMode, &value.ResourceType, &value.ResourceID, &value.ActionDigest, &value.IdempotencyKey, &value.RequestHash, &value.Status, &value.Reason, &value.Version, &value.CorrelationID, &value.CausationID, &value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt, &value.ApprovedAt, &value.RejectedAt, &value.CancelledAt, &value.ExpiredAt, &value.ConsumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return authorization.ApprovalRequest{}, authorization.ErrRequestNotFound
	}
	return value, mapError(err)
}

func scanUse(row scanner) (authorization.ApprovalUse, error) {
	var value authorization.ApprovalUse
	err := row.Scan(&value.ID, &value.RequestID, &value.OrganizationID, &value.ConsumedByRoleID, &value.ActionDigest, &value.CorrelationID, &value.CausationID, &value.CreatedAt)
	return value, mapError(err)
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func appendEvent(ctx context.Context, tx pgx.Tx, request authorization.ApprovalRequest, eventType string, effect authorization.Effect, status authorization.RequestStatus, reason authorization.ReasonCode, actorRoleID string, outboxMax int) error {
	outbox := map[string]any{
		"schema_version":           1,
		"authorization_request_id": request.ID,
		"organization_id":          request.OrganizationID,
		"capability_id":            request.CapabilityID,
		"resource_type":            request.ResourceType,
		"resource_id":              request.ResourceID,
	}
	if effect != "" {
		outbox["effect"] = effect
	}
	if status != "" {
		outbox["status"] = status
	}
	if reason != "" {
		outbox["reason_code"] = reason
	}
	if actorRoleID != "" {
		outbox["actor_role_id"] = actorRoleID
	}
	outboxJSON, err := json.Marshal(outbox)
	if err != nil {
		return fmt.Errorf("encode authorization outbox: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,schema_version,payload,max_attempts) VALUES('authorization_request',$1,$2,1,$3::jsonb,$4)`, strconv.FormatInt(request.ID, 10), eventType, outboxJSON, outboxMax); err != nil {
		return mapError(err)
	}
	audit := map[string]any{
		"schema_version":           1,
		"authorization_request_id": request.ID,
		"organization_id":          request.OrganizationID,
		"organization_revision_id": request.OrganizationRevisionID,
		"capability_matrix_hash":   request.CapabilityMatrixHash,
		"requester_role_id":        request.RequesterRoleID,
		"capability_id":            request.CapabilityID,
		"approval_mode":            request.ApprovalMode,
		"resource_type":            request.ResourceType,
		"resource_id":              request.ResourceID,
		"action_digest":            request.ActionDigest,
	}
	if effect != "" {
		audit["effect"] = effect
	}
	if status != "" {
		audit["status"] = status
	}
	if reason != "" {
		audit["reason_code"] = reason
	}
	auditJSON, err := json.Marshal(audit)
	if err != nil {
		return fmt.Errorf("encode authorization audit: %w", err)
	}
	actorType := "system"
	actorID := "orgd"
	if actorRoleID != "" {
		actorType, actorID = "role", actorRoleID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,correlation_id,causation_id,payload) VALUES($1,$2,$3,'authorization_request',$4,$5,$6,$7::jsonb)`, eventType, actorType, actorID, strconv.FormatInt(request.ID, 10), request.CorrelationID, request.CausationID, auditJSON); err != nil {
		return mapError(err)
	}
	return nil
}

func getOrganizationTx(ctx context.Context, tx pgx.Tx, id string) (registry.Organization, error) {
	var value registry.Organization
	err := tx.QueryRow(ctx, `SELECT id,display_name,owner_role_id,ceo_role_id,runtime_target,state_store_target,cells_are_external,schema_version,document_status,COALESCE(current_revision_id,0),created_at,updated_at,retired_at FROM organizations WHERE id=$1 FOR SHARE`, id).Scan(
		&value.ID, &value.DisplayName, &value.OwnerRoleID, &value.CEORoleID, &value.RuntimeTarget, &value.StateStoreTarget, &value.CellsAreExternal, &value.SchemaVersion, &value.DocumentStatus, &value.CurrentRevision, &value.CreatedAt, &value.UpdatedAt, &value.RetiredAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return registry.Organization{}, registry.ErrNotFound
	}
	return value, mapError(err)
}

func getRoleTx(ctx context.Context, tx pgx.Tx, organizationID, roleID string) (registry.Role, error) {
	return scanRole(tx.QueryRow(ctx, `SELECT `+authorizationRoleColumns+` FROM organization_roles WHERE organization_id=$1 AND id=$2 FOR SHARE`, organizationID, roleID))
}

func getRevisionTx(ctx context.Context, tx pgx.Tx, organizationID string) (*registry.Revision, error) {
	return getCurrentRevision(ctx, tx, organizationID)
}

func lockRequest(ctx context.Context, tx pgx.Tx, id int64) (authorization.ApprovalRequest, error) {
	return scanRequest(tx.QueryRow(ctx, `SELECT `+requestColumns+` FROM authorization_requests WHERE id=$1 FOR UPDATE`, id))
}

func eventExists(ctx context.Context, tx pgx.Tx, requestID int64, eventType string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE subject_type='authorization_request' AND subject_id=$1 AND event_type=$2)`, strconv.FormatInt(requestID, 10), eventType).Scan(&exists)
	return exists, mapError(err)
}

func withEventCorrelation(request authorization.ApprovalRequest, correlationID, causationID string) authorization.ApprovalRequest {
	if value := strings.TrimSpace(correlationID); value != "" {
		request.CorrelationID = &value
	}
	if value := strings.TrimSpace(causationID); value != "" {
		request.CausationID = &value
	}
	return request
}

func authorizationErrorForReason(reason authorization.ReasonCode) error {
	switch reason {
	case authorization.ReasonHardDeny:
		return fmt.Errorf("%w: %s", authorization.ErrCapabilityDenied, reason)
	case authorization.ReasonRoleNotFound:
		return authorization.ErrRoleNotFound
	case authorization.ReasonRoleDisabled:
		return authorization.ErrRoleDisabled
	case authorization.ReasonRoleRetired:
		return authorization.ErrRoleRetired
	case authorization.ReasonRoleNotExecutable:
		return authorization.ErrRoleNotExecutable
	case authorization.ReasonUnknownAuthorityClass:
		return authorization.ErrUnknownAuthorityClass
	default:
		return authorization.ErrApprovalPolicyDrift
	}
}
