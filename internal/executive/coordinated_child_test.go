package executive

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// The property under test is ordering, so almost every assertion below is
// about what is true DURING the sequence, not after it. A test that only
// inspected the final state would pass against the very defect this fix
// removes: the end state was always correct, it was the window in the middle
// that was claimable.

func TestCoordinatedChildIsNeverClaimableBeforeItsCoordinationExists(t *testing.T) {
	fixture := newChildFixture()
	var observed []string
	// Claiming is attempted from INSIDE each coordination step, which is the
	// interleaving a polling worker produces and the only place the old defect
	// was ever observable.
	probe := func(stage string) {
		fixture.tasks.mu.Lock()
		id := fixture.tasks.nextID - 1
		fixture.tasks.mu.Unlock()
		_, _, _, err := fixture.tasks.ClaimTask(context.Background(), ClaimTaskCommand{TaskID: id, HolderPrincipalID: "role-bound/worker", LeaseDuration: time.Minute})
		if err == nil {
			t.Errorf("a child was claimable at %s, before its coordination was durable", stage)
		}
		observed = append(observed, stage)
	}
	fixture.budgets.before = func() { probe("budget inheritance") }
	fixture.messages.before = func() { probe("delegation") }

	child, _, err := fixture.children().Materialize(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 2 {
		t.Fatalf("both coordination steps must have run, saw %v", observed)
	}
	if child.Status != "ready" {
		t.Fatalf("a published independent child must be ready, got %q", child.Status)
	}
	if _, _, _, err = fixture.tasks.ClaimTask(context.Background(), ClaimTaskCommand{TaskID: child.ID, HolderPrincipalID: "role-bound/worker", LeaseDuration: time.Minute}); err != nil {
		t.Fatalf("a published child must be claimable: %v", err)
	}
}

func TestCoordinationFailureLeavesTheChildDurableAndUnpublished(t *testing.T) {
	for _, tc := range []struct {
		name         string
		arm          func(*childFixture)
		wantBudgets  int
		wantMessages int
	}{
		{"budget fails", func(f *childFixture) { f.budgets.err = errors.New("ledger unavailable") }, 1, 0},
		{"delegation fails", func(f *childFixture) { f.messages.err = errors.New("messaging unavailable") }, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newChildFixture()
			tc.arm(fixture)
			if _, _, err := fixture.children().Materialize(context.Background(), fixture.request()); err == nil {
				t.Fatal("a coordination failure must surface")
			}
			// The durable task survives. Creation genuinely happened, and
			// discarding it would throw away durable truth to tidy up a
			// retryable failure.
			child := fixture.only(t)
			if !heldForCoordination(child) {
				t.Fatalf("child must remain held, got status %q reason %q", child.Status, child.ReasonCode)
			}
			if _, _, _, err := fixture.tasks.ClaimTask(context.Background(), ClaimTaskCommand{TaskID: child.ID, HolderPrincipalID: "role-bound/worker", LeaseDuration: time.Minute}); err == nil {
				t.Fatal("a child whose coordination failed must not be claimable")
			}

			// The retry is the same call again, not a second task.
			fixture.budgets.err, fixture.messages.err = nil, nil
			retried, reused, err := fixture.children().Materialize(context.Background(), fixture.request())
			if err != nil {
				t.Fatalf("retry: %v", err)
			}
			if !reused || retried.ID != child.ID {
				t.Fatalf("retry must resume the same durable child, got id %d reused=%v", retried.ID, reused)
			}
			if retried.Status != "ready" {
				t.Fatalf("retry must publish the child, got %q", retried.Status)
			}
			if fixture.budgets.calls != tc.wantBudgets+1 || fixture.messages.calls != tc.wantMessages+1 {
				t.Fatalf("coordination must be re-driven exactly once more, budgets=%d messages=%d", fixture.budgets.calls, fixture.messages.calls)
			}
			if len(fixture.tasks.tasks) != 1 {
				t.Fatalf("recovery must not create a second task, got %d", len(fixture.tasks.tasks))
			}
		})
	}
}

