package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This file owns the single atomic pre-send transaction boundary shared by
// modelegress (authorization/egress evaluation) and modeldispatch (assignment
// consumption and quota), per branch 10's requirement that exactly one
// PostgreSQL implementation writes model_egress_evaluations,
// model_dispatcher_assignment_uses, model_dispatcher_assignments,
// model_dispatch_attempts, model_invocations and audit_events for this
// transition. It supersedes the narrower branch-09 version that used to live
// in internal/modelegress/postgres.

type pinnedInvocationRef struct {
	dispatcherAssignmentID *int64
	executionPrincipalID   *int64
}

func verifyClaim(ctx context.Context, tx pgx.Tx, evaluation modelegress.PreSendEvaluation, rawToken string) (pinnedInvocationRef, error) {
	digest := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(digest[:])
	var ref pinnedInvocationRef
	var invocationStatus, attemptStatus, storedToken string
	var invocationPolicyID *int64
	var policyHash *string
	var attemptInvocationID int64
	var organizationID, dispatchActorRoleID, subjectRoleID, providerID, providerTransport, requestHash string
	var organizationRevisionID, modelProfileVersionID int64
	var capabilityMatrixHash *string
	var claimActive, policyBound, evaluationExists bool
	err := tx.QueryRow(ctx, `
SELECT i.status,a.status,a.claim_token_hash,a.invocation_id,
       i.model_egress_policy_version_id,i.model_egress_policy_hash,
       i.organization_id,i.organization_revision_id,i.dispatch_actor_role_id,
       i.subject_role_id,i.model_profile_version_id,i.provider_id,v.transport,i.request_hash,
       r.document_hashes->>'capability-matrix.yaml',
       i.dispatcher_assignment_id,i.execution_principal_id,
       a.claim_expires_at > clock_timestamp(),
       EXISTS (
           SELECT 1
           FROM model_egress_revision_bindings b
           WHERE b.organization_id=i.organization_id
             AND b.organization_revision_id=i.organization_revision_id
             AND b.policy_version_id=i.model_egress_policy_version_id
             AND b.canonical_hash=i.model_egress_policy_hash
       ),
       EXISTS (
           SELECT 1
           FROM model_egress_evaluations e
           WHERE e.dispatch_attempt_id=a.id
       )
FROM model_invocations i
JOIN model_dispatch_attempts a ON a.id=$2 AND a.invocation_id=i.id
JOIN model_profile_versions v
  ON v.id=i.model_profile_version_id
 AND v.organization_id=i.organization_id
 AND v.profile_id=i.model_profile_id
 AND v.provider_id=i.provider_id
 AND v.provider_model_id=i.provider_model_id
JOIN organization_registry_revisions r ON r.id=i.organization_revision_id
WHERE i.id=$1
FOR UPDATE OF i,a`, evaluation.InvocationID, evaluation.DispatchAttemptID).Scan(
		&invocationStatus, &attemptStatus, &storedToken, &attemptInvocationID,
		&invocationPolicyID, &policyHash, &organizationID, &organizationRevisionID,
		&dispatchActorRoleID, &subjectRoleID, &modelProfileVersionID, &providerID,
		&providerTransport, &requestHash, &capabilityMatrixHash,
		&ref.dispatcherAssignmentID, &ref.executionPrincipalID,
		&claimActive, &policyBound, &evaluationExists,
	)
	if err != nil {
		return ref, mapError(err)
	}
	if invocationStatus != "claimed" || attemptStatus != "claimed" || attemptInvocationID != evaluation.InvocationID || !claimActive || evaluationExists {
		return ref, modelegress.ErrEvaluationConflict
	}
	if storedToken != tokenHash {
		return ref, modelegress.ErrClaimMismatch
	}
	if invocationPolicyID == nil || policyHash == nil || *invocationPolicyID != evaluation.PolicyVersionID || *policyHash != evaluation.PolicyHash || !policyBound {
		return ref, modelegress.ErrPolicyUnpinned
	}
	if ref.dispatcherAssignmentID == nil || ref.executionPrincipalID == nil {
		return ref, modelruntime.ErrDispatcherAssignmentUnpinned
	}
	expectedActionDigest, digestErr := modelruntime.ActionDigest(modelruntime.Invocation{
		ID: evaluation.InvocationID, RequestHash: requestHash,
		DispatcherAssignmentID: ref.dispatcherAssignmentID, ExecutionPrincipalID: ref.executionPrincipalID,
		ModelEgressPolicyVersionID: &evaluation.PolicyVersionID, ModelEgressPolicyHash: evaluation.PolicyHash,
	})
	if digestErr != nil || expectedActionDigest != evaluation.ActionDigest {
		return ref, modelegress.ErrEvaluationConflict
	}
	if organizationID != evaluation.OrganizationID || organizationRevisionID != evaluation.OrganizationRevisionID ||
		dispatchActorRoleID != evaluation.DispatchActorRoleID || subjectRoleID != evaluation.SubjectRoleID ||
		modelProfileVersionID != evaluation.ModelProfileVersionID || providerID != evaluation.ProviderID ||
		providerTransport != evaluation.ProviderTransport || capabilityMatrixHash == nil || *capabilityMatrixHash != evaluation.CapabilityMatrixHash {
		return ref, modelegress.ErrEvaluationConflict
	}
	return ref, nil
}

