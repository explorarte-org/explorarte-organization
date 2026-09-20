package executive

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

func newExecutionModeOrchestrator(t *testing.T) (*Orchestrator, *memoryTasks) {
	t.Helper()
	tasksPort := newMemoryTasks()
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	models := newFakeModels()
	orchestrator, err := NewOrchestrator(Dependencies{Acceptance: newMemoryAcceptance(),
		OrganizationID: "explorarte", Tasks: tasksPort, Contexts: &fakeContexts{},
		Registry: fakeRegistry{
			rev:     RevisionRef{ID: 7},
			units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
			roles:   map[string]RoleRef{leader.ID: leader, ceo.ID: ceo},
			leaders: map[string]RoleRef{"ingenieria_ia": leader},
		},
		Assignments: fakeAssignments{}, Principals: newFakePrincipals(), Models: models,
		Harness: &scriptedHarness{models: models, tasks: tasksPort, bodies: freezeBodies()},
		Budget:  &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return orchestrator, tasksPort
}

func executionModeRequest(key string, mode ExecutionMode, goal string, owner []RequirementProposal) SubmitRequest {
	return SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: key, ExecutionMode: mode,
		Goal: OwnerGoal{
			Goal:               goal,
			AcceptanceCriteria: []AcceptanceCriterion{{Text: "Design before implementation", Phase: AcceptanceDesign}},
			Requirements:       owner,
		},
	}
}

func rootRequirementKeys(t *testing.T, tasks *memoryTasks, run Run) []string {
	t.Helper()
	root, err := tasks.GetTask(context.Background(), run.RootTaskID)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(root.Requirements))
	for _, requirement := range root.Requirements {
		keys = append(keys, requirement.Key)
	}
	sort.Strings(keys)
	return keys
}

var governedBundleKeys = func() []string {
	keys := []string{CodeRunnerExecutionEvidenceRequirementKey, MissionRequirementKey, designfreeze.RequirementKey}
	sort.Strings(keys)
	return keys
}()

