package executive

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/missionplan"
)

// Production root 1007 (2026-09-21) was the first governed run to pass the design
// freeze and reach the mission. Its implementation planner wrote a patch with the
// hunk header "@@ -X,X +X,X @@"; the code-runner's `git apply --check` rejected it
// five times, identically. These tests are that run's regressions: a patch that
// cannot apply never reaches the code-runner, and never creates a mission.

const identifiersTestPath = "internal/identifiers/identifiers_test.go"

const identifiersGoal = "Add one table case to TestExtractDigitRunsCoreCases in " + identifiersTestPath + ", " +
	"grounded in ExtractDigitRuns (internal/identifiers/identifiers.go)."

const identifiersSource = "package identifiers\n\nfunc TestExtractDigitRunsCoreCases(t *testing.T) {\n\tcases := []struct {\n\t\tname string\n\t}{\n\t\t{\"no digits\"},\n\t}\n}\n"

// planBodyWithPatch is an implementation-plan/v1 body for path carrying patch.
func planBodyWithPatch(path, patch string) string {
	escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\t", "\\t").Replace(patch)
	return `{"schema_version":"implementation-plan/v1","objective":"Add the case.",` +
		`"changes":[{"path":"` + path + `","intent":"Add a table case.","patch":"` + escaped + `"}],` +
		`"verification_expectations":["go test ./internal/identifiers/..."],"dependency_order":[],"evidence_refs":[]}`
}

func headerFor(path string) string {
	return "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n"
}

// validPatch is structurally sound: real header, counts that match the body.
func validPatch(path string) string {
	return headerFor(path) + "@@ -6,2 +6,3 @@\n \t}{\n \t\t{\"no digits\"},\n+\t\t{\"digits adjacent to letters\"},\n"
}

// placeholderPatch is root 1007's patch, character for character in its header.
func placeholderPatch(path string) string {
	return headerFor(path) + "@@ -X,X +X,X @@\n \t\t{\n+\t\t\tname: \"digits adjacent to letters\",\n \t\t},\n"
}

// fakeWorkbench is the frozen repository: a set of files at the design-freeze
// commit, and git's verdict on patches.
type fakeWorkbench struct {
	mu       sync.Mutex
	files    map[string]string
	verdict  func(call int, patch string) (PatchCheckResult, error)
	checks   []string
	checkSHA []string
	reads    []string
}

func (f *fakeWorkbench) CheckPatch(_ context.Context, baseSHA, patch string) (PatchCheckResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, patch)
	f.checkSHA = append(f.checkSHA, baseSHA)
	if f.verdict == nil {
		return PatchCheckResult{Applies: true}, nil
	}
	return f.verdict(len(f.checks), patch)
}

func (f *fakeWorkbench) ReadFile(_ context.Context, baseSHA, path string, _ int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, baseSHA+":"+path)
	content, ok := f.files[path]
	if !ok {
		return nil, errors.New("file not found at commit")
	}
	return []byte(content), nil
}

func (f *fakeWorkbench) checkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.checks)
}

func planFixture(t *testing.T, body string, workbench *fakeWorkbench) *missionFixture {
	t.Helper()
	fixture := newMissionFixtureWith(t, identifiersTestPath, true, identifiersGoal, WithPatchWorkbench(workbench))
	fixture.harness.bodies[PurposeImplementationPlan] = body
	return fixture
}

