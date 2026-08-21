package engineeringmission

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type fakeRecoveryPort struct {
	tasks.TaskReader
	detail   tasks.TaskDetail
	attempts []tasks.Attempt
	letters  []tasks.DeadLetter
	redrives []tasks.RedriveCommand
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
	return tasks.RedriveResult{Successor: tasks.Task{ID: 999}, Created: true, Episode: 1}, nil
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

func (h fixedHead) ResolveHead(context.Context, string, string) (string, error) {
	return string(h), nil
}

const (
	failedBase = "41ef0164ffffffffffffffffffffffffffffffff"
	movedHead  = "7cb5d5c4ffffffffffffffffffffffffffffffff"
)

func retryableAttempt(retryable bool) tasks.Attempt {
	return tasks.Attempt{State: tasks.AttemptFailed, Retryable: &retryable}
}

func recoveryUnderTest(port *fakeRecoveryPort, mission MissionResolver, head string) Recovery {
	return Recovery{
		Tasks: port, Mission: mission, Head: fixedHead(head),
		RepositoryID: "explorarte-organization", TargetRef: "refs/heads/main",
		MaxRecoveryEpisodes: 2,
		RequestedBy:         "principal-engineer", ActorType: "system", ActorID: "code-runner",
	}
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

// The rule this suite exists to hold: a recovery episode requires BOTH a
// failure the runtime itself classified as retryable AND a world that actually
// moved. Either half alone reproduces a bug we have already paid for --
// classification alone re-runs a deterministic failure until the episode
// budget is gone, and movement alone recovers failures that can never pass.
func TestRecoveryEligibility(t *testing.T) {
	ctx := context.Background()
	dead := tasks.DeadLetter{ID: 7, TaskID: 82668}

	t.Run("retryable failure against a moved head is eligible", func(t *testing.T) {
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{retryableAttempt(true)}}
		decision, err := recoveryUnderTest(port, basePolicy(), movedHead).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryEligible {
			t.Fatalf("reason=%q, want eligible", decision.Reason)
		}
		if decision.ObservedChange == "" {
			t.Fatal("an eligible decision must carry the change that justifies it")
		}
	})

	t.Run("retryable failure against an unchanged head is refused", func(t *testing.T) {
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{retryableAttempt(true)}}
		decision, err := recoveryUnderTest(port, basePolicy(), failedBase).Evaluate(ctx, dead)
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
		decision, err := recoveryUnderTest(port, basePolicy(), movedHead).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryPermanent {
			t.Fatalf("reason=%q, want permanent", decision.Reason)
		}
	})

	t.Run("an unclassified failure fails closed", func(t *testing.T) {
		// No attempt carries a verdict: the runtime never said this was
		// transient. Absence of information is not evidence of a
		// transient failure, so recovery must not happen.
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{{State: tasks.AttemptFailed}}}
		decision, err := recoveryUnderTest(port, basePolicy(), movedHead).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryUnclassified {
			t.Fatalf("reason=%q, want unclassified", decision.Reason)
		}
	})

	t.Run("the last classified attempt decides", func(t *testing.T) {
		// The attempt that exhausted the task is the one that matters. An
		// earlier transient hiccup does not make a final permanent
		// failure recoverable.
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{retryableAttempt(true), retryableAttempt(false)}}
		decision, err := recoveryUnderTest(port, basePolicy(), movedHead).Evaluate(ctx, dead)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryPermanent {
			t.Fatalf("reason=%q, want permanent", decision.Reason)
		}
	})

	t.Run("an already recovered dead letter is not recovered twice", func(t *testing.T) {
		successor := int64(500)
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{retryableAttempt(true)}}
		recovered := tasks.DeadLetter{ID: 7, TaskID: 82668, RedriveTaskID: &successor}
		decision, err := recoveryUnderTest(port, basePolicy(), movedHead).Evaluate(ctx, recovered)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != RecoveryAlreadyRecovered {
			t.Fatalf("reason=%q, want already_recovered", decision.Reason)
		}
	})

	t.Run("a dead letter that is not a mission is left alone", func(t *testing.T) {
		port := &fakeRecoveryPort{attempts: []tasks.Attempt{retryableAttempt(true)}}
		decision, err := recoveryUnderTest(port, missingMission{}, movedHead).Evaluate(ctx, dead)
		if err != nil {
			t.Fatalf("a non-mission dead letter must not look like a malfunction: %v", err)
		}
		if decision.Reason != RecoveryNotAMission {
			t.Fatalf("reason=%q, want not_a_mission", decision.Reason)
		}
	})
}

// The successor must be pinned to the commit that makes a different outcome
// possible. A successor that inherited the failed mission's base would be the
// same work under the same conditions -- and, because the mission identity
// digest covers BaseSHA, it would also collide with the predecessor's own
// idempotency key and quietly resolve to the failed task itself.
func TestRecoverySuccessorIsPinnedToTheNewHead(t *testing.T) {
	ctx := context.Background()
	port := &fakeRecoveryPort{
		attempts: []tasks.Attempt{retryableAttempt(true)},
		detail:   tasks.TaskDetail{Task: tasks.Task{ID: 82668, OrganizationID: "explorarte", Instructions: "{\"operations\":[]}"}},
	}
	recovery := recoveryUnderTest(port, basePolicy(), movedHead)
	decision, result, err := recovery.Recover(ctx, tasks.DeadLetter{ID: 7, TaskID: 82668})
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
	if command.MaxRecoveryEpisodes != 2 {
		t.Fatalf("max_recovery_episodes=%d, want the configured 2", command.MaxRecoveryEpisodes)
	}
	if command.ObservedChange == "" {
		t.Fatal("the successor must carry its justification durably")
	}

	// Rebuild the predecessor's identity and prove the successor's differs.
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
	if command.Successor.Instructions != "{\"operations\":[]}" {
		t.Fatalf("the successor must carry the mission plan, got %q", command.Successor.Instructions)
	}
	// The successor belongs to the failed mission's organization, taken
	// from the failed task itself rather than from recovery configuration
	// that could disagree with it.
	if command.Successor.OrganizationID != "explorarte" {
		t.Fatalf("successor organization=%q, want the failed mission's", command.Successor.OrganizationID)
	}
}

// Exhaustion is a governed stop, not an incident: the sweep reports it as a
// decision rather than surfacing it as an error, so a bounded loop ending
// normally does not page anyone.
func TestRecoveryReportsExhaustionAsADecision(t *testing.T) {
	ctx := context.Background()
	port := &fakeRecoveryPort{
		attempts: []tasks.Attempt{retryableAttempt(true)},
		detail:   tasks.TaskDetail{Task: tasks.Task{ID: 82668, OrganizationID: "explorarte", Instructions: "{\"operations\":[]}"}},
		letters:  []tasks.DeadLetter{{ID: 7, TaskID: 82668}},
	}
	port.redrive = func(tasks.RedriveCommand) (tasks.RedriveResult, error) {
		return tasks.RedriveResult{}, tasks.ErrRecoveryEpisodesExhausted
	}
	decisions, err := recoveryUnderTest(port, basePolicy(), movedHead).RecoverPending(ctx, 10)
	if err != nil {
		t.Fatalf("exhaustion must not be reported as a sweep failure: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Reason != RecoveryExhausted {
		t.Fatalf("decisions=%+v, want a single exhausted decision", decisions)
	}
}
