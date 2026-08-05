package postgres

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Reconcile(ctx context.Context, organizationID string, batch, outboxMax int) (modelruntime.ReconcileResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.ReconcileResult, error) {
		rows, err := tx.Query(ctx, `
SELECT a.id,a.invocation_id
FROM model_dispatch_attempts a
JOIN model_invocations i ON i.id=a.invocation_id
WHERE i.organization_id=$1
  AND a.status IN ('claimed','send_started','response_received')
  AND a.claim_expires_at<=clock_timestamp()
ORDER BY a.claim_expires_at,a.id
LIMIT $2`, organizationID, batch)
		if err != nil {
			return modelruntime.ReconcileResult{}, mapError(err)
		}
		type candidate struct{ attemptID, invocationID int64 }
		candidates := make([]candidate, 0, batch)
		for rows.Next() {
			var value candidate
			if err = rows.Scan(&value.attemptID, &value.invocationID); err != nil {
				rows.Close()
				return modelruntime.ReconcileResult{}, mapError(err)
			}
			candidates = append(candidates, value)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return modelruntime.ReconcileResult{}, mapError(err)
		}
		rows.Close()

		result := modelruntime.ReconcileResult{}
		for _, candidate := range candidates {
			locked, lockErr := tryLockInvocation(ctx, tx, candidate.invocationID)
			if lockErr != nil {
				return result, lockErr
			}
			if !locked {
				continue
			}
			invocation, scanErr := scanInvocation(tx.QueryRow(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE id=$1 FOR UPDATE`, candidate.invocationID))
			if scanErr != nil {
				return result, scanErr
			}
			attempt, scanErr := scanAttempt(tx.QueryRow(ctx, `
SELECT `+attemptColumns+`
FROM model_dispatch_attempts
WHERE id=$1 AND invocation_id=$2
FOR UPDATE`, candidate.attemptID, candidate.invocationID))
			if scanErr != nil {
				return result, scanErr
			}
			var expired bool
			if scanErr = tx.QueryRow(ctx, `SELECT claim_expires_at<=clock_timestamp() FROM model_dispatch_attempts WHERE id=$1`, attempt.ID).Scan(&expired); scanErr != nil {
				return result, mapError(scanErr)
			}
			if !expired {
				continue
			}
			result.Inspected++

			switch attempt.Status {
			case modelruntime.DispatchClaimed:
				if invocation.Status != modelruntime.InvocationClaimed {
					continue
				}
				if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='failed_before_send',retry_safety='safe_before_send',
    outcome_classification='claim_expired_before_send',error_code='claim_expired',
    finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID); err != nil {
					return result, mapError(err)
				}
				invocation, scanErr = scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='requested',error_code=NULL,updated_at=clock_timestamp(),terminal_at=NULL
WHERE id=$1 AND status='claimed'
RETURNING `+invocationColumns, invocation.ID))
				if scanErr != nil {
					return result, scanErr
				}
				if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationReconciled, "system", "orgctl", false, outboxMax, map[string]any{
					"dispatch_attempt_id": attempt.ID,
					"action":              "released_before_send",
				}); err != nil {
					return result, err
				}
				result.ReleasedBeforeSend++

			case modelruntime.DispatchSendStarted:
				if invocation.Status != modelruntime.InvocationSendStarted {
					continue
				}
				if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='ambiguous',retry_safety='not_retryable',
    outcome_classification='ambiguous_external_outcome',
    error_code='claim_expired_after_send',finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID); err != nil {
					return result, mapError(err)
				}
				invocation, scanErr = scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='ambiguous',error_code='claim_expired_after_send',
    updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1 AND status='send_started'
RETURNING `+invocationColumns, invocation.ID))
				if scanErr != nil {
					return result, scanErr
				}
				if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationAmbiguous, "system", "orgctl", true, outboxMax, map[string]any{
					"dispatch_attempt_id":    attempt.ID,
					"outcome_classification": "ambiguous_external_outcome",
				}); err != nil {
					return result, err
				}
				if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationReconciled, "system", "orgctl", false, outboxMax, map[string]any{
					"dispatch_attempt_id": attempt.ID,
					"action":              "marked_ambiguous",
				}); err != nil {
					return result, err
				}
				result.MarkedAmbiguous++

			case modelruntime.DispatchResponseReceived:
				if invocation.Status != modelruntime.InvocationResponseReceived {
					continue
				}
				if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='completed',retry_safety='not_retryable',
    outcome_classification='response_received_result_lost',
    error_code='result_lost_after_response',finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID); err != nil {
					return result, mapError(err)
				}
				invocation, scanErr = scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='failed',error_code='result_lost_after_response',
    updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1 AND status='response_received'
RETURNING `+invocationColumns, invocation.ID))
				if scanErr != nil {
					return result, scanErr
				}
				if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationFailed, "system", "orgctl", true, outboxMax, map[string]any{
					"dispatch_attempt_id":    attempt.ID,
					"outcome_classification": "response_received_result_lost",
				}); err != nil {
					return result, err
				}
				if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationReconciled, "system", "orgctl", false, outboxMax, map[string]any{
					"dispatch_attempt_id": attempt.ID,
					"action":              "failed_after_response",
				}); err != nil {
					return result, err
				}
				result.FailedAfterResponse++
			}
		}
		return result, nil
	})
}
