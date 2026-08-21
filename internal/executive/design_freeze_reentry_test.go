package executive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

func countPurpose(purposes []ExecutionPurpose, target ExecutionPurpose) int {
	total := 0
	for _, purpose := range purposes {
		if purpose == target {
			total++
		}
	}
	return total
}

// The adversarial review is the most expensive execution in the run and the
// one an idempotency mistake punishes hardest. Resume is called repeatedly by
// a worker loop, so "exactly once" has to hold across many calls, not just
// across two.
//
// This is a regression test with a real history: deriving the round from the
// count of completed adjudications made every Resume after a finished round
// compute a higher round, create a fresh review/adjudication pair, and never
// evaluate the gate for the round that had just completed. The run looped
// forever, spending the reviewer every pass, and never recorded a freeze.
func TestRepeatedResumeExecutesTheReviewExactlyOnce(t *testing.T) {
	fixture := newFreezeFixture(t, "freeze", true)
	for i := 0; i < 30; i++ {
		run, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("resume %d: %v", i, err)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	purposes := fixture.purposes()
	if got := countPurpose(purposes, PurposeAdversarialReview); got != 1 {
		t.Fatalf("adversarial review ran %d times: %v", got, purposes)
	}
	if got := countPurpose(purposes, PurposeDesignAdjudication); got != 1 {
		t.Fatalf("adjudication ran %d times: %v", got, purposes)
	}

	// Resuming a completed run adds nothing.
	before := len(fixture.purposes())
	for i := 0; i < 5; i++ {
		if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("post-completion resume: %v", err)
		}
	}
	if after := len(fixture.purposes()); after != before {
		t.Fatalf("resuming a completed run performed %d more executions", after-before)
	}
}

// Exactly one review and one adjudication task are ever created, whatever the
// resume cadence. A task-per-resume leak would be invisible to a purely
// execution-count assertion if creation and execution ever diverged.
func TestResumeCreatesOneReviewAndOneAdjudicationTask(t *testing.T) {
	fixture := newFreezeFixture(t, "freeze", true)
	fixture.drive(t)
	reviews, adjudications := 0, 0
	fixture.tasks.mu.Lock()
	for _, task := range fixture.tasks.tasks {
		switch task.TaskClass {
		case TaskClassCoordinationAdversarialReview:
			reviews++
		case TaskClassCoordinationDesignAdjudication:
			adjudications++
		}
	}
	fixture.tasks.mu.Unlock()
	if reviews != 1 || adjudications != 1 {
		t.Fatalf("reviews=%d adjudications=%d -- the phase is not idempotent", reviews, adjudications)
	}
}

// A process restart mid-phase must resume the same round, not open a new one.
// The durable task store is the only thing that survives, which is exactly the
// condition under which the round counter has to be derivable.
func TestRestartResumesTheSameRoundWithoutRepeatingTheReview(t *testing.T) {
	fixture := newFreezeFixture(t, "freeze", true)

	// Drive only until the review has been executed, then abandon the process.
	for i := 0; i < 24; i++ {
		if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("resume: %v", err)
		}
		if countPurpose(fixture.purposes(), PurposeAdversarialReview) == 1 {
			break
		}
	}
	if countPurpose(fixture.purposes(), PurposeAdversarialReview) != 1 {
		t.Fatal("the review never ran")
	}
	reviewsBefore := countPurpose(fixture.purposes(), PurposeAdversarialReview)

	// A brand new Orchestrator over the SAME durable store: new process, new
	// in-memory lease map, nothing carried over but the tasks themselves.
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	workerRole := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	reviewer := RoleRef{ID: AdversarialReviewerRoleID, UnitID: "investigacion", Enabled: true, Executable: true}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	restarted, err := NewOrchestrator(Dependencies{Acceptance: fixture.acceptance,
		OrganizationID: "explorarte",
		Registry: fakeRegistry{
			rev:     RevisionRef{ID: 7},
			units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
			roles:   map[string]RoleRef{leader.ID: leader, workerRole.ID: workerRole, reviewer.ID: reviewer, ceo.ID: ceo},
			leaders: map[string]RoleRef{"ingenieria_ia": leader},
		},
		Tasks: fixture.tasks, Contexts: &fakeContexts{}, Assignments: fakeAssignments{},
		Principals: newFakePrincipals(), Models: fixture.harness.models, Harness: fixture.harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 24; i++ {
		run, resumeErr := restarted.Resume(context.Background(), fixture.root)
		if resumeErr != nil && !errors.Is(resumeErr, ErrRunBlocked) {
			t.Fatalf("restarted resume: %v", resumeErr)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	if got := countPurpose(fixture.purposes(), PurposeAdversarialReview); got != reviewsBefore {
		t.Fatalf("the restart re-ran the adversarial review (%d -> %d)", reviewsBefore, got)
	}
	if status := requirementStatus(fixture.rootRecord(t), designfreeze.RequirementKey); status != "satisfied" {
		t.Fatalf("the restarted run did not reach a freeze: status=%q", status)
	}
	reviews := 0
	fixture.tasks.mu.Lock()
	for _, task := range fixture.tasks.tasks {
		if task.TaskClass == TaskClassCoordinationAdversarialReview {
			reviews++
		}
	}
	fixture.tasks.mu.Unlock()
	if reviews != 1 {
		t.Fatalf("the restart opened a new round: %d review tasks", reviews)
	}
}

// Once satisfied, the phase is inert: it must not re-review a design the
// organization already froze.
func TestSatisfiedFreezeIsNotReEvaluated(t *testing.T) {
	fixture := newFreezeFixture(t, "freeze", true)
	fixture.drive(t)
	if status := requirementStatus(fixture.rootRecord(t), designfreeze.RequirementKey); status != "satisfied" {
		t.Fatal("setup did not reach a freeze")
	}
	before := len(fixture.purposes())
	for i := 0; i < 3; i++ {
		if _, err := fixture.orchestrator.Resume(context.Background(), fixture.root); err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("resume: %v", err)
		}
	}
	if after := len(fixture.purposes()); after != before {
		t.Fatalf("a satisfied freeze was re-evaluated: %d extra executions", after-before)
	}
}
