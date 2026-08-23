// Package postgres persists the composition lifecycle.
//
// Every write is one step. There is no Save(world) that rewrites everything,
// because a whole-world write assumes the writer's copy is still the truth,
// and the entire reason this state is durable is that another process may
// have moved it since. A step carries the state it expects to find, and the
// UPDATE fails rather than overwriting a world that changed underneath it.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mireuz13/explorarte-organization/internal/composition"
)

// ErrStaleStep reports that the world moved between reading it and writing to
// it. It is not a failure of the reconciler: it means another turn got there
// first, and the correct response is to observe again rather than retry the
// step that was computed against the older world.
var ErrStaleStep = errors.New("composition: the episode changed since it was observed")

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("composition store requires a PostgreSQL pool")
	}
	return &Store{pool: pool}, nil
}

// Load rebuilds the whole world in one snapshot, inside a repeatable-read
// transaction so the episodes and the bindings that reference them are read
// from the same instant. Reading them separately can observe a binding whose
// episode has not appeared yet and reject a world that is actually fine.
func (s *Store) Load(ctx context.Context) (*composition.World, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
SELECT id, component_id, state, lease_expires_at, adjudicated
FROM composition_episodes
ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var episodes []composition.Episode
	for rows.Next() {
		var e composition.Episode
		if err := rows.Scan(&e.ID, &e.ComponentID, &e.State, &e.LeaseExpiresAt, &e.Adjudicated); err != nil {
			rows.Close()
			return nil, err
		}
		episodes = append(episodes, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	bindingRows, err := tx.Query(ctx, `
SELECT consumer_episode, key, provider_episode
FROM composition_bindings
ORDER BY consumer_episode, key`)
	if err != nil {
		return nil, err
	}
	var bindings []composition.CommittedBinding
	for bindingRows.Next() {
		var b composition.CommittedBinding
		if err := bindingRows.Scan(&b.Consumer, &b.Key, &b.Provider); err != nil {
			bindingRows.Close()
			return nil, err
		}
		bindings = append(bindings, b)
	}
	bindingRows.Close()
	if err := bindingRows.Err(); err != nil {
		return nil, err
	}
	return composition.LoadWorld(episodes, bindings)
}

// Start records a new episode in Reloading.
func (s *Store) Start(ctx context.Context, id composition.EpisodeID, componentID string, leaseExpiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO composition_episodes (id, component_id, state, lease_expires_at)
VALUES ($1, $2, 'reloading', $3)`, id, componentID, leaseExpiresAt)
	return err
}

// Heartbeat extends a lease, and refuses to extend one that already lapsed.
//
// The WHERE clause is the guard, not a read-then-write in Go: two processes
// racing to renew cannot both decide the lease was still valid, because
// Postgres evaluates lease_expires_at > $3 against the row it is locking.
func (s *Store) Heartbeat(ctx context.Context, id composition.EpisodeID, until, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE composition_episodes
   SET lease_expires_at = $2, updated_at = now()
 WHERE id = $1
   AND state IN ('active', 'reloading', 'unloading')
   AND lease_expires_at > $3`, id, until, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q cannot renew a lapsed lease", ErrStaleStep, id)
	}
	return nil
}

// Bind commits a consumer episode to a provider episode for a key. The
// provider must still be Active at write time; a provider that started
// leaving between the decision and the write must not acquire a new consumer.
func (s *Store) Bind(ctx context.Context, b composition.CommittedBinding) error {
	tag, err := s.pool.Exec(ctx, `
INSERT INTO composition_bindings (consumer_episode, key, provider_episode)
SELECT $1, $2, $3
 WHERE EXISTS (SELECT 1 FROM composition_episodes WHERE id = $3 AND state = 'active')
ON CONFLICT (consumer_episode, key) DO NOTHING`, b.Consumer, b.Key, b.Provider)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: provider %q is not active, or %q is already committed for %q",
			ErrStaleStep, b.Provider, b.Consumer, b.Key)
	}
	return nil
}

// ApplyStep writes exactly one lifecycle move, conditional on the episode
// still being in the state the step was computed against.
//
// expectedState is what makes a stale step fail loudly instead of silently
// undoing somebody else's work. The reconciler observes, decides, and writes;
// between the observing and the writing the world is free to move, and when
// it does the right answer is to observe again, never to force the decision
// that was made about a world that no longer exists.
func (s *Store) ApplyStep(ctx context.Context, step composition.Step, expectedState composition.Lifecycle, now time.Time) error {
	var (
		sql  string
		args []any
	)
	switch step.Kind {
	case composition.StepAdjudicate:
		// Only a lapsed lease may be adjudicated, and the lapse is
		// checked here rather than trusted from the caller's clock.
		sql = `
UPDATE composition_episodes
   SET state = 'failed', adjudicated = TRUE, updated_at = now()
 WHERE id = $1 AND state = $2 AND lease_expires_at <= $3`
		args = []any{step.Episode, string(expectedState), now}
	case composition.StepLeave:
		sql = `
UPDATE composition_episodes
   SET state = 'unloading', updated_at = now()
 WHERE id = $1 AND state = $2 AND lease_expires_at > $3`
		args = []any{step.Episode, string(expectedState), now}
	case composition.StepUnload:
		// The teardown gate lives in SQL as well as in the domain: no
		// live holder may exist at the instant the row changes. A
		// check that passed a moment ago in Go is not the same claim.
		sql = `
UPDATE composition_episodes
   SET state = 'inactive', updated_at = now()
 WHERE id = $1 AND state = $2
   AND NOT EXISTS (
       SELECT 1 FROM composition_bindings b
       JOIN composition_episodes c ON c.id = b.consumer_episode
       WHERE b.provider_episode = $1
         AND c.state IN ('active', 'reloading', 'unloading')
         AND c.lease_expires_at > $3)`
		args = []any{step.Episode, string(expectedState), now}
	case composition.StepActivate:
		sql = `
UPDATE composition_episodes
   SET state = 'active', updated_at = now()
 WHERE id = $1 AND state = $2 AND lease_expires_at > $3`
		args = []any{step.Episode, string(expectedState), now}
	default:
		return fmt.Errorf("composition: unknown step %q", step.Kind)
	}
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s on %q expected state %q", ErrStaleStep, step.Kind, step.Episode, expectedState)
	}
	return nil
}
