package memory

import "context"

type ApprovedFilter struct {
	OrganizationID string
	RoleID         string
	Limit          int
}

type SaveCommand struct {
	Entry            Entry
	ExpectedRevision int64
	ActorID          string
	Reason           string
}

type Repository interface {
	// CreateCandidate persists a candidate and its immutable content version.
	// Reused is true only when an exact canonical duplicate already exists and
	// the operation is therefore idempotent.
	CreateCandidate(context.Context, Entry, string) (entry Entry, reused bool, err error)
	Get(context.Context, string) (Entry, error)
	Save(context.Context, SaveCommand) (Entry, error)
	ListApproved(context.Context, ApprovedFilter) ([]Entry, error)
}