func insertEvaluation(ctx context.Context, tx pgx.Tx, evaluation modelegress.PreSendEvaluation) error {
	if err := modelegress.ValidatePreSendEvaluation(evaluation); err != nil {
		return err
	}
	classifications, classHash := modelegress.NormalizeClassifications(evaluation.ContextClassifications)
	reasons := modelegress.NormalizeReasonCodes(evaluation.EgressReasonCodes)
	if evaluation.ContextClassificationsHash != classHash {
		return modelegress.ErrEvaluationConflict
	}
	if evaluation.DecisionHash == "" || evaluation.DecisionHash != modelegress.DecisionHash(evaluation) {
		return modelegress.ErrEvaluationConflict
	}
	classJSON, _ := json.Marshal(classifications)
	reasonJSON, _ := json.Marshal(reasons)
	_, err := tx.Exec(ctx, `
INSERT INTO model_egress_evaluations(
    invocation_id,dispatch_attempt_id,policy_version_id,organization_id,
    organization_revision_id,model_profile_version_id,provider_id,provider_transport,
    action_digest,capability_matrix_hash,context_classifications,
    context_classifications_hash,authorization_effect,authorization_reason_code,
    egress_effect,egress_reason_codes,decision_hash
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14,$15,$16::jsonb,$17)`,
		evaluation.InvocationID, evaluation.DispatchAttemptID, evaluation.PolicyVersionID,
		evaluation.OrganizationID, evaluation.OrganizationRevisionID,
		evaluation.ModelProfileVersionID, evaluation.ProviderID, evaluation.ProviderTransport,
		evaluation.ActionDigest, evaluation.CapabilityMatrixHash, classJSON, classHash,
		evaluation.AuthorizationEffect, evaluation.AuthorizationReasonCode,
		evaluation.EgressEffect, reasonJSON, evaluation.DecisionHash)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return modelegress.ErrEvaluationConflict
		}
	}
	return mapError(err)
}

func requireOneRow(tag pgconn.CommandTag, conflict error) error {
	if tag.RowsAffected() != 1 {
		return conflict
	}
	return nil
}

