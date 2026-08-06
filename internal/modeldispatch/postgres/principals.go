package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/jackc/pgx/v5"
)

func insertPrincipalAudit(ctx context.Context, tx pgx.Tx, eventType string, principal modeldispatch.ExecutionPrincipal, actorRoleID string, extra map[string]any) error {
	payload := map[string]any{
		"principal_id":           principal.ID,
		"principal_key":          principal.PrincipalKey,
		"dispatch_actor_role_id": principal.DispatchActorRoleID,
		"principal_kind":         principal.PrincipalKind,
		"status":                 principal.Status,
	}
	for k, v := range extra {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `
INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload)
VALUES($1,'role',$2,'model_execution_principal',$3,$4::jsonb)`,
		eventType, actorRoleID, fmt.Sprint(principal.ID), body)
	return mapError(err)
}

func (s *Store) RegisterPrincipal(ctx context.Context, p modeldispatch.PreparedRegisterPrincipal) (modeldispatch.RegisterPrincipalResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modeldispatch.RegisterPrincipalResult, error) {
		row := tx.QueryRow(ctx, `
INSERT INTO model_execution_principals(
    organization_id,principal_key,dispatch_actor_role_id,principal_kind,
    status,idempotency_key,request_hash,registered_by_role_id
) VALUES($1,$2,$3,$4,'active',$5,$6,$7)
ON CONFLICT(organization_id,idempotency_key) DO NOTHING
RETURNING `+principalColumns,
			p.Command.OrganizationID, p.Command.PrincipalKey, p.Command.DispatchActorRoleID,
			p.Command.PrincipalKind, p.Command.IdempotencyKey, p.RequestHash, p.RegisteredByRoleID)
		principal, err := scanPrincipal(row)
		if err == nil {
			if auditErr := insertPrincipalAudit(ctx, tx, modeldispatch.AuditPrincipalRegistered, principal, p.RegisteredByRoleID, nil); auditErr != nil {
				return modeldispatch.RegisterPrincipalResult{}, auditErr
			}
			return modeldispatch.RegisterPrincipalResult{Principal: principal}, nil
		}
		if !errors.Is(err, modeldispatch.ErrNotFound) {
			return modeldispatch.RegisterPrincipalResult{}, err
		}
		principal, err = scanPrincipal(tx.QueryRow(ctx, `SELECT `+principalColumns+` FROM model_execution_principals WHERE organization_id=$1 AND idempotency_key=$2 FOR UPDATE`, p.Command.OrganizationID, p.Command.IdempotencyKey))
		if err != nil {
			return modeldispatch.RegisterPrincipalResult{}, err
		}
		if principal.RequestHash != p.RequestHash {
			return modeldispatch.RegisterPrincipalResult{}, fmt.Errorf("%w: idempotency key reused with a different request", modeldispatch.ErrConflict)
		}
		if err = insertPrincipalAudit(ctx, tx, modeldispatch.AuditPrincipalReused, principal, p.RegisteredByRoleID, nil); err != nil {
			return modeldispatch.RegisterPrincipalResult{}, err
		}
		return modeldispatch.RegisterPrincipalResult{Principal: principal, Reused: true}, nil
	})
}

func (s *Store) GetPrincipal(ctx context.Context, id int64) (modeldispatch.ExecutionPrincipal, error) {
	return scanPrincipal(s.pool.QueryRow(ctx, `SELECT `+principalColumns+` FROM model_execution_principals WHERE id=$1`, id))
}

func (s *Store) ListPrincipals(ctx context.Context, organizationID string, limit int) ([]modeldispatch.ExecutionPrincipal, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+principalColumns+` FROM model_execution_principals WHERE organization_id=$1 ORDER BY id DESC LIMIT $2`, organizationID, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := []modeldispatch.ExecutionPrincipal{}
	for rows.Next() {
		v, scanErr := scanPrincipal(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, v)
	}
	return result, mapError(rows.Err())
}

func (s *Store) DisablePrincipal(ctx context.Context, id int64, actorRoleID, reasonCode string) (modeldispatch.ExecutionPrincipal, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modeldispatch.ExecutionPrincipal, error) {
		existing, err := scanPrincipal(tx.QueryRow(ctx, `SELECT `+principalColumns+` FROM model_execution_principals WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return modeldispatch.ExecutionPrincipal{}, err
		}
		if existing.Status == modeldispatch.PrincipalDisabled {
			return existing, nil
		}
		updated, err := scanPrincipal(tx.QueryRow(ctx, `
UPDATE model_execution_principals
SET status='disabled',disabled_at=clock_timestamp(),disabled_by_role_id=$2,disable_reason_code=$3,updated_at=clock_timestamp()
WHERE id=$1 AND status='active'
RETURNING `+principalColumns, id, actorRoleID, reasonCode))
		if err != nil {
			return modeldispatch.ExecutionPrincipal{}, err
		}
		if err = insertPrincipalAudit(ctx, tx, modeldispatch.AuditPrincipalDisabled, updated, actorRoleID, map[string]any{"reason_code": reasonCode}); err != nil {
			return modeldispatch.ExecutionPrincipal{}, err
		}
		return updated, nil
	})
}

func (s *Store) ResolveByKey(ctx context.Context, organizationID, principalKey string) (modeldispatch.ExecutionPrincipal, error) {
	return scanPrincipal(s.pool.QueryRow(ctx, `SELECT `+principalColumns+` FROM model_execution_principals WHERE organization_id=$1 AND principal_key=$2`, organizationID, principalKey))
}
