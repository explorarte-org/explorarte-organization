package memory

import (
	"context"
	"time"
)

type ApprovedFilter struct {
	OrganizationID string
	RoleID         string
	Limit          int
}

type ListFilter struct {
	OrganizationID string
	RoleID         string
	Status         Status
	Limit          int
}

type CreateCandidateCommand struct {
	Entry          Entry
	IdempotencyKey string
}

type SaveCommand struct {
	Entry            Entry
	ExpectedRevision int64
	ActorID          string
	Reason           string
}

type Repository interface {
	// CreateCandidate persists a candidate and its immutable content version.
	// Reused is true only when the same idempotency key or exact canonical
	// duplicate resolves to the same content. Same key with different content
	// must fail closed.
	CreateCandidate(context.Context, CreateCandidateCommand) (entry Entry, reused bool, err error)
	Get(context.Context, string, string) (Entry, error) // organizationID, entryID
	Save(context.Context, SaveCommand) (Entry, error)
	List(context.Context, ListFilter) ([]Entry, error)
	ListApproved(context.Context, ApprovedFilter) ([]Entry, error)
}

// EntryEmbedding is one derived vector for an already-approved, immutable
// memory version — stored in the separate organizational_memory_embeddings
// table (migration 000028), never as a column mutation on the version
// itself. Mirrors rag.ChunkEmbedding exactly; internal/memory cannot import
// internal/rag (see scripts/check-memory-fitness.sh), so this is a
// deliberate, small duplication rather than a shared type.
type EntryEmbedding struct {
	OrganizationID        string
	EntryID               string
	EmbeddingModelID      string
	EmbeddingModelVersion string
	EmbeddingDimension    int
	PromptTemplateVersion string
	InputHash             string
	Vector                []float32
	CreatedAt             time.Time
}

// ScoredEntry is one nearest-neighbor hit: EntryID plus the raw pgvector
// cosine distance (<=>) — lower is more similar.
type ScoredEntry struct {
	EntryID  string
	Distance float64
}

// BGEM3EntryEmbedding is R30's local, operational counterpart to
// EntryEmbedding — stored in organizational_memory_embeddings_bge_m3
// (migration 000032, vector(1024)), never organizational_memory_embeddings
// (vector(768), R29's frozen Gemini reference index). Mirrors
// rag.BGEM3ChunkEmbedding; internal/memory cannot import internal/rag, so
// this is the same deliberate, small duplication as EntryEmbedding itself.
type BGEM3EntryEmbedding struct {
	OrganizationID        string
	EntryID               string
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
// vector index over approved memory entries — additive and separate from
// EmbeddingRepository, never sharing a table or query path with it.
type BGEM3EmbeddingRepository interface {
	InsertBGEM3EntryEmbedding(ctx context.Context, embedding BGEM3EntryEmbedding) error
	NearestBGEM3Entries(ctx context.Context, organizationID, roleID string, queryVector []float32, limit int) ([]ScoredEntry, error)
}

// EmbeddingRepository is the persistence boundary for the derived vector
// index over approved memory entries — deliberately separate from
// Repository, same reasoning as rag.EmbeddingRepository.
type EmbeddingRepository interface {
	InsertEntryEmbedding(ctx context.Context, embedding EntryEmbedding) error
	// NearestEntries searches only within roleID's own memory — Search
	// never crosses role boundaries in R29 (see Manager.Search).
	NearestEntries(ctx context.Context, organizationID, roleID string, queryVector []float32, limit int) ([]ScoredEntry, error)
	// Search fuses exact-identifier and (when queryVector is non-empty)
	// vector channels by Reciprocal Rank Fusion, scoped to
	// organizationID+roleID+status=approved, returning full Entry values
	// ready to render — the equivalent of rag.Repository.Query for memory.
	Search(ctx context.Context, organizationID, roleID, queryText string, queryVector []float32, limit int) ([]Entry, error)
}
