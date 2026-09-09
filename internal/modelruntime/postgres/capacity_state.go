package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelrouting"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

// CapacityState implements modelruntime.CapacityStateReader against the
// durable model_routing_capacity_state projection (migration 000072). A
// candidate this (organization, provider, model) has never produced a
// ProviderOutcome for has no row at all -- missing means eligible, the
// same zero-value modelrouting.CandidateState AlwaysAvailableCapacityState
// always returns. A cooldown_until in the past can still be present here;
// this reader does not interpret it -- the selector (which already
// receives "now") decides whether it is still active.
func (s *Store) CapacityState(ctx context.Context, organizationID, providerID, providerModelID string) (modelrouting.CandidateState, error) {
	var out modelrouting.CandidateState
	err := s.pool.QueryRow(ctx, `
SELECT disabled, quota_exhausted, cooldown_until
FROM model_routing_capacity_state
WHERE organization_id=$1 AND provider_id=$2 AND provider_model_id=$3`,
		organizationID, providerID, providerModelID,
	).Scan(&out.Disabled, &out.QuotaExhausted, &out.CooldownUntil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return modelrouting.CandidateState{}, nil
		}
		return modelrouting.CandidateState{}, mapError(err)
	}
	return out, nil
}

var _ modelruntime.CapacityStateReader = (*Store)(nil)

// applyCapacityFeedback projects one already-classified ProviderOutcome's
// capacity signal onto model_routing_capacity_state, inside the SAME
// transaction insertProviderOutcome just used to persist that outcome
// (Section 5 of Model Capacity State V1) -- callers below pass the tx that
// is still open, never a fresh one.
//
// The projection is a single guarded UPSERT. last_provider_outcome_id is
// the write-ordering version: the ON CONFLICT ... DO UPDATE ... WHERE
// clause makes an older outcome's projection physically incapable of
// overwriting a newer one no matter what order two concurrent transactions
// against the same candidate happen to commit in (Section 6) -- there is
// no read-then-write window for a race to land in, because the guard is
// evaluated by PostgreSQL as part of the same statement that would apply
// the write.
//
// disabled is never referenced in the INSERT column list or the UPDATE SET
// list: a first-ever row gets the table's DEFAULT FALSE, and every later
// write leaves whatever value is already there completely untouched.
// Disabled is reserved for administrative/canonical control this function
// has no authority over (Section 4).
func applyCapacityFeedback(
	ctx context.Context,
	tx pgx.Tx,
	organizationID, providerID, providerModelID string,
	outcomeID, invocationID, attemptID int64,
	outcome modelruntime.ProviderOutcome,
	feedback modelruntime.CapacityFeedback,
) error {
	var httpStatus any
	if outcome.HTTPStatus > 0 {
		httpStatus = outcome.HTTPStatus
	}
	_, err := tx.Exec(ctx, `
INSERT INTO model_routing_capacity_state(
    organization_id, provider_id, provider_model_id,
    quota_exhausted, cooldown_until, consecutive_capacity_failures,
    last_provider_outcome_id, last_invocation_id, last_dispatch_attempt_id,
    last_outcome_classification, last_http_status, last_error_class, last_error_code, last_retryable,
    last_observed_at, updated_at
) VALUES (
    $1, $2, $3,
    ($4 = 'quota'),
    CASE WHEN $4 = 'cooldown' THEN clock_timestamp() + make_interval(secs => $5) ELSE NULL END,
    CASE WHEN $4 = 'cooldown' THEN 1 ELSE 0 END,
    $6, $7, $8,
    $9, $10, NULLIF($11,''), NULLIF($12,''), $13,
    clock_timestamp(), clock_timestamp()
)
ON CONFLICT (organization_id, provider_id, provider_model_id) DO UPDATE SET
    quota_exhausted = CASE
        WHEN $4 = 'healthy' THEN FALSE
        WHEN $4 = 'quota' THEN TRUE
        ELSE model_routing_capacity_state.quota_exhausted
    END,
    cooldown_until = CASE
        WHEN $4 = 'healthy' THEN NULL
        WHEN $4 = 'cooldown' THEN clock_timestamp() + make_interval(secs => $5)
        ELSE model_routing_capacity_state.cooldown_until
    END,
    consecutive_capacity_failures = CASE
        WHEN $4 = 'healthy' THEN 0
        WHEN $4 = 'cooldown' THEN model_routing_capacity_state.consecutive_capacity_failures + 1
        ELSE model_routing_capacity_state.consecutive_capacity_failures
    END,
    last_provider_outcome_id = EXCLUDED.last_provider_outcome_id,
    last_invocation_id = EXCLUDED.last_invocation_id,
    last_dispatch_attempt_id = EXCLUDED.last_dispatch_attempt_id,
    last_outcome_classification = EXCLUDED.last_outcome_classification,
    last_http_status = EXCLUDED.last_http_status,
    last_error_class = EXCLUDED.last_error_class,
    last_error_code = EXCLUDED.last_error_code,
    last_retryable = EXCLUDED.last_retryable,
    last_observed_at = EXCLUDED.last_observed_at,
    updated_at = EXCLUDED.updated_at
WHERE model_routing_capacity_state.last_provider_outcome_id IS NULL
   OR model_routing_capacity_state.last_provider_outcome_id < EXCLUDED.last_provider_outcome_id`,
		organizationID, providerID, providerModelID,
		string(feedback.Kind), int64(feedback.Duration/time.Second),
		outcomeID, invocationID, attemptID,
		outcome.OutcomeClassification, httpStatus, outcome.ErrorClass, outcome.ErrorCode, outcome.Retryable,
	)
	if err != nil {
		return mapError(err)
	}
	return nil
}
