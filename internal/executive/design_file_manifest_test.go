package executive

import (
	"slices"
	"strings"
	"testing"
)

// External audit A3: the mission's allowed paths came from the implementation plan, written after the
// freeze, and nothing compared them to what was approved. The freeze records the design's files; the
// plan may name only those; the sealed candidate is compared to them again.

func TestTheFreezeRecordsTheDesignsFiles(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)
	manifest, recorded, err := fixture.orchestrator.frozenDesignManifest(t.Context(), fixture.root)
	if err != nil || !recorded || !slices.Equal(manifest, []string{smokePath}) {
		t.Fatalf("frozen manifest %v (recorded %v, %v), want [%s]", manifest, recorded, err, smokePath)
	}
}

// Local smoke #38 (root 1916): review and adjudication correctly froze a design for
// campaign_topology.go, but the implementation planner was shown only the broad owner goal. It
// rediscovered an unrelated carryDepartmentConstraints defect and the manifest gate correctly
// refused it. The planner must see the frozen decision before it writes a patch.
func TestImplementationPlannerReceivesTheFrozenCandidateAndManifest(t *testing.T) {
	const marker = "SMOKE_38_FROZEN_CANDIDATE"
	const distraction = "DISTRACTING_OWNER_GOAL internal/executive/orchestrator.go carryDepartmentConstraints"
	fixture := newMissionFixtureWith(t, smokePath, false, distraction)
	fixture.harness.bodies[PurposeDepartmentWorker] = `{"schema_version":"worker-result/v1",` +
		`"summary":"` + marker + `: change only the frozen smoke file.",` +
		`"evidence_refs":[],"proposed_files":["` + smokePath + `"]}`
	fixture.drive(t)

	planTask := planTaskOf(t, fixture)
	for _, want := range []string{"FROZEN DESIGN", marker, smokePath, DesignFileManifestKey} {
		if !strings.Contains(planTask.Instructions, want) {
			t.Fatalf("implementation planner instructions do not contain %q:\n%s", want, planTask.Instructions)
		}
	}
	if strings.Contains(planTask.Instructions, distraction) || strings.Contains(planTask.Instructions, "OWNER GOAL") {
		t.Fatalf("the broad owner goal still competes with the frozen decision:\n%s", planTask.Instructions)
	}
	if len(planTask.Instructions) > DefaultLimits().MaxInstructionsBytes {
		t.Fatalf("implementation planner instructions are %d bytes, over limit %d", len(planTask.Instructions), DefaultLimits().MaxInstructionsBytes)
	}
}

func TestImplementationPlannerReadsSourceFromTheFrozenManifestBeforeQueryDistractions(t *testing.T) {
	const distractingPath = "docs/implementation/other.md"
	const distraction = "Inspect " + distractingPath + " instead"
	workbench := &fakeWorkbench{files: map[string]string{
		smokePath:       "frozen source\n",
		distractingPath: "distracting source\n",
	}}
	fixture := newMissionFixtureWith(t, smokePath, false, distraction, WithPatchWorkbench(workbench))
	fixture.drive(t)

	workbench.mu.Lock()
	reads := append([]string(nil), workbench.reads...)
	workbench.mu.Unlock()
	want := targetSHA + ":" + smokePath
	if !slices.Contains(reads, want) {
		t.Fatalf("source reads %v do not contain frozen manifest file %q", reads, want)
	}
	if slices.Contains(reads, targetSHA+":"+distractingPath) {
		t.Fatalf("source reads %v include query distraction outside the frozen manifest", reads)
	}
}

func TestImplementationPlanInstructionsFailClosedInsteadOfTruncatingTheFrozenDesign(t *testing.T) {
	if _, err := implementationPlanInstructions("policy", strings.Repeat("f", 100), 40); err == nil {
		t.Fatal("oversized frozen design instructions were silently accepted")
	}
}

func TestAPlanNamingAFileTheDesignDoesNotIsRefusedBeforeAnyMission(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.harness.bodies[PurposeDepartmentWorker] = `{"schema_version":"worker-result/v1","summary":"The design changes one other file.",` +
		`"evidence_refs":[],"proposed_files":["docs/implementation/autonomy-smoke/OTHER.md"]}`
	fixture.drive(t)
	root := fixture.rootRecord(t)
	if root.ReasonCode != ReasonMissionPolicyRejected || !strings.Contains(root.Reason, "which the frozen design does not name") {
		t.Fatalf("root %s/%s: %s", root.Status, root.ReasonCode, root.Reason)
	}
	if fixture.provisioner.count() != 0 {
		t.Fatalf("a mission was provisioned for a plan outside the frozen design: %d", fixture.provisioner.count())
	}
}