// driveThroughRejections resumes the run the way the worker loop does: a contract
// rejection closes the attempt and the next pass retries it; a check that could not
// run is retried too; the run stops when it blocks or ends.
func driveThroughRejections(t *testing.T, fixture *missionFixture) Run {
	t.Helper()
	var last Run
	for pass := 0; pass < 40; pass++ {
		run, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		last = run
		switch {
		case err == nil, errors.Is(err, ErrModelResultContractRejected), errors.Is(err, ErrCompletionFailed):
		case errors.Is(err, ErrRunBlocked):
			return run
		default:
			t.Fatalf("resume pass %d: %v", pass, err)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	return last
}

func planTaskOf(t *testing.T, fixture *missionFixture) TaskRecord {
	t.Helper()
	all, err := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	task, ok := findTaskByKey(all, childKey(fixture.root, "implementation-plan"))
	if !ok {
		t.Fatal("no implementation plan task exists")
	}
	return task
}

func implementationPlanRuns(fixture *missionFixture) int {
	count := 0
	for _, purpose := range fixture.purposes() {
		if purpose == PurposeImplementationPlan {
			count++
		}
	}
	return count
}

func patchFailuresOnRoot(t *testing.T, fixture *missionFixture, planTaskID int64) int {
	t.Helper()
	return patchValidationFailures(fixture.rootRecord(t).Evidence, planTaskID)
}

// "@@ -X,X +X,X @@" -> refused before any mission, without asking git or the
// code-runner; the planner is given its bounded attempts and then the root blocks
// with an explicit reason.
func TestAPlaceholderHunkPatchNeverCreatesAMission(t *testing.T) {
	workbench := &fakeWorkbench{files: map[string]string{identifiersTestPath: identifiersSource}}
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, placeholderPatch(identifiersTestPath)), workbench)

	// Step the run the way a worker does and watch the pass that spends the planner's
	// LAST attempt: it must already answer "blocked, patch invalid" -- not a generic
	// failure that only a later pass would translate.
	var lastPass Run
	for pass := 0; pass < 12 && implementationPlanRuns(fixture) < 3; pass++ {
		run, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && !errors.Is(err, ErrModelResultContractRejected) && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("pass %d: %v", pass, err)
		}
		lastPass = run
	}
	if implementationPlanRuns(fixture) != 3 {
		t.Fatalf("the planner ran %d time(s), want its 3 attempts", implementationPlanRuns(fixture))
	}
	if lastPass.State != StateBlocked || lastPass.ReasonCode != ReasonImplementationPlanPatchInvalid {
		t.Fatalf("the pass that exhausted the planner answered %+v, want blocked with %s", lastPass, ReasonImplementationPlanPatchInvalid)
	}
	run := driveThroughRejections(t, fixture)

	if run.State != StateBlocked || run.ReasonCode != ReasonImplementationPlanPatchInvalid {
		t.Fatalf("run = %+v, want blocked with %s", run, ReasonImplementationPlanPatchInvalid)
	}
	if fixture.provisioner.count() != 0 {
		t.Fatal("a mission was provisioned for a patch that cannot apply")
	}
	if status := requirementStatus(fixture.rootRecord(t), MissionRequirementKey); status == "satisfied" {
		t.Fatal("the implementation-mission requirement was satisfied by a patch that cannot apply")
	}
	// The structure was refused from the diff text; git was not even asked.
	if workbench.checkCount() != 0 {
		t.Fatalf("git apply --check ran %d time(s) for a structurally invalid patch", workbench.checkCount())
	}
	// Bounded regeneration: the task's own attempts, no more.
	planTask := planTaskOf(t, fixture)
	if got := implementationPlanRuns(fixture); got != planTask.MaxAttempts || got != 3 {
		t.Fatalf("the planner ran %d time(s), want exactly its %d attempts", got, planTask.MaxAttempts)
	}
	if got := patchFailuresOnRoot(t, fixture, planTask.ID); got != 3 {
		t.Fatalf("recorded %d patch validation failures, want one per attempt (3)", got)
	}
	// The metric is durable evidence naming the rule that failed.
	var recorded EvidenceRecord
	for _, record := range fixture.rootRecord(t).Evidence {
		if strings.HasPrefix(record.Reference, PatchValidationFailureReference) {
			recorded = record
			break
		}
	}
	if checks, _ := recorded.Metadata["checks"].([]string); len(checks) != 1 || checks[0] != "hunk_header" {
		t.Fatalf("evidence metadata = %+v", recorded.Metadata)
	}
}