func auditPayload(evaluation modelegress.PreSendEvaluation) []byte {
	classes, _ := modelegress.NormalizeClassifications(evaluation.ContextClassifications)
	reasons := modelegress.NormalizeReasonCodes(evaluation.EgressReasonCodes)
	payload, _ := json.Marshal(map[string]any{
		"invocation_id":                evaluation.InvocationID,
		"dispatch_attempt_id":          evaluation.DispatchAttemptID,
		"organization_revision_id":     evaluation.OrganizationRevisionID,
		"dispatch_actor_role_id":       evaluation.DispatchActorRoleID,
		"subject_role_id":              evaluation.SubjectRoleID,
		"model_profile_version_id":     evaluation.ModelProfileVersionID,
		"policy_version_id":            evaluation.PolicyVersionID,
		"provider_id":                  evaluation.ProviderID,
		"provider_transport":           evaluation.ProviderTransport,
		"context_classifications":      classes,
		"context_classifications_hash": evaluation.ContextClassificationsHash,
		"capability_matrix_hash":       evaluation.CapabilityMatrixHash,
		"authorization_effect":         evaluation.AuthorizationEffect,
		"authorization_reason_code":    evaluation.AuthorizationReasonCode,
		"egress_effect":                evaluation.EgressEffect,
		"egress_reason_codes":          reasons,
		"action_digest":                evaluation.ActionDigest,
		"decision_hash":                evaluation.DecisionHash,
	})
	return payload
}

func insertAudit(ctx context.Context, tx pgx.Tx, eventType string, evaluation modelegress.PreSendEvaluation) error {
	_, err := tx.Exec(ctx, `
INSERT INTO audit_events(
    event_type,actor_type,actor_id,subject_type,subject_id,
    correlation_id,causation_id,payload
) VALUES($1,'service',$2,'model_invocation',$3,NULLIF($4,''),NULLIF($5,''),$6::jsonb)`,
		eventType, evaluation.DispatchActorRoleID, fmt.Sprint(evaluation.InvocationID),
		evaluation.CorrelationID, evaluation.CausationID, auditPayload(evaluation))
	return mapError(err)
}

// lockAssignmentForConsume locks and returns the current quota state of the
// assignment pinned to this invocation. It is called only from the allow
// path: a deny never touches the assignment.
func lockAssignmentForConsume(ctx context.Context, tx pgx.Tx, organizationID string, assignmentID int64) (status string, maxInvocations, usedInvocations int, assignmentHash string, err error) {
	err = tx.QueryRow(ctx, `
SELECT status,max_invocations,used_invocations,assignment_hash
FROM model_dispatcher_assignments
WHERE id=$1 AND organization_id=$2
FOR UPDATE`, assignmentID, organizationID).Scan(&status, &maxInvocations, &usedInvocations, &assignmentHash)
	return status, maxInvocations, usedInvocations, assignmentHash, mapError(err)
}

func insertAssignmentConsumedAudit(ctx context.Context, tx pgx.Tx, eventType string, assignmentID, invocationID, dispatchAttemptID, executionPrincipalID int64, usedInvocations, maxInvocations int, status string) error {
	payload, _ := json.Marshal(map[string]any{
		"assignment_id":          assignmentID,
		"invocation_id":          invocationID,
		"dispatch_attempt_id":    dispatchAttemptID,
		"execution_principal_id": executionPrincipalID,
		"used_invocations":       usedInvocations,
		"max_invocations":        maxInvocations,
		"status":                 status,
	})
	_, err := tx.Exec(ctx, `
INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload)
VALUES($1,'system','orgd',$2,$3,$4::jsonb)`,
		eventType, "model_dispatcher_assignment", fmt.Sprint(assignmentID), payload)
	return mapError(err)
}

