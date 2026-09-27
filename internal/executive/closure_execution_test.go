package executive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// See closure_execution.go: the CEO closure is shown the code-runner run the host verified.

// root1406Mission is the mission record of root 1406: every operation exited 0 on one changed file, and
// the independent engineering review that precedes promotion is still pending.
func root1406Mission() TaskRecord {
	mission := codeRunnerTaskForTest()
	for i, evidence := range mission.Evidence {
		if !strings.HasPrefix(evidence.Reference, codeRunnerEvidenceReferencePrefix) {
			continue
		}
		operations := []any{}
		for _, kind := range []string{"APPLY_PATCH", "GOFMT", "GO_BUILD", "GO_VET", "GO_TEST", "FITNESS"} {
			operations = append(operations, map[string]any{"type": kind, "success": true, "truncated": false, "exit_code": float64(0)})
		}
		evidence.Metadata["operations_executed"] = operations
		evidence.Metadata["changed_files"] = map[string]any{"count": float64(1)}
		revision := evidence.Metadata["candidate_revision"].(map[string]any)
		revision["candidate_commit"] = "4b442883b01803636e5e1943c283eaeee2041173"
		mission.Evidence[i] = evidence
	}
	return mission
}

func closureExecutionFixture(t *testing.T, mission TaskRecord) (*Orchestrator, TaskRecord) {
	t.Helper()
	tasks := newMemoryTasks()
	root := codeRunnerRootForTest()
	root.Requirements[1].Status = "satisfied"
	root.CorrelationID = "executive:1406"
	root.AssignedRoleID = CEORoleID
	root.AssignedUnitID = "empresa"
	tasks.tasks[root.ID] = root
	tasks.tasks[mission.ID] = mission
	return &Orchestrator{tasks: tasks, limits: DefaultLimits(), clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) })}, root
}

