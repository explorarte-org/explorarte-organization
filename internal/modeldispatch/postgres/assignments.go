package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/jackc/pgx/v5"
)

// sha256Pattern mirrors the CHECK constraint every hash column this file
// reads already enforces (role_model_bindings.binding_hash,
// routing_policies.canonical_hash); re-checked here purely as defense in
// depth against a malformed row, never relied on to catch what the
// database itself already guarantees.
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func insertAssignmentAudit(ctx context.Context, tx pgx.Tx, eventType string, assignment modeldispatch.DispatcherAssignment, actorRoleID string, extra map[string]any) error {
	payload := map[string]any{
		"assignment_id":            assignment.ID,
		"task_id":                  assignment.TaskID,
		"attempt_id":               assignment.AttemptID,
		"subject_role_id":          assignment.SubjectRoleID,
		"dispatch_actor_role_id":   assignment.DispatchActorRoleID,
		"execution_principal_id":   assignment.ExecutionPrincipalID,
		"organization_revision_id": assignment.OrganizationRevisionID,
		"max_invocations":          assignment.MaxInvocations,
		"used_invocations":         assignment.UsedInvocations,
		"status":                   assignment.Status,
		"assignment_hash":          assignment.AssignmentHash,
	}
	for k, v := range extra {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `
INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload)
VALUES($1,'role',$2,'model_dispatcher_assignment',$3,$4::jsonb)`,
		eventType, actorRoleID, fmt.Sprint(assignment.ID), body)
	return mapError(err)
}

func (s *Store) CreateAssignment(ctx context.Context, p modeldispatch.PreparedCreateAssignment) (modeldispatch.CreateAssignmentResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modeldispatch.CreateAssignmentResult, error) {
		row := tx.QueryRow(ctx, `
INSERT INTO model_dispatcher_assignments(
    organization_id,organization_revision_id,task_id,attempt_id,subject_role_id,
    dispatch_actor_role_id,execution_principal_id,status,valid_from,valid_until,
    max_invocations,used_invocations,assignment_hash,idempotency_key,request_hash,created_by_role_id
) VALUES($1,$2,$3,$4,$5,$6,$7,'active',$8,$9,$10,0,$11,$12,$13,$14)
ON CONFLICT(organization_id,idempotency_key) DO NOTHING
RETURNING `+assignmentColumns,
			p.Command.OrganizationID, p.OrganizationRevisionID, p.Command.TaskID, p.Command.AttemptID, p.Command.SubjectRoleID,
			p.Principal.DispatchActorRoleID, p.Principal.ID, p.ValidFrom, p.ValidUntil,
			p.Command.MaxInvocations, p.AssignmentHash, p.Command.IdempotencyKey, p.RequestHash, p.CreatedByRoleID)
		assignment, err := scanAssignment(row)
		if err == nil {
			if auditErr := insertAssignmentAudit(ctx, tx, modeldispatch.AuditAssignmentCreated, assignment, p.CreatedByRoleID, nil); auditErr != nil {
				return modeldispatch.CreateAssignmentResult{}, auditErr
			}
			return modeldispatch.CreateAssignmentResult{Assignment: assignment}, nil
		}
		if !errors.Is(err, modeldispatch.ErrNotFound) {
			return modeldispatch.CreateAssignmentResult{}, err
		}
		assignment, err = scanAssignment(tx.QueryRow(ctx, `SELECT `+assignmentColumns+` FROM model_dispatcher_assignments WHERE organization_id=$1 AND idempotency_key=$2 FOR UPDATE`, p.Command.OrganizationID, p.Command.IdempotencyKey))
		if err != nil {
			return modeldispatch.CreateAssignmentResult{}, err
		}
		if assignment.RequestHash != p.RequestHash {
			return modeldispatch.CreateAssignmentResult{}, fmt.Errorf("%w: idempotency key reused with a different request", modeldispatch.ErrConflict)
		}
		if err = insertAssignmentAudit(ctx, tx, modeldispatch.AuditAssignmentReused, assignment, p.CreatedByRoleID, nil); err != nil {
			return modeldispatch.CreateAssignmentResult{}, err
		}
		return modeldispatch.CreateAssignmentResult{Assignment: assignment, Reused: true}, nil
	})
}

func (s *Store) GetAssignment(ctx context.Context, id int64) (modeldispatch.DispatcherAssignment, error) {
	return scanAssignment(s.pool.QueryRow(ctx, `SELECT `+assignmentColumns+` FROM model_dispatcher_assignments WHERE id=$1`, id))
}

func (s *Store) ListAssignments(ctx context.Context, organizationID string, limit int) ([]modeldispatch.DispatcherAssignment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+assignmentColumns+` FROM model_dispatcher_assignments WHERE organization_id=$1 ORDER BY id DESC LIMIT $2`, organizationID, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := []modeldispatch.DispatcherAssignment{}
	for rows.Next() {
		v, scanErr := scanAssignment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, v)
	}
	return result, mapError(rows.Err())
}

