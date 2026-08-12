package gemini

import "errors"

// ErrInvalidVector fires when Gemini's response decodes successfully at
// the JSON level but the vector itself is unusable: wrong dimension (never
// matches request.OutputDimensionality), or a component that is NaN/±Inf.
// Mirrors bgem3.ErrInvalidVector exactly (R31 hardening -- §6): this is a
// RESPONSE_RECEIVED-class failure, not a BEFORE_REQUEST one -- the provider
// was reached and answered, the answer just cannot be trusted as-is. RAG
// and Memory callers must degrade to non-vector retrieval on this error,
// never let a malformed vector reach pgvector.
var ErrInvalidVector = errors.New("embeddingruntime gemini: provider returned an invalid vector (wrong dimension, empty, or non-finite component)")
