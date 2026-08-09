package memory

import (
	"context"
	"fmt"
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

// EmbeddingIdentity mirrors rag.EmbeddingIdentity exactly — see that
// type's doc comment. internal/memory cannot import internal/rag, so this
// is the same deliberate, small duplication as EntryEmbedding/
// BGEM3EntryEmbedding.
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

// EmbeddingRepository is the persistence boundary for the derived vector
// index over approved memory entries — deliberately separate from
// Repository, same reasoning as rag.EmbeddingRepository.
type EmbeddingRepository interface {
	InsertEntryEmbedding(ctx context.Context, embedding EntryEmbedding) error
	// NearestEntries searches only within roleID's own memory — Search
	// never crosses role boundaries in R29 (see Manager.Search). It does
	// not filter by embedding identity — it exists for tests/direct
	// access, not the production path (Manager.Search always goes through
	// Search below, never this method).
	NearestEntries(ctx context.Context, organizationID, roleID string, queryVector []float32, limit int) ([]ScoredEntry, error)
	// Search fuses exact-identifier and (when queryVector is non-empty)
	// vector channels by Reciprocal Rank Fusion, scoped to
	// organizationID+roleID+status=approved, returning full Entry values
	// ready to render — the equivalent of rag.Repository.Query for memory.
	// identity+promptTemplateVersion are required whenever queryVector is
	// non-empty — see EmbeddingIdentity's doc comment for why: an entry's
	// embedding table primary key allows more than one row per entry
	// (re-embedding under a new revision is a new row), so without this
	// filter the vector channel could return more than one row for the
	// same entry, awarding it multiple RRF votes and potentially mixing
	// incompatible embedding spaces that merely share a dimension.
	Search(ctx context.Context, organizationID, roleID, queryText string, queryVector []float32, identity EmbeddingIdentity, promptTemplateVersion string, limit int) ([]Entry, error)
}