func TestAWellFormedPatchOverMissingContextIsRefusedByGitBeforeAnyMission(t *testing.T) {
	workbench := &fakeWorkbench{
		files: map[string]string{identifiersTestPath: identifiersSource},
		verdict: func(int, string) (PatchCheckResult, error) {
			return PatchCheckResult{Applies: false, Detail: "error: patch failed: " + identifiersTestPath + ":6"}, nil
		},
	}
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, validPatch(identifiersTestPath)), workbench)

	run := driveThroughRejections(t, fixture)

	if run.State != StateBlocked || run.ReasonCode != ReasonImplementationPlanPatchInvalid {
		t.Fatalf("run = %+v", run)
	}
	if fixture.provisioner.count() != 0 {
		t.Fatal("a mission was provisioned for a patch git refused")
	}
	if workbench.checkCount() != 3 {
		t.Fatalf("git was asked %d time(s), want once per attempt (3)", workbench.checkCount())
	}
}

// A patch may touch only its own declared path: the declared path is what scope and
// the denylist are judged on.
func TestAPatchTouchingAFileOtherThanItsDeclaredPathIsRefused(t *testing.T) {
	workbench := &fakeWorkbench{}
	other := "internal/authorization/policy.go"
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, validPatch(other)), workbench)

	run := driveThroughRejections(t, fixture)

	if run.ReasonCode != ReasonImplementationPlanPatchInvalid || fixture.provisioner.count() != 0 {
		t.Fatalf("run = %+v, missions = %d", run, fixture.provisioner.count())
	}
	if workbench.checkCount() != 0 {
		t.Fatal("git was asked about a patch whose structure already failed")
	}
}

// A path outside the mission's scope is still a refusal before any mission. Scope is
// the host's, so it is not a matter of asking the planner again.
func TestAPatchOutsideTheMissionScopeIsRefusedBeforeAnyMission(t *testing.T) {
	workbench := &fakeWorkbench{}
	outside := "internal/authorization/policy.go"
	fixture := planFixture(t, planBodyWithPatch(outside, validPatch(outside)), workbench)

	run := driveThroughRejections(t, fixture)

	if run.State != StateBlocked || run.ReasonCode != ReasonMissionPolicyRejected {
		t.Fatalf("run = %+v, want blocked with %s", run, ReasonMissionPolicyRejected)
	}
	if fixture.provisioner.count() != 0 {
		t.Fatal("a mission was provisioned for a path outside its scope")
	}
}

// A patch that applies to the FROZEN commit creates the mission, and the mission
// evidence carries the cost of thinking the patch.
func TestAValidPatchOverTheFrozenCommitCreatesTheMission(t *testing.T) {
	workbench := &fakeWorkbench{files: map[string]string{identifiersTestPath: identifiersSource}}
	patch := validPatch(identifiersTestPath)
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, patch), workbench)

	driveThroughRejections(t, fixture)

	if fixture.provisioner.count() != 1 {
		t.Fatalf("missions provisioned = %d, want 1", fixture.provisioner.count())
	}
	if workbench.checkCount() != 1 || workbench.checks[0] != patch {
		t.Fatalf("git was asked %d time(s) about %q", workbench.checkCount(), workbench.checks)
	}
	if workbench.checkSHA[0] != targetSHA {
		t.Fatalf("the patch was checked against %s, want the design-freeze commit %s", workbench.checkSHA[0], targetSHA)
	}
	if got := patchFailuresOnRoot(t, fixture, planTaskOf(t, fixture).ID); got != 0 {
		t.Fatalf("recorded %d patch validation failures for a valid patch", got)
	}
	var mission EvidenceRecord
	for _, record := range fixture.rootRecord(t).Evidence {
		if strings.HasPrefix(record.Reference, "engineering-mission://") {
			mission = record
		}
	}
	if attempts, _ := mission.Metadata["implementation_plan_attempts"].(int); attempts != 1 {
		t.Fatalf("mission evidence = %+v, want implementation_plan_attempts=1", mission.Metadata)
	}
	if failures, _ := mission.Metadata["patch_validation_failures"].(int); failures != 0 {
		t.Fatalf("mission evidence = %+v, want patch_validation_failures=0", mission.Metadata)
	}
}

