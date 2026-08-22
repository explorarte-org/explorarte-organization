//go:build integration

package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// Recovery by succession is a claim about two rows changing together and one
// row never changing at all, and none of those are properties of the Go code:
// they are properties of the statements Postgres runs and the lock they run
// under. A fake store can show that Redrive sets some fields; only the real
// database can show that the successor and the stamp are inseparable, that a
// terminal task stays terminal, and that a recursive walk over redrive links
// counts the episodes that actually happened.
func TestRecoverySuccessorPostgreSQL(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	t.Run("a successor and its stamp become durable together", func(t *testing.T) {
		h.resetTasks(t)
		failedID, deadLetterID := driveToDeadLetter(t, h, "recovery-origin")

		successor := baseRequest("recovery-origin-episode-1")
		result, err := h.tasks.RedriveDeadLetter(h.ctx, tasks.RedriveCommand{
			DeadLetterID:        deadLetterID,
			Successor:           successor,
			ObservedChange:      "base commit 41ef0164 is no longer the target head",
			MaxRecoveryEpisodes: 3,
			ActorType:           "system",
			ActorID:             "executive-orchestrator",
		})
		if err != nil || !result.Created {
			t.Fatalf("redrive: created=%v err=%v", result.Created, err)
		}
		if result.Episode != 1 {
			t.Fatalf("first recovery of an original failure is episode 1, got %d", result.Episode)
		}
		if result.Successor.ID == failedID {
			t.Fatal("the successor must be a new task, not the failed one")
		}
		if result.Successor.Status != tasks.StatusReady {
			t.Fatalf("successor status=%q, want ready", result.Successor.Status)
		}

		stamped, err := h.tasks.GetDeadLetter(h.ctx, deadLetterID)
		if err != nil {
			t.Fatal(err)
		}
		if stamped.RedrivenAt == nil || stamped.RedriveTaskID == nil {
			t.Fatalf("dead letter must carry both redrive marks, got at=%v id=%v", stamped.RedrivenAt, stamped.RedriveTaskID)
		}
		if *stamped.RedriveTaskID != result.Successor.ID {
			t.Fatalf("redrive_task_id=%d, want the successor %d", *stamped.RedriveTaskID, result.Successor.ID)
		}

		// The whole point of succession: the terminal fact is untouched.
		failed, err := h.tasks.GetTask(h.ctx, failedID)
		if err != nil {
			t.Fatal(err)
		}
		if failed.Task.Status != tasks.StatusDeadLetter {
			t.Fatalf("the recovered task must stay dead_letter, got %q", failed.Task.Status)
		}

		assertEventRecorded(t, h, result.Successor.ID, "task.recovery_successor_created")
		assertEventRecorded(t, h, failedID, "task.recovery_successor_opened")
	})

	t.Run("redriving twice returns the successor that already exists", func(t *testing.T) {
		h.resetTasks(t)
		_, deadLetterID := driveToDeadLetter(t, h, "recovery-idempotent")

		command := tasks.RedriveCommand{
			DeadLetterID:        deadLetterID,
			Successor:           baseRequest("recovery-idempotent-episode-1"),
			ObservedChange:      "the gate that failed no longer exists at the target head",
			MaxRecoveryEpisodes: 3,
			ActorType:           "system",
			ActorID:             "executive-orchestrator",
		}
		first, err := h.tasks.RedriveDeadLetter(h.ctx, command)
		if err != nil || !first.Created {
			t.Fatalf("first redrive: created=%v err=%v", first.Created, err)
		}
		// A caller retrying after a lost response must not open a second
		// episode: at most one successor per dead letter is the invariant
		// that keeps the episode budget meaningful.
		command.Successor = baseRequest("recovery-idempotent-episode-1-again")
		second, err := h.tasks.RedriveDeadLetter(h.ctx, command)
		if err != nil {
			t.Fatalf("second redrive must succeed idempotently: %v", err)
		}
		if second.Created {
			t.Fatal("second redrive must not report a creation")
		}
		if second.Successor.ID != first.Successor.ID {
			t.Fatalf("second redrive returned task %d, want the existing successor %d", second.Successor.ID, first.Successor.ID)
		}
		var successors int
		if err := h.store.Pool().QueryRow(h.ctx,
			`SELECT count(*) FROM tasks WHERE idempotency_key LIKE 'recovery-idempotent-episode-1%'`).Scan(&successors); err != nil {
			t.Fatal(err)
		}
		if successors != 1 {
			t.Fatalf("exactly one successor must exist, found %d", successors)
		}
	})

	t.Run("episodes are counted from the links and bounded", func(t *testing.T) {
		h.resetTasks(t)
		_, firstDeadLetter := driveToDeadLetter(t, h, "recovery-chain")

		first, err := h.tasks.RedriveDeadLetter(h.ctx, tasks.RedriveCommand{
			DeadLetterID:        firstDeadLetter,
			Successor:           chainRequest("recovery-chain-episode-1"),
			ObservedChange:      "target head advanced past the failing commit",
			MaxRecoveryEpisodes: 2,
			ActorType:           "system", ActorID: "executive-orchestrator",
		})
		if err != nil || first.Episode != 1 {
			t.Fatalf("episode 1: %+v err=%v", first, err)
		}

		secondDeadLetter := failClaimedTask(t, h, first.Successor.ID)

		// The successor of a successor is episode 2. Nothing stores that
		// number: it is the length of the chain of redrive links that
		// really exist, so it cannot drift from what happened.
		exhausted := tasks.RedriveCommand{
			DeadLetterID:        secondDeadLetter,
			Successor:           chainRequest("recovery-chain-episode-2"),
			ObservedChange:      "target head advanced again",
			MaxRecoveryEpisodes: 1,
			ActorType:           "system", ActorID: "executive-orchestrator",
		}
		if _, err := h.tasks.RedriveDeadLetter(h.ctx, exhausted); !errors.Is(err, tasks.ErrRecoveryEpisodesExhausted) {
			t.Fatalf("episode 2 under a limit of 1 must be refused as exhausted, got %v", err)
		}
		var stampedAfterRefusal int
		if err := h.store.Pool().QueryRow(h.ctx,
			`SELECT count(*) FROM task_dead_letters WHERE id=$1 AND redrive_task_id IS NOT NULL`, secondDeadLetter).Scan(&stampedAfterRefusal); err != nil {
			t.Fatal(err)
		}
		if stampedAfterRefusal != 0 {
			t.Fatal("a refused recovery must leave no stamp behind")
		}
		var createdAfterRefusal int
		if err := h.store.Pool().QueryRow(h.ctx,
			`SELECT count(*) FROM tasks WHERE idempotency_key='recovery-chain-episode-2'`).Scan(&createdAfterRefusal); err != nil {
			t.Fatal(err)
		}
		if createdAfterRefusal != 0 {
			t.Fatal("a refused recovery must create no successor")
		}

		allowed := exhausted
		allowed.MaxRecoveryEpisodes = 2
		second, err := h.tasks.RedriveDeadLetter(h.ctx, allowed)
		if err != nil || second.Episode != 2 {
			t.Fatalf("episode 2 under a limit of 2: %+v err=%v", second, err)
		}
	})

	t.Run("a recovery without a stated change is refused", func(t *testing.T) {
		h.resetTasks(t)
		failedID, deadLetterID := driveToDeadLetter(t, h, "recovery-unjustified")

		command := tasks.RedriveCommand{
			DeadLetterID:        deadLetterID,
			Successor:           baseRequest("recovery-unjustified-episode-1"),
			MaxRecoveryEpisodes: 3,
			ActorType:           "system", ActorID: "executive-orchestrator",
		}
		if _, err := h.tasks.RedriveDeadLetter(h.ctx, command); !errors.Is(err, tasks.ErrInvalidInput) {
			t.Fatalf("recovery with no observed change must be refused, got %v", err)
		}

		// Reusing the failed task's own key would make the "successor"
		// resolve to the predecessor row, and recovery would report
		// success while scheduling nothing.
		command.ObservedChange = "nothing in particular"
		command.Successor = baseRequest("recovery-unjustified")
		if _, err := h.tasks.RedriveDeadLetter(h.ctx, command); !errors.Is(err, tasks.ErrInvalidInput) {
			t.Fatalf("a successor reusing the failed task's key must be refused, got %v", err)
		}
		failed, err := h.tasks.GetTask(h.ctx, failedID)
		if err != nil || failed.Task.Status != tasks.StatusDeadLetter {
			t.Fatalf("refused recoveries must not disturb the failed task: %+v err=%v", failed.Task.Status, err)
		}
	})
}

