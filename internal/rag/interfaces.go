package rag

import "context"

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
}

type AuthorizationRequest struct {
	OrganizationID string
	ActorRoleID    string
	CapabilityID   string
	ResourceType   string
	ResourceID     string
	ActionDigest   string
}

type AuthorizationGate interface {
	Authorize(context.Context, AuthorizationRequest) error
}

type NamespaceResolver interface {
	ResolveNamespace(ctx context.Context, organizationID, actorRoleID string, kind NamespaceKind) (string, error)
}

type EmbeddingRef struct {
	ModelID   string
	Version   string
	Dimension int
	Vector    []float32
}

// EmbeddingProvider is the boundary for a future vector retrieval backend.
// R20 ships no concrete production implementation; PostgreSQL FTS is the
// productive retrieval path and chunk persistence never requires this port.
type EmbeddingProvider interface {
	Embed(ctx context.Context, chunkerID, text string) (EmbeddingRef, error)
}