// Regeneration: the first patch git refuses, the planner's next answer applies, and
// the run continues normally -- without ever having reached the code-runner.
func TestARegeneratedPatchThatAppliesContinuesNormally(t *testing.T) {
	workbench := &fakeWorkbench{
		files: map[string]string{identifiersTestPath: identifiersSource},
		verdict: func(call int, _ string) (PatchCheckResult, error) {
			if call == 1 {
				return PatchCheckResult{Applies: false, Detail: "error: patch failed"}, nil
			}
			return PatchCheckResult{Applies: true}, nil
		},
	}
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, validPatch(identifiersTestPath)), workbench)

	run := driveThroughRejections(t, fixture)

	if fixture.provisioner.count() != 1 {
		t.Fatalf("run = %+v, missions = %d; the regenerated patch did not create the mission", run, fixture.provisioner.count())
	}
	planTask := planTaskOf(t, fixture)
	if planTask.AttemptCount != 2 || implementationPlanRuns(fixture) != 2 {
		t.Fatalf("plan attempts = %d, runs = %d; want one regeneration", planTask.AttemptCount, implementationPlanRuns(fixture))
	}
	if got := patchFailuresOnRoot(t, fixture, planTask.ID); got != 1 {
		t.Fatalf("recorded %d failures, want exactly the first", got)
	}
}

// A check that could not RUN says nothing about the patch: it is not counted as a
// rejected patch, does not consume a regeneration as one, and blocks nothing as
// "patch invalid".
func TestAPatchCheckThatCannotRunIsNotABadPatch(t *testing.T) {
	workbench := &fakeWorkbench{
		files: map[string]string{identifiersTestPath: identifiersSource},
		verdict: func(int, string) (PatchCheckResult, error) {
			return PatchCheckResult{}, errors.New("git: timed out")
		},
	}
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, validPatch(identifiersTestPath)), workbench)
	driveThroughRejections(t, fixture)
	if fixture.provisioner.count() != 0 {
		t.Fatal("a mission was created on an unverified patch")
	}
	if got := patchFailuresOnRoot(t, fixture, planTaskOf(t, fixture).ID); got != 0 {
		t.Fatalf("recorded %d patch validation failures for a check that never ran", got)
	}
	if run, _ := fixture.orchestrator.Status(context.Background(), fixture.root); run.ReasonCode == ReasonImplementationPlanPatchInvalid {
		t.Fatal("an infrastructure failure was reported as an invalid patch")
	}
}