// chainRequest builds a successor that can itself be driven to a dead letter,
// so that a recovery of a recovery is reachable in a test.
func chainRequest(key string) tasks.CreateRequest {
	request := baseRequest(key)
	request.MaxAttempts = 1
	return request
}

// driveToDeadLetter exhausts a task's attempts and returns the failed task and
// the dead letter it produced.
func driveToDeadLetter(t *testing.T, h *harness, key string) (int64, int64) {
	t.Helper()
	request := baseRequest(key)
	request.MaxAttempts = 1
	created, inserted, err := h.tasks.CreateTask(h.ctx, request, "human", "eduardo")
	if err != nil || !inserted {
		t.Fatalf("create %s: inserted=%v err=%v", key, inserted, err)
	}
	return created.ID, failClaimedTask(t, h, created.ID)
}

// failClaimedTask consumes a task's last attempt and returns its dead letter.
func failClaimedTask(t *testing.T, h *harness, taskID int64) int64 {
	t.Helper()
	claimed, err := h.tasks.ClaimTaskByID(h.ctx, taskID, tasks.ClaimRequest{
		WorkerID: "recovery-worker", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("claim %d: %v", taskID, err)
	}
	command := tasks.LeaseCommand{
		TaskID: taskID, AttemptID: claimed.Attempt.ID,
		LeaseToken: claimed.LeaseToken, ActorID: "recovery-worker",
	}
	if _, err := h.tasks.StartAttempt(h.ctx, command); err != nil {
		t.Fatal(err)
	}
	dead, err := h.tasks.RecordAttemptResult(h.ctx, tasks.RecordAttemptResultCommand{
		LeaseCommand: command,
		Result: tasks.AttemptResult{
			Outcome: tasks.OutcomeRetryableFailure, FailureCode: "transient", Summary: "provider timed out",
		},
	})
	if err != nil || dead.Status != tasks.StatusDeadLetter {
		t.Fatalf("drive %d to dead letter: status=%q err=%v", taskID, dead.Status, err)
	}
	var deadLetterID int64
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT id FROM task_dead_letters WHERE task_id=$1 ORDER BY id DESC LIMIT 1`, taskID).Scan(&deadLetterID); err != nil {
		t.Fatal(err)
	}
	return deadLetterID
}

func assertEventRecorded(t *testing.T, h *harness, taskID int64, eventType string) {
	t.Helper()
	events, err := h.tasks.ListEvents(h.ctx, taskID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.EventType == eventType {
			return
		}
	}
	t.Fatalf("task %d has no %s event", taskID, eventType)
}
