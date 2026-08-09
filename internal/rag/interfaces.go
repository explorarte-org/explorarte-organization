package rag

import (
	"context"
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
	Limit          int
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