// The planner is handed the exact file, with its commit and its line numbers, so
// that placeholder coordinates and an invented layout have nothing to stand on.
func TestThePlannerReceivesTheExactSourceItPatches(t *testing.T) {
	workbench := &fakeWorkbench{files: map[string]string{
		identifiersTestPath:                identifiersSource,
		"internal/authorization/policy.go": "package authorization\n", // named by the goal, but out of scope
	}}
	fixture := newMissionFixtureWith(t, identifiersTestPath, true,
		identifiersGoal+" Do not touch internal/authorization/policy.go.", WithPatchWorkbench(workbench))
	fixture.harness.bodies[PurposeImplementationPlan] = planBodyWithPatch(identifiersTestPath, validPatch(identifiersTestPath))
	driveThroughRejections(t, fixture)

	command, ok := fixture.commandFor(PurposeImplementationPlan)
	if !ok {
		t.Fatal("the implementation plan never ran")
	}
	contract := command.ExecutionContract
	for _, want := range []string{
		"SOURCE FOR PATCHING (read by the host at commit " + targetSHA + ")",
		"FILE " + identifiersTestPath + " @ " + targetSHA + " (9 lines):",
		"    1 | package identifiers",
		"    7 | \t\t{\"no digits\"},",
		"Never use placeholder coordinates such as @@ -X,X +X,X @@",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("the plan contract lacks %q", want)
		}
	}
	// A file the goal names but the mission could never change is not shown, and is
	// not even read, though the workbench could.
	workbench.mu.Lock()
	readPolicy := false
	for _, read := range workbench.reads {
		readPolicy = readPolicy || strings.HasSuffix(read, "internal/authorization/policy.go")
	}
	workbench.mu.Unlock()
	if readPolicy || strings.Contains(contract, "FILE internal/authorization/policy.go") {
		t.Error("a file outside the mission scope was shown to the planner")
	}
	// Only the implementation planner receives it.
	for _, purpose := range []ExecutionPurpose{PurposeDepartmentWorker, PurposeDepartmentReview, PurposeDesignAdjudication} {
		if other, ok := fixture.commandFor(purpose); ok && strings.Contains(other.ExecutionContract, "SOURCE FOR PATCHING") {
			t.Errorf("%s received the patching source", purpose)
		}
	}
}

// The feedback a planner gets starts with the marker and names the frozen commit,
// the failing rule and the instruction to regenerate.
func TestPatchValidationFeedbackNamesWhatFailedAndWhatToDo(t *testing.T) {
	workbench := &fakeWorkbench{}
	orchestrator := &Orchestrator{patchWorkbench: workbench}
	plan := ImplementationPlan{Changes: []PlannedChange{{Path: identifiersTestPath, Intent: "i", Patch: placeholderPatch(identifiersTestPath)}}}
	err := orchestrator.validateImplementationPlanPatches(context.Background(), targetSHA, plan)
	var rejected *PatchValidationError
	if !errors.As(err, &rejected) || !errors.Is(err, ErrContractRejected) {
		t.Fatalf("err = %v, want a contract-rejecting PatchValidationError", err)
	}
	text := err.Error()
	for _, want := range []string{"PATCH_VALIDATION_FAILED\n", "base_sha: " + targetSHA, "check=hunk_header", "Regenerate the patch against the supplied source.", "Do not use placeholder hunk coordinates."} {
		if !strings.Contains(text, want) {
			t.Errorf("feedback lacks %q:\n%s", want, text)
		}
	}
	if len(text) > 1500 {
		t.Fatalf("feedback is %d bytes; it must fit an attempt summary", len(text))
	}
}

// The mission the code-runner receives contains exactly the validated patch: what
// was checked is what will be applied.
func TestTheValidatedPatchIsTheMissionsPatch(t *testing.T) {
	workbench := &fakeWorkbench{files: map[string]string{identifiersTestPath: identifiersSource}}
	patch := validPatch(identifiersTestPath)
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, patch), workbench)
	driveThroughRejections(t, fixture)
	command, ok := fixture.provisioner.last()
	if !ok {
		t.Fatal("no mission")
	}
	parsed, err := coderunner.ParsePlan(command.PlanJSON)
	if err != nil {
		t.Fatal(err)
	}
	var applied string
	for _, operation := range parsed.Operations {
		if operation.Type == coderunner.ApplyPatch {
			applied = operation.Patch
		}
	}
	if applied != patch || workbench.checks[0] != applied {
		t.Fatalf("the mission applies %q but %q was checked", applied, workbench.checks)
	}
}

// Root 1062 (2026-09-21): the planner wrote the right change with the wrong hunk
// counts and without the a/ b/ prefixes, three times, and the run blocked. The
// arithmetic and the prefix convention are the host's to settle, not a reason to ask
// the model again.
const (
	miscountedPatchHunks = "@@ -6,9 +6,9 @@\n \t}{\n \t\t{\"no digits\"},\n+\t\t{\"digits adjacent to letters\"},\n"
	canonicalPatchHunks  = "@@ -6,2 +6,3 @@\n \t}{\n \t\t{\"no digits\"},\n+\t\t{\"digits adjacent to letters\"},\n"
)