func TestADesignNamingNoFileCannotBeImplemented(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.harness.bodies[PurposeDepartmentWorker] = `{"schema_version":"worker-result/v1","summary":"The design names what changes in words.","evidence_refs":[]}`
	fixture.drive(t)
	root := fixture.rootRecord(t)
	if root.ReasonCode != ReasonMissionPolicyRejected || !strings.Contains(root.Reason, "names no file to change") || fixture.provisioner.count() != 0 {
		t.Fatalf("root %s/%s: %s (missions %d)", root.Status, root.ReasonCode, root.Reason, fixture.provisioner.count())
	}
}

func TestTheSealedCandidateIsComparedToTheManifest(t *testing.T) {
	evidence := EvidenceRecord{Metadata: map[string]any{"changed_files": map[string]any{
		"count": 2, "paths": []any{"internal/a/a.go", "internal/b/b.go"},
	}}}
	changed, complete := sealedChangedPaths(evidence)
	if !complete {
		t.Fatalf("complete list reported incomplete: %v", changed)
	}
	if outside := outsideManifest(changed, []string{"internal/a/a.go"}); !slices.Equal(outside, []string{"internal/b/b.go"}) {
		t.Fatalf("outside = %v, want internal/b/b.go", outside)
	}
	partial := EvidenceRecord{Metadata: map[string]any{"changed_files": map[string]any{"count": 3, "paths": []any{"internal/a/a.go"}}}}
	if _, complete := sealedChangedPaths(partial); complete {
		t.Fatal("a partial path list was taken as complete")
	}
}

func TestProposedFilesMustBeCleanRelativePaths(t *testing.T) {
	for _, file := range []string{"/etc/passwd", "../x.go", "internal/../x.go", "internal//x.go", ""} {
		body := `{"schema_version":"worker-result/v1","summary":"s","evidence_refs":[],"proposed_files":["` + file + `"]}`
		if _, err := ParseWorkerResult([]byte(body), DefaultLimits()); err == nil {
			t.Errorf("proposed file %q was accepted", file)
		}
	}
}

// Through the execution barrier: a sealed candidate that changed a file outside the frozen design's
// manifest is not accepted as this root's execution.
func TestTheExecutionBarrierRefusesACandidateOutsideTheFrozenDesign(t *testing.T) {
	mission := codeRunnerTaskForTest()
	for i, evidence := range mission.Evidence {
		if strings.HasPrefix(evidence.Reference, codeRunnerEvidenceReferencePrefix) {
			evidence.Metadata["changed_files"] = map[string]any{"count": float64(1), "paths": []any{"internal/identifiers/identifiers_test.go"}}
			mission.Evidence[i] = evidence
		}
	}
	o, root := reviewFixture(mission, nil)
	changed, complete := sealedChangedPaths(verifiedAttemptEvidenceOf(t, mission))
	if !complete || len(changed) != 1 {
		t.Fatalf("scenario: changed %v complete %v", changed, complete)
	}
	memory := o.tasks.(*memoryTasks)
	stored := memory.tasks[root.ID]
	stored.Evidence = append(stored.Evidence, EvidenceRecord{Reference: "freeze", Metadata: map[string]any{DesignFileManifestKey: []any{"internal/elsewhere/other.go"}}})
	memory.tasks[root.ID] = stored
	err := o.ensureRequiredCodeRunnerExecution(t.Context(), root)
	if err == nil || !strings.Contains(err.Error(), "which the frozen design does not name") {
		t.Fatalf("err = %v, want a refusal naming the file outside the frozen design", err)
	}
	stored.Evidence[len(stored.Evidence)-1].Metadata[DesignFileManifestKey] = anyStrings(changed)
	memory.tasks[root.ID] = stored
	if err := o.ensureRequiredCodeRunnerExecution(t.Context(), root); err != nil {
		t.Fatalf("a candidate within the frozen design was refused: %v", err)
	}
}

func verifiedAttemptEvidenceOf(t *testing.T, mission TaskRecord) EvidenceRecord {
	t.Helper()
	attempt, _, err := verifiedCodeRunnerEvidence(mission)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func anyStrings(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}
