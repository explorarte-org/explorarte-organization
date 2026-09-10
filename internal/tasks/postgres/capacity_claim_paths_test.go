//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// ============================================================
// DYNAMIC_ROUTING_STACK_FINAL_INTEGRATION_REVIEW, FASE 12/13: this file
// exercises the capacity pre-claim gate and the assignee_unavailable
// durability fix directly against internal/tasks/postgres.Store, on BOTH
// claim paths Persistence exposes -- Claim (the batch path Reconcile and a
// generic worker pool use) and ClaimSpecific (the single, ID-targeted path
// Executive's own driveDepartments actually uses, and the one that was
// found completely ungated, with its own block rolled back by withTx,
// during CAPACITY_EXHAUSTION_SCHEDULING_V1's closure). Every capacity-wait
// integration test in internal/executive only ever exercised ClaimSpecific
// (Executive never calls the batch path); this file is the low-level,
// Executive-independent proof that both paths behave identically, without
// needing a whole CEO/leader/department campaign to reach either one.
// ============================================================

// blockingCapacityGate reports every task as durably unavailable with a
// fixed, real, future RetryAt -- exactly the shape a real
// CapacityValidator (internal/executive/bootstrap's newCapacityGate)
// reports for a genuine transient-no-capacity finding. It is not itself a
// second implementation of pool eligibility: it is a deterministic test
// double standing in for whatever eligibility computation produced that
// determination, which is not this file's concern.
func blockingCapacityGate(retryAt time.Time) tasks.CapacityValidator {
	return func(context.Context, tasks.Task) (tasks.CapacityCheck, error) {
		return tasks.CapacityCheck{Available: false, RetryAt: retryAt, Reason: "test: pool has no eligible candidate"}, nil
	}
}

// durableFutureTimestamp reads a future timestamp from PostgreSQL's own
// clock_timestamp() -- this package's durable PostgreSQL code, its own
// tests included, must never reason about time via application wall
// clock reads, only the database's own clock.
func durableFutureTimestamp(t *testing.T, h *harness, offset string) time.Time {
	t.Helper()
	var ts time.Time
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT clock_timestamp() + $1::interval`, offset).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestCapacityGateBlocksBatchClaimBeforeAttempt(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()
	h.resetTasks(t)

	retryAt := durableFutureTimestamp(t, h, "90 seconds")
	h.tasks.SetCapacityGate(blockingCapacityGate(retryAt))
	defer h.tasks.SetCapacityGate(nil)

	created, _, err := h.tasks.CreateTask(h.ctx, baseRequest("capacity-batch-claim"), "human", "eduardo")
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != tasks.StatusReady {
		t.Fatalf("fresh task with no dependencies must start ready, got %q", created.Status)
	}

	claimed, err := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: "batch-worker", BatchSize: 10, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("a capacity-blocked task must never be claimed, got %d claimed: %+v", len(claimed), claimed)
	}

	detail, err := h.tasks.GetTask(h.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.Status != tasks.StatusBlocked {
		t.Fatalf("task must be durably blocked, got status=%q", detail.Task.Status)
	}
	if detail.Task.StatusReasonCode == nil || *detail.Task.StatusReasonCode != "capacity" {
		t.Fatalf("status_reason_code must be capacity, got %v", detail.Task.StatusReasonCode)
	}
	if !detail.Task.AvailableAt.Equal(retryAt) {
		t.Fatalf("available_at must be the gate's own RetryAt, got %s want %s", detail.Task.AvailableAt, retryAt)
	}
	if detail.Task.AttemptCount != 0 {
		t.Fatalf("BATCH_CLAIM: capacity block must not consume an attempt, got attempt_count=%d", detail.Task.AttemptCount)
	}
	if len(detail.Attempts) != 0 {
		t.Fatalf("BATCH_CLAIM: no TaskAttempt row may exist for the block, found %d", len(detail.Attempts))
	}
	var activeLeases int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM task_leases WHERE task_id=$1 AND status='active'`, created.ID).Scan(&activeLeases); err != nil {
		t.Fatal(err)
	}
	if activeLeases != 0 {
		t.Fatalf("BATCH_CLAIM: no active Lease may exist for the block, found %d", activeLeases)
	}
}