func (s *Store) PersistPreSendAllowAndMarkSendStarted(ctx context.Context, command modelegress.PersistAllowCommand) error {
	if err := modelegress.ValidatePersistAllowCommand(command); err != nil {
		return err
	}
	_, err := withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (struct{}, error) {
		ref, err := verifyClaim(ctx, tx, command.Evaluation, command.ClaimToken)
		if err != nil {
			return struct{}{}, err
		}
		status, maxInvocations, usedInvocations, assignmentHash, err := lockAssignmentForConsume(ctx, tx, command.Evaluation.OrganizationID, *ref.dispatcherAssignmentID)
		if err != nil {
			return struct{}{}, err
		}
		if status != string(modeldispatch.AssignmentActive) {
			return struct{}{}, modeldispatch.ErrAssignmentInactive
		}
		if usedInvocations >= maxInvocations {
			return struct{}{}, modeldispatch.ErrAssignmentExhausted
		}
		if err := insertEvaluation(ctx, tx, command.Evaluation); err != nil {
			return struct{}{}, err
		}
		if err := insertAudit(ctx, tx, modelegress.AuditInvocationAuthorized, command.Evaluation); err != nil {
			return struct{}{}, err
		}
		if err := insertAudit(ctx, tx, modelegress.AuditEgressAllowed, command.Evaluation); err != nil {
			return struct{}{}, err
		}

		usedAt := time.Now().UTC()
		usageHash, hashErr := modeldispatch.UsageHash(*ref.dispatcherAssignmentID, assignmentHash, command.Evaluation.InvocationID, command.Evaluation.DispatchAttemptID, *ref.executionPrincipalID, usedAt)
		if hashErr != nil {
			return struct{}{}, hashErr
		}
		if _, err = tx.Exec(ctx, `
INSERT INTO model_dispatcher_assignment_uses(
    organization_id,assignment_id,invocation_id,dispatch_attempt_id,execution_principal_id,used_at,usage_hash
) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			command.Evaluation.OrganizationID, *ref.dispatcherAssignmentID, command.Evaluation.InvocationID,
			command.Evaluation.DispatchAttemptID, *ref.executionPrincipalID, usedAt, usageHash); err != nil {
			return struct{}{}, mapError(err)
		}
		newUsed := usedInvocations + 1
		newStatus := string(modeldispatch.AssignmentActive)
		if newUsed >= maxInvocations {
			newStatus = string(modeldispatch.AssignmentExhausted)
		}
		tag, err := tx.Exec(ctx, `
UPDATE model_dispatcher_assignments
SET used_invocations=$2,
    status=$3,
    terminal_at=CASE WHEN $3='exhausted' THEN clock_timestamp() ELSE terminal_at END,
    updated_at=clock_timestamp()
WHERE id=$1 AND status='active' AND used_invocations=$4`,
			*ref.dispatcherAssignmentID, newUsed, newStatus, usedInvocations)
		if err != nil {
			return struct{}{}, mapError(err)
		}
		if err = requireOneRow(tag, modeldispatch.ErrAssignmentConflict); err != nil {
			return struct{}{}, err
		}
		if err = insertAssignmentConsumedAudit(ctx, tx, modeldispatch.AuditAssignmentConsumed, *ref.dispatcherAssignmentID, command.Evaluation.InvocationID, command.Evaluation.DispatchAttemptID, *ref.executionPrincipalID, newUsed, maxInvocations, newStatus); err != nil {
			return struct{}{}, err
		}
		if newStatus == string(modeldispatch.AssignmentExhausted) {
			if err = insertAssignmentConsumedAudit(ctx, tx, modeldispatch.AuditAssignmentExhausted, *ref.dispatcherAssignmentID, command.Evaluation.InvocationID, command.Evaluation.DispatchAttemptID, *ref.executionPrincipalID, newUsed, maxInvocations, newStatus); err != nil {
				return struct{}{}, err
			}
		}

		tag, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='send_started',send_started_at=clock_timestamp(),retry_safety='unsafe_after_send',
    provider_idempotency_key_hash=$2,claim_expires_at=GREATEST(claim_expires_at,$3)
WHERE id=$1 AND status='claimed'`, command.Evaluation.DispatchAttemptID, command.ProviderIdempotencyKeyHash, command.Deadline)
		if err != nil {
			return struct{}{}, mapError(err)
		}
		if err = requireOneRow(tag, modelegress.ErrEvaluationConflict); err != nil {
			return struct{}{}, err
		}
		tag, err = tx.Exec(ctx, `
UPDATE model_invocations
SET status='send_started',updated_at=clock_timestamp()
WHERE id=$1 AND status='claimed' AND cancel_requested_at IS NULL`, command.Evaluation.InvocationID)
		if err != nil {
			return struct{}{}, mapError(err)
		}
		if err = requireOneRow(tag, modelegress.ErrEvaluationConflict); err != nil {
			return struct{}{}, err
		}
		if err := insertAudit(ctx, tx, "model.invocation_dispatched", command.Evaluation); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) PersistPreSendDenyAndFail(ctx context.Context, command modelegress.PersistDenyCommand) error {
	if err := modelegress.ValidatePersistFailureCommand(command); err != nil {
		return err
	}
	_, err := withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (struct{}, error) {
		if _, err := verifyClaim(ctx, tx, command.Evaluation, command.ClaimToken); err != nil {
			return struct{}{}, err
		}
		if err := insertEvaluation(ctx, tx, command.Evaluation); err != nil {
			return struct{}{}, err
		}
		if command.Evaluation.AuthorizationEffect == modelegress.AuthorizationAllow {
			if err := insertAudit(ctx, tx, modelegress.AuditInvocationAuthorized, command.Evaluation); err != nil {
				return struct{}{}, err
			}
			eventType := modelegress.AuditEgressDenied
			if command.Evaluation.EgressEffect == modelegress.EffectError {
				eventType = modelegress.AuditEgressError
			}
			if err := insertAudit(ctx, tx, eventType, command.Evaluation); err != nil {
				return struct{}{}, err
			}
		} else {
			eventType := modelegress.AuditInvocationAuthorizationDenied
			if command.Evaluation.AuthorizationEffect == modelegress.AuthorizationError {
				eventType = modelegress.AuditInvocationAuthorizationError
			}
			if err := insertAudit(ctx, tx, eventType, command.Evaluation); err != nil {
				return struct{}{}, err
			}
		}
		tag, err := tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='failed_before_send',retry_safety='not_retryable',
    outcome_classification='failed_before_send',error_code=$2,finished_at=clock_timestamp()
WHERE id=$1 AND status='claimed'`, command.Evaluation.DispatchAttemptID, command.ErrorCode)
		if err != nil {
			return struct{}{}, mapError(err)
		}
		if err = requireOneRow(tag, modelegress.ErrEvaluationConflict); err != nil {
			return struct{}{}, err
		}
		tag, err = tx.Exec(ctx, `
UPDATE model_invocations
SET status='failed',error_code=$2,updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1 AND status='claimed'`, command.Evaluation.InvocationID, command.ErrorCode)
		if err != nil {
			return struct{}{}, mapError(err)
		}
		if err = requireOneRow(tag, modelegress.ErrEvaluationConflict); err != nil {
			return struct{}{}, err
		}
		if err := insertAudit(ctx, tx, "model.invocation_failed", command.Evaluation); err != nil {
			return struct{}{}, err
		}
		minimal, _ := json.Marshal(map[string]any{
			"schema_version": 1,
			"invocation_id":  command.Evaluation.InvocationID,
			"event_type":     "model.invocation_failed",
			"status":         "failed",
			"error_code":     command.ErrorCode,
		})
		if _, err := tx.Exec(ctx, `
INSERT INTO outbox_events(
    aggregate_type,aggregate_id,event_type,schema_version,payload,max_attempts
) VALUES('model_invocation',$1,'model.invocation_failed',1,$2::jsonb,$3)`,
			fmt.Sprint(command.Evaluation.InvocationID), minimal, command.OutboxMaxAttempts); err != nil {
			return struct{}{}, mapError(err)
		}
		return struct{}{}, nil
	})
	return err
}
