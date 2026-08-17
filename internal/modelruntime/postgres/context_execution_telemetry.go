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
    COALESCE(t.execution_context_view_id, 0), COALESCE(t.token_estimator_id, ''), COALESCE(t.token_estimator_version, ''),
    COALESCE(t.estimated_provider_visible_tokens, 0), COALESCE(t.estimated_stable_prefix_tokens, 0), COALESCE(t.estimated_dynamic_suffix_tokens, 0),
    COALESCE(t.segment_token_estimates, '[]'::jsonb),
    mi.provider_id, mi.provider_model_id, mi.model_profile_version_id,
    u.input_tokens, u.output_tokens, u.total_tokens, u.provider_reported,
    u.prompt_cache_hit_tokens, u.prompt_cache_miss_tokens
FROM model_invocations mi
JOIN model_invocation_render_telemetry t ON t.invocation_id = mi.id
LEFT JOIN model_invocation_usage u ON u.invocation_id = mi.id
WHERE mi.organization_id = $1 AND mi.id = $2
`, organizationID, invocationID)

	var (
		result       modelruntime.ContextExecutionTelemetry
		segmentsJSON []byte
	)
	err := row.Scan(
		&result.InvocationID, &result.TaskID, &result.AttemptID, &result.ContextSnapshotID,
		&result.ExecutionContextViewID, &result.EstimatorID, &result.EstimatorVersion,
		&result.EstimatedProviderVisibleTokens, &result.EstimatedStablePrefixTokens, &result.EstimatedDynamicSuffixTokens,
		&segmentsJSON,
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
