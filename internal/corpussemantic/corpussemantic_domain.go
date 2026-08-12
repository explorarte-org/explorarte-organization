// Package corpussemantic computes a per-Work embedding (Gemini
// Embedding 2, title+abstract) for the Silver corpus and clusters those
// embeddings via average-link agglomerative clustering with cosine
// distance -- the semantic clustering layer that replaces
// internal/corpuscluster's TF-IDF/single-link baseline once it proved
// insufficient (owner decision: single-link chaining produced a
// 3,021-of-4,035 mega-cluster; TF-IDF/title-only survives only as an
// auxiliary lexical signal, never the final clustering decision).
//
// This package deliberately does NOT go through internal/rag's
// CostGate/wallet/authorization machinery: that machinery is scoped to
// the organization's Knowledge namespace (rag_knowledge_chunks,
// approved retrieval), and this step produces a pre-Knowledge,
// pre-curation diagnostic signal, never Knowledge itself (owner's own
// framing: "la clasificación semántica es señal de census; NO otorga
// aprobación de Knowledge"). It reuses the SAME audited Gemini adapter
// construction internal/rag/bootstrap uses (gemini.LoadConfig +
// gemini.New, env-driven, the exact client with the x-goog-api-key auth
// fix from earlier this branch) so credential handling and wire-format
// correctness are not reimplemented -- only cost/wallet accounting is
// intentionally out of scope here, tracked instead in this package's own
// resumable store (tokens, provider-reported usage, timestamps), and
// reported explicitly rather than silently absent.
package corpussemantic

import "time"

// EmbeddingRecord is this package's resumable, checkpointed unit of
// state -- one per Work, keyed by WorkID. Mirrors
// internal/corpusenrich.Store's periodic-flush discipline (fixing the
// same checkpointing gap logged against internal/corpuscensus earlier
// in this branch).
type EmbeddingRecord struct {
	WorkID              string    `json:"work_id"`
	InputHash           string    `json:"input_hash"`
	Vector              []float32 `json:"vector"`
	EmbeddingProviderID string    `json:"embedding_provider_id"`
	EmbeddingModelID    string    `json:"embedding_model_id"`
	EmbeddingDimension  int       `json:"embedding_dimension"`
	InputTokens         int64     `json:"input_tokens"`
	CreatedAt           time.Time `json:"created_at"`
}
