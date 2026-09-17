//go:build integration

// GAP 3 of CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_CLOSURE_V1:
// prove exactly what happens when DispatchProvisioner (the seam
// GAP1/GAP2's whole fix wired in right after StartAttempt) itself fails.
// The production ordering is ClaimTaskByID -> StartAttempt ->
// EnsureAuthorizedAssignmentForRunningAttempt -> Contexts.Build ->
// Harness -> Model Runtime; a failure at that one seam must reach the
// Harness, the model executor, the tool executor, and Contexts.Build
// exactly zero times, and must never fabricate an answer.
package ceochat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// failingDispatchProvisioner always denies
// EnsureAuthorizedAssignmentForRunningAttempt with a fixed error, counting
// how many times it was actually called (must be exactly 1: ceochat.Send
// calls it once per turn, never retries within the same call).
type failingDispatchProvisioner struct {
	calls int
	err   error
}

func (p *failingDispatchProvisioner) EnsureAuthorizedAssignmentForRunningAttempt(context.Context, int64, int64) error {
	p.calls++
	return p.err
}

// countingContextBuilder wraps the fixture's real ContextBuilder, counting
// calls without changing behavior -- this test asserts it is NEVER called,
// so what it would have returned never matters.
type countingContextBuilder struct {
	real  ceochat.ContextBuilder
	calls int
}

func (c *countingContextBuilder) Build(ctx context.Context, request ceochat.ContextRequest) (ceochat.ContextSnapshot, error) {
	c.calls++
	return c.real.Build(ctx, request)
}

// withFailingAssignments builds a *ceochat.Service identical to the
// fixture's bootstrapped one -- real Store, Tasks, Principals, Authority,
// HarnessHistory, DescriptorStore -- except Assignments always denies, and
// Contexts/NewModelExecutor/ToolExecutor are all counting spies so this
// test can prove none of them are ever reached.
func (f *chatFixture) withFailingAssignments(t *testing.T, provisionErr error) (service *ceochat.Service, provisioner *failingDispatchProvisioner, contexts *countingContextBuilder, modelExecutorBuilds *int, toolExecutorCalls *countingTopicLister) {
	t.Helper()
	return f.withFailingAssignmentsAndLease(t, provisionErr, 0)
}

// withFailingAssignmentsAndLease is withFailingAssignments generalized to
// accept an explicit LeaseDuration (0 keeps Service's own default) --
// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_FINAL_CLOSURE_V1's lease-
// expiry recovery test needs a short, controllable lease so it can drive a
// real expiry + Reconcile without waiting out the production default.
func (f *chatFixture) withFailingAssignmentsAndLease(t *testing.T, provisionErr error, leaseDuration time.Duration) (service *ceochat.Service, provisioner *failingDispatchProvisioner, contexts *countingContextBuilder, modelExecutorBuilds *int, toolExecutorCalls *countingTopicLister) {
	t.Helper()
	base := *f.runtime.Service
	provisioner = &failingDispatchProvisioner{err: provisionErr}
	base.Assignments = provisioner
	if leaseDuration > 0 {
		base.LeaseDuration = leaseDuration
	}
	wrappedContexts := &countingContextBuilder{real: base.Contexts}
	base.Contexts = wrappedContexts
	builds := 0
	base.NewModelExecutor = func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
		builds++
		return neverInvokedModel{t: t}, nil
	}
	topics := &countingTopicLister{}
	base.ToolExecutor = ceochat.ToolExecutor{Topics: topics, Findings: &fakeFindingsOnly{}}
	opened, err := ceochat.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	return opened, provisioner, wrappedContexts, &builds, topics
}

// neverInvokedModel fails the test outright if the Harness ever reaches a
// real model call -- a provisioning failure must never let the run get
// this far.
type neverInvokedModel struct{ t *testing.T }

func (m neverInvokedModel) Invoke(context.Context, executionharness.RunIdentity, executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.t.Fatal("model executor Invoke reached despite a dispatch provisioning failure")
	return executionharness.ModelResult{}, nil
}

// TestCEOChatDispatchProvisioningFailureHasNoSideEffects is
// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_CLOSURE_V1's GAP 3.
func TestCEOChatDispatchProvisioningFailureHasNoSideEffects(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	provisionErr := errors.New("simulated authorized-attempt provisioning failure")
	service, provisioner, contexts, modelExecutorBuilds, topics := f.withFailingAssignments(t, provisionErr)

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "provision-failure", Content: "hola",
	})

	if err == nil || !errors.Is(err, provisionErr) {
		t.Fatalf("err=%v, want it to wrap the simulated provisioning failure", err)
	}
	if result.Outcome != "" || result.AssistantMessage != nil {
		t.Fatalf("result=%+v, want no outcome and no assistant message on a provisioning failure", result)
	}
	if provisioner.calls != 1 {
		t.Fatalf("provisioner calls=%d, want exactly 1", provisioner.calls)
	}
	if contexts.calls != 0 {
		t.Fatalf("Contexts.Build calls=%d, want 0 (provisioning must fail before context build, per the production ordering)", contexts.calls)
	}
	if *modelExecutorBuilds != 0 {
		t.Fatalf("NewModelExecutor factory calls=%d, want 0", *modelExecutorBuilds)
	}
	if topics.calls != 0 {
		t.Fatalf("tool executor calls=%d, want 0", topics.calls)
	}

	// No assistant message: RecordAttemptResult/FinalizeTask are never
	// reached on this path (Send returns before the Harness even runs), so
	// nothing durable claims this turn succeeded or failed either way.
	history, err := service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	ownerCount, assistantCount := 0, 0
	var ownerTaskID int64
	for _, message := range history {
		switch message.Role {
		case ceochat.MessageOwner:
			ownerCount++
			ownerTaskID = message.TaskID
		case ceochat.MessageAssistant:
			assistantCount++
		}
	}
	if ownerCount != 1 {
		t.Fatalf("owner messages=%d, want 1 (the owner's message is still durably recorded)", ownerCount)
	}
	if assistantCount != 0 {
		t.Fatal("no assistant message may be persisted when dispatch provisioning failed")
	}

	// RECOVERABLE_VIA_EXISTING_PATH: the attempt StartAttempt already
	// claimed is left exactly where it was -- "running", holding its real
	// lease -- because Send returns before RecordAttemptResult/FinalizeTask
	// are ever reached (see driveTurn's ordering: those two run only after
	// the Harness itself returns a result, and the Harness is never
	// entered here). This is the SAME durable shape
	// TestCEOChatAuthorityLossBeforeToolExecutionLeavesNoSideEffect already
	// proves for a different pre-Harness failure (authority loss before
	// tool execution): the task is not completed, not dead_letter, not
	// cancelled -- a retry of the SAME turn (same idempotency key) resumes
	// by reclaiming the SAME still-open attempt via the Task Engine's own
	// existing ClaimTaskByID replay path, once whatever made the
	// provisioner fail (e.g. a transient authority/lineage/routing outage)
	// clears. No new recovery mechanism exists or is needed for this.
	detail, err := f.runtime.Tasks.GetTask(ctx, ownerTaskID)
	if err != nil {
		t.Fatalf("read task after provisioning failure: %v", err)
	}
	if detail.Task.Status != tasks.StatusRunning {
		t.Fatalf("task status=%q, want %q (RECOVERABLE_VIA_EXISTING_PATH: still open for a future retry)", detail.Task.Status, tasks.StatusRunning)
	}
	if detail.ActiveLease == nil {
		t.Fatal("task has no active lease after provisioning failure, want the StartAttempt lease still held")
	}
}
