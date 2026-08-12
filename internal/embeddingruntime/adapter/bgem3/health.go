package bgem3

import (
	"context"
	"fmt"
)

// BGE_SIDECAR_CONTRACT_UPDATE_REQUIRED (R31 hardening §7): as of this
// change, the productive Python sidecar this adapter talks to
// (/opt/explorarte/bgem3-sidecar/server.py on the VPS) lives OUTSIDE this
// repository with no source control of its own (confirmed: no .git
// present there at implementation time) -- it is not edited here. The Go
// wire contract below now REQUIRES tokenizer_revision/normalization/
// pooling in both /v1/health and /v1/embed responses and hard-fails
// (ErrModelIdentityDrift) if they are missing or do not match the pinned
// Config, because an unattested field must never be silently treated as
// matching. Until server.py is updated to actually send these three
// fields, EVERY real call through this adapter will fail identity
// verification -- this is intentional fail-closed behavior, not a
// regression: BGE_M3 is not enabled in the current deployment (no
// ORG_BGE_M3_* activation in compose.yaml at implementation time), so
// nothing live depends on the previous, incomplete check. Do NOT enable
// this profile in production until the sidecar contract is updated to
// match.
//
// Healthy calls the sidecar's readiness endpoint — deliberately separate
// from Embed's /v1/embed path, per R30's "healthcheck/readiness separado"
// requirement, so liveness/monitoring never competes with the bounded
// embed queue for a slot. It verifies the sidecar's reported model
// identity against the pinned Config before returning success: a sidecar
// that is technically responding but serving a different model revision,
// artifact, tokenizer revision, normalization, or pooling strategy than
// what was provisioned is not healthy, whatever its own status field
// claims.
func (a *Adapter) Healthy(ctx context.Context) (Health, error) {
	if a == nil {
		return Health{}, ErrDisabled
	}
	var health Health
	if err := a.doJSON(ctx, "GET", "/v1/health", nil, &health); err != nil {
		return Health{}, fmt.Errorf("embeddingruntime bge-m3: health check failed: %w", err)
	}
	if health.ModelRevision != a.config.ModelRevision || health.ArtifactSHA256 != a.config.ArtifactSHA256 {
		return health, ErrModelIdentityDrift
	}
	if health.TokenizerRevision != a.config.TokenizerRevision || health.Normalization != a.config.Normalization || health.Pooling != a.config.Pooling {
		return health, fmt.Errorf("%w: sidecar attested tokenizer_revision=%q normalization=%q pooling=%q, want %q/%q/%q",
			ErrModelIdentityDrift, health.TokenizerRevision, health.Normalization, health.Pooling,
			a.config.TokenizerRevision, a.config.Normalization, a.config.Pooling)
	}
	if health.Dimension != a.config.ExpectedDimension {
		return health, fmt.Errorf("%w: reported dimension %d, want %d", ErrInvalidVector, health.Dimension, a.config.ExpectedDimension)
	}
	if !health.Ready() {
		return health, fmt.Errorf("embeddingruntime bge-m3: sidecar reports status %q, not ready", health.Status)
	}
	return health, nil
}
