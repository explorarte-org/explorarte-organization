package driver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RootCoordinator guards multi-replica root advancement. Exactly one driver
// instance may advance a root at any moment.
type RootCoordinator interface {
	TryClaimRoot(ctx context.Context, orgID string, rootID int64) (release func(), claimed bool, err error)
}

// PostgresRootCoordinator uses PostgreSQL session-level advisory locks to guarantee
// multi-replica mutual exclusion without requiring a separate lease table.
// If a process crashes or network drops, PostgreSQL automatically releases the
// session lock when the client connection terminates.
type PostgresRootCoordinator struct {
	pool *pgxpool.Pool
}

func NewPostgresRootCoordinator(pool *pgxpool.Pool) *PostgresRootCoordinator {
	return &PostgresRootCoordinator{pool: pool}
}

func (c *PostgresRootCoordinator) TryClaimRoot(ctx context.Context, orgID string, rootID int64) (func(), bool, error) {
	if c.pool == nil {
		return nil, false, fmt.Errorf("postgres pool is nil")
	}
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire connection for root advisory lock: %w", err)
	}

	lockKey := fmt.Sprintf("executive-root:%s:%d", orgID, rootID)
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", lockKey).Scan(&locked); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("query pg_try_advisory_lock: %w", err)
	}

	if !locked {
		conn.Release()
		return nil, false, nil
	}

	release := sync.OnceFunc(func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey)
		conn.Release()
	})

	return release, true, nil
}

// MemoryRootCoordinator provides in-memory root mutual exclusion for unit tests.
type MemoryRootCoordinator struct {
	mu     sync.Mutex
	claims map[string]bool
}

func NewMemoryRootCoordinator() *MemoryRootCoordinator {
	return &MemoryRootCoordinator{claims: make(map[string]bool)}
}

func (m *MemoryRootCoordinator) TryClaimRoot(ctx context.Context, orgID string, rootID int64) (func(), bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%d", orgID, rootID)
	if m.claims[key] {
		return nil, false, nil
	}
	m.claims[key] = true

	release := sync.OnceFunc(func() {
		m.mu.Lock()
		delete(m.claims, key)
		m.mu.Unlock()
	})

	return release, true, nil
}
