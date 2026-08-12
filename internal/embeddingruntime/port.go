// Package embeddingruntime is the boundary for text-embedding provider
// calls — deliberately separate from internal/modelruntime, whose
// ProviderAdapter/CanonicalRequest/RawResponse are modeled entirely around
// chat dispatch (messages, MaxOutputTokens, ReasoningEffort, OutputMode).
// An embedding call has no natural fit there: no output tokens, no tool
// calls, no messages — it is "text in, vector out", plus (for the
// asynchronous Batch API) a job lifecycle chat dispatch never has.
package embeddingruntime

import "context"

// TaskKind distinguishes how a text will be used, so an adapter can render
// it with the deterministic, versioned prefix Gemini's docs recommend for
// gemini-embedding-2. A "query" text is what a caller searches with; a
// "document" text is content being indexed for later retrieval — the two
// use different embedding orientations even for identical underlying text.
type TaskKind string

const (
	TaskQuery    TaskKind = "query"
	TaskDocument TaskKind = "document"
)

func (k TaskKind) Valid() bool {
	switch k {
	case TaskQuery, TaskDocument:
		return true
	default:
		return false
	}
}

// EmbedItem is one input to embed. Key is caller-assigned and must be
// unique within a single request/batch — callers use it to associate a
// result back to the input that produced it. Never assume response order
// matches request order beyond what a specific adapter method explicitly
// documents.
//
// An item is either text (Text set, MimeType empty) or inline binary media
// (MimeType set to the content's actual MIME type, Data holding the raw
// bytes, Text ignored). Exactly one of the two must be populated — an item
// with both or neither set is invalid. Media items only apply to adapters
// that advertise multimodal support (see the adapter's own Config/adapter
// doc comment); an adapter that only understands text must reject a media
// item with ErrInvalidRequest rather than silently drop it or stringify
// the bytes as text.
type EmbedItem struct {
	Key      string
	Text     string
	MimeType string
	Data     []byte
	Task     TaskKind
}

// IsMedia reports whether this item carries inline binary content instead
// of plain text.
func (i EmbedItem) IsMedia() bool { return i.MimeType != "" }

// Valid checks the item's own shape (key present, task valid, exactly one
// of Text/media populated) — it does not know a specific adapter's
// modality or size limits, which stay adapter-local.
func (i EmbedItem) Valid() bool {
	if i.Key == "" || !i.Task.Valid() {
		return false
	}
	hasText := i.Text != ""
	hasMedia := i.MimeType != "" || len(i.Data) > 0
	if hasText == hasMedia {
		return false // both or neither
	}
	if hasMedia && (i.MimeType == "" || len(i.Data) == 0) {
		return false // media requires both fields, not just one
	}
	return true
}

type EmbedRequest struct {
	ProviderID            string
	ProviderModelID       string
	OutputDimensionality  int
	PromptTemplateVersion string
	Items                 []EmbedItem
}

type EmbedResult struct {
	Key    string
	Vector []float32
}

// EmbedResponse.InputTokens comes from the provider's own reported usage
// (Gemini's usageMetadata.promptTokenCount), never a local estimate — the
// cost ledger reconciles against real, provider-confirmed usage.
type EmbedResponse struct {
	Results           []EmbedResult
	InputTokens       int64
	ProviderRequestID string
}

// OnlineAdapter embeds text synchronously, for interactive callers
// (rag.Query, memory.Search) where latency matters. It must never be used
// for bulk reindexing of an entire namespace — see BatchAdapter, which is
// the actual discounted, asynchronous surface for that.
type OnlineAdapter interface {
	Embed(ctx context.Context, request EmbedRequest) (EmbedResponse, error)
}

// BatchJobStatus mirrors Gemini's asynchronous Batch API job states
// (JOB_STATE_* in Google's docs). This is a genuinely different surface
// from OnlineAdapter and from a provider's synchronous "grouped" embed call
// (e.g. Gemini's batchEmbedContents) — the word "batch" in the latter's name
// does not mean asynchronous or discounted; conflating the two was a real
// mistake caught in this branch's design review before any code existed.
type BatchJobStatus string

const (
	BatchJobPending   BatchJobStatus = "pending"
	BatchJobRunning   BatchJobStatus = "running"
	BatchJobSucceeded BatchJobStatus = "succeeded"
	BatchJobFailed    BatchJobStatus = "failed"
	BatchJobCancelled BatchJobStatus = "cancelled"
	BatchJobExpired   BatchJobStatus = "expired"
)

func (s BatchJobStatus) Valid() bool {
	switch s {
	case BatchJobPending, BatchJobRunning, BatchJobSucceeded, BatchJobFailed, BatchJobCancelled, BatchJobExpired:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further state transition will occur for this
// job — Succeeded/Failed/Cancelled/Expired are all end states, but only
// Succeeded has results worth reading.
func (s BatchJobStatus) Terminal() bool {
	switch s {
	case BatchJobSucceeded, BatchJobFailed, BatchJobCancelled, BatchJobExpired:
		return true
	default:
		return false
	}
}

type CreateBatchRequest struct {
	ProviderID            string
	ProviderModelID       string
	OutputDimensionality  int
	PromptTemplateVersion string
	Items                 []EmbedItem
}

// CreateBatchResponse.ProviderJobName is the only thing a caller can rely
// on from this call. Google's Batch API documents job creation as NOT
// idempotent: retrying a create request after an ambiguous network failure
// creates a second, independent job rather than deduplicating the first.
// A caller-supplied idempotency key here would be misleading, since it
// prevents nothing on the provider's side — callers that need to guard
// against duplicate submission must do so by recording ProviderJobName
// durably before the create call can be considered ambiguous vs. confirmed,
// and reconciling by inspecting results afterward, not by trusting creation
// itself to be safe to retry.
type CreateBatchResponse struct {
	ProviderJobName string
}

type BatchJobState struct {
	ProviderJobName string
	Status          BatchJobStatus
	FailedItemCount int64
}

// BatchItemResult.Err is set (Vector is nil) when this specific item failed
// independently of the rest of the job — a job in BatchJobSucceeded can
// still carry individual item failures, and a job in BatchJobFailed may
// still have some items that succeeded before the failure; callers must
// handle both per item, not assume job-level status implies uniform
// per-item outcome.
type BatchItemResult struct {
	Key    string
	Vector []float32
	Err    string
}

type BatchAdapter interface {
	CreateBatch(ctx context.Context, request CreateBatchRequest) (CreateBatchResponse, error)
	GetBatch(ctx context.Context, providerJobName string) (BatchJobState, error)
	CancelBatch(ctx context.Context, providerJobName string) error
	ReadBatchResults(ctx context.Context, providerJobName string) ([]BatchItemResult, error)
}
