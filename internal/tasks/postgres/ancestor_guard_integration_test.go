//go:build integration

package postgres_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// A task is never claimed while an ancestor's state withdraws the work beneath
// it, decided atomically INSIDE the claim transaction. These run against real
// PostgreSQL, through both claim paths (ClaimTaskByID -- the Executive's -- and
// the batch ClaimTasks).

type guardTree struct {
	root, plan, worker tasks.Task
}

func createChild(t *testing.T, h *harness, key, class string, parent int64) tasks.Task {
	t.Helper()
	request := baseRequest(key)
	request.TaskClass = class
	request.CausationID = fmt.Sprintf("task:%d", parent)
	created, _, err := h.tasks.CreateTask(h.ctx, request, "human", "eduardo")
	if err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
	return created
}

// newGuardTree builds root(owner.goal) -> plan(coordination.department_plan)
// -> worker(engineering.work), the shape an Executive campaign has, with the
// plan completed (as a real plan is by the time its workers exist) and the
// root in rootStatus.
func newGuardTree(t *testing.T, h *harness, suffix, rootStatus string) guardTree {
	t.Helper()
	request := baseRequest("guard-root-" + suffix)
	request.TaskClass = "owner.goal"
	root, _, err := h.tasks.CreateTask(h.ctx, request, "human", "eduardo")
	if err != nil {
		t.Fatal(err)
	}
	plan := createChild(t, h, "guard-plan-"+suffix, "coordination.department_plan", root.ID)
	worker := createChild(t, h, "guard-worker-"+suffix, "engineering.work", plan.ID)
	setStatus(t, h, plan.ID, "completed")
	setStatus(t, h, root.ID, rootStatus)
	return guardTree{root: root, plan: plan, worker: worker}
}

func setStatus(t *testing.T, h *harness, id int64, status string) {
	t.Helper()
	// tasks_check: a status is terminal exactly when terminal_at is set.
	if _, err := h.store.Pool().Exec(h.ctx, `
		UPDATE tasks SET status=$2::text,
		       terminal_at = CASE WHEN $2::text IN ('completed','no_action','failed','dead_letter','rejected','cancelled') THEN clock_timestamp() ELSE NULL END
		WHERE id=$1`, id, status); err != nil {
		t.Fatalf("set task %d to %s: %v", id, status, err)
	}
}

func claimSpecific(h *harness, id int64) (tasks.ClaimedTask, error) {
	return h.tasks.ClaimTaskByID(h.ctx, id, tasks.ClaimRequest{WorkerID: "guard-worker", LeaseDuration: time.Minute})
}

