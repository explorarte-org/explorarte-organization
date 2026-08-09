package rag

import (
	"context"
	"fmt"
	"time"
)

type CreateCandidateCommand struct {
	Version        KnowledgeVersion
	IdempotencyKey string
}

type SaveCommand struct {
	Version          KnowledgeVersion
	ExpectedRevision int64
	ActorID          string
	Reason           string
}

type ListFilter struct {
	OrganizationID string
	NamespaceKind  NamespaceKind
	NamespaceID    string
	Lifecycle      Lifecycle
	Limit          int
}

type ReindexCommand struct {
	OrganizationID string
	NamespaceKind  NamespaceKind
	NamespaceID    string
	ChunkerID      string
	ChunkerVersion string
	Chunks         []Chunk
}

type QueryCommand struct {
	OrganizationID string
	NamespaceKind  NamespaceKind
	NamespaceID    string
	QueryText      string
	// QueryVector is nil when the vector channel is unavailable for this
	// call (semantic search disabled, or embedding the query failed/was
	// skipped — see Manager.embedQuery) — Query must still return correct
	// exact+lexical results in that case, never treat a nil vector as an
	// error.
	QueryVector []float32
	// EmbeddingIdentity and EmbeddingPromptTemplateVersion pin the exact
	// vector space QueryVector was produced under — required whenever
	// QueryVector is non-empty. A chunk table's primary key deliberately
	// allows more than one embedding row per chunk (re-embedding under a
	// new model revision is a new row, never an UPDATE — see migrations
	// 000028/000032), so without this filter the vector channel could
	// return more than one row for the same chunk (mixing incompatible
	// embedding spaces and awarding it multiple RRF votes) the moment a
	// second revision exists. See internal/rag/postgres/hybrid_query.go's
	// vectorChannelTable.
	EmbeddingIdentity              EmbeddingIdentity
	EmbeddingPromptTemplateVersion string
	Limit                          int
}

// EmbeddingIdentity is the full identity of the vector space an embedding
// row belongs to, beyond just its dimension — every field that, if it
// changed, would make an old vector incomparable to a new one. Exactly one
// of ModelVersion (Gemini's shape) or
// ModelRevision+ArtifactSHA256+TokenizerRevision+Normalization+Pooling
// (BGE-M3's shape) must be set — see Validate.
type EmbeddingIdentity struct {
	ModelID           string
	ModelVersion      string // Gemini: embedding_model_version. Empty for BGE-M3.
	ModelRevision     string // BGE-M3: model_revision. Empty for Gemini.
	ArtifactSHA256    string // BGE-M3 only.
	TokenizerRevision string // BGE-M3 only.
	Normalization     string // BGE-M3 only.
	Pooling           string // BGE-M3 only.
}

func (id EmbeddingIdentity) Validate() error {
	if id.ModelID == "" {
		return fmt.Errorf("%w: embedding identity requires a model id", ErrInvalidRequest)
	}
	geminiShaped := id.ModelVersion != ""
	bgeM3Shaped := id.ModelRevision != "" || id.ArtifactSHA256 != "" || id.TokenizerRevision != "" || id.Normalization != "" || id.Pooling != ""
	if geminiShaped == bgeM3Shaped {
		return fmt.Errorf("%w: embedding identity must set exactly one of model_version (gemini-shaped) or model_revision+artifact_sha256+tokenizer_revision+normalization+pooling (bge-m3-shaped)", ErrInvalidRequest)
	}
	if bgeM3Shaped && (id.ModelRevision == "" || len(id.ArtifactSHA256) != 64 || id.TokenizerRevision == "" || id.Normalization == "" || id.Pooling == "") {
		return fmt.Errorf("%w: bge-m3-shaped embedding identity requires model_revision, a 64-character hex artifact_sha256, tokenizer_revision, normalization, and pooling all set", ErrInvalidRequest)
	}
	return nil
}

type Repository interface {
	CreateCandidate(context.Context, CreateCandidateCommand) (KnowledgeVersion, bool, error)
	Get(context.Context, string, string) (KnowledgeVersion, error)
	Save(context.Context, SaveCommand) (KnowledgeVersion, error)
	List(context.Context, ListFilter) ([]KnowledgeVersion, error)
	ApprovedForNamespace(context.Context, string, NamespaceKind, string) ([]KnowledgeVersion, error)
	Reindex(context.Context, ReindexCommand) (IndexGeneration, error)
	Query(context.Context, QueryCommand) ([]QueryResult, error)
	ActiveGeneration(context.Context, string, NamespaceKind, string) (IndexGeneration, bool, error)
	ExistingEvidenceReferences(ctx context.Context, organizationID, referencePrefix string) (map[string]bool, error)
}

type AuthorizationRequest struct {
	OrganizationID string
	ActorRoleID    string
	CapabilityID   string
	ResourceType   string
	ResourceID     string
	ActionDigest   string
	// ApprovalRequestID references a prior orgctl authorization
	// request/decide sequence for capabilities that carry a non-empty
	// approval mode in capability-matrix.yaml (e.g. rag.publish_approved).
	// Those capabilities always evaluate as approval-required regardless
	// of grants; only a decided request consumed against a matching
	// action digest resolves to allow.
	ApprovalRequestID *int64
}

