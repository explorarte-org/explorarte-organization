//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The coordination hold asserts atomicity in one concrete SQL statement: the
// task and its hold are written by the same INSERT, so no instant exists in
// which the row is durable and claimable without its coordination.
//
// A fake cannot demonstrate that. A fake can only show that some code path
// sets a field; whether Postgres accepts the statement, and whether the
// columns are actually written, is a property of the statement itself. This
// suite exists because the first version of this fix passed every unit test
// while the INSERT named 17 columns and was handed 19 arguments -- it would
// have failed at bind time on the first real held child.
func TestCoordinationHoldPostgreSQL(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	t.Run("a held task is created blocked in one statement", func(t *testing.T) {
		h.resetTasks(t)
		request := baseRequest("hold-create")
		request.HoldForCoordination = true
		created, inserted, err := h.tasks.CreateTask(h.ctx, request, "system", "executive-orchestrator")
		if err != nil || !inserted {
			t.Fatalf("create held task: inserted=%v err=%v", inserted, err)
		}
		if created.Status != tasks.StatusBlocked {
			t.Fatalf("a held task must be born blocked, got %q", created.Status)
		}
		if created.StatusReasonCode == nil || *created.StatusReasonCode != tasks.ReasonCodeCoordinationHold {
			t.Fatalf("status_reason_code must be %q, got %v", tasks.ReasonCodeCoordinationHold, created.StatusReasonCode)
		}
		if created.StatusReason == nil || *created.StatusReason == "" {
			t.Fatal("status_reason must be persisted alongside the code")
		}
		// Read the columns back from the row rather than trusting the
		// returned struct: the point of this test is that the INSERT wrote
		// them, not that the Go value carried them.
		var status, reasonCode, reason string
		if err = h.store.Pool().QueryRow(h.ctx,
			`SELECT status,status_reason_code,status_reason FROM tasks WHERE id=$1`, created.ID,
		).Scan(&status, &reasonCode, &reason); err != nil {
			t.Fatal(err)
		}
		if status != string(tasks.StatusBlocked) || reasonCode != tasks.ReasonCodeCoordinationHold || reason == "" {
			t.Fatalf("durable row does not carry the hold: status=%q reason_code=%q reason=%q", status, reasonCode, reason)
		}
		// Exactly one INSERT: a hold applied by a follow-up UPDATE would
		// leave the task claimable in between, and would show up here as a
		// second version.
		if created.Version != 1 {
			t.Fatalf("the hold must be written by the creating statement, but the row is already at version %d", created.Version)
		}

		claimed, err := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: "worker-01", BatchSize: 10, LeaseDuration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 0 {
			t.Fatalf("a held task must not be claimable, got %d claims", len(claimed))
		}
	})

	t.Run("releasing the hold publishes an independent task", func(t *testing.T) {
		h.resetTasks(t)
		request := baseRequest("hold-release")
		request.HoldForCoordination = true
		created, _, err := h.tasks.CreateTask(h.ctx, request, "system", "executive-orchestrator")
		if err != nil {
			t.Fatal(err)
		}
		published, err := h.tasks.ReleaseCoordinationHold(h.ctx, tasks.ReleaseCoordinationHoldCommand{
			TaskID: created.ID, ActorType: "system", ActorID: "executive-orchestrator",
		})
		if err != nil {
			t.Fatal(err)
		}
		if published.Status != tasks.StatusReady {
			t.Fatalf("an independent published task must be ready, got %q", published.Status)
		}
		if published.StatusReasonCode != nil {
			t.Fatalf("the hold reason must be cleared on publication, got %v", published.StatusReasonCode)
		}
		claimed := claimOne(t, h, "worker-01")
		if claimed.Task.ID != created.ID {
			t.Fatalf("a published task must be claimable, claimed %d", claimed.Task.ID)
		}

		// Releasing again is a no-op, which is what makes a resumed
		// orchestrator safe to re-run the whole sequence.
		again, err := h.tasks.ReleaseCoordinationHold(h.ctx, tasks.ReleaseCoordinationHoldCommand{
			TaskID: created.ID, ActorType: "system", ActorID: "executive-orchestrator",
		})
		if err != nil {
			t.Fatalf("releasing an already published task must not error: %v", err)
		}
		if again.Version != claimed.Task.Version {
			t.Fatalf("a second release must not transition the task again: version %d became %d", claimed.Task.Version, again.Version)
		}
	})

	t.Run("publication does not bypass the dependency barrier", func(t *testing.T) {
		h.resetTasks(t)
		prerequisite, _, err := h.tasks.CreateTask(h.ctx, baseRequest("hold-dependency-a"), "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		dependent := baseRequest("hold-dependency-b")
		dependent.HoldForCoordination = true
		dependent.Dependencies = []int64{prerequisite.ID}
		created, _, err := h.tasks.CreateTask(h.ctx, dependent, "system", "executive-orchestrator")
		if err != nil {
			t.Fatal(err)
		}
		if created.Status != tasks.StatusBlocked {
			t.Fatalf("the hold outranks the dependency-derived status at creation, got %q", created.Status)
		}
		published, err := h.tasks.ReleaseCoordinationHold(h.ctx, tasks.ReleaseCoordinationHoldCommand{
			TaskID: created.ID, ActorType: "system", ActorID: "executive-orchestrator",
		})
		if err != nil {
			t.Fatal(err)
		}
		if published.Status != tasks.StatusPending {
			t.Fatalf("releasing the publication barrier must not satisfy the dependency barrier, got %q", published.Status)
		}
		claimed, err := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: "worker-01", BatchSize: 10, LeaseDuration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range claimed {
			if task.Task.ID == created.ID {
				t.Fatal("a published task whose prerequisite has not completed must not be claimable")
			}
		}
	})

	t.Run("an unheld create is unchanged", func(t *testing.T) {
		h.resetTasks(t)
		created, inserted, err := h.tasks.CreateTask(h.ctx, baseRequest("no-hold"), "human", "eduardo")
		if err != nil || !inserted {
			t.Fatalf("create: inserted=%v err=%v", inserted, err)
		}
		if created.Status != tasks.StatusReady {
			t.Fatalf("an independent unheld task must still be born ready, got %q", created.Status)
		}
		if created.StatusReasonCode != nil || created.StatusReason != nil {
			t.Fatalf("an unheld task must carry no status reason, got code=%v reason=%v", created.StatusReasonCode, created.StatusReason)
		}
		if claimed := claimOne(t, h, "worker-01"); claimed.Task.ID != created.ID {
			t.Fatalf("an unheld task must be claimable, claimed %d", claimed.Task.ID)
		}
	})

	t.Run("recreating a held task is idempotent", func(t *testing.T) {
		h.resetTasks(t)
		request := baseRequest("hold-idempotent")
		request.HoldForCoordination = true
		created, inserted, err := h.tasks.CreateTask(h.ctx, request, "system", "executive-orchestrator")
		if err != nil || !inserted {
			t.Fatalf("create: inserted=%v err=%v", inserted, err)
		}
		// This is the restart path: the orchestrator re-runs the whole
		// sequence and must find the same durable child, still held, not a
		// second one.
		existing, inserted, err := h.tasks.CreateTask(h.ctx, request, "system", "executive-orchestrator")
		if err != nil {
			t.Fatal(err)
		}
		if inserted || existing.ID != created.ID {
			t.Fatalf("restart must reuse the durable child: id=%d inserted=%v", existing.ID, inserted)
		}
		if existing.Status != tasks.StatusBlocked || existing.StatusReasonCode == nil || *existing.StatusReasonCode != tasks.ReasonCodeCoordinationHold {
			t.Fatalf("the reused child must still be held, got status=%q reason=%v", existing.Status, existing.StatusReasonCode)
		}
		var rows int
		if err = h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM tasks WHERE idempotency_key=$1`, request.IdempotencyKey).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("recovery must never create a second task, found %d", rows)
		}
	})

	t.Run("an operator unblock cannot publish a held task", func(t *testing.T) {
		h.resetTasks(t)
		request := baseRequest("hold-unblock")
		request.HoldForCoordination = true
		created, _, err := h.tasks.CreateTask(h.ctx, request, "system", "executive-orchestrator")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.tasks.UnblockTask(h.ctx, tasks.UnblockCommand{TaskID: created.ID, ActorType: "human", ActorID: "eduardo"}); err == nil {
			t.Fatal("an operator clearing an operational block must not be able to publish a child whose coordination never happened")
		}
		reloaded, err := h.tasks.GetTask(h.ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Task.Status != tasks.StatusBlocked {
			t.Fatalf("the refused unblock must leave the task held, got %q", reloaded.Task.Status)
		}
	})
}