// fingerprint hashes the task row and counts everything a claim would write.
func fingerprint(t *testing.T, h *harness, id int64) string {
	t.Helper()
	var value string
	if err := h.store.Pool().QueryRow(h.ctx, `
		SELECT md5(t::text) || ':' || (SELECT count(*) FROM task_attempts WHERE task_id=t.id)
		    || ':' || (SELECT count(*) FROM task_leases WHERE task_id=t.id)
		    || ':' || (SELECT count(*) FROM task_events WHERE task_id=t.id)
		FROM tasks t WHERE t.id=$1`, id).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAncestorGuardPostgreSQL17(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	// The semantics, for EVERY status of the root above the worker.
	t.Run("only withdrawing ancestor states refuse the claim", func(t *testing.T) {
		for _, status := range []string{"pending", "ready", "leased", "running", "awaiting_verification", "retry_wait", "completed", "no_action",
			"blocked", "cancelled", "rejected", "failed", "dead_letter"} {
			h.resetTasks(t)
			tree := newGuardTree(t, h, status, status)
			_, err := claimSpecific(h, tree.worker.ID)
			wantBlocked := tasks.Status(status).BlocksDescendantClaim()
			if wantBlocked && !errors.Is(err, tasks.ErrAncestorBlocked) {
				t.Errorf("root %s: want ErrAncestorBlocked, got: %v", status, err)
			}
			if !wantBlocked && err != nil {
				t.Errorf("root %s must not block its descendants, got: %v", status, err)
			}
		}
	})

	// The refusal has no side effect: the row is exactly as it was.
	t.Run("a refused claim writes nothing", func(t *testing.T) {
		h.resetTasks(t)
		tree := newGuardTree(t, h, "nowrite", "blocked")
		before := fingerprint(t, h, tree.worker.ID)
		for i := 0; i < 3; i++ {
			if _, err := claimSpecific(h, tree.worker.ID); !errors.Is(err, tasks.ErrAncestorBlocked) {
				t.Fatalf("attempt %d: %v", i, err)
			}
		}
		if after := fingerprint(t, h, tree.worker.ID); after != before {
			t.Fatalf("the refused task changed: %s -> %s", before, after)
		}
		var status string
		var attempts int
		if err := h.store.Pool().QueryRow(h.ctx, `SELECT status, attempt_count FROM tasks WHERE id=$1`, tree.worker.ID).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		if status != "ready" || attempts != 0 {
			t.Fatalf("worker is %s with %d attempts; must stay ready with none", status, attempts)
		}
	})

	// The batch claim skips such a task and, crucially, is not starved by it.
	t.Run("the batch claim skips withdrawn work without starving the rest", func(t *testing.T) {
		h.resetTasks(t)
		tree := newGuardTree(t, h, "batch", "blocked")
		setPriority := func(id int64, p int) {
			if _, err := h.store.Pool().Exec(h.ctx, `UPDATE tasks SET priority=$2 WHERE id=$1`, id, p); err != nil {
				t.Fatal(err)
			}
		}
		setPriority(tree.worker.ID, 100) // outranks everything, and cannot run
		free := baseRequest("guard-unrelated")
		free.Priority = 1
		unrelated, _, err := h.tasks.CreateTask(h.ctx, free, "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		before := fingerprint(t, h, tree.worker.ID)
		claimed, err := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: "batch-worker", BatchSize: 1, LeaseDuration: time.Minute})
		if err != nil || len(claimed) != 1 || claimed[0].Task.ID != unrelated.ID {
			t.Fatalf("batch of 1 claimed %+v (err %v); want only the unrelated task %d, not starved by the withdrawn worker", claimed, err, unrelated.ID)
		}
		if after := fingerprint(t, h, tree.worker.ID); after != before {
			t.Fatalf("the skipped task changed: %s -> %s", before, after)
		}
	})

	// Nothing is cancelled or failed for good: the work resumes with its scope.
	t.Run("the work resumes when the ancestor is unblocked", func(t *testing.T) {
		h.resetTasks(t)
		tree := newGuardTree(t, h, "resume", "ready")
		if _, err := h.tasks.BlockTask(h.ctx, tasks.BlockCommand{TaskID: tree.root.ID, ReasonCode: "test", Reason: "scope withdrawn", ActorType: "service", ActorID: "test"}); err != nil {
			t.Fatal(err)
		}
		if _, err := claimSpecific(h, tree.worker.ID); !errors.Is(err, tasks.ErrAncestorBlocked) {
			t.Fatalf("while blocked: %v", err)
		}
		if _, err := h.tasks.UnblockTask(h.ctx, tasks.UnblockCommand{TaskID: tree.root.ID, ActorType: "service", ActorID: "test"}); err != nil {
			t.Fatal(err)
		}
		if _, err := claimSpecific(h, tree.worker.ID); err != nil {
			t.Fatalf("after unblock the work must be claimable: %v", err)
		}
	})

	// A failed chat turn must not withdraw the review it requested (production
	// task 837 beneath failed turn 836); the same shape under an execution
	// scope is refused.
	t.Run("a failed chat turn does not withdraw what it requested", func(t *testing.T) {
		h.resetTasks(t)
		turnRequest := baseRequest("guard-turn")
		turnRequest.TaskClass = tasks.TaskClassCEOChatTurn
		turn, _, err := h.tasks.CreateTask(h.ctx, turnRequest, "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		review := createChild(t, h, "guard-review", "campaign.financial_review", turn.ID)
		setStatus(t, h, turn.ID, "failed")
		if _, err := claimSpecific(h, review.ID); err != nil {
			t.Fatalf("the review beneath a failed chat turn must still run: %v", err)
		}

		h.resetTasks(t)
		scopeRequest := baseRequest("guard-scope")
		scopeRequest.TaskClass = "owner.goal"
		scope, _, err := h.tasks.CreateTask(h.ctx, scopeRequest, "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		child := createChild(t, h, "guard-scoped-child", "campaign.financial_review", scope.ID)
		setStatus(t, h, scope.ID, "failed")
		if _, err := claimSpecific(h, child.ID); !errors.Is(err, tasks.ErrAncestorBlocked) {
			t.Fatalf("the same shape under a failed execution scope must be refused, got: %v", err)
		}
	})

	// A withdrawn scope reaches every generation, not just the parent.
	t.Run("the whole ancestor chain is consulted", func(t *testing.T) {
		h.resetTasks(t)
		tree := newGuardTree(t, h, "deep", "blocked")
		grandchild := createChild(t, h, "guard-grandchild", "engineering.work", tree.worker.ID)
		setStatus(t, h, tree.worker.ID, "completed")
		if _, err := claimSpecific(h, grandchild.ID); !errors.Is(err, tasks.ErrAncestorBlocked) {
			t.Fatalf("a grandchild beneath a blocked root must be refused, got: %v", err)
		}
	})

	// THE RACE. Blocking the root and claiming beneath it run concurrently.
	// Whatever the interleaving, no claim may BEGIN after the block committed.
	t.Run("a claim racing a block never starts after the block", func(t *testing.T) {
		const rounds = 120
		claimedFirst, refused := 0, 0
		for i := 0; i < rounds; i++ {
			h.resetTasks(t)
			tree := newGuardTree(t, h, fmt.Sprintf("race-%d", i), "ready")
			var wg sync.WaitGroup
			start := make(chan struct{})
			var claimErr, blockErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, claimErr = claimSpecific(h, tree.worker.ID)
			}()
			go func() {
				defer wg.Done()
				<-start
				_, blockErr = h.tasks.BlockTask(h.ctx, tasks.BlockCommand{TaskID: tree.root.ID, ReasonCode: "race", Reason: "race", ActorType: "service", ActorID: "test"})
			}()
			close(start)
			wg.Wait()
			if blockErr != nil {
				t.Fatalf("round %d: block: %v", i, blockErr)
			}
			if claimErr != nil && !errors.Is(claimErr, tasks.ErrAncestorBlocked) {
				t.Fatalf("round %d: claim: %v", i, claimErr)
			}
			var leases int
			var leaseAt, blockedAt time.Time
			if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM task_leases WHERE task_id=$1`, tree.worker.ID).Scan(&leases); err != nil {
				t.Fatal(err)
			}
			if err := h.store.Pool().QueryRow(h.ctx, `SELECT occurred_at FROM task_events WHERE task_id=$1 AND event_type='task.blocked' ORDER BY id DESC LIMIT 1`, tree.root.ID).Scan(&blockedAt); err != nil {
				t.Fatal(err)
			}
			if claimErr == nil {
				claimedFirst++
				if leases != 1 {
					t.Fatalf("round %d: claim succeeded but %d leases exist", i, leases)
				}
				if err := h.store.Pool().QueryRow(h.ctx, `SELECT issued_at FROM task_leases WHERE task_id=$1`, tree.worker.ID).Scan(&leaseAt); err != nil {
					t.Fatal(err)
				}
				if leaseAt.After(blockedAt) {
					t.Fatalf("round %d: the lease was issued at %s, AFTER the root's block committed at %s -- work escaped a withdrawn scope", i, leaseAt, blockedAt)
				}
			} else {
				refused++
				if leases != 0 {
					t.Fatalf("round %d: claim was refused yet %d leases exist", i, leases)
				}
			}
		}
		t.Logf("race outcomes over %d rounds: claim won %d, block won (claim refused) %d", rounds, claimedFirst, refused)
	})
}