// TestPublishedChildIsNotRepublishedOnRestart covers the crash windows on
// either side of publication. Both are driven by re-running the whole
// sequence, which is the only recovery mechanism this design has.
func TestPublishedChildIsNotRepublishedOnRestart(t *testing.T) {
	fixture := newChildFixture()
	first, _, err := fixture.children().Materialize(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.tasks.publications) != 1 {
		t.Fatalf("want one publication, got %d", len(fixture.tasks.publications))
	}
	again, reused, err := fixture.children().Materialize(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	if !reused || again.ID != first.ID {
		t.Fatalf("restart must find the same child, got id %d reused=%v", again.ID, reused)
	}
	if len(fixture.tasks.publications) != 1 {
		t.Fatalf("an already-published child must not be published again, got %d publications", len(fixture.tasks.publications))
	}
	// Budget inheritance and delegation ARE re-driven, and that is correct:
	// both are durably idempotent on the child task id, and re-driving them is
	// how the crash window between them closes. What must not happen is a
	// second effect, which their own stores guarantee.
	if fixture.budgets.calls != 2 || fixture.messages.calls != 2 {
		t.Fatalf("coordination is re-driven idempotently, budgets=%d messages=%d", fixture.budgets.calls, fixture.messages.calls)
	}
}

func TestSameRoleChildGetsNoDelegationEdge(t *testing.T) {
	fixture := newChildFixture()
	request := fixture.request()
	request.Sender.AssignedRoleID = request.Command.AssignedRoleID
	child, _, err := fixture.children().Materialize(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.messages.calls != 0 {
		t.Fatal("a role handing work to itself crosses no trust boundary; inventing a delegation edge would be denied by the topology validator")
	}
	if fixture.budgets.calls != 1 || child.Status != "ready" {
		t.Fatalf("budget still applies and the child must publish, budgets=%d status=%q", fixture.budgets.calls, child.Status)
	}
}

func TestAbsentProvidersCreateNoObligations(t *testing.T) {
	fixture := newChildFixture()
	children := coordinatedChildren{tasks: fixture.tasks, clock: fixture.clock}
	child, _, err := children.Materialize(context.Background(), fixture.request())
	if err != nil {
		t.Fatalf("an orchestrator with no budget or messaging provider owes the child nothing: %v", err)
	}
	if child.Status != "ready" {
		t.Fatalf("child must still publish, got %q", child.Status)
	}
}

// TestPublicationBarrierDoesNotBypassTheDependencyBarrier keeps the two
// concepts separate. Publishing says the creator finished; it says nothing
// about whether the prerequisites did.
func TestPublicationBarrierDoesNotBypassTheDependencyBarrier(t *testing.T) {
	fixture := newChildFixture()
	first, _, err := fixture.children().Materialize(context.Background(), fixture.request())
	if err != nil {
		t.Fatal(err)
	}
	dependent := fixture.request()
	dependent.Command.IdempotencyKey = "child:dependent"
	dependent.Command.Dependencies = []int64{first.ID}
	child, _, err := fixture.children().Materialize(context.Background(), dependent)
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != "pending" {
		t.Fatalf("a published child with an unsatisfied dependency must be pending, got %q", child.Status)
	}
	if _, _, _, err = fixture.tasks.ClaimTask(context.Background(), ClaimTaskCommand{TaskID: child.ID, HolderPrincipalID: "role-bound/worker", LeaseDuration: time.Minute}); err == nil {
		t.Fatal("a dependent child must not be claimable before its prerequisite completes")
	}
}

func TestAnOperatorUnblockCannotPublishAHeldChild(t *testing.T) {
	fixture := newChildFixture()
	fixture.budgets.err = errors.New("ledger unavailable")
	if _, _, err := fixture.children().Materialize(context.Background(), fixture.request()); err == nil {
		t.Fatal("expected the coordination failure")
	}
	child := fixture.only(t)
	if _, err := fixture.tasks.UnblockTask(context.Background(), child.ID, "human", "owner"); err == nil {
		t.Fatal("an operator clearing an operational block must not be able to publish a child whose coordination never happened")
	}
}

// TestEveryGovernedChildGoesThroughThePublicationBarrier is the guard that
// keeps the fix from decaying. The defect was a two-step sequence available to
// any caller, so the fix is only durable if the raw sequence cannot reappear.
//
// It asserts the exact set of direct CreateTask call sites rather than their
// absence, because "none" is false here and a test that claimed it would have
// to be weakened the first time someone read it. Any NEW direct call site
// fails this test and has to justify itself in this list.
func TestEveryGovernedChildGoesThroughThePublicationBarrier(t *testing.T) {
	// The remaining direct callers, and why each one is not a governed child:
	//
	//	orchestrator.go       the ROOT task. It has no parent, so it inherits
	//	                      no budget (it gets CreateRootBudget instead) and
	//	                      nobody delegates it. There is no coordination to
	//	                      wait for, so there is nothing to hold it for.
	//
	//	design_freeze_phase.go  the adversarial review and adjudication tasks,
	//	mission_phase.go        and the implementation-plan task. These never
	//	                        called attachChildCoordination either, so they
	//	                        are not a vulnerable create-then-coordinate
	//	                        sequence -- they attach no coordination at all.
	//	                        That is a SEPARATE question from this race and
	//	                        changing it here would be scope this fix was
	//	                        told not to take. They are listed so the next
	//	                        reader finds them deliberately excluded rather
	//	                        than accidentally missed.
	allowed := map[string]int{
		"orchestrator.go":        1,
		"design_freeze_phase.go": 2,
		"mission_phase.go":       1,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if count := strings.Count(string(body), "o.tasks.CreateTask("); count > 0 {
			found[name] = count
		}
		if strings.Contains(string(body), "attachChildCoordination") {
			t.Fatalf("%s: the old create-then-coordinate helper is back; two equivalent paths means the race can return through the other one", name)
		}
	}
	for name, count := range found {
		if allowed[name] != count {
			t.Errorf("%s has %d direct CreateTask call sites, expected %d: a governed child must be created through coordinatedChildren, or its publication barrier is optional again", name, count, allowed[name])
		}
	}
	for name, count := range allowed {
		if found[name] != count {
			t.Errorf("%s no longer has the %d direct CreateTask call sites this list documents; update the list and say why", name, count)
		}
	}
}

// ---- fixture ---------------------------------------------------------

type childFixture struct {
	tasks    *memoryTasks
	budgets  *recordingBudgets
	messages *recordingMessages
	clock    Clock
}

func newChildFixture() *childFixture {
	return &childFixture{
		tasks:    newMemoryTasks(),
		budgets:  &recordingBudgets{},
		messages: &recordingMessages{},
		clock:    ClockFunc(func() time.Time { return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC) }),
	}
}

func (f *childFixture) children() coordinatedChildren {
	return coordinatedChildren{tasks: f.tasks, budgets: f.budgets, messages: f.messages, clock: f.clock}
}

func (f *childFixture) request() childRequest {
	root := TaskRecord{ID: 900, OrganizationID: "explorarte", AssignedRoleID: "empresa/ceo", CorrelationID: "corr-1"}
	return childRequest{
		Root: root, Sender: root, Depth: 2,
		Command: CreateTaskCommand{
			RequestedByRoleID: "empresa/ceo", AssignedRoleID: "ingenieria_ia/lider",
			TaskClass: "coordination.department_plan", IdempotencyKey: "child:plan",
			Title: "Department planning", Instructions: "plan", CorrelationID: "corr-1",
		},
	}
}

func (f *childFixture) only(t *testing.T) TaskRecord {
	t.Helper()
	f.tasks.mu.Lock()
	defer f.tasks.mu.Unlock()
	if len(f.tasks.tasks) != 1 {
		t.Fatalf("want exactly one durable task, got %d", len(f.tasks.tasks))
	}
	for _, task := range f.tasks.tasks {
		return task
	}
	return TaskRecord{}
}

// recordingBudgets and recordingMessages count calls and can fail on demand.
// before runs INSIDE the step, which is what lets a test observe the window
// rather than only its outcome.
type recordingBudgets struct {
	calls  int
	err    error
	before func()
}

func (b *recordingBudgets) CreateRootBudget(context.Context, TaskRecord, time.Time) error { return nil }
func (b *recordingBudgets) InheritForChild(_ context.Context, _, _ TaskRecord, _ int64, _ time.Time) error {
	b.calls++
	if b.before != nil {
		b.before()
	}
	return b.err
}

type recordingMessages struct {
	calls  int
	err    error
	before func()
}

func (m *recordingMessages) SendDelegation(_ context.Context, _, _ TaskRecord, _ time.Time) error {
	m.calls++
	if m.before != nil {
		m.before()
	}
	return m.err
}