func TestRoot1406TheClosureIsShownTheVerifiedCodeRunnerRun(t *testing.T) {
	o, root := closureExecutionFixture(t, root1406Mission())
	plan := ExecutivePlan{Objective: "add one case", SuccessCriteria: []string{"go test ./internal/identifiers/... exits 0"}}

	closure, _, err := o.createClosureTask(context.Background(), root, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary := strings.TrimPrefix(closure.Instructions, ceoClosureInstructionPrefix)
	var decoded struct {
		Execution closureExecution `json:"engineering_execution"`
	}
	if err := json.Unmarshal([]byte(summary), &decoded); err != nil {
		t.Fatalf("the closure summary is not the bounded JSON it should be: %v\n%s", err, summary)
	}
	execution := decoded.Execution
	if execution.MissionTaskID != 91 || execution.MissionStatus != "awaiting_verification" {
		t.Fatalf("the closure is not told which mission ran: %+v", execution)
	}
	if execution.EvidenceRef != "code-runner-attempt-evidence://task/91/attempt/7" || len(execution.EvidenceDigest) != 64 {
		t.Fatalf("the closure cannot cite the verified record: %+v", execution)
	}
	if len(execution.Operations) != 6 {
		t.Fatalf("operations=%d, want the six the code-runner ran", len(execution.Operations))
	}
	for _, operation := range execution.Operations {
		if !operation.Success || operation.ExitCode == nil || *operation.ExitCode != 0 {
			t.Fatalf("operation %s is not shown with its exit code 0: %+v", operation.Type, operation)
		}
	}
	if len(execution.Checks) != 4 || execution.ChangedFiles != 1 || execution.CandidateCommit != "4b442883b01803636e5e1943c283eaeee2041173" {
		t.Fatalf("checks, changed files or candidate are missing: %+v", execution)
	}
	if len(execution.PendingMissionRequirements) != 1 || execution.PendingMissionRequirements[0] != "review" {
		t.Fatalf("the pending engineering review is not reported as pending: %v", execution.PendingMissionRequirements)
	}
	if !strings.Contains(summary, `"type":"GO_TEST"`) {
		t.Fatal("GO_TEST is not in what the closure reads")
	}
}

func TestTheClosurePrefixSaysWhatTheExecutionRecordIs(t *testing.T) {
	for _, want := range []string{"engineering_execution", "host-verified record", "cite its evidence_ref", "pending_mission_requirements", "only when this root's own goal asks for it"} {
		if !strings.Contains(ceoClosureInstructionPrefix, want) {
			t.Errorf("the closure prefix lacks %q", want)
		}
	}
}

func TestARootWithoutACodeRunnerRequirementGetsNoExecutionRecord(t *testing.T) {
	// No task port at all: a root that does not require execution must not read one.
	execution, err := (&Orchestrator{}).closureEngineeringExecution(context.Background(), TaskRecord{ID: 5})
	if err != nil || execution != nil {
		t.Fatalf("execution=%+v err=%v, want none", execution, err)
	}
	if summary := boundedClosureSummary(ExecutivePlan{Objective: "x"}, nil, 5, nil, 16000); strings.Contains(summary, "engineering_execution") {
		t.Fatalf("a summary without execution carries the key: %s", summary)
	}
}

func TestAClosureIsNeverCreatedWithoutTheExecutionItDependsOn(t *testing.T) {
	mission := root1406Mission()
	for i, evidence := range mission.Evidence {
		if strings.HasPrefix(evidence.Reference, codeRunnerEvidenceReferencePrefix) {
			evidence.Metadata["checks_run"] = evidence.Metadata["checks_run"].([]any)[:3]
			mission.Evidence[i] = evidence
		}
	}
	o, root := closureExecutionFixture(t, mission)
	if _, _, err := o.createClosureTask(context.Background(), root, ExecutivePlan{Objective: "x"}, nil); !errors.Is(err, ErrCodeRunnerExecutionInvalid) {
		t.Fatalf("a closure was created over an unverifiable execution: %v", err)
	}
	all, err := o.tasks.ListByCorrelation(context.Background(), root.CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if task.TaskClass == TaskClassCoordinationCEOClosure {
			t.Fatal("the closure task exists")
		}
	}
}

// root1428Mission is root 1406's record as the code-runner now writes it: every gate with the command
// it ran, and the sealed paths.
func root1428Mission(paths []any) TaskRecord {
	mission := root1406Mission()
	for i, evidence := range mission.Evidence {
		if !strings.HasPrefix(evidence.Reference, codeRunnerEvidenceReferencePrefix) {
			continue
		}
		commands := map[string][]any{
			"GO_BUILD": {"go", "build", "./..."}, "GO_VET": {"go", "vet", "./..."}, "GO_TEST": {"go", "test", "./..."},
			"FITNESS": {"make", "test-kernel-governance-fitness", "test-executive-fitness"},
		}
		for _, raw := range evidence.Metadata["checks_run"].([]any) {
			check := raw.(map[string]any)
			check["command"] = commands[check["type"].(string)]
		}
		evidence.Metadata["changed_files"] = map[string]any{"count": float64(1), "paths": paths}
		mission.Evidence[i] = evidence
	}
	return mission
}

func TestRoot1428TheClosureIsShownWhatRanAndWhichFileChanged(t *testing.T) {
	o, root := closureExecutionFixture(t, root1428Mission([]any{"internal/identifiers/identifiers_test.go"}))
	execution, err := o.closureEngineeringExecution(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.ChangedPaths) != 1 || execution.ChangedPaths[0] != "internal/identifiers/identifiers_test.go" {
		t.Fatalf("the closure is not told which file changed: %v", execution.ChangedPaths)
	}
	commands := map[string]string{}
	for _, check := range execution.Checks {
		commands[check.Type] = check.Command
	}
	if commands["GO_TEST"] != "go test ./..." {
		t.Fatalf("the closure is not told what GO_TEST ran: %q", commands["GO_TEST"])
	}
	for _, want := range []string{"finished and the host verified it", "only thing pending is the independent engineering review", "separate owner decision"} {
		if !strings.Contains(execution.MissionState, want) {
			t.Errorf("the mission state lacks %q: %s", want, execution.MissionState)
		}
	}
	closure, _, err := o.createClosureTask(context.Background(), root, ExecutivePlan{Objective: "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"changed_paths":["internal/identifiers/identifiers_test.go"]`, `"command":"go test ./..."`, `"mission_state":"the code-runner execution finished`} {
		if !strings.Contains(closure.Instructions, want) {
			t.Errorf("the closure instructions lack %s", want)
		}
	}
}

func TestPathsThatDoNotAccountForEveryChangeAreNotShown(t *testing.T) {
	mission := root1428Mission([]any{"internal/identifiers/identifiers_test.go"})
	for i, evidence := range mission.Evidence {
		if strings.HasPrefix(evidence.Reference, codeRunnerEvidenceReferencePrefix) {
			evidence.Metadata["changed_files"].(map[string]any)["count"] = float64(2)
			mission.Evidence[i] = evidence
		}
	}
	o, root := closureExecutionFixture(t, mission)
	execution, err := o.closureEngineeringExecution(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ChangedPaths != nil || execution.ChangedFiles != 2 {
		t.Fatalf("a partial path list was shown as the changed files: %v of %d", execution.ChangedPaths, execution.ChangedFiles)
	}
}

func TestTheMissionStateSaysWhatIsLeft(t *testing.T) {
	for _, c := range []struct {
		status  string
		pending []string
		want    string
	}{
		{"awaiting_verification", nil, "nothing is pending on the mission"},
		{"awaiting_verification", []string{"review", "engineering-required-gates"}, "still pending on the mission: review, engineering-required-gates"},
		{"completed", nil, "mission status completed"},
	} {
		if got := missionStateForClosure(c.status, c.pending); !strings.Contains(got, c.want) {
			t.Errorf("missionStateForClosure(%q, %v) = %q, want it to say %q", c.status, c.pending, got, c.want)
		}
	}
}
