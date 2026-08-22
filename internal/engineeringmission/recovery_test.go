package engineeringmission

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const (
	failedBase  = "41ef0164ffffffffffffffffffffffffffffffff"
	movedHead   = "7cb5d5c4ffffffffffffffffffffffffffffffff"
	missionRepo = "explorarte-organization"
	missionRef  = "refs/heads/release"
)

type fakeRecoveryPort struct {
	tasks.TaskReader
	detail   tasks.TaskDetail
	attempts []tasks.Attempt
	letters  []tasks.DeadLetter
	redrives []tasks.RedriveCommand
	evidence []tasks.RecordEvidenceCommand
	released []int64
	redrive  func(tasks.RedriveCommand) (tasks.RedriveResult, error)
}

func (f *fakeRecoveryPort) GetTask(context.Context, int64) (tasks.TaskDetail, error) {
	return f.detail, nil
}
func (f *fakeRecoveryPort) ListAttempts(context.Context, int64) ([]tasks.Attempt, error) {
	return f.attempts, nil
}
func (f *fakeRecoveryPort) ListDeadLetters(context.Context, int) ([]tasks.DeadLetter, error) {
	return f.letters, nil
}
func (f *fakeRecoveryPort) RedriveDeadLetter(_ context.Context, c tasks.RedriveCommand) (tasks.RedriveResult, error) {
	f.redrives = append(f.redrives, c)
	if f.redrive != nil {
		return f.redrive(c)
	}
	return tasks.RedriveResult{Successor: tasks.Task{ID: 999, Status: tasks.StatusBlocked}, Created: true, Episode: 1}, nil
}
func (f *fakeRecoveryPort) RecordEvidence(_ context.Context, c tasks.RecordEvidenceCommand) (tasks.Evidence, error) {
	f.evidence = append(f.evidence, c)
	return tasks.Evidence{}, nil
}
func (f *fakeRecoveryPort) ReleaseCoordinationHold(_ context.Context, c tasks.ReleaseCoordinationHoldCommand) (tasks.Task, error) {
	f.released = append(f.released, c.TaskID)
	return tasks.Task{ID: c.TaskID}, nil
}

type fixedMission MissionPolicy

func (m fixedMission) Resolve(context.Context, int64) (MissionPolicy, error) {
	return MissionPolicy(m), nil
}

type missingMission struct{}

func (missingMission) Resolve(context.Context, int64) (MissionPolicy, error) {
	return MissionPolicy{}, errors.New("engineering policy missing")
}

type fixedHead string

func (h fixedHead) ResolveHead(_ context.Context, repositoryID, targetRef string) (string, error) {
	// Only the mission's OWN world answers. A resolver asked about any
	// other ref returns a head that never equals a real base, which is how
	// these tests notice if recovery starts consulting the wrong target.
	if repositoryID != missionRepo || targetRef != missionRef {
		return "0000000000000000000000000000000000000000", nil
	}
	return string(h), nil
}

type fixedWorkspaces []staging.Workspace

func (w fixedWorkspaces) ListWorkspaces(_ context.Context, f staging.WorkspaceFilter) ([]staging.Workspace, error) {
	if f.TaskID == 0 {
		return nil, errors.New("recovery must ask for one task's workspaces")
	}
	return []staging.Workspace(w), nil
}

type ambiguity int

func (a ambiguity) UnreconciledAmbiguousInvocations(context.Context, int64) (int, error) {
	return int(a), nil
}

type budget BudgetVerdict

func (b budget) Admit(context.Context, int64) (BudgetVerdict, error) { return BudgetVerdict(b), nil }

func admitted() budget {
	return budget{Admitted: true, Family: "engineering", Remaining: 5e9, Estimated: 1e9}
}
func exhausted() budget { return budget{Family: "engineering", Reason: "family engineering is spent"} }

func missionWorkspace() fixedWorkspaces {
	return fixedWorkspaces{{
		ID: 11, TaskID: 82668, RepositoryID: missionRepo,
		TargetRef: missionRef, BaseCommit: failedBase,
	}}
}

func retryableAttempt(retryable bool) tasks.Attempt {
	return tasks.Attempt{State: tasks.AttemptFailed, Retryable: &retryable}
}