type AuthorizationGate interface {
	Authorize(context.Context, AuthorizationRequest) error
}

type NamespaceResolver interface {
	ResolveNamespace(ctx context.Context, organizationID, actorRoleID string, kind NamespaceKind) (string, error)
}

// ChunkEmbedding is one derived vector for an already-persisted, immutable
// chunk (rag_knowledge_chunks) — stored in the separate rag_chunk_embeddings
// table (migration 000028), never as a column mutation on the chunk itself.
// Re-embedding under a new model version is a new ChunkEmbedding row with
// that version, never an update to an existing one.
type ChunkEmbedding struct {
	OrganizationID        string
	ChunkID               string
	EmbeddingModelID      string
	EmbeddingModelVersion string
	EmbeddingDimension    int
	PromptTemplateVersion string
	InputHash             string
	Vector                []float32
	CreatedAt             time.Time
}

// ScoredChunk is one nearest-neighbor hit: ChunkID plus the raw pgvector
// cosine distance (<=>) — lower is more similar. Callers that fuse this
// with other retrieval channels (see Fase 6's RRF) convert distance to a
// rank themselves; this type carries no opinion about how to combine it
// with lexical or exact-match scores.
type ScoredChunk struct {
	ChunkID  string
	Distance float64
}

// EmbeddingBatchJob tracks one shard of an asynchronous embeddings Batch
// API job (internal/embeddingruntime.BatchAdapter) submitted while
// reindexing a namespace — a single reindex can produce more chunks than
// fit in one provider batch job, so a generation's embedding work may be
// sharded across several EmbeddingBatchJob rows.
type EmbeddingBatchJob struct {
	ID              int64
	OrganizationID  string
	NamespaceKind   NamespaceKind
	NamespaceID     string
	GenerationID    string
	ProviderID      string
	ProviderModelID string
	ProviderJobName string
	Status          string
	ShardIndex      int
	ItemCount       int
	FailedItemCount int
	SubmittedAt     *time.Time
	CompletedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EmbeddingBatchJobItem is one chunk's outcome within a batch job — tracked
// individually so a job that partially fails, expires, or is cancelled
// never loses track of which specific chunks still need embedding.
type EmbeddingBatchJobItem struct {
	JobID        int64
	ItemKey      string
	ChunkID      string
	Status       string
	ErrorMessage string
}

// EmbeddingRepository is the persistence boundary for the derived vector
// index — deliberately separate from Repository (the canonical
// candidate/version/lifecycle store): embeddings are a rebuildable index
// over already-approved, already-immutable content, not evidence in their
// own right.
type EmbeddingRepository interface {
	InsertChunkEmbedding(ctx context.Context, embedding ChunkEmbedding) error
	// NearestChunks searches only within generationID — the same scoping
	// Repository.Query already applies for the lexical channel, so both
	// channels see an identical candidate set before fusion.
	NearestChunks(ctx context.Context, organizationID, generationID string, queryVector []float32, limit int) ([]ScoredChunk, error)

	CreateEmbeddingBatchJob(ctx context.Context, job EmbeddingBatchJob, items []EmbeddingBatchJobItem) (EmbeddingBatchJob, error)
	RecordEmbeddingBatchJobItemResult(ctx context.Context, jobID int64, itemKey string, embedding *ChunkEmbedding, errorMessage string) error
	CompleteEmbeddingBatchJob(ctx context.Context, jobID int64, status string, completedAt time.Time, failedItemCount int) error
}

// BGEM3ChunkEmbedding is R30's local, operational counterpart to
// ChunkEmbedding — stored in rag_chunk_embeddings_bge_m3 (migration
// 000032, vector(1024)), never rag_chunk_embeddings (vector(768), R29's
// frozen Gemini reference index). The two are never mixed: this type has
// no field that could be confused with a Gemini row, and a query against
// one table can never return rows from the other.
//
// A self-hosted model has no provider-assigned version string the way
// Gemini does — ModelRevision+ArtifactSHA256 (the pinned weights hash) is
// what actually identifies which model produced a given vector, so both
// are part of the row's identity, not just informational metadata.
type BGEM3ChunkEmbedding struct {
	OrganizationID        string
	ChunkID               string
	EmbeddingModelID      string
	ModelRevision         string
	ArtifactSHA256        string
	TokenizerRevision     string
	EmbeddingDimension    int
	Normalization         string
	Pooling               string
	PromptTemplateVersion string
	InputHash             string
	Vector                []float32
	CreatedAt             time.Time
}

// BGEM3EmbeddingRepository is the persistence boundary for R30's local
// vector index — additive and separate from EmbeddingRepository, never
// implemented by the same table or sharing a query path with it.
type BGEM3EmbeddingRepository interface {
	InsertBGEM3ChunkEmbedding(ctx context.Context, embedding BGEM3ChunkEmbedding) error
	// NearestBGEM3Chunks searches only within generationID, same scoping
	// discipline as NearestChunks — and only ever against
	// rag_chunk_embeddings_bge_m3, never rag_chunk_embeddings.
	NearestBGEM3Chunks(ctx context.Context, organizationID, generationID string, queryVector []float32, limit int) ([]ScoredChunk, error)
}
