package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

// recoveryChainDepthCap bounds the recursive walk over redrive links.
//
// The links form a DAG by construction -- every successor is created after
// the dead letter that points at it -- so a cycle would mean the table is
// already corrupt. The cap is here so that corruption surfaces as a bounded
// query rather than as an unbounded one holding a row lock.
const recoveryChainDepthCap = 1000

// Redrive creates a dead letter's recovery successor and stamps the link, in
// one transaction.
//
// Atomicity is the whole point. A successor without the stamp could be created
// again by the next caller; a stamp without a successor would record a
// recovery that never ran. Both halves become durable together or neither
// does.
func (s *Store) Redrive(ctx context.Context, input tasks.PreparedRedrive) (tasks.RedriveResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (tasks.RedriveResult, error) {
		var failedTaskID int64
		var alreadyRedriven *int64
		if err := tx.QueryRow(ctx, `
			SELECT task_id, redrive_task_id FROM task_dead_letters WHERE id=$1 FOR UPDATE
		`, input.DeadLetterID).Scan(&failedTaskID, &alreadyRedriven); err != nil {
			return tasks.RedriveResult{}, mapError(err)
		}

		priorEpisodes, err := recoveryChainDepth(ctx, tx, input.DeadLetterID)
		if err != nil {
			return tasks.RedriveResult{}, err
		}
		episode := priorEpisodes + 1

		// Already redriven: return the successor that exists rather than
		// creating a second one. The row lock above makes this the only
		// possible answer for a concurrent caller too, which is what
		// keeps "at most one successor per dead letter" true under
		// races and not merely by convention.
		if alreadyRedriven != nil {
			existing, scanErr := scanTask(tx.QueryRow(ctx, `
				SELECT `+taskColumns+` FROM tasks WHERE id=$1
			`, *alreadyRedriven))
			if scanErr != nil {
				return tasks.RedriveResult{}, scanErr
			}
			return tasks.RedriveResult{Successor: existing, Created: false, Episode: episode}, nil
		}

		if episode > input.MaxRecoveryEpisodes {
			return tasks.RedriveResult{}, fmt.Errorf("%w: dead letter %d would open episode %d of at most %d",
				tasks.ErrRecoveryEpisodesExhausted, input.DeadLetterID, episode, input.MaxRecoveryEpisodes)
		}

		created, err := createInTx(ctx, tx, input.Successor)
		if err != nil {
			return tasks.RedriveResult{}, err
		}
		// createInTx returns an existing row when the idempotency key was
		// already used. Adopting that row as this dead letter's successor
		// would link a task created for some other reason into a recovery
		// chain it never belonged to, and would report a recovery as
		// having happened when nothing new was scheduled.
		if !created.Created {
			return tasks.RedriveResult{}, fmt.Errorf("%w: successor idempotency key already belongs to task %d",
				tasks.ErrConflict, created.Task.ID)
		}

		stamped, err := tx.Exec(ctx, `
			UPDATE task_dead_letters SET redriven_at=clock_timestamp(), redrive_task_id=$2
			WHERE id=$1 AND redrive_task_id IS NULL
		`, input.DeadLetterID, created.Task.ID)
		if err != nil {
			return tasks.RedriveResult{}, mapError(err)
		}
		if stamped.RowsAffected() != 1 {
			// Unreachable while the FOR UPDATE lock above is held, which
			// is exactly why it must not be ignored: if it ever fires,
			// the locking assumption this function rests on is wrong.
			return tasks.RedriveResult{}, fmt.Errorf("%w: dead letter %d was redriven concurrently",
				tasks.ErrConflict, input.DeadLetterID)
		}

		if err := appendTaskEvent(ctx, tx, created.Task, nil, nil, "task.recovery_successor_created",
			input.ActorType, input.ActorID, map[string]any{
				"dead_letter_id":    input.DeadLetterID,
				"recovered_task_id": failedTaskID,
				"recovery_episode":  episode,
				"observed_change":   input.ObservedChange,
			}, input.OutboxMaxAttempts); err != nil {
			return tasks.RedriveResult{}, err
		}

		// The failed task's own timeline records that a successor exists.
		// Without this, the only way to discover that a dead-lettered task
		// was recovered is to query the dead-letter table sideways, and
		// the task that everyone actually looks at stays silent about it.
		failed, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id=$1`, failedTaskID))
		if err != nil {
			if errors.Is(err, tasks.ErrNotFound) {
				return tasks.RedriveResult{}, fmt.Errorf("%w: dead letter %d references missing task %d",
					tasks.ErrConflict, input.DeadLetterID, failedTaskID)
			}
			return tasks.RedriveResult{}, err
		}
		if err := appendTaskEvent(ctx, tx, failed, nil, nil, "task.recovery_successor_opened",
			input.ActorType, input.ActorID, map[string]any{
				"dead_letter_id":    input.DeadLetterID,
				"successor_task_id": created.Task.ID,
				"recovery_episode":  episode,
				"observed_change":   input.ObservedChange,
			}, input.OutboxMaxAttempts); err != nil {
			return tasks.RedriveResult{}, err
		}

		return tasks.RedriveResult{Successor: created.Task, Created: true, Episode: episode}, nil
	})
}

// recoveryChainDepth counts how many recovery episodes already happened before
// the given dead letter, by walking the redrive links backwards in time.
//
// The count is derived from the links themselves rather than kept in a column,
// so it cannot drift from what actually happened: a chain is exactly as long
// as the successors that were really created.
func recoveryChainDepth(ctx context.Context, tx pgx.Tx, deadLetterID int64) (int, error) {
	var depth int
	if err := tx.QueryRow(ctx, `
		WITH RECURSIVE chain AS (
			SELECT id, task_id, 0 AS depth FROM task_dead_letters WHERE id=$1
			UNION ALL
			SELECT prior.id, prior.task_id, chain.depth+1
			FROM task_dead_letters prior
			JOIN chain ON prior.redrive_task_id = chain.task_id
			WHERE chain.depth < $2
		)
		SELECT COALESCE(MAX(depth),0) FROM chain
	`, deadLetterID, recoveryChainDepthCap).Scan(&depth); err != nil {
		return 0, mapError(err)
	}
	return depth, nil
}

var _ tasks.RedrivePersistence = (*Store)(nil)