func basePolicy() fixedMission {
	return fixedMission(MissionPolicy{
		SchemaVersion: PolicySchema, BaseSHA: failedBase,
		Objective:          "restore the umask guard",
		AllowedPaths:       []string{"internal/secrets/"},
		AcceptanceCriteria: []string{"go test passes"},
		RequiredGates:      []RequiredGate{{Type: GateTest}},
	})
}

type recoveryOpts struct {
	workspaces fixedWorkspaces
	ambiguous  int
	budget     budget
	mission    MissionResolver
	head       string
}

func recoveryWith(port *fakeRecoveryPort, o recoveryOpts) Recovery {
	if o.mission == nil {
		o.mission = basePolicy()
	}
	if o.head == "" {
		o.head = movedHead
	}
	if o.workspaces == nil {
		o.workspaces = missionWorkspace()
	}
	if o.budget == (budget{}) {
		o.budget = admitted()
	}
	return Recovery{
		Tasks: port, Mission: o.mission, Head: fixedHead(o.head),
		Workspaces: o.workspaces, Ambiguity: ambiguity(o.ambiguous), Budget: o.budget,
		MaxRecoveryEpisodes: 2, RequestedBy: "principal-engineer",
		ActorType: "system", ActorID: "code-runner",
	}
}

func recoveryUnderTest(port *fakeRecoveryPort, mission MissionResolver, head string) Recovery {
	return recoveryWith(port, recoveryOpts{mission: mission, head: head})
}

