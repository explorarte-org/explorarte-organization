package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

// RecordContextTokenTelemetry implements
// modelruntime.ContextTokenTelemetryRecorder (M1.2). It layers M1.2's
// versioned token estimate onto the SAME model_invocation_render_telemetry
// row R10.4 already owns for invocationID -- RecordProviderRenderTelemetry
// must have created that base row first; this call is a no-op error
// (never a panic, never a partial write) if it hasn't. Best-effort by the
// same contract as R10.4: dispatch_service.go never fails or retries a
// dispatch because of an error returned here (section 12).
//
// The write proves, IN THE SAME QUERY, that telemetry.ExecutionContextViewID
// actually belongs to this invocation's own context_snapshot_id -- never
// trusting the caller's word for it (section 11): a mismatched binding is
// refused with ErrContextTokenTelemetryBindingMismatch and is never
// persisted, full stop.
//
// First-write-wins, fail-closed on contradiction (section 19): a second
// call for the same invocation with logically identical telemetry is a
// silent no-op; with different telemetry it returns
// ErrContextTokenTelemetryContradiction and never overwrites the original
// durable record.
func (s *Store) RecordContextTokenTelemetry(ctx context.Context, invocationID int64, telemetry modelruntime.ContextTokenTelemetry) error {
	if invocationID <= 0 || telemetry.ExecutionContextViewID <= 0 {
		return fmt.Errorf("context token telemetry requires a positive invocation ID and execution context view ID")
	}
	segments := telemetry.SegmentTokenEstimates
	if segments == nil {
		segments = []modelruntime.SegmentTokenEstimate{}
	}
	segmentsJSON, err := json.Marshal(segments)
	if err != nil {
		return fmt.Errorf("marshal segment token estimates: %w", err)
	}

	var wroteID int64
	err = s.pool.QueryRow(ctx, `
UPDATE model_invocation_render_telemetry t
SET execution_context_view_id = $2, token_estimator_id = $3, token_estimator_version = $4,
    estimated_provider_visible_tokens = $5, estimated_stable_prefix_tokens = $6,
    estimated_dynamic_suffix_tokens = $7, segment_token_estimates = $8
FROM model_invocations mi, execution_context_views ecv
WHERE t.invocation_id = $1
  AND mi.id = $1
  AND ecv.id = $2
  AND mi.context_snapshot_id = ecv.context_snapshot_id
  AND t.execution_context_view_id IS NULL
RETURNING t.invocation_id
`, invocationID, telemetry.ExecutionContextViewID, telemetry.EstimatorID, telemetry.EstimatorVersion,
		telemetry.EstimatedProviderVisibleTokens, telemetry.EstimatedStablePrefixTokens,
		telemetry.EstimatedDynamicSuffixTokens, segmentsJSON,
	).Scan(&wroteID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return mapError(err)
	}

	// No row was updated: either the binding does not hold, the R10.4 base
	// row does not exist yet, or durable M1.2 telemetry already exists for
	// this invocation. Distinguish these before deciding fail-closed vs
	// idempotent -- never assume "no row updated" means "already correct".
	var invocationSnapshotID, viewSnapshotID int64
	bindingErr := s.pool.QueryRow(ctx, `SELECT mi.context_snapshot_id, ecv.context_snapshot_id FROM model_invocations mi, execution_context_views ecv WHERE mi.id=$1 AND ecv.id=$2`, invocationID, telemetry.ExecutionContextViewID).Scan(&invocationSnapshotID, &viewSnapshotID)
	if bindingErr != nil {
		if errors.Is(bindingErr, pgx.ErrNoRows) {
			return fmt.Errorf("%w: invocation %d or execution context view %d not found", modelruntime.ErrContextTokenTelemetryBindingMismatch, invocationID, telemetry.ExecutionContextViewID)
		}
		return mapError(bindingErr)
	}
	if invocationSnapshotID != viewSnapshotID {
		return fmt.Errorf("%w: invocation %d context_snapshot_id=%d, view %d context_snapshot_id=%d", modelruntime.ErrContextTokenTelemetryBindingMismatch, invocationID, invocationSnapshotID, telemetry.ExecutionContextViewID, viewSnapshotID)
	}

	var existingViewID *int64
	var existingEstimatorID, existingEstimatorVersion *string
	var existingProviderTokens, existingStableTokens, existingDynamicTokens *int64
	var existingSegmentsJSON []byte
	rowErr := s.pool.QueryRow(ctx, `
SELECT execution_context_view_id, token_estimator_id, token_estimator_version,
       estimated_provider_visible_tokens, estimated_stable_prefix_tokens, estimated_dynamic_suffix_tokens,
       segment_token_estimates
FROM model_invocation_render_telemetry WHERE invocation_id=$1`, invocationID).
		Scan(&existingViewID, &existingEstimatorID, &existingEstimatorVersion,
			&existingProviderTokens, &existingStableTokens, &existingDynamicTokens, &existingSegmentsJSON)
	if rowErr != nil {
		if errors.Is(rowErr, pgx.ErrNoRows) {
			return fmt.Errorf("context token telemetry: no base render telemetry row for invocation %d", invocationID)
		}
		return mapError(rowErr)
	}
	if existingViewID == nil {
		return fmt.Errorf("context token telemetry: base render telemetry row for invocation %d is not yet estimator-populated", invocationID)
	}
	var existingSegments []modelruntime.SegmentTokenEstimate
	if err := json.Unmarshal(existingSegmentsJSON, &existingSegments); err != nil {
		return fmt.Errorf("unmarshal existing segment token estimates: %w", err)
	}
	if *existingViewID != telemetry.ExecutionContextViewID ||
		existingEstimatorID == nil || *existingEstimatorID != telemetry.EstimatorID ||
		existingEstimatorVersion == nil || *existingEstimatorVersion != telemetry.EstimatorVersion ||
		existingProviderTokens == nil || *existingProviderTokens != telemetry.EstimatedProviderVisibleTokens ||
		existingStableTokens == nil || *existingStableTokens != telemetry.EstimatedStablePrefixTokens ||
		existingDynamicTokens == nil || *existingDynamicTokens != telemetry.EstimatedDynamicSuffixTokens ||
		!reflect.DeepEqual(existingSegments, segments) {
		return fmt.Errorf("%w: invocation %d", modelruntime.ErrContextTokenTelemetryContradiction, invocationID)
	}
	return nil
}
