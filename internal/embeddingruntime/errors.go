package embeddingruntime

import "errors"

var (
	ErrInvalidRequest = errors.New("embeddingruntime: invalid request")
	// ErrResultCountMismatch fires when a provider returns a different
	// number of vectors than texts submitted, or a result cannot be matched
	// to a request item by key/order. This must always be a hard failure —
	// silently zipping mismatched slices together would attach a chunk's
	// embedding to the wrong content.
	ErrResultCountMismatch = errors.New("embeddingruntime: result count or ordering does not match request")
	// ErrTextTooLong fires when the provider rejects a text as exceeding its
	// input token window (8192 tokens for gemini-embedding-2). This package
	// never truncates text itself — it has no local tokenizer and truncating
	// silently would corrupt what gets embedded without any caller ever
	// knowing. The provider's own rejection is surfaced as-is.
	ErrTextTooLong  = errors.New("embeddingruntime: text exceeds provider input token limit")
	ErrJobNotFound  = errors.New("embeddingruntime: batch job not found")
	ErrJobNotReady  = errors.New("embeddingruntime: batch job has not reached a terminal state")
	ErrJobAmbiguous = errors.New("embeddingruntime: batch job creation outcome is unknown (network failure after send)")
)