func TestCapacityGateBlocksClaimSpecificBeforeAttempt(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()
	h.resetTasks(t)

	retryAt := durableFutureTimestamp(t, h, "90 seconds")
	h.tasks.SetCapacityGate(blockingCapacityGate(retryAt))
	defer h.tasks.SetCapacityGate(nil)

	created, _, err := h.tasks.CreateTask(h.ctx, baseRequest("capacity-claim-specific"), "human", "eduardo")
	if err != nil {
		t.Fatal(err)
	}

	_, claimErr := h.tasks.ClaimTaskByID(h.ctx, created.ID, tasks.ClaimRequest{WorkerID: "specific-worker", LeaseDuration: time.Minute})
	if claimErr == nil {
		t.Fatal("ClaimTaskByID must fail for a capacity-blocked task, got nil error")
	}
	if !errors.Is(claimErr, tasks.ErrNoCapacity) {
		t.Fatalf("CLAIM_SPECIFIC: expected tasks.ErrNoCapacity, got %v", claimErr)
	}

	detail, err := h.tasks.GetTask(h.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// This is the exact regression the round's own fix addressed: the
	// block transition must have committed even though ClaimTaskByID
	// returned an error from the same call -- withTx used to roll back
	// the transition whenever the callback returned any non-nil error,
	// silently discarding it.
	if detail.Task.Status != tasks.StatusBlocked {
		t.Fatalf("CLAIM_SPECIFIC: task must be durably blocked despite the call returning an error, got status=%q", detail.Task.Status)
	}
	if detail.Task.StatusReasonCode == nil || *detail.Task.StatusReasonCode != "capacity" {
		t.Fatalf("CLAIM_SPECIFIC: status_reason_code must be capacity, got %v", detail.Task.StatusReasonCode)
	}
	if !detail.Task.AvailableAt.Equal(retryAt) {
		t.Fatalf("CLAIM_SPECIFIC: available_at must be the gate's own RetryAt, got %s want %s", detail.Task.AvailableAt, retryAt)
	}
	if detail.Task.AttemptCount != 0 {
		t.Fatalf("CLAIM_SPECIFIC: capacity block must not consume an attempt, got attempt_count=%d", detail.Task.AttemptCount)
	}
	if len(detail.Attempts) != 0 {
		t.Fatalf("CLAIM_SPECIFIC: no TaskAttempt row may exist for the block, found %d", len(detail.Attempts))
	}
}

// TestClaimSpecificAssigneeUnavailableCommitsBlockDurably is the FASE 13
// dedicated production regression: ClaimSpecific's assignee_unavailable
// branch existed, unchanged in shape, before this round -- and shared the
// exact same withTx rollback bug the capacity fix (commit a528eda)
// corrected as an unavoidable side effect of restructuring the same
// function. This locks in that the fix covers BOTH branches, not just the
// new capacity one.
func TestClaimSpecificAssigneeUnavailableCommitsBlockDurably(t *testing.T) {
	h := newHarness(t)
	defer h.cleanup()
	h.resetTasks(t)

	created, _, err := h.tasks.CreateTask(h.ctx, baseRequest("assignee-unavailable-claim-specific"), "human", "eduardo")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.store.Pool().Exec(h.ctx, `UPDATE organization_roles SET enabled=false,executable=false WHERE organization_id='explorarte' AND id=$1`, testRole); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.store.Pool().Exec(h.ctx, `UPDATE organization_roles SET enabled=true,executable=true WHERE organization_id='explorarte' AND id=$1`, testRole)
	})

	_, claimErr := h.tasks.ClaimTaskByID(h.ctx, created.ID, tasks.ClaimRequest{WorkerID: "specific-worker", LeaseDuration: time.Minute})
	if claimErr == nil {
		t.Fatal("ClaimTaskByID must fail while the assigned role is disabled, got nil error")
	}
	if !errors.Is(claimErr, tasks.ErrAssigneeUnavailable) {
		t.Fatalf("expected tasks.ErrAssigneeUnavailable, got %v", claimErr)
	}

	detail, err := h.tasks.GetTask(h.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.Status != tasks.StatusBlocked {
		t.Fatalf("task must be durably blocked despite the call returning an error, got status=%q", detail.Task.Status)
	}
	if detail.Task.StatusReasonCode == nil || *detail.Task.StatusReasonCode != "assignee_unavailable" {
		t.Fatalf("status_reason_code must be assignee_unavailable, got %v", detail.Task.StatusReasonCode)
	}
	if detail.Task.AttemptCount != 0 {
		t.Fatalf("assignee_unavailable block must not consume an attempt, got attempt_count=%d", detail.Task.AttemptCount)
	}
	if len(detail.Attempts) != 0 {
		t.Fatalf("no TaskAttempt row may exist for the block, found %d", len(detail.Attempts))
	}
	var activeLeases int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM task_leases WHERE task_id=$1 AND status='active'`, created.ID).Scan(&activeLeases); err != nil {
		t.Fatal(err)
	}
	if activeLeases != 0 {
		t.Fatalf("no active Lease may exist for the block, found %d", activeLeases)
	}

	// Batch Claim must keep equivalent semantics for the same condition.
	claimedBatch, err := h.tasks.ClaimTasks(h.ctx, tasks.ClaimRequest{WorkerID: "batch-worker", BatchSize: 10, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range claimedBatch {
		if c.Task.ID == created.ID {
			t.Fatalf("batch Claim must not claim an assignee-blocked task")
		}
	}
}
