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
	ArtifactSHA256        string       `json:"artifact_sha256"`
	PromptTemplateVersion string       `json:"prompt_template_version"`
	Dimension             int          `json:"dimension"`
	Results               []wireResult `json:"results"`
	TextCount             int          `json:"text_count"`
	CPUTimeMS             int64        `json:"cpu_time_ms"`
}

// Health is the sidecar's readiness report — a separate endpoint from
// embed, per R30's "healthcheck/readiness separado" requirement, and the
// only place peak RSS/CPU/queue depth are sourced from (this adapter has
// no way to measure another process's resource usage itself).
type Health struct {
	Status         string `json:"status"`
	ModelRevision  string `json:"model_revision"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	Dimension      int    `json:"dimension"`
	QueueDepth     int    `json:"queue_depth"`
	PeakRSSBytes   int64  `json:"peak_rss_bytes"`
	CPUTimeMS      int64  `json:"cpu_time_ms"`
	ProcessedCount int64  `json:"processed_count"`
}

func (h Health) Ready() bool { return h.Status == "ready" }
