package modelruntime

import "context"

// SegmentTokenEstimate is a plain, provider-agnostic mirror of
// contextcompiler.SegmentTokenEstimate (M1.2). It duplicates the same
// pattern ProviderRenderTelemetry already established for
// ExecutionContextView/CompilationResult: a plain value type in this
// package so DispatchService never has to import internal/contextcompiler
// to know this shape. It never carries segment Content -- only metadata,
// counts, and estimates already durable elsewhere.
type SegmentTokenEstimate struct {
	SourceReference          string
	AuthorityTier            string
	SourceKind               string
	SourceVersion            string
	InstructionClass         string
	TrustClass               string
	DataClass                string
	RenderOrdinal            int
	OriginalBytes            int
	DeliveredBytes           int
	OriginalEstimatedTokens  int64
	DeliveredEstimatedTokens int64
	Projected                bool
	ProjectionReason         string
}

// ContextTokenTelemetry is a plain mirror of
// contextcompiler.ContextTokenTelemetry (M1.2 section 7). EstimatorID/
// EstimatorVersion make every historical row interpretable forever, even
// after a future estimator formula change (which becomes a new
// ID/Version, never a silent mutation of this one -- see
// contextcompiler.EstimateTokens's doc comment).
//
// This is a deterministic, versioned HOST ESTIMATE for the exact durable
// ExecutionContextView -- it is never provider-reported usage (see Usage,
// ContextExecutionTelemetry's ActualProvider* fields) and must never be
// treated as equivalent to it (M1.2 section 3).
type ContextTokenTelemetry struct {
	ExecutionContextViewID         int64
	ContextSnapshotID              int64
	ContextProfileID               string
	ContextProfileVersion          string
	EstimatorID                    string
	EstimatorVersion               string
	ProviderVisibleBytes           int
	EstimatedProviderVisibleTokens int64
	StablePrefixBytes              int
	EstimatedStablePrefixTokens    int64
	DynamicSuffixBytes             int
	EstimatedDynamicSuffixTokens   int64
	SegmentTokenEstimates          []SegmentTokenEstimate
}

// ContextTokenTelemetryReader is an OPTIONAL capability a ContextReader
// implementation may additionally provide (M1.2, mirrors R10.4's
// ProviderRenderTelemetryReader). DispatchService type-asserts for it --
// an implementation that doesn't support it leaves dispatch behaving
// exactly as it did before M1.2 existed.
type ContextTokenTelemetryReader interface {
	GetContextTokenTelemetry(ctx context.Context, contextSnapshotID int64) (ContextTokenTelemetry, error)
}

// ContextTokenTelemetryRecorder is an OPTIONAL capability a Store may
// additionally provide. Writing this telemetry is always best-effort,
// exactly like ProviderRenderTelemetryRecorder: a failure here must never
// fail or retry a dispatch, alter M0/Harness state, or gate provider
// dispatch in any way (M1.2 section 12).
type ContextTokenTelemetryRecorder interface {
	RecordContextTokenTelemetry(ctx context.Context, invocationID int64, telemetry ContextTokenTelemetry) error
}

// ContextExecutionTelemetry is the combined, organization-scoped read
// model spanning Context Assembly's M1.2 estimate and Model Runtime's own
// already-canonical provider usage (M1.2 section 14). It is intentionally
// NOT a second source of truth: every field here is read from a table
// Model Runtime or Context Assembly already owns, joined for a single
// convenient query, never duplicated into a new write path.
//
// ActualProvider*/ProviderReported/PromptCache* follow Usage's exact
// nullability contract (M1.2 section 15): nil means the provider never
// reported that field (or no usage row exists at all for this
// invocation yet), never coalesced to zero.
type ContextExecutionTelemetry struct {
	InvocationID           int64
	TaskID                 int64
	AttemptID              int64
	ContextSnapshotID      int64
	ExecutionContextViewID int64
	ContextProfileID       string
	ContextProfileVersion  string

	EstimatorID                    string
	EstimatorVersion               string
	EstimatedProviderVisibleTokens int64
	EstimatedStablePrefixTokens    int64
	EstimatedDynamicSuffixTokens   int64
	SegmentTokenEstimates          []SegmentTokenEstimate

	ProviderID            string
	ProviderModelID       string
	ModelProfileVersionID int64

	// ActualProviderInputTokens/OutputTokens/TotalTokens are nil when no
	// model_invocation_usage row exists for this invocation at all -- never
	// zero-defaulted. See Usage's own doc comment for why "no usage row"
	// and "provider reported zero" must never collapse into the same fact.
	ActualProviderInputTokens  *int64
	ActualProviderOutputTokens *int64
	ActualProviderTotalTokens  *int64
	ProviderReported           *bool
	PromptCacheHitTokens       *int64
	PromptCacheMissTokens      *int64
}
