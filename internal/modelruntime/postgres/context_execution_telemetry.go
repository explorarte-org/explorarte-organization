package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

// GetContextExecutionTelemetry answers M1.2 section 14's combined,
// organization-scoped read: Context Assembly's M1.2 estimate for one
// invocation JOINed with whatever provider usage Model Runtime already
// recorded for it, under explicitly different field names (never
// coalescing "provider input tokens" and "estimated context tokens" into
// one ambiguous figure -- section 3).
//
// This method represents ONLY a PRESENT M1.2 combined record. A
// model_invocation_render_telemetry row may legitimately exist with its
// M1.2 columns still NULL (a historical R10.4-only row that predates
// migration 000052, or a best-effort M1.2 write that never happened) --
// that absence is a real fact, never zero/empty-string-sentinel
// telemetry. The INNER JOIN to execution_context_views below, gated on
// t.execution_context_view_id IS NOT NULL, is what enforces this: no M1.2
// telemetry means no row, means ErrNotFound, regardless of whether
// provider usage independently exists for the same invocation (that
// remains readable through Usage/model_invocation_usage's own existing
// APIs, never deleted or reinterpreted here).
//
// The JOIN to execution_context_views also proves -- not merely trusts --
// that the referenced view belongs to this exact invocation's own
// (context_snapshot_id, organization_id): ecv.id alone is never enough.
//
// Every field is read from a table this package or contextcompiler/postgres
// already owns; nothing here is a second write path. organizationID scopes
// every row this can ever return -- a caller for one organization can never
// read another organization's invocation/view telemetry through this
// method (section 18), enforced in the WHERE clause, not only by trusting
// the caller's own bookkeeping.
func (s *Store) GetContextExecutionTelemetry(ctx context.Context, organizationID string, invocationID int64) (modelruntime.ContextExecutionTelemetry, error) {
	if organizationID == "" || invocationID <= 0 {
		return modelruntime.ContextExecutionTelemetry{}, fmt.Errorf("%w: organization ID and invocation ID are required", modelruntime.ErrInvalidRequest)
	}
	row := s.pool.QueryRow(ctx, `
SELECT
    mi.id, mi.task_id, mi.attempt_id, mi.context_snapshot_id,
    t.execution_context_view_id, ecv.context_profile_id, ecv.context_profile_version,
    t.token_estimator_id, t.token_estimator_version,
    t.estimated_provider_visible_tokens, t.estimated_stable_prefix_tokens, t.estimated_dynamic_suffix_tokens,
    t.segment_token_estimates,
    t.stable_prefix_hash, t.stable_prefix_bytes, t.fallback_to_legacy, t.fallback_reason,
    mi.provider_id, mi.provider_model_id, mi.model_profile_version_id,
    u.input_tokens, u.output_tokens, u.total_tokens, u.provider_reported,
    u.prompt_cache_hit_tokens, u.prompt_cache_miss_tokens
FROM model_invocations mi
JOIN model_invocation_render_telemetry t ON t.invocation_id = mi.id
JOIN execution_context_views ecv
    ON ecv.id = t.execution_context_view_id
    AND ecv.context_snapshot_id = mi.context_snapshot_id
    AND ecv.organization_id = mi.organization_id
LEFT JOIN model_invocation_usage u ON u.invocation_id = mi.id
WHERE mi.organization_id = $1 AND mi.id = $2 AND t.execution_context_view_id IS NOT NULL
`, organizationID, invocationID)

	var (
		result       modelruntime.ContextExecutionTelemetry
		segmentsJSON []byte
	)
	err := row.Scan(
		&result.InvocationID, &result.TaskID, &result.AttemptID, &result.ContextSnapshotID,
		&result.ExecutionContextViewID, &result.ContextProfileID, &result.ContextProfileVersion,
		&result.EstimatorID, &result.EstimatorVersion,
		&result.EstimatedProviderVisibleTokens, &result.EstimatedStablePrefixTokens, &result.EstimatedDynamicSuffixTokens,
		&segmentsJSON,
		&result.StablePrefixHash, &result.StablePrefixBytes, &result.FallbackToLegacy, &result.FallbackReason,
		&result.ProviderID, &result.ProviderModelID, &result.ModelProfileVersionID,
		&result.ActualProviderInputTokens, &result.ActualProviderOutputTokens, &result.ActualProviderTotalTokens, &result.ProviderReported,
		&result.PromptCacheHitTokens, &result.PromptCacheMissTokens,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return modelruntime.ContextExecutionTelemetry{}, modelruntime.ErrNotFound
		}
		return modelruntime.ContextExecutionTelemetry{}, mapError(err)
	}
	if err := json.Unmarshal(segmentsJSON, &result.SegmentTokenEstimates); err != nil {
		return modelruntime.ContextExecutionTelemetry{}, fmt.Errorf("unmarshal segment token estimates: %w", err)
	}
	return result, nil
}
