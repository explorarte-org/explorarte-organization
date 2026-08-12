package postgres

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// RecordProviderRenderTelemetry implements
// modelruntime.ProviderRenderTelemetryRecorder (R10.4). One row per
// invocation, best-effort by contract at the call site (dispatch_service.go
// never fails or retries a dispatch because of an error returned here) --
// this is observability for prefix-cache behavior, never a correctness
// gate. idempotent on invocation_id via ON CONFLICT DO NOTHING: a dispatch
// path that somehow calls this twice for the same invocation (e.g. a retry
// racing this best-effort write) must not error or duplicate the row.
func (s *Store) RecordProviderRenderTelemetry(ctx context.Context, invocationID int64, telemetry modelruntime.ProviderRenderTelemetry) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO model_invocation_render_telemetry(
    invocation_id, provider_render_version, fallback_to_legacy, fallback_reason,
    stable_prefix_hash, stable_prefix_bytes, dynamic_suffix_hash, dynamic_suffix_bytes,
    provider_render_hash, provider_visible_bytes
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (invocation_id) DO NOTHING`,
		invocationID, telemetry.Version, telemetry.FallbackToLegacy, nullIfEmpty(telemetry.FallbackReason),
		telemetry.StablePrefixHash, telemetry.StablePrefixBytes, telemetry.DynamicSuffixHash, telemetry.DynamicSuffixBytes,
		telemetry.ProviderRenderHash, telemetry.ProviderVisibleBytes,
	)
	return err
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
