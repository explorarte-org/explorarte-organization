//go:build integration

// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_FINAL_CLOSURE_V1 item 1:
// verify, against the real productive state machine, exactly what happens
// when a caller retries the same turn after DispatchProvisioner failed.
// The prior round's report claimed a retry "resumes... rather than
// duplicating work"; these two tests establish the actual truth and pin
// it, without weakening ceochat's active-lease ownership invariant
// (EXECUTIVE_ACTIVE_LEASE_BARRIER_FIX_V1) to make anything pass.
package ceochat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// TestCEOChatImmediateRetryAfterProvisioningFailureNeverAdoptsTheActiveLease
// proves scenario B (not A): an immediate Send() retry of the exact same
// turn (same conversation, same idempotency key, same content) does NOT
// resume where the failed attempt left off. driveTurn's own status switch
// runs on every call, sees the task is StatusRunning (StartAttempt already
// made that durable before the provisioning failure), and returns
// ErrRunNotReady before EnsureAuthorizedAssignmentForRunningAttempt is
// ever reached again -- the active-lease barrier is never bypassed, even
// for the same process that holds the lease.
func TestCEOChatImmediateRetryAfterProvisioningFailureNeverAdoptsTheActiveLease(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	provisionErr := errors.New("simulated authorized-attempt provisioning failure")
	service, provisioner, _, modelExecutorBuilds, topics := f.withFailingAssignments(t, provisionErr)

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	const key = "provision-failure-immediate-retry"

	_, err = service.Send(ctx, ceochat.SendRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human", IdempotencyKey: key, Content: "hola"})
	if err == nil || !errors.Is(err, provisionErr) {
		t.Fatalf("first send err=%v, want it to wrap the simulated provisioning failure", err)
	}
	if provisioner.calls != 1 {
		t.Fatalf("provisioner calls after first send=%d, want 1", provisioner.calls)
	}

	// Immediate retry: same conversation, same key, same content.
	second, err := service.Send(ctx, ceochat.SendRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human", IdempotencyKey: key, Content: "hola"})
	if !errors.Is(err, ceochat.ErrRunNotReady) {
		t.Fatalf("immediate retry err=%v, want ErrRunNotReady (the active-lease barrier, never adopted)", err)
	}
	if second.Outcome != "" || second.AssistantMessage != nil {
		t.Fatalf("immediate retry result=%+v, want the zero value alongside ErrRunNotReady", second)
	}
	// The provisioner must NOT have been called again: ErrRunNotReady is
	// returned by driveTurn's status switch, strictly before reaching
	// EnsureAuthorizedAssignmentForRunningAttempt.
	if provisioner.calls != 1 {
		t.Fatalf("provisioner calls after immediate retry=%d, want still 1 (never re-invoked while the lease is active)", provisioner.calls)
	}
	if *modelExecutorBuilds != 0 || topics.calls != 0 {
		t.Fatalf("model executor builds=%d tool calls=%d, want both 0", *modelExecutorBuilds, topics.calls)
	}

	// Exactly one owner message durably recorded across both calls (the
	// retry's own recordOwnerMessage call is itself idempotent and simply
	// finds the existing row) -- the active-lease barrier denying the
	// retry did not somehow also duplicate the owner-visible message.
	history, err := service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	ownerCount := 0
	var taskID int64
	for _, message := range history {
		if message.Role == ceochat.MessageOwner {
			ownerCount++
			taskID = message.TaskID
		}
	}
	if ownerCount != 1 {
		t.Fatalf("owner messages=%d, want exactly 1", ownerCount)
	}

	// first.OwnerMessage is the zero value: Send returns SendResult{} on
	// error, so the task ID for the still-running task is read back from
	// durable history above, not from that call's own return value.
	detail, err := f.runtime.Tasks.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	if detail.Task.Status != tasks.StatusRunning {
		t.Fatalf("task status=%q, want %q (still running, holding its original StartAttempt lease)", detail.Task.Status, tasks.StatusRunning)
	}
}

// TestCEOChatProvisioningFailureAfterLeaseExpiryReachesDeadLetterSafely
// establishes the real end state, and corrects the prior round's report:
// because MaxAttempts is 1 for every ceochat turn task,
// internal/tasks/postgres/reconcile.go's reconcileExpiredLeases finds
// AttemptCount >= MaxAttempts already true once this task's lease expires
// and moves it straight to StatusDeadLetter -- a terminal state -- rather
// than back to StatusReady. The SAME idempotency key's turn therefore
// never succeeds after a provisioning failure, by any path: a further
// Send() with the same key returns cleanly (no error, no ErrRunNotReady
// loop) but with Outcome=Incomplete, and zero duplicate side effects. The
// owner's actual intent is only recoverable by sending a NEW message (a
// new idempotency key, and therefore a wholly independent
// task/attempt/assignment) in the same conversation -- not exercised
// here, since it needs no special proof: it is ordinary new-turn
// creation, unrelated to any of this recovery machinery.
func TestCEOChatProvisioningFailureAfterLeaseExpiryReachesDeadLetterSafely(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	provisionErr := errors.New("simulated authorized-attempt provisioning failure")
	const shortLease = 200 * time.Millisecond
	service, provisioner, _, modelExecutorBuilds, topics := f.withFailingAssignmentsAndLease(t, provisionErr, shortLease)

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	const key = "provision-failure-lease-expiry"

	_, err = service.Send(ctx, ceochat.SendRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human", IdempotencyKey: key, Content: "hola"})
	if err == nil || !errors.Is(err, provisionErr) {
		t.Fatalf("first send err=%v, want it to wrap the simulated provisioning failure", err)
	}
	// Send returns SendResult{} on this error path, so the task ID is
	// read back from durable history, not from that call's own return
	// value.
	history, err := service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	var taskID int64
	for _, message := range history {
		if message.Role == ceochat.MessageOwner {
			taskID = message.TaskID
		}
	}
	if taskID == 0 {
		t.Fatal("could not find the owner message's task ID in history")
	}

	// Let the short lease actually expire, then run the SAME reconciliation
	// orgd runs periodically in production -- no shortcut, no direct SQL.
	time.Sleep(shortLease + 100*time.Millisecond)
	if _, err = f.runtime.Tasks.Reconcile(ctx, 10); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	detail, err := f.runtime.Tasks.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("read task after reconcile: %v", err)
	}
	if detail.Task.Status != tasks.StatusDeadLetter {
		t.Fatalf("task status after lease expiry + reconcile=%q, want %q (MaxAttempts=1 exhausted by the one StartAttempt call)", detail.Task.Status, tasks.StatusDeadLetter)
	}

	// A further Send() with the SAME idempotency key must return cleanly
	// -- no error, no infinite ErrRunNotReady -- but must never fabricate
	// success, and must never re-touch the provisioner, model, or tools.
	retry, err := service.Send(ctx, ceochat.SendRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human", IdempotencyKey: key, Content: "hola"})
	if err != nil {
		t.Fatalf("retry after dead_letter err=%v, want nil", err)
	}
	if retry.Outcome != ceochat.RunOutcomeIncomplete {
		t.Fatalf("retry outcome=%v, want Incomplete", retry.Outcome)
	}
	if retry.AssistantMessage != nil {
		t.Fatal("retry produced an assistant message despite the turn being permanently dead_letter")
	}
	if provisioner.calls != 1 {
		t.Fatalf("provisioner calls after dead_letter retry=%d, want still 1 (never re-invoked once the task is terminal)", provisioner.calls)
	}
	if *modelExecutorBuilds != 0 || topics.calls != 0 {
		t.Fatalf("model executor builds=%d tool calls=%d, want both 0", *modelExecutorBuilds, topics.calls)
	}

	history, err = service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	ownerCount, assistantCount := 0, 0
	for _, message := range history {
		switch message.Role {
		case ceochat.MessageOwner:
			ownerCount++
		case ceochat.MessageAssistant:
			assistantCount++
		}
	}
	if ownerCount != 1 {
		t.Fatalf("owner messages=%d, want exactly 1 (no duplicate owner message)", ownerCount)
	}
	if assistantCount != 0 {
		t.Fatal("no assistant message may ever be persisted for a permanently dead_letter turn")
	}
}
