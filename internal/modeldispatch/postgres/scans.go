package postgres

import (
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

type scanner interface{ Scan(...any) error }

const principalColumns = `id,organization_id,principal_key,dispatch_actor_role_id,principal_kind,status,idempotency_key,request_hash,registered_by_role_id,created_at,updated_at,disabled_at,disabled_by_role_id,disable_reason_code`

func scanPrincipal(row scanner) (modeldispatch.ExecutionPrincipal, error) {
	var v modeldispatch.ExecutionPrincipal
	var disabledByRoleID, disableReasonCode *string
	err := row.Scan(&v.ID, &v.OrganizationID, &v.PrincipalKey, &v.DispatchActorRoleID, &v.PrincipalKind, &v.Status, &v.IdempotencyKey, &v.RequestHash, &v.RegisteredByRoleID, &v.CreatedAt, &v.UpdatedAt, &v.DisabledAt, &disabledByRoleID, &disableReasonCode)
	if err != nil {
		return v, mapError(err)
	}
	if disabledByRoleID != nil {
		v.DisabledByRoleID = *disabledByRoleID
	}
	if disableReasonCode != nil {
		v.DisableReasonCode = *disableReasonCode
	}
	return v, nil
}

const assignmentColumns = `id,organization_id,organization_revision_id,task_id,attempt_id,subject_role_id,dispatch_actor_role_id,execution_principal_id,status,valid_from,valid_until,max_invocations,used_invocations,assignment_hash,idempotency_key,request_hash,created_by_role_id,created_at,updated_at,terminal_at,revoked_at,revoked_by_role_id,revocation_reason_code`

func scanAssignment(row scanner) (modeldispatch.DispatcherAssignment, error) {
	var v modeldispatch.DispatcherAssignment
	var revokedByRoleID, revocationReasonCode *string
	err := row.Scan(&v.ID, &v.OrganizationID, &v.OrganizationRevisionID, &v.TaskID, &v.AttemptID, &v.SubjectRoleID, &v.DispatchActorRoleID, &v.ExecutionPrincipalID, &v.Status, &v.ValidFrom, &v.ValidUntil, &v.MaxInvocations, &v.UsedInvocations, &v.AssignmentHash, &v.IdempotencyKey, &v.RequestHash, &v.CreatedByRoleID, &v.CreatedAt, &v.UpdatedAt, &v.TerminalAt, &v.RevokedAt, &revokedByRoleID, &revocationReasonCode)
	if err != nil {
		return v, mapError(err)
	}
	if revokedByRoleID != nil {
		v.RevokedByRoleID = *revokedByRoleID
	}
	if revocationReasonCode != nil {
		v.RevocationReasonCode = *revocationReasonCode
	}
	return v, nil
}