func unprefixedMiscountedPatch(path string) string {
	return "--- " + path + "\n+++ " + path + "\n" + miscountedPatchHunks
}

func canonicalPatch(path string) string {
	return "--- a/" + path + "\n+++ b/" + path + "\n" + canonicalPatchHunks
}

func missionEvidenceOf(t *testing.T, fixture *missionFixture) EvidenceRecord {
	t.Helper()
	for _, record := range fixture.rootRecord(t).Evidence {
		if strings.HasPrefix(record.Reference, "engineering-mission://") {
			return record
		}
	}
	t.Fatal("no engineering-mission evidence was recorded")
	return EvidenceRecord{}
}

func TestAMiscountedUnprefixedPatchCreatesTheMissionWithTheCanonicalPatch(t *testing.T) {
	workbench := &fakeWorkbench{files: map[string]string{identifiersTestPath: identifiersSource}}
	raw := unprefixedMiscountedPatch(identifiersTestPath)
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, raw), workbench)

	driveThroughRejections(t, fixture)

	if fixture.provisioner.count() != 1 {
		t.Fatalf("missions provisioned = %d, want 1: the planner's arithmetic must not cost a regeneration", fixture.provisioner.count())
	}
	if planTask := planTaskOf(t, fixture); planTask.AttemptCount != 1 || implementationPlanRuns(fixture) != 1 {
		t.Fatalf("plan attempts = %d, runs = %d; want a single attempt", planTask.AttemptCount, implementationPlanRuns(fixture))
	}
	want := canonicalPatch(identifiersTestPath)
	// git was asked about the canonical text, once, and the mission carries those bytes.
	if workbench.checkCount() != 1 || workbench.checks[0] != want {
		t.Fatalf("git was asked about %q, want %q", workbench.checks, want)
	}
	command, ok := fixture.provisioner.last()
	if !ok {
		t.Fatal("no mission")
	}
	parsed, err := coderunner.ParsePlan(command.PlanJSON)
	if err != nil {
		t.Fatal(err)
	}
	var applied string
	for _, operation := range parsed.Operations {
		if operation.Type == coderunner.ApplyPatch {
			applied = operation.Patch
		}
	}
	if applied != want {
		t.Fatalf("the mission applies %q, want the validated canonical patch %q", applied, want)
	}

	// Provenance: both representations are accounted for, and the body digest shows
	// the host changed only header metadata.
	evidence := missionEvidenceOf(t, fixture)
	provenance, _ := evidence.Metadata["patch_provenance"].([]missionplan.PatchProvenance)
	if len(provenance) != 1 {
		t.Fatalf("mission evidence = %+v", evidence.Metadata)
	}
	got := provenance[0]
	if got.Path != identifiersTestPath || got.RawSHA256 == got.NormalizedSHA256 || got.BodySHA256 == "" {
		t.Fatalf("provenance = %+v", got)
	}
	if want := []string{missionplan.NormalizationPathPrefix, missionplan.NormalizationHunkRecount}; !equalStrings(got.Normalizations, want) {
		t.Fatalf("normalizations = %v, want %v", got.Normalizations, want)
	}
	if id, _ := evidence.Metadata["implementation_plan_invocation_id"].(int64); id == 0 {
		t.Fatalf("the raw patch is not traceable to its invocation: %+v", evidence.Metadata)
	}
	if got := patchFailuresOnRoot(t, fixture, planTaskOf(t, fixture).ID); got != 0 {
		t.Fatalf("recorded %d patch validation failures", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A patch that is already canonical is not rewritten and says so.
func TestACanonicalPatchCarriesNoNormalizations(t *testing.T) {
	workbench := &fakeWorkbench{files: map[string]string{identifiersTestPath: identifiersSource}}
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, canonicalPatch(identifiersTestPath)), workbench)
	driveThroughRejections(t, fixture)
	provenance, _ := missionEvidenceOf(t, fixture).Metadata["patch_provenance"].([]missionplan.PatchProvenance)
	if len(provenance) != 1 || len(provenance[0].Normalizations) != 0 || provenance[0].RawSHA256 != provenance[0].NormalizedSHA256 {
		t.Fatalf("provenance = %+v", provenance)
	}
}

