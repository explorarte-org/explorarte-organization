package memory

import "context"

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