func (s *Store) RevokeAssignment(ctx context.Context, id int64, actorRoleID, reasonCode string) (modeldispatch.DispatcherAssignment, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modeldispatch.DispatcherAssignment, error) {
		existing, err := scanAssignment(tx.QueryRow(ctx, `SELECT `+assignmentColumns+` FROM model_dispatcher_assignments WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return modeldispatch.DispatcherAssignment{}, err
		}
		if existing.Status == modeldispatch.AssignmentRevoked {
			return existing, nil
		}
		if existing.Status != modeldispatch.AssignmentActive {
			return modeldispatch.DispatcherAssignment{}, modeldispatch.ErrAssignmentInactive
		}
		updated, err := scanAssignment(tx.QueryRow(ctx, `
UPDATE model_dispatcher_assignments
SET status='revoked',terminal_at=clock_timestamp(),revoked_at=clock_timestamp(),
    revoked_by_role_id=$2,revocation_reason_code=$3,updated_at=clock_timestamp()
WHERE id=$1 AND status='active'
RETURNING `+assignmentColumns, id, actorRoleID, reasonCode))
		if err != nil {
			return modeldispatch.DispatcherAssignment{}, err
		}
		if err = insertAssignmentAudit(ctx, tx, modeldispatch.AuditAssignmentRevoked, updated, actorRoleID, map[string]any{"reason_code": reasonCode}); err != nil {
			return modeldispatch.DispatcherAssignment{}, err
		}
		return updated, nil
	})
}

func (s *Store) ExpireAssignments(ctx context.Context, organizationID string, batch int, now time.Time) (modeldispatch.ExpireResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modeldispatch.ExpireResult, error) {
		rows, err := tx.Query(ctx, `
UPDATE model_dispatcher_assignments
SET status='expired',terminal_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE id IN (
    SELECT id FROM model_dispatcher_assignments
    WHERE organization_id=$1 AND status='active' AND valid_until<=$2
    ORDER BY id
    LIMIT $3
    FOR UPDATE SKIP LOCKED
)
RETURNING id`, organizationID, now, batch)
		if err != nil {
			return modeldispatch.ExpireResult{}, mapError(err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if scanErr := rows.Scan(&id); scanErr != nil {
				rows.Close()
				return modeldispatch.ExpireResult{}, mapError(scanErr)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err = mapError(rows.Err()); err != nil {
			return modeldispatch.ExpireResult{}, err
		}
		if len(ids) > 0 {
			body, _ := json.Marshal(map[string]any{"organization_id": organizationID, "assignment_ids": ids, "count": len(ids)})
			if _, err = tx.Exec(ctx, `
INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload)
VALUES($1,'system','orgctl','model_dispatcher_assignment',$2,$3::jsonb)`,
				modeldispatch.AuditAssignmentExpired, organizationID, body); err != nil {
				return modeldispatch.ExpireResult{}, mapError(err)
			}
		}
		return modeldispatch.ExpireResult{Expired: len(ids), Inspected: len(ids)}, nil
	})
}

func (s *Store) ResolveActive(ctx context.Context, organizationID string, taskID, attemptID int64, subjectRoleID string) (modeldispatch.ResolvedAssignment, error) {
	assignment, err := scanAssignment(s.pool.QueryRow(ctx, `
SELECT `+assignmentColumns+`
FROM model_dispatcher_assignments
WHERE organization_id=$1 AND task_id=$2 AND attempt_id=$3 AND subject_role_id=$4 AND status='active'
  AND valid_from <= clock_timestamp() AND valid_until > clock_timestamp()`,
		organizationID, taskID, attemptID, subjectRoleID))
	if err != nil {
		return modeldispatch.ResolvedAssignment{}, err
	}
	return s.withPrincipal(ctx, assignment)
}

func (s *Store) GetByID(ctx context.Context, organizationID string, assignmentID int64) (modeldispatch.ResolvedAssignment, error) {
	assignment, err := scanAssignment(s.pool.QueryRow(ctx, `SELECT `+assignmentColumns+` FROM model_dispatcher_assignments WHERE id=$1 AND organization_id=$2`, assignmentID, organizationID))
	if err != nil {
		return modeldispatch.ResolvedAssignment{}, err
	}
	return s.withPrincipal(ctx, assignment)
}

// GetRoleRoutingAuthority derives organization_roles.model_policy for
// (organizationID, revisionID, roleID) and resolves it to exactly one
// authority: a static role_model_bindings row XOR a materialized pool
// routing_policies row (see migration 000070's own comment: a pool-routed
// role has no role_model_bindings row at all). Every check here fails
// closed -- role missing, revision-stale, retired, disabled,
// non-executable, no model_policy, both authorities present, neither
// authority present, a static binding whose own policy_id disagrees with
// the role's model_policy, or a pool policy with zero materialized
// candidates. No candidate/provider/model is ever read: RouteResolver
// alone picks one, per-invocation, at Invocation-creation time inside
// internal/modelruntime.
func (s *Store) GetRoleRoutingAuthority(ctx context.Context, organizationID string, revisionID int64, roleID string) (modeldispatch.RoleRoutingAuthorityRef, error) {
	var (
		modelPolicy                       string
		sourceRevisionID                  int64
		retired, enabled, executable      bool
		hasBinding                        bool
		bindingPolicyID, bindingProfileID string
		bindingModelProfileVersionID      int64
		bindingHash                       string
		hasPool                           bool
		poolCanonicalHash                 string
		hasCandidates                     bool
	)
	err := s.pool.QueryRow(ctx, `
SELECT COALESCE(r.model_policy,''),
       r.source_revision_id,
       r.retired_at IS NOT NULL,
       r.enabled,
       r.executable,
       b.role_id IS NOT NULL,
       COALESCE(b.policy_id,''),
       COALESCE(b.profile_id,''),
       COALESCE(b.model_profile_version_id,0),
       COALESCE(b.binding_hash,''),
       p.policy_id IS NOT NULL,
       COALESCE(p.canonical_hash,''),
       EXISTS (
           SELECT 1 FROM routing_candidates c
           WHERE c.organization_id=r.organization_id
             AND c.organization_revision_id=$2
             AND c.policy_id=r.model_policy
       )
FROM organization_roles r
LEFT JOIN role_model_bindings b
  ON b.organization_id=r.organization_id
 AND b.organization_revision_id=$2
 AND b.role_id=r.id
 AND b.active
LEFT JOIN routing_policies p
  ON p.organization_id=r.organization_id
 AND p.organization_revision_id=$2
 AND p.policy_id=r.model_policy
 AND p.routing_mode='pool'
WHERE r.organization_id=$1 AND r.id=$3`, organizationID, revisionID, roleID).Scan(
		&modelPolicy, &sourceRevisionID, &retired, &enabled, &executable,
		&hasBinding, &bindingPolicyID, &bindingProfileID, &bindingModelProfileVersionID, &bindingHash,
		&hasPool, &poolCanonicalHash, &hasCandidates,
	)
	if err != nil {
		return modeldispatch.RoleRoutingAuthorityRef{}, mapError(err)
	}
	if sourceRevisionID != revisionID {
		return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q organization revision drift: role source_revision_id=%d, want %d", roleID, sourceRevisionID, revisionID)
	}
	if retired {
		return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q is retired", roleID)
	}
	if !enabled {
		return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q is not enabled", roleID)
	}
	if !executable {
		return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q is not executable", roleID)
	}
	if modelPolicy == "" {
		return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q has no model_policy", roleID)
	}
	if hasBinding && hasPool {
		return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q model_policy %q has both a static binding and a pool policy", roleID, modelPolicy)
	}
	switch {
	case hasBinding:
		if bindingPolicyID != modelPolicy {
			return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q static binding policy_id %q does not match model_policy %q", roleID, bindingPolicyID, modelPolicy)
		}
		if bindingModelProfileVersionID <= 0 || !sha256Pattern.MatchString(bindingHash) {
			return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q static binding is malformed", roleID)
		}
		return modeldispatch.RoleRoutingAuthorityRef{
			OrganizationID: organizationID, OrganizationRevisionID: revisionID, RoleID: roleID,
			PolicyID: bindingPolicyID, Kind: modeldispatch.RoleRoutingStaticBinding, AuthorityHash: bindingHash,
			ProfileID: bindingProfileID, ModelProfileVersionID: bindingModelProfileVersionID,
		}, nil
	case hasPool:
		if !hasCandidates {
			return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q pool policy %q has no materialized candidates", roleID, modelPolicy)
		}
		return modeldispatch.RoleRoutingAuthorityRef{
			OrganizationID: organizationID, OrganizationRevisionID: revisionID, RoleID: roleID,
			PolicyID: modelPolicy, Kind: modeldispatch.RoleRoutingPoolPolicy, AuthorityHash: poolCanonicalHash,
		}, nil
	default:
		return modeldispatch.RoleRoutingAuthorityRef{}, fmt.Errorf("role %q model_policy %q has neither a static binding nor a pool policy", roleID, modelPolicy)
	}
}

func (s *Store) withPrincipal(ctx context.Context, assignment modeldispatch.DispatcherAssignment) (modeldispatch.ResolvedAssignment, error) {
	principal, err := s.GetPrincipal(ctx, assignment.ExecutionPrincipalID)
	if err != nil {
		return modeldispatch.ResolvedAssignment{}, err
	}
	return modeldispatch.ResolvedAssignment{Assignment: assignment, Principal: principal}, nil
}
