package driver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RootClaim represents an active, pinned root ownership lease.
// The underlying physical database session remains pinned until Release is called.
type RootClaim interface {
	Release(ctx context.Context) error
	SessionPID() uint32
}

// RootCoordinator guards multi-replica root advancement. Exactly one driver
// instance may advance a root at any moment.
type RootCoordinator interface {
	TryClaimRoot(ctx context.Context, orgID string, rootID int64) (claim RootClaim, claimed bool, err error)
}

// PostgresRootClaim pins the dedicated pgx connection acquired from the pool.
type PostgresRootClaim struct {
	conn    *pgxpool.Conn
	lockKey string
	pid     uint32
	once    sync.Once
	err     error
}

func (c *PostgresRootClaim) SessionPID() uint32 {
	return c.pid
}

// Release unlocks the advisory lock on the same physical PostgreSQL session and returns
// the connection to the pool. If unlocking fails, times out, or the lock release state
// is uncertain, the session is forcibly destroyed (hijacked and closed) to guarantee that
// no locked session enters the pool and that PostgreSQL automatically drops all session
// advisory locks upon backend termination.
func (c *PostgresRootClaim) Release(ctx context.Context) error {
	c.once.Do(func() {
		if c.conn == nil {
			return
		}
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var unlocked bool
		queryErr := c.conn.QueryRow(unlockCtx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", c.lockKey).Scan(&unlocked)

		if queryErr != nil || !unlocked {
			// Fail-safe: connection state is uncertain. Destroy the session.
			rawConn := c.conn.Hijack()
			_ = rawConn.Close(context.Background())
			if queryErr != nil {
				c.err = fmt.Errorf("advisory unlock failed on session pid %d (session destroyed): %w", c.pid, queryErr)
			} else {
				c.err = fmt.Errorf("advisory unlock returned false on session pid %d (session destroyed)", c.pid)
			}
			return
		}

		// Unlock cleanly succeeded on the exact same session. Return to pool.
		c.conn.Release()
	})
	return c.err
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

func (c *PostgresRootCoordinator) TryClaimRoot(ctx context.Context, orgID string, rootID int64) (RootClaim, bool, error) {
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

	pid := conn.Conn().PgConn().PID()
	claim := &PostgresRootClaim{
		conn:    conn,
		lockKey: lockKey,
		pid:     pid,
	}

	return claim, true, nil
}

// MemoryRootClaim provides an in-memory RootClaim implementation for unit tests.
type MemoryRootClaim struct {
	release func()
	once    sync.Once
}

func (m *MemoryRootClaim) Release(ctx context.Context) error {
	m.once.Do(m.release)
	return nil
}

func (m *MemoryRootClaim) SessionPID() uint32 {
	return 0
}

// MemoryRootCoordinator provides in-memory root mutual exclusion for unit tests.
type MemoryRootCoordinator struct {
	mu     sync.Mutex
	claims map[string]bool
}

func NewMemoryRootCoordinator() *MemoryRootCoordinator {
	return &MemoryRootCoordinator{claims: make(map[string]bool)}
}

func (m *MemoryRootCoordinator) TryClaimRoot(ctx context.Context, orgID string, rootID int64) (RootClaim, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%d", orgID, rootID)
	if m.claims[key] {
		return nil, false, nil
	}
	m.claims[key] = true

	claim := &MemoryRootClaim{
		release: func() {
			m.mu.Lock()
			delete(m.claims, key)
			m.mu.Unlock()
		},
	}

	return claim, true, nil
}
