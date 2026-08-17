//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const testRole = "ingenieria_ia/qa"

type harness struct {
	ctx     context.Context
	store   *platformpostgres.Store
	tasks   *tasks.Service
	taskDB  *taskpostgres.Store
	cleanup func()
}

func TestDurableTaskEnginePostgreSQL17(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()

	t.Run("create is strict, idempotent, and atomically audited", func(t *testing.T) {
		h.resetTasks(t)
		request := baseRequest("create-1")
		created, inserted, err := h.tasks.CreateTask(h.ctx, request, "human", "eduardo")
		if err != nil || !inserted {
			t.Fatalf("create task: inserted=%v err=%v", inserted, err)
		}
		existing, inserted, err := h.tasks.CreateTask(h.ctx, request, "human", "eduardo")
		if err != nil || inserted || existing.ID != created.ID {
			t.Fatalf("idempotent create: task=%+v inserted=%v err=%v", existing, inserted, err)
		}
		changed := request
		changed.Title = "different request"
		if _, _, err = h.tasks.CreateTask(h.ctx, changed, "human", "eduardo"); !errors.Is(err, tasks.ErrIdempotencyConflict) {
			t.Fatalf("idempotency conflict error=%v", err)
		}
		var events, outbox, audits int
		if err = h.store.Pool().QueryRow(h.ctx, `
			SELECT (SELECT count(*) FROM task_events WHERE task_id=$1),
			       (SELECT count(*) FROM outbox_events WHERE aggregate_type='task' AND aggregate_id=$2),
			       (SELECT count(*) FROM audit_events WHERE subject_type='task' AND subject_id=$2)
		`, created.ID, fmt.Sprint(created.ID)).Scan(&events, &outbox, &audits); err != nil {
			t.Fatal(err)
		}
		if events != 1 || outbox != 1 || audits != 1 {
			t.Fatalf("atomic records events=%d outbox=%d audits=%d", events, outbox, audits)
		}
	})

	t.Run("claim ordering and SKIP LOCKED prevent duplicate execution", func(t *testing.T) {
		h.resetTasks(t)
		priorities := []int{1, 9, 5}
		idsByPriority := map[int]int64{}
		for index, priority := range priorities {
			req := baseRequest(fmt.Sprintf("order-%d", index))
			req.Priority = priority
			created, _, err := h.tasks.CreateTask(h.ctx, req, "human", "eduardo")
			if err != nil {
				t.Fatal(err)
			}
			idsByPriority[priority] = created.ID
		}
		claimed, err := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: "order-worker", BatchSize: 3, LeaseDuration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 3 || claimed[0].Task.ID != idsByPriority[9] || claimed[1].Task.ID != idsByPriority[5] || claimed[2].Task.ID != idsByPriority[1] {
			t.Fatalf("unexpected claim order: %+v", claimed)
		}

		h.resetTasks(t)
		const total = 24
		for index := 0; index < total; index++ {
			if _, _, err := h.tasks.CreateTask(h.ctx, baseRequest(fmt.Sprintf("concurrent-%02d", index)), "human", "eduardo"); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		claimedIDs := make(chan int64, total)
		errorsCh := make(chan error, 8)
		for worker := 0; worker < 8; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				items, claimErr := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: fmt.Sprintf("worker-%d", worker), BatchSize: 8, LeaseDuration: time.Minute})
				if claimErr != nil {
					errorsCh <- claimErr
					return
				}
				for _, item := range items {
					claimedIDs <- item.Task.ID
				}
			}(worker)
		}
		wg.Wait()
		close(errorsCh)
		for claimErr := range errorsCh {
			t.Fatalf("concurrent claim: %v", claimErr)
		}
		close(claimedIDs)
		seen := map[int64]bool{}
		for id := range claimedIDs {
			if seen[id] {
				t.Fatalf("task %d was claimed twice", id)
			}
			seen[id] = true
		}
		if len(seen) != total {
			t.Fatalf("claimed=%d want=%d", len(seen), total)
		}
	})

	t.Run("lease tokens are opaque, hashed, holder-bound, and expire", func(t *testing.T) {
		h.resetTasks(t)
		created, _, err := h.tasks.CreateTask(h.ctx, baseRequest("lease-1"), "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		claimed := claimOne(t, h, "lease-worker")
		if claimed.Task.ID != created.ID || claimed.LeaseToken == "" {
			t.Fatalf("invalid claim: %+v", claimed)
		}
		var storedHash string
		if err = h.store.Pool().QueryRow(h.ctx, `SELECT token_hash FROM task_leases WHERE id=$1`, claimed.Lease.ID).Scan(&storedHash); err != nil {
			t.Fatal(err)
		}
		if storedHash == claimed.LeaseToken || len(storedHash) != 64 {
			t.Fatalf("lease token storage is unsafe: %q", storedHash)
		}
		wrong := tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: "wrong", ActorID: "lease-worker"}
		if _, err = h.tasks.StartAttempt(h.ctx, wrong); !errors.Is(err, tasks.ErrLeaseMismatch) {
			t.Fatalf("wrong token error=%v", err)
		}
		wrongHolder := tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "other-worker"}
		if _, err = h.tasks.StartAttempt(h.ctx, wrongHolder); !errors.Is(err, tasks.ErrLeaseMismatch) {
			t.Fatalf("wrong holder error=%v", err)
		}
		command := tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "lease-worker"}
		started, err := h.tasks.StartAttempt(h.ctx, command)
		if err != nil || started.Status != tasks.StatusRunning {
			t.Fatalf("start: status=%s err=%v", started.Status, err)
		}
		heartbeat, err := h.tasks.Heartbeat(h.ctx, tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "lease-worker", Extension: 2 * time.Minute})
		if err != nil || heartbeat.Status != tasks.LeaseActive {
			t.Fatalf("heartbeat: %+v err=%v", heartbeat, err)
		}
		if _, err = h.store.Pool().Exec(h.ctx, `UPDATE task_leases SET issued_at=clock_timestamp()-interval '2 minutes', heartbeat_at=clock_timestamp()-interval '2 minutes', expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claimed.Lease.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = h.tasks.Heartbeat(h.ctx, tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "lease-worker", Extension: time.Minute}); !errors.Is(err, tasks.ErrLeaseExpired) {
			t.Fatalf("expired heartbeat error=%v", err)
		}
	})

	t.Run("successful process requires explicit verification and evidence", func(t *testing.T) {
		h.resetTasks(t)
		required := true
		req := baseRequest("verify-1")
		req.Requirements = []tasks.RequirementSpec{{Key: "qa", Type: tasks.RequirementCheck, Description: "QA passed", Required: &required}}
		created, _, err := h.tasks.CreateTask(h.ctx, req, "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.tasks.FinalizeTask(h.ctx, tasks.FinalizeCommand{TaskID: created.ID, Outcome: tasks.FinalCompleted, ActorID: "eduardo"}); !errors.Is(err, tasks.ErrInvalidTransition) {
			t.Fatalf("ready -> completed must fail, got %v", err)
		}
		claimed := claimOne(t, h, "qa-worker")
		leaseCommand := tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "qa-worker"}
		if _, err = h.tasks.StartAttempt(h.ctx, leaseCommand); err != nil {
			t.Fatal(err)
		}
		if _, err = h.tasks.FinalizeTask(h.ctx, tasks.FinalizeCommand{TaskID: created.ID, Outcome: tasks.FinalFailed, ReasonCode: "premature", Reason: "worker is still running", ActorID: "qa"}); !errors.Is(err, tasks.ErrInvalidTransition) {
			t.Fatalf("running task cannot be explicitly finalized failed, got %v", err)
		}
		awaiting, err := h.tasks.RecordAttemptResult(h.ctx, tasks.RecordAttemptResultCommand{LeaseCommand: leaseCommand, Result: tasks.AttemptResult{Outcome: tasks.OutcomeSucceeded, Summary: "execution succeeded"}})
		if err != nil || awaiting.Status != tasks.StatusAwaitingVerification {
			t.Fatalf("result: status=%s err=%v", awaiting.Status, err)
		}
		if _, err = h.tasks.FinalizeTask(h.ctx, tasks.FinalizeCommand{TaskID: created.ID, Outcome: tasks.FinalCompleted, ActorID: "qa"}); !errors.Is(err, tasks.ErrRequirementsUnsatisfied) {
			t.Fatalf("completion without evidence error=%v", err)
		}
		detail, err := h.tasks.GetTask(h.ctx, created.ID)
		if err != nil || len(detail.Requirements) != 1 {
			t.Fatalf("detail=%+v err=%v", detail, err)
		}
		requirementID := detail.Requirements[0].ID
		if _, err = h.tasks.RecordEvidence(h.ctx, tasks.RecordEvidenceCommand{TaskID: created.ID, RequirementID: &requirementID, Type: tasks.RequirementCheck, Reference: "artifact://qa/report-1", RecordedBy: "qa", Satisfies: true}); err != nil {
			t.Fatal(err)
		}
		completed, err := h.tasks.FinalizeTask(h.ctx, tasks.FinalizeCommand{TaskID: created.ID, Outcome: tasks.FinalCompleted, ActorID: "qa"})
		if err != nil || completed.Status != tasks.StatusCompleted || completed.TerminalAt == nil {
			t.Fatalf("complete: %+v err=%v", completed, err)
		}
		if _, err = h.tasks.BlockTask(h.ctx, tasks.BlockCommand{TaskID: created.ID, ReasonCode: "reopen", Reason: "not allowed", ActorID: "eduardo"}); !errors.Is(err, tasks.ErrInvalidTransition) {
			t.Fatalf("terminal reopen error=%v", err)
		}
	})

	t.Run("only completed satisfies dependencies and cycles are rejected", func(t *testing.T) {
		h.resetTasks(t)
		parent, _, err := h.tasks.CreateTask(h.ctx, baseRequest("dependency-parent"), "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		childReq := baseRequest("dependency-child")
		childReq.Dependencies = []int64{parent.ID}
		child, _, err := h.tasks.CreateTask(h.ctx, childReq, "human", "eduardo")
		if err != nil || child.Status != tasks.StatusPending {
			t.Fatalf("child: %+v err=%v", child, err)
		}
		if err = h.tasks.AddDependency(h.ctx, tasks.AddDependencyCommand{TaskID: parent.ID, DependsOnID: child.ID, ActorID: "eduardo"}); !errors.Is(err, tasks.ErrDependencyCycle) {
			t.Fatalf("cycle error=%v", err)
		}
		parent = executeToAwaiting(t, h, parent.ID, "parent-worker")
		parent, err = h.tasks.FinalizeTask(h.ctx, tasks.FinalizeCommand{TaskID: parent.ID, Outcome: tasks.FinalNoAction, ReasonCode: "nothing_needed", Reason: "no changes required", ActorID: "qa"})
		if err != nil || parent.Status != tasks.StatusNoAction {
			t.Fatalf("no action: %+v err=%v", parent, err)
		}
		if _, err = h.store.Pool().Exec(h.ctx, `UPDATE tasks SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1`, child.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = h.tasks.Reconcile(h.ctx, 100); err != nil {
			t.Fatal(err)
		}
		detail, err := h.tasks.GetTask(h.ctx, child.ID)
		if err != nil || detail.Task.Status != tasks.StatusBlocked || detail.Task.StatusReasonCode == nil || *detail.Task.StatusReasonCode != "dependency_terminal" {
			t.Fatalf("child after no_action: %+v err=%v", detail.Task, err)
		}
	})

	t.Run("retry, dead letter, lease recovery, and explicit cancellation", func(t *testing.T) {
		h.resetTasks(t)
		req := baseRequest("retry-1")
		req.MaxAttempts = 2
		created, _, err := h.tasks.CreateTask(h.ctx, req, "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		claimed := claimOne(t, h, "retry-worker")
		command := tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "retry-worker"}
		if _, err = h.tasks.StartAttempt(h.ctx, command); err != nil {
			t.Fatal(err)
		}
		retrying, err := h.tasks.RecordAttemptResult(h.ctx, tasks.RecordAttemptResultCommand{LeaseCommand: command, Result: tasks.AttemptResult{Outcome: tasks.OutcomeRetryableFailure, FailureCode: "temporary", Summary: "retry later"}})
		if err != nil || retrying.Status != tasks.StatusRetryWait {
			t.Fatalf("retry: %+v err=%v", retrying, err)
		}
		if _, err = h.store.Pool().Exec(h.ctx, `UPDATE tasks SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1`, created.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = h.tasks.Reconcile(h.ctx, 100); err != nil {
			t.Fatal(err)
		}
		claimed = claimOne(t, h, "retry-worker-2")
		command = tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "retry-worker-2"}
		if _, err = h.tasks.StartAttempt(h.ctx, command); err != nil {
			t.Fatal(err)
		}
		dead, err := h.tasks.RecordAttemptResult(h.ctx, tasks.RecordAttemptResultCommand{LeaseCommand: command, Result: tasks.AttemptResult{Outcome: tasks.OutcomeRetryableFailure, FailureCode: "still_temporary"}})
		if err != nil || dead.Status != tasks.StatusDeadLetter {
			t.Fatalf("dead letter: %+v err=%v", dead, err)
		}
		letters, err := h.tasks.ListDeadLetters(h.ctx, 10)
		if err != nil || len(letters) != 1 || letters[0].TaskID != created.ID {
			t.Fatalf("letters=%+v err=%v", letters, err)
		}

		h.resetTasks(t)
		recoverTask, _, _ := h.tasks.CreateTask(h.ctx, baseRequest("lease-recovery"), "human", "eduardo")
		expiredClaim := claimOne(t, h, "crashed-worker")
		if _, err = h.store.Pool().Exec(h.ctx, `UPDATE task_leases SET issued_at=clock_timestamp()-interval '2 minutes', heartbeat_at=clock_timestamp()-interval '2 minutes', expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, expiredClaim.Lease.ID); err != nil {
			t.Fatal(err)
		}
		result, err := h.tasks.Reconcile(h.ctx, 100)
		if err != nil || result.ExpiredLeases != 1 {
			t.Fatalf("lease reconcile=%+v err=%v", result, err)
		}
		detail, _ := h.tasks.GetTask(h.ctx, recoverTask.ID)
		if detail.Task.Status != tasks.StatusRetryWait || detail.ActiveLease != nil {
			t.Fatalf("recovered task=%+v", detail)
		}

		h.resetTasks(t)
		cancelTask, _, _ := h.tasks.CreateTask(h.ctx, baseRequest("cancel-active"), "human", "eduardo")
		active := claimOne(t, h, "cancel-worker")
		if _, err = h.tasks.BlockTask(h.ctx, tasks.BlockCommand{TaskID: cancelTask.ID, ReasonCode: "manual", Reason: "manual block", ActorID: "eduardo"}); !errors.Is(err, tasks.ErrActiveLease) {
			t.Fatalf("block without revocation error=%v", err)
		}
		cancelled, err := h.tasks.CancelTask(h.ctx, tasks.CancelCommand{TaskID: cancelTask.ID, ReasonCode: "owner_cancelled", Reason: "cancel requested", ActorID: "eduardo"})
		if err != nil || cancelled.Status != tasks.StatusCancelled {
			t.Fatalf("cancel: %+v err=%v", cancelled, err)
		}
		var leaseStatus, attemptStatus string
		if err = h.store.Pool().QueryRow(h.ctx, `SELECT l.status,a.state FROM task_leases l JOIN task_attempts a ON a.id=l.attempt_id WHERE l.id=$1`, active.Lease.ID).Scan(&leaseStatus, &attemptStatus); err != nil {
			t.Fatal(err)
		}
		if leaseStatus != "revoked" || attemptStatus != "cancelled" {
			t.Fatalf("lease=%s attempt=%s", leaseStatus, attemptStatus)
		}

		h.resetTasks(t)
		finalCancelTask, _, _ := h.tasks.CreateTask(h.ctx, baseRequest("finalize-cancel-active"), "human", "eduardo")
		finalActive := claimOne(t, h, "final-cancel-worker")
		finalCancelled, err := h.tasks.FinalizeTask(h.ctx, tasks.FinalizeCommand{TaskID: finalCancelTask.ID, Outcome: tasks.FinalCancelled, ReasonCode: "owner_cancelled", Reason: "cancelled through explicit finalization", ActorID: "eduardo"})
		if err != nil || finalCancelled.Status != tasks.StatusCancelled {
			t.Fatalf("finalize cancellation: %+v err=%v", finalCancelled, err)
		}
		if err = h.store.Pool().QueryRow(h.ctx, `SELECT l.status,a.state FROM task_leases l JOIN task_attempts a ON a.id=l.attempt_id WHERE l.id=$1`, finalActive.Lease.ID).Scan(&leaseStatus, &attemptStatus); err != nil {
			t.Fatal(err)
		}
		if leaseStatus != "revoked" || attemptStatus != "cancelled" {
			t.Fatalf("finalized cancellation lease=%s attempt=%s", leaseStatus, attemptStatus)
		}
	})

	t.Run("assignee revalidation blocks stale assignments", func(t *testing.T) {
		h.resetTasks(t)
		created, _, err := h.tasks.CreateTask(h.ctx, baseRequest("assignee-change"), "human", "eduardo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.store.Pool().Exec(h.ctx, `UPDATE organization_roles SET enabled=false,executable=false WHERE organization_id='explorarte' AND id=$1`, testRole); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = h.store.Pool().Exec(context.Background(), `UPDATE organization_roles SET enabled=true,executable=true WHERE organization_id='explorarte' AND id=$1`, testRole)
		})
		claimed, err := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: "stale-worker", BatchSize: 1})
		if err != nil || len(claimed) != 0 {
			t.Fatalf("claim stale assignee=%+v err=%v", claimed, err)
		}
		detail, err := h.tasks.GetTask(h.ctx, created.ID)
		if err != nil || detail.Task.Status != tasks.StatusBlocked || detail.Task.StatusReasonCode == nil || *detail.Task.StatusReasonCode != "assignee_unavailable" {
			t.Fatalf("stale task=%+v err=%v", detail.Task, err)
		}
	})

	t.Run("outbox claim, nack, ack, and expired-claim recovery are durable", func(t *testing.T) {
		h.resetTasks(t)
		for index := 0; index < 3; index++ {
			if _, _, err := h.tasks.CreateTask(h.ctx, baseRequest(fmt.Sprintf("outbox-%d", index)), "human", "eduardo"); err != nil {
				t.Fatal(err)
			}
		}
		claimed, err := h.tasks.ClaimOutbox(h.ctx, tasks.OutboxClaimRequest{ConsumerID: "publisher", BatchSize: 3, ClaimDuration: time.Minute})
		if err != nil || len(claimed) != 3 {
			t.Fatalf("outbox claim=%+v err=%v", claimed, err)
		}
		for _, item := range claimed {
			if item.ClaimToken == "" {
				t.Fatal("outbox token missing")
			}
			if _, present := item.Event.Payload["instructions"]; present {
				t.Fatalf("outbox leaked instructions: %+v", item.Event.Payload)
			}
		}
		if err = h.tasks.AckOutbox(h.ctx, tasks.OutboxDisposition{EventID: claimed[0].Event.ID, ClaimToken: "wrong", ConsumerID: "publisher"}); !errors.Is(err, tasks.ErrLeaseMismatch) {
			t.Fatalf("wrong outbox token error=%v", err)
		}
		if err = h.tasks.AckOutbox(h.ctx, tasks.OutboxDisposition{EventID: claimed[0].Event.ID, ClaimToken: claimed[0].ClaimToken, ConsumerID: "publisher"}); err != nil {
			t.Fatal(err)
		}
		if err = h.tasks.NackOutbox(h.ctx, tasks.OutboxDisposition{EventID: claimed[1].Event.ID, ClaimToken: claimed[1].ClaimToken, ConsumerID: "publisher", Error: "temporary broker failure"}); err != nil {
			t.Fatal(err)
		}
		if _, err = h.store.Pool().Exec(h.ctx, `UPDATE outbox_events SET claim_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claimed[2].Event.ID); err != nil {
			t.Fatal(err)
		}
		recovered, err := h.tasks.RecoverOutbox(h.ctx, 10)
		if err != nil || recovered != 1 {
			t.Fatalf("recover=%d err=%v", recovered, err)
		}
		stats, err := h.tasks.OutboxStats(h.ctx)
		if err != nil || stats.Published != 1 || stats.Claimed != 0 || stats.Pending != 2 {
			t.Fatalf("stats=%+v err=%v", stats, err)
		}
	})

	t.Run("event sequences are append-only and monotonically increasing", func(t *testing.T) {
		h.resetTasks(t)
		created, _, _ := h.tasks.CreateTask(h.ctx, baseRequest("events"), "human", "eduardo")
		claimed := claimOne(t, h, "event-worker")
		command := tasks.LeaseCommand{TaskID: created.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: "event-worker"}
		_, _ = h.tasks.StartAttempt(h.ctx, command)
		_, _ = h.tasks.RecordAttemptResult(h.ctx, tasks.RecordAttemptResultCommand{LeaseCommand: command, Result: tasks.AttemptResult{Outcome: tasks.OutcomeSucceeded}})
		events, err := h.tasks.ListEvents(h.ctx, created.ID, 100)
		if err != nil || len(events) < 4 {
			t.Fatalf("events=%+v err=%v", events, err)
		}
		sequences := make([]int64, len(events))
		for index, event := range events {
			sequences[index] = event.Sequence
			if event.Sequence != int64(index+1) {
				t.Fatalf("sequence gap at %d: %+v", index, sequences)
			}
		}
		if !sort.SliceIsSorted(sequences, func(i, j int) bool { return sequences[i] < sequences[j] }) {
			t.Fatalf("sequences not sorted: %+v", sequences)
		}
	})

	// TASK_IDEMPOTENCY_PROOF (M1.3 section 9/18.A): a task row created
	// before TaskClass existed -- its stored request_hash computed by the
	// exact pre-M1.3 algorithm, with no task_class concept at all -- must
	// remain resumable through the SAME idempotency key once the caller
	// upgrades and starts supplying a real TaskClass. A resumed request
	// whose OTHER fields genuinely differ from the historical row must
	// still fail closed with ErrIdempotencyConflict.
	t.Run("pre-M1.3 idempotent task remains resumable, contradictory identity fails closed", func(t *testing.T) {
		h.resetTasks(t)
		const key = "upgrade-compat"
		historical := tasks.CreateRequest{
			// OrganizationID must match exactly what Service.CreateTask
			// itself defaults an empty request.OrganizationID to
			// (s.cfg.OrganizationID) -- it is part of the hashed request,
			// so a mismatch here would make even the LEGACY-shape
			// comparison fail for reasons unrelated to TaskClass.
			OrganizationID: "explorarte",
			AssignedRoleID: testRole, IdempotencyKey: key,
			Title: "Historical task", Instructions: "Pre-M1.3 instructions.",
			AcceptanceCriteria: []string{"criterion"},
			// MaxAttempts must likewise match what Service.CreateTask
			// defaults an unset request.MaxAttempts to before hashing
			// (s.cfg.DefaultMaxAttempts) -- also part of the hashed
			// request. Matches the raw INSERT's max_attempts below.
			MaxAttempts: 5,
		} // TaskClass intentionally left empty: this IS the pre-M1.3 shape.
		legacyHash, err := tasks.HashCreateRequestLegacy(historical)
		if err != nil {
			t.Fatal(err)
		}
		var historicalID int64
		if err := h.store.Pool().QueryRow(h.ctx, `
			INSERT INTO tasks(
				organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,
				idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts
			) VALUES('explorarte',1,$1,'ingenieria_ia',$2,$3,$4,$5,'["criterion"]'::jsonb,'ready',0,clock_timestamp(),5)
			RETURNING id`,
			testRole, key, legacyHash, historical.Title, historical.Instructions,
		).Scan(&historicalID); err != nil {
			t.Fatalf("simulate pre-M1.3 historical row: %v", err)
		}
		var storedTaskClass string
		if err := h.store.Pool().QueryRow(h.ctx, `SELECT task_class FROM tasks WHERE id=$1`, historicalID).Scan(&storedTaskClass); err != nil || storedTaskClass != "legacy.unspecified" {
			t.Fatalf("historical row task_class=%q err=%v, want the migration default", storedTaskClass, err)
		}

		resumed := historical
		resumed.TaskClass = "general.work" // a real M1.3-era resume now supplies this
		reused, inserted, err := h.tasks.CreateTask(h.ctx, resumed, "human", "eduardo")
		if err != nil || inserted || reused.ID != historicalID {
			t.Fatalf("resumed pre-M1.3 idempotent create: task=%+v inserted=%v err=%v", reused, inserted, err)
		}

		contradictory := historical
		contradictory.TaskClass = "general.work"
		contradictory.Instructions = "This is not the same task."
		if _, _, err := h.tasks.CreateTask(h.ctx, contradictory, "human", "eduardo"); !errors.Is(err, tasks.ErrIdempotencyConflict) {
			t.Fatalf("want ErrIdempotencyConflict for a genuinely different request under the same key, got %v", err)
		}
	})
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "24",
			"ORG_DATABASE_MIN_CONNS": "0",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "task-engine-integration-test")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, platformStore.Pool()); err != nil {
		platformStore.Close()
		cancel()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(platformStore.Pool(), rootmigrations.Files)
	if err != nil {
		platformStore.Close()
		cancel()
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		platformStore.Close()
		cancel()
		t.Fatalf("migrate: %v", err)
	}
	if err := testdbguard.RequireDestructive(ctx, databaseURL, platformStore.Pool()); err != nil {
		platformStore.Close()
		cancel()
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err = platformStore.Pool().Exec(ctx, `
		TRUNCATE outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,
		         task_requirements,task_dependencies,tasks,organization_reporting_lines,
		         organization_registry_revision_documents,organization_roles,organizational_units,
		         organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE
	`); err != nil {
		platformStore.Close()
		cancel()
		t.Fatalf("reset schema: %v", err)
	}
	registryRepo, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		platformStore.Close()
		cancel()
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		platformStore.Close()
		cancel()
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, "explorarte", 30*time.Second)
	if err != nil {
		platformStore.Close()
		cancel()
		t.Fatal(err)
	}
	if result, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || !result.Applied {
		platformStore.Close()
		cancel()
		t.Fatalf("sync registry: result=%+v err=%v", result, syncErr)
	}
	catalog, err := registryadapter.New(registryRepo)
	if err != nil {
		platformStore.Close()
		cancel()
		t.Fatal(err)
	}
	taskDB, err := taskpostgres.New(platformStore)
	if err != nil {
		platformStore.Close()
		cancel()
		t.Fatal(err)
	}
	service, err := tasks.NewService(taskDB, catalog, tasks.Config{
		OrganizationID:       "explorarte",
		DefaultMaxAttempts:   5,
		DefaultLeaseDuration: time.Minute,
		MaxLeaseDuration:     15 * time.Minute,
		RetryPolicy:          tasks.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute},
		OutboxMaxAttempts:    3,
		OutboxClaimDuration:  time.Minute,
	})
	if err != nil {
		platformStore.Close()
		cancel()
		t.Fatal(err)
	}
	return &harness{ctx: ctx, store: platformStore, tasks: service, taskDB: taskDB, cleanup: func() { platformStore.Close(); cancel() }}
}

func (h *harness) resetTasks(t *testing.T) {
	t.Helper()
	if err := testdbguard.RequireDestructive(h.ctx, os.Getenv("ORG_TEST_DATABASE_URL"), h.store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err := h.store.Pool().Exec(h.ctx, `
		TRUNCATE outbox_events,task_dead_letters,task_events,task_leases,task_attempts,
		         task_evidence,task_requirements,task_dependencies,tasks RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Pool().Exec(h.ctx, `UPDATE organization_roles SET enabled=true,executable=true WHERE organization_id='explorarte' AND id=$1`, testRole); err != nil {
		t.Fatal(err)
	}
}

func baseRequest(key string) tasks.CreateRequest {
	return tasks.CreateRequest{
		AssignedRoleID:     testRole,
		IdempotencyKey:     key,
		Title:              "Integration task " + key,
		Instructions:       "Execute the deterministic integration scenario.",
		AcceptanceCriteria: []string{"state is persisted", "events are durable"},
	}
}

func claimOne(t *testing.T, h *harness, worker string) tasks.ClaimedTask {
	t.Helper()
	claimed, err := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: worker, BatchSize: 1, LeaseDuration: time.Minute})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim one: claimed=%+v err=%v", claimed, err)
	}
	return claimed[0]
}

func executeToAwaiting(t *testing.T, h *harness, taskID int64, worker string) tasks.Task {
	t.Helper()
	claimed := claimOne(t, h, worker)
	if claimed.Task.ID != taskID {
		t.Fatalf("claimed task=%d want=%d", claimed.Task.ID, taskID)
	}
	command := tasks.LeaseCommand{TaskID: taskID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: worker}
	if _, err := h.tasks.StartAttempt(h.ctx, command); err != nil {
		t.Fatal(err)
	}
	result, err := h.tasks.RecordAttemptResult(h.ctx, tasks.RecordAttemptResultCommand{LeaseCommand: command, Result: tasks.AttemptResult{Outcome: tasks.OutcomeSucceeded}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