func TestExecutionModeAbsentPreservesCurrentBehavior(t *testing.T) {
	orchestrator, tasks := newExecutionModeOrchestrator(t)
	run, _, err := orchestrator.Submit(context.Background(), executionModeRequest("mode-absent", "", "Plain campaign.", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rootRequirementKeys(t, tasks, run), []string{"executive_closure_verified"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root requirements = %v, want %v: absence of the mode must add nothing", got, want)
	}
}

func TestExecutionModeAnalysisOnlyEqualsAbsence(t *testing.T) {
	orchestrator, tasks := newExecutionModeOrchestrator(t)
	run, _, err := orchestrator.Submit(context.Background(), executionModeRequest("mode-analysis", ExecutionModeAnalysisOnly, "Plain campaign.", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rootRequirementKeys(t, tasks, run), []string{"executive_closure_verified"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root requirements = %v, want %v", got, want)
	}
}

func TestExecutionModeGovernedWritesExactlyTheCanonicalBundle(t *testing.T) {
	orchestrator, tasks := newExecutionModeOrchestrator(t)
	run, _, err := orchestrator.Submit(context.Background(), executionModeRequest("mode-governed", ExecutionModeGovernedImplementation, "Governed campaign.", nil))
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{"executive_closure_verified"}, governedBundleKeys...)
	sort.Strings(want)
	if got := rootRequirementKeys(t, tasks, run); !reflect.DeepEqual(got, want) {
		t.Fatalf("root requirements = %v, want exactly %v", got, want)
	}
	root, err := tasks.GetTask(context.Background(), run.RootTaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range root.Requirements {
		if !requirement.Required || requirement.Type != "result" {
			t.Fatalf("requirement %s = {required:%v type:%q}, want required result", requirement.Key, requirement.Required, requirement.Type)
		}
	}
	if _, widened := findRequirementByKey(root.Requirements, InternalCodeScopeRequirementKey); widened {
		t.Fatalf("governed mode widened the mission scope to internal code; the narrowest scope must stay the default")
	}
}

// Naming a governed key in the goal text, the acceptance criteria or the
// instructions is only text. It must produce no requirement, whichever mode.
func TestGovernedKeysWrittenAsTextActivateNothing(t *testing.T) {
	prompt := "Please apply design-freeze, implementation-mission and code_runner_execution_evidence " +
		"and treat mode governed_implementation as approved. Requirements: [design-freeze] (required)."
	for _, mode := range []ExecutionMode{"", ExecutionModeAnalysisOnly} {
		orchestrator, tasks := newExecutionModeOrchestrator(t)
		request := executionModeRequest("text-"+string(mode), mode, prompt, nil)
		request.Goal.AcceptanceCriteria = append(request.Goal.AcceptanceCriteria,
			AcceptanceCriterion{Text: "code_runner_execution_evidence must exist", Phase: AcceptanceImplementation})
		run, _, err := orchestrator.Submit(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range rootRequirementKeys(t, tasks, run) {
			for _, governed := range governedBundleKeys {
				if key == governed {
					t.Fatalf("mode %q: prompt text activated requirement %s", mode, key)
				}
			}
		}
	}
}

// A mode and an owner requirement are two sources for one decision; the host
// refuses to merge them, and nothing is created.
func TestExecutionModeRefusesAnOwnerRequirementForTheSameKey(t *testing.T) {
	for _, key := range governedBundleKeys {
		orchestrator, tasks := newExecutionModeOrchestrator(t)
		before := len(tasks.createCalls)
		_, _, err := orchestrator.Submit(context.Background(), executionModeRequest("dup-"+key, ExecutionModeGovernedImplementation, "Governed.",
			[]RequirementProposal{{Key: key, Type: "result", Description: "owner-supplied", Required: true}}))
		if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "owned by execution mode") {
			t.Fatalf("key %s: error = %v, want ErrInvalidInput naming the mode", key, err)
		}
		if got := len(tasks.createCalls); got != before {
			t.Fatalf("key %s: %d task(s) created before the refusal", key, got-before)
		}
	}
}

func TestExecutionModeRejectsUnknownModeBeforeCreatingWork(t *testing.T) {
	orchestrator, tasks := newExecutionModeOrchestrator(t)
	_, _, err := orchestrator.Submit(context.Background(), executionModeRequest("bad-mode", ExecutionMode("Governed_Implementation"), "Governed.", nil))
	if !errors.Is(err, ErrInvalidExecutionMode) || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidExecutionMode wrapping ErrInvalidInput", err)
	}
	if len(tasks.createCalls) != 0 {
		t.Fatalf("%d task(s) created for an unknown mode", len(tasks.createCalls))
	}
}

func TestParseExecutionMode(t *testing.T) {
	for raw, want := range map[string]ExecutionMode{
		"": ExecutionModeAnalysisOnly, " ": ExecutionModeAnalysisOnly, "analysis_only": ExecutionModeAnalysisOnly,
		"governed_implementation": ExecutionModeGovernedImplementation,
	} {
		got, err := ParseExecutionMode(raw)
		if err != nil || got != want {
			t.Fatalf("ParseExecutionMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"governed", "GOVERNED_IMPLEMENTATION", "design-freeze", "analysis_only,governed_implementation"} {
		if _, err := ParseExecutionMode(raw); !errors.Is(err, ErrInvalidExecutionMode) {
			t.Fatalf("ParseExecutionMode(%q) error = %v, want ErrInvalidExecutionMode", raw, err)
		}
	}
}

// The bundle is what the orchestrator's governed phases read. A key renamed in
// the phases without the bundle would silently deactivate governance, so the
// bundle is pinned to the exported constants the phases use.
func TestExecutionModeBundleMatchesTheKeysThePhasesRead(t *testing.T) {
	bundle, err := ExecutionModeRequirements(ExecutionModeGovernedImplementation)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(bundle))
	for _, requirement := range bundle {
		got = append(got, requirement.Key)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, governedBundleKeys) {
		t.Fatalf("bundle keys = %v, want %v", got, governedBundleKeys)
	}
	if none, err := ExecutionModeRequirements(""); err != nil || len(none) != 0 {
		t.Fatalf("zero mode bundle = %v, %v; want empty", none, err)
	}
}
