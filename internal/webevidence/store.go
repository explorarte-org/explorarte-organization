package webevidence

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("webevidence: evidence not found or expired")

// Store is the durable-but-ephemeral persistence boundary: every method
// that reads must treat an expired row as absent (ErrNotFound), never
// return it, regardless of whether a reaper has physically deleted it
// yet.
type Store interface {
	Save(ctx context.Context, evidence Evidence) error
	Get(ctx context.Context, organizationID, id string, now time.Time) (Evidence, error)
	ListForTask(ctx context.Context, organizationID string, taskID int64, now time.Time) ([]Evidence, error)
	// Reap deletes rows whose expires_at is at or before now, up to limit
	// per call, returning how many were removed — bounded so a large
	// backlog cannot turn one reap call into an unbounded transaction.
	Reap(ctx context.Context, now time.Time, limit int) (int, error)
}
