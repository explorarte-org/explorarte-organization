package bgem3

// wireItem is one text to embed, as sent to the sidecar. InputHash (not the
// raw text) is what R30's "idempotency key + input hash" requirement asks
// the wire protocol to carry — the sidecar can use it to deduplicate or
// cache without this adapter ever logging or persisting the text itself.
type wireItem struct {
	Key       string `json:"key"`
	Text      string `json:"text"`
	Task      string `json:"task"`
	InputHash string `json:"input_hash"`
}

type embedWireRequest struct {
	ModelRevision         string     `json:"model_revision"`
	PromptTemplateVersion string     `json:"prompt_template_version"`
	IdempotencyKey        string     `json:"idempotency_key"`
	Items                 []wireItem `json:"items"`
}

type wireResult struct {
	Key    string    `json:"key"`
	Vector []float32 `json:"vector"`
}

type embedWireResponse struct {
	ModelRevision string `json:"model_revision"`
	// ArtifactSHA256 and PromptTemplateVersion are the sidecar's own report
	// of which pinned weights and which prompt template it actually used
	// for this call — Embed verifies both against the adapter's Config,
	// the same identity check Healthy already performs against
	// /v1/health. Without this, a sidecar could serve wrong weights (or a
	// stale prompt template) under a matching model_revision and produce
	// vectors this adapter would accept as if nothing were wrong.
	ArtifactSHA256        string `json:"artifact_sha256"`
	PromptTemplateVersion string `json:"prompt_template_version"`
	// TokenizerRevision, Normalization, and Pooling (R31 hardening §7):
	// Config already pins all three (see config.go), but until this
	// change the wire response never asked the sidecar to attest them --
	// a sidecar could silently run a different tokenizer revision, or
	// mean-pool instead of cls-pool, or skip L2 normalization, and this
	// adapter would have no way to detect it even though the resulting
	// vectors are NOT comparable to ones produced under the pinned
	// configuration. See BGE_SIDECAR_CONTRACT_UPDATE_REQUIRED in
	// adapter.go/health.go: the productive Python sidecar (outside this
	// repo, unversioned as of this change) does not yet send these
	// fields, so a real sidecar's response currently decodes them as
	// empty strings, which the identity check now treats as a hard
	// mismatch by design (fail closed, never assume an unattested field
	// silently matches).
	TokenizerRevision string       `json:"tokenizer_revision"`
	Normalization     string       `json:"normalization"`
	Pooling           string       `json:"pooling"`
	Dimension         int          `json:"dimension"`
	Results           []wireResult `json:"results"`
	TextCount         int          `json:"text_count"`
	CPUTimeMS         int64        `json:"cpu_time_ms"`
}

// Health is the sidecar's readiness report — a separate endpoint from
// embed, per R30's "healthcheck/readiness separado" requirement, and the
// only place peak RSS/CPU/queue depth are sourced from (this adapter has
// no way to measure another process's resource usage itself).
type Health struct {
	Status         string `json:"status"`
	ModelRevision  string `json:"model_revision"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	// TokenizerRevision, Normalization, and Pooling: see the identical
	// fields on embedWireResponse above (R31 hardening §7) -- same
	// full-identity-attestation requirement, checked here for the
	// readiness path instead of the embed path.
	TokenizerRevision string `json:"tokenizer_revision"`
	Normalization     string `json:"normalization"`
	Pooling           string `json:"pooling"`
	Dimension         int    `json:"dimension"`
	QueueDepth        int    `json:"queue_depth"`
	PeakRSSBytes      int64  `json:"peak_rss_bytes"`
	CPUTimeMS         int64  `json:"cpu_time_ms"`
	ProcessedCount    int64  `json:"processed_count"`
}

func (h Health) Ready() bool { return h.Status == "ready" }