// Normalization is not a way through the checks that follow it: what the host cannot
// canonicalize without choosing between readings goes back to the planner, and git is
// never asked.
func TestAnAmbiguousPatchIsRefusedBeforeGit(t *testing.T) {
	workbench := &fakeWorkbench{files: map[string]string{identifiersTestPath: identifiersSource}}
	ambiguous := "--- " + identifiersTestPath + "\n+++ " + identifiersTestPath + "\n@@ -6,9 +6,9 @@\n \t}{\n+\t\t{\"x\"},\n\n"
	fixture := planFixture(t, planBodyWithPatch(identifiersTestPath, ambiguous), workbench)

	run := driveThroughRejections(t, fixture)

	if run.State != StateBlocked || run.ReasonCode != ReasonImplementationPlanPatchInvalid || fixture.provisioner.count() != 0 {
		t.Fatalf("run = %+v, missions = %d", run, fixture.provisioner.count())
	}
	if workbench.checkCount() != 0 {
		t.Fatalf("git was asked %d time(s) about a patch the host could not canonicalize", workbench.checkCount())
	}
	if got := patchFailuresOnRoot(t, fixture, planTaskOf(t, fixture).ID); got != 3 {
		t.Fatalf("recorded %d failures, want one per attempt", got)
	}
}

// The final newline is not header metadata; the planner is told, precisely.
func TestAPatchWithoutItsFinalNewlineIsRefusedNotCompleted(t *testing.T) {
	workbench := &fakeWorkbench{}
	patch := strings.TrimSuffix(unprefixedMiscountedPatch(identifiersTestPath), "\n")
	orchestrator := &Orchestrator{patchWorkbench: workbench}
	err := orchestrator.validateImplementationPlanPatches(context.Background(), targetSHA,
		ImplementationPlan{Changes: []PlannedChange{{Path: identifiersTestPath, Intent: "i", Patch: patch}}})
	var rejected *PatchValidationError
	if !errors.As(err, &rejected) || !strings.Contains(err.Error(), "must end with a newline") {
		t.Fatalf("err = %v", err)
	}
	if workbench.checkCount() != 0 {
		t.Fatal("git was asked about a patch the host does not complete")
	}
}

// When git refuses the canonical patch, the planner is told which rewrites the host
// made (so it does not chase its own header arithmetic) and the failure evidence keeps
// the provenance.
func TestARejectionAfterNormalizationSaysWhatTheHostRewrote(t *testing.T) {
	workbench := &fakeWorkbench{verdict: func(int, string) (PatchCheckResult, error) {
		return PatchCheckResult{Applies: false, Detail: "error: patch failed: " + identifiersTestPath + ":6"}, nil
	}}
	orchestrator := &Orchestrator{patchWorkbench: workbench}
	err := orchestrator.validateImplementationPlanPatches(context.Background(), targetSHA,
		ImplementationPlan{Changes: []PlannedChange{{Path: identifiersTestPath, Intent: "i", Patch: unprefixedMiscountedPatch(identifiersTestPath)}}})
	var rejected *PatchValidationError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"check=git_apply_check", "checked after the host canonicalized path_prefix_canonicalization, hunk_recount", "line numbers are unchanged"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("feedback lacks %q:\n%s", want, err.Error())
		}
	}
	if len(rejected.Patches) != 1 || !equalStrings(rejected.Patches[0].Normalizations, []string{missionplan.NormalizationPathPrefix, missionplan.NormalizationHunkRecount}) {
		t.Fatalf("provenance = %+v", rejected.Patches)
	}
}
