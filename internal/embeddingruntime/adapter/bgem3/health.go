package bgem3

import (
	"context"
	"fmt"
)

// Healthy calls the sidecar's readiness endpoint — deliberately separate
// from Embed's /v1/embed path, per R30's "healthcheck/readiness separado"
// requirement, so liveness/monitoring never competes with the bounded
// embed queue for a slot. It verifies the sidecar's reported model
// identity against the pinned Config before returning success: a sidecar
// that is technically responding but serving a different model revision
// or artifact than what was provisioned is not healthy, whatever its own
// status field claims.
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
	if health.Dimension != a.config.ExpectedDimension {
		return health, fmt.Errorf("%w: reported dimension %d, want %d", ErrInvalidVector, health.Dimension, a.config.ExpectedDimension)
	}
	if !health.Ready() {
		return health, fmt.Errorf("embeddingruntime bge-m3: sidecar reports status %q, not ready", health.Status)
	}
	return health, nil
}