// The rule this suite holds: a recovery episode requires a failure the runtime
// itself classified as retryable, an outcome that actually settled, a world
// that moved for THIS mission, and a budget that can still pay for it. Each
// half alone reproduces a bug already paid for.
func TestRecoveryEligibility(t *testing.T) {
	ctx := context.Background()
	dead := tasks.DeadLetter{ID: 7, TaskID: 82668}
	transient := func() *fakeRecoveryPort {
		return &fakeRecoveryPort{attempts: []tasks.Attempt{retryableAttempt(true)}}
	}

	t.Run("retryable failure against a moved head is eligible", func(t *testing.T) {
		decision, err := recoveryWith(transient(), recoveryOpts{}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryEligible {
			t.Fatalf("reason=%q, want eligible", decision.Reason)
		}
		if decision.ObservedChange == "" {
			t.Fatal("an eligible decision must carry the change that justifies it")
		}
		if decision.RepositoryID != missionRepo || decision.TargetRef != missionRef {
			t.Fatalf("decision must name the mission's own world, got %s@%s", decision.RepositoryID, decision.TargetRef)
		}
	})

	t.Run("retryable failure against an unchanged head is refused", func(t *testing.T) {
		decision, err := recoveryWith(transient(), recoveryOpts{head: failedBase}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryUnchangedWorld {
			t.Fatalf("reason=%q, want unchanged_world", decision.Reason)
		}
		if decision.ObservedChange != "" {
			t.Fatal("a refusal must not carry a justification")
		}
	})

	t.Run("a permanent failure is refused even when the head moved", func(t *testing.T) {
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{retryableAttempt(false)}}
		decision, err := recoveryWith(port, recoveryOpts{}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryPermanent {
			t.Fatalf("reason=%q, want permanent", decision.Reason)
		}
	})

	t.Run("an unclassified failure fails closed", func(t *testing.T) {
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{{State: tasks.AttemptFailed}}}
		decision, err := recoveryWith(port, recoveryOpts{}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryUnclassified {
			t.Fatalf("reason=%q, want unclassified", decision.Reason)
		}
	})

	t.Run("the exhausting attempt decides, even when it was never classified", func(t *testing.T) {
		// The case adversarial review broke the first version on: an
		// earlier transient hiccup must NOT lend its verdict to a final
		// attempt that carries none. Skipping unclassified attempts
		// turned "nobody classified this failure" into "retryable".
		earlier := retryableAttempt(true)
		earlier.Ordinal = 1
		unclassified := tasks.Attempt{Ordinal: 2, State: tasks.AttemptFailed}
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{earlier, unclassified}}
		decision, err := recoveryWith(port, recoveryOpts{}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryUnclassified {
			t.Fatalf("reason=%q, want unclassified", decision.Reason)
		}
	})

	t.Run("an earlier transient failure does not rescue a final permanent one", func(t *testing.T) {
		first := retryableAttempt(true)
		first.Ordinal = 1
		last := retryableAttempt(false)
		last.Ordinal = 2
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{first, last}}
		decision, err := recoveryWith(port, recoveryOpts{}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryPermanent {
			t.Fatalf("reason=%q, want permanent", decision.Reason)
		}
	})

	t.Run("an unsettled outcome is not a transient failure", func(t *testing.T) {
		// The provider may already have received and acted on the
		// request. Repeating it would duplicate side effects and spend
		// again, on the strength of an outcome nobody reconciled.
		decision, err := recoveryWith(transient(), recoveryOpts{ambiguous: 1}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryUnreconciled {
			t.Fatalf("reason=%q, want unreconciled_ambiguous_outcome", decision.Reason)
		}
	})

	t.Run("a mission that never opened a workspace fails closed", func(t *testing.T) {
		decision, err := recoveryWith(transient(), recoveryOpts{workspaces: fixedWorkspaces{}}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryNoWorkspace {
			t.Fatalf("reason=%q, want no_workspace", decision.Reason)
		}
	})

	t.Run("an exhausted program budget refuses the episode", func(t *testing.T) {
		decision, err := recoveryWith(transient(), recoveryOpts{budget: exhausted()}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryBudgetExhausted {
			t.Fatalf("reason=%q, want budget_exhausted", decision.Reason)
		}
	})

	t.Run("an already recovered dead letter is not recovered twice", func(t *testing.T) {
		successor := int64(500)
		recovered := tasks.DeadLetter{ID: 7, TaskID: 82668, RedriveTaskID: &successor}
		decision, err := recoveryWith(transient(), recoveryOpts{}).Evaluate(ctx, recovered)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryAlreadyRecovered {
			t.Fatalf("reason=%q, want already_recovered", decision.Reason)
		}
	})

	t.Run("a dead letter that is not a mission is left alone", func(t *testing.T) {
		decision, err := recoveryWith(transient(), recoveryOpts{mission: missingMission{}}).Evaluate(ctx, dead)
		if err != nil {
			t.Fatalf("a non-mission dead letter must not look like a malfunction: %v", err)
		}
		if decision.Reason != RecoveryNotAMission {
			t.Fatalf("reason=%q, want not_a_mission", decision.Reason)
		}
	})
}

// The world that matters is the one the MISSION inhabited, recorded on its own
// workspace -- not whatever repository and ref the running worker happens to
// be configured with. Adversarial review broke the first version here: a
// mission pinned to refs/heads/release was "recovered" because main moved.
func TestRecoveryComparesTheMissionsOwnTarget(t *testing.T) {
	ctx := context.Background()
	port := &fakeRecoveryPort{attempts: []tasks.Attempt{retryableAttempt(true)}}
	// The head resolver answers movedHead ONLY for the mission's own
	// repository and ref. A recovery consulting any other target sees a
	// different SHA and would still call it eligible -- so this test also
	// asserts the decision reports the world it actually consulted.
	decision, err := recoveryWith(port, recoveryOpts{}).Evaluate(ctx, tasks.DeadLetter{ID: 7, TaskID: 82668})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Reason != RecoveryEligible {
		t.Fatalf("reason=%q", decision.Reason)
	}
	if decision.TargetRef != missionRef {
		t.Fatalf("target=%q, want the mission's own %q", decision.TargetRef, missionRef)
	}
	if decision.BaseSHA != failedBase {
		t.Fatalf("base=%q, want the base the workspace recorded", decision.BaseSHA)
	}
	if decision.Head != movedHead {
		t.Fatalf("head=%q, want the head of the mission's own target", decision.Head)
	}
}

// A successor that is not durably a mission is worse than no successor: a
// runner would either reject it or execute it unconstrained by mission policy.
func TestRecoverySuccessorIsARealMissionPinnedToTheNewHead(t *testing.T) {
	ctx := context.Background()
	port := &fakeRecoveryPort{
		attempts: []tasks.Attempt{retryableAttempt(true)},
		detail:   tasks.TaskDetail{Task: tasks.Task{ID: 82668, OrganizationID: "explorarte", Status: tasks.StatusBlocked, Instructions: "{\"operations\":[]}"}},
	}
	decision, result, err := recoveryWith(port, recoveryOpts{}).Recover(ctx, tasks.DeadLetter{ID: 7, TaskID: 82668})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Eligible() || !result.Created {
		t.Fatalf("decision=%+v result=%+v", decision, result)
	}
	if len(port.redrives) != 1 {
		t.Fatalf("expected exactly one redrive, got %d", len(port.redrives))
	}
	command := port.redrives[0]

	// Born held: durable at once, claimable by nobody until its policy is.
	if !command.Successor.HoldForCoordination {
		t.Fatal("the successor must be created under a coordination hold")
	}
	if command.MaxRecoveryEpisodes != 2 {
		t.Fatalf("max_recovery_episodes=%d, want the configured 2", command.MaxRecoveryEpisodes)
	}
	if command.ObservedChange == "" {
		t.Fatal("the successor must carry its justification durably")
	}
	if command.Successor.OrganizationID != "explorarte" {
		t.Fatalf("successor organization=%q, want the failed mission's", command.Successor.OrganizationID)
	}

	// The mission policy must become durable against the SUCCESSOR's id,
	// pinned to the new head -- this is what makes Mission.Resolve work on
	// it at all.
	if len(port.evidence) != 1 {
		t.Fatalf("expected the successor's mission policy to be recorded once, got %d", len(port.evidence))
	}
	recorded := port.evidence[0]
	if recorded.TaskID != result.Successor.ID {
		t.Fatalf("policy recorded against task %d, want the successor %d", recorded.TaskID, result.Successor.ID)
	}
	if recorded.Reference != "engineering-mission://999" {
		t.Fatalf("reference=%q", recorded.Reference)
	}
	persisted, err := DecodeEvidence(recorded.Metadata)
	if err != nil {
		t.Fatalf("the recorded policy must decode as a mission policy: %v", err)
	}
	if persisted.BaseSHA != movedHead {
		t.Fatalf("persisted base_sha=%q, want the new head %q", persisted.BaseSHA, movedHead)
	}
	if persisted.TaskID != result.Successor.ID {
		t.Fatalf("persisted task_id=%d, want the successor %d", persisted.TaskID, result.Successor.ID)
	}
	if len(persisted.AllowedPaths) != 1 || persisted.AllowedPaths[0] != "internal/secrets" {
		t.Fatalf("the successor must inherit the mission's allowed paths, got %v", persisted.AllowedPaths)
	}

	// Only then is it published.
	if len(port.released) != 1 || port.released[0] != result.Successor.ID {
		t.Fatalf("the hold must be released exactly once for the successor, got %v", port.released)
	}

	// Identity differs from the predecessor's, because BaseSHA is hashed.
	predecessor := MissionPolicy(basePolicy())
	predecessor.TaskID = 0
	predecessor, err = predecessor.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	_, predecessorDigest, err := predecessor.MarshalEvidence()
	if err != nil {
		t.Fatal(err)
	}
	if command.Successor.IdempotencyKey == "engineering-mission/"+predecessorDigest {
		t.Fatal("the successor must not reuse the failed mission's identity")
	}
}

// Exhaustion and a taken identity are governed stops, not incidents: the sweep
// reports them as decisions, so a bounded loop ending normally does not page
// anyone and does not retry every minute forever.
func TestRecoveryReportsGovernedStopsAsDecisions(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		name string
		err  error
		want RecoveryReason
	}{
		{"exhausted", tasks.ErrRecoveryEpisodesExhausted, RecoveryExhausted},
		{"identity taken", tasks.ErrSuccessorIdentityTaken, RecoveryIdentityTaken},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			port := &fakeRecoveryPort{
				attempts: []tasks.Attempt{retryableAttempt(true)},
				detail:   tasks.TaskDetail{Task: tasks.Task{ID: 82668, OrganizationID: "explorarte", Instructions: "{\"operations\":[]}"}},
				letters:  []tasks.DeadLetter{{ID: 7, TaskID: 82668}},
			}
			port.redrive = func(tasks.RedriveCommand) (tasks.RedriveResult, error) {
				return tasks.RedriveResult{}, testCase.err
			}
			decisions, err := recoveryWith(port, recoveryOpts{}).RecoverPending(ctx, 10)
			if err != nil {
				t.Fatalf("a governed stop must not be reported as a sweep failure: %v", err)
			}
			if len(decisions) != 1 || decisions[0].Reason != testCase.want {
				t.Fatalf("decisions=%+v, want a single %q decision", decisions, testCase.want)
			}
			if len(port.released) != 0 {
				t.Fatal("nothing may be published when no successor was created")
			}
		})
	}
}
