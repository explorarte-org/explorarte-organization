package executive

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// B2 -- autonomous ambiguity reconciliation, validated against the exact
// shape R14 died of: an adjudication call whose send started and whose
// outcome was lost to a transport timeout. The invocation row stays
// ambiguous forever (Model Runtime's verdict is immutable); what is new is
// the durable resolution beside it, authored by host policy for pure-model
// executions only, and honored by every barrier that used to block forever.
//
// The guards pin both edges: with the policy behind it, a campaign recovers
// ITSELF -- no operator, no model judgment; without it (unknown effect
// class), every writer blocks exactly as before.

// ambiguousAdjudication seeds the R14 world: a design-adjudication task with
// one terminal-ambiguous invocation on its expired attempt. The fabricated
// task carries an idempotency key the freeze phase never looks up, so it
// stays exactly what this checkpoint is about -- durable ambiguity state --
// without disturbing the scripted pipeline around it.
func ambiguousAdjudication(t *testing.T, fixture *wiringFixture) (TaskRecord, InvocationRecord) {
	t.Helper()
	root := fixture.rootRecord(t)
	task := TaskRecord{
		ID:                     900340,
		CorrelationID:          root.CorrelationID,
		OrganizationRevisionID: root.OrganizationRevisionID,
		TaskClass:              TaskClassCoordinationDesignAdjudication,
		Status:                 "ready",
		AssignedRoleID:         CEORoleID,
		IdempotencyKey:         "fixture-ambiguous-adjudication",
		Attempts:               []AttemptRecord{{ID: 384, State: "lease_expired"}},
	}
	fixture.tasks.tasks[task.ID] = task
	invocation := InvocationRecord{
		ID: 340, TaskID: task.ID, AttemptID: 384, Status: "ambiguous",
		CorrelationID: root.CorrelationID,
	}
	fixture.harness.models.setInvocations(task.ID, 384, invocation)
	return task, invocation
}

func resolutionRows(t *testing.T, fixture *wiringFixture, taskID int64) []EvidenceRecord {
	t.Helper()
	detail, err := fixture.tasks.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	rows := []EvidenceRecord{}
	for _, evidence := range detail.Evidence {
		if strings.HasPrefix(evidence.Reference, AmbiguityResolutionReference) {
			rows = append(rows, evidence)
		}
	}
	return rows
}

// THE acceptance property: the barrier meets a pure-model ambiguity, the
// host policy durably authorizes the retry on first sight, and the drive
// proceeds -- no operator step anywhere.
func TestTheBarrierAuthorizesAPureModelAmbiguityAndStandsDown(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	task, invocation := ambiguousAdjudication(t, fixture)
	root := fixture.rootRecord(t)

	handled, _, err := fixture.orchestrator.priorExecutionBarrier(context.Background(), root, task)
	if err != nil {
		t.Fatalf("a reconciled ambiguity must not block the drive: %v", err)
	}
	if handled {
		t.Fatal("the barrier stood in the way of an authorized retry")
	}

	rows := resolutionRows(t, fixture, task.ID)
	if len(rows) != 1 {
		t.Fatalf("want exactly one durable resolution, got %d", len(rows))
	}
	row := rows[0]
	if !validAmbiguityResolution(row, invocation.ID) {
		t.Fatalf("resolution does not validate against its own reference:\n%+v", row.Metadata)
	}
	for key, want := range map[string]string{
		"resolution":   AmbiguityDispositionRetryAuthorized,
		"authority":    AmbiguityAuthorityHostPolicy,
		"policy":       AmbiguityPolicyPureModelV1,
		"effect_class": string(EffectPureModel),
	} {
		if got, _ := row.Metadata[key].(string); got != want {
			t.Errorf("resolution metadata %q = %q, want %q", key, got, want)
		}
	}

	// The second meeting is a no-op that reads the same fact once more:
	// idempotency means one ambiguity carries exactly one resolution row no
	// matter how many passes inspect it.
	handled, _, err = fixture.orchestrator.priorExecutionBarrier(context.Background(), root, task)
	if err != nil || handled {
		t.Fatalf("re-meeting the resolved ambiguity must stay transparent: handled=%v err=%v", handled, err)
	}
	if rows = resolutionRows(t, fixture, task.ID); len(rows) != 1 {
		t.Fatalf("a second pass duplicated the resolution: %d rows", len(rows))
	}
}

// The frontier: an execution the classifier cannot prove pure NEVER gets a
// resolution written, and every barrier behaves exactly as before B2.
func TestAnUnknownEffectClassStillBlocksForever(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	task, _ := ambiguousAdjudication(t, fixture)
	detail, _ := fixture.tasks.GetTask(context.Background(), task.ID)
	detail.TaskClass = "some.future.externally_effectful_class"
	fixture.tasks.tasks[task.ID] = detail
	root := fixture.rootRecord(t)

	handled, blocked, err := fixture.orchestrator.priorExecutionBarrier(context.Background(), root, detail)
	if !handled || !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("an unknown-effect ambiguity must block as before: handled=%v err=%v", handled, err)
	}
	if blocked.Status == "" {
		t.Fatal("barrier returned no blocked task record")
	}
	if rows := resolutionRows(t, fixture, task.ID); len(rows) != 0 {
		t.Fatalf("the policy wrote a resolution for an unknown effect class: %+v", rows[0].Metadata)
	}
}

// The end-to-end criterion from the checkpoint, at the decision that matters:
// a root durably blocked on model_outcome_ambiguous reopens BY ITSELF when
// every ambiguity in the correlation is resolvable -- same root, no new
// submit, no manual reconcile -- and the campaign then drives normally. The
// immutable verdict stays exactly as Model Runtime wrote it.
func TestARootBlockedOnAmbiguitySelfRecoversWhenPureModel(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `"],` +
			`"evidence":[{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"}]}`
	task, invocation := ambiguousAdjudication(t, fixture)
	root := fixture.rootRecord(t)

	// The durable state R14 actually reached: root fail-closed on the
	// ambiguous adjudication, nothing else wrong in the world.
	if _, err := fixture.tasks.BlockTask(context.Background(), root.ID, "model_outcome_ambiguous",
		"task="+strconv.FormatInt(task.ID, 10)+" attempt=384 invocation="+strconv.FormatInt(invocation.ID, 10)+" requires explicit inspection",
		"service", orchestratorWorkerID); err != nil {
		t.Fatal(err)
	}

	run, err := fixture.driveUntilStopped(t, 24)
	if err != nil {
		t.Fatalf("the campaign did not recover on its own: %v", err)
	}
	if run.State != StateCompleted {
		t.Fatalf("the campaign did not run to completion after recovery: %+v", run)
	}
	if _, ok := fixture.commandFor(PurposeDepartmentWorker); !ok {
		t.Fatal("the reopened root never drove any work")
	}
	// The historical fact survived untouched; only the resolution is new.
	still, err := fixture.harness.models.GetInvocation(context.Background(), invocation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Status != "ambiguous" {
		t.Fatalf("the immutable verdict was rewritten: %s", still.Status)
	}
	if rows := resolutionRows(t, fixture, task.ID); len(rows) != 1 {
		t.Fatalf("want exactly one resolution after recovery, got %d", len(rows))
	}
}

// One unresolvable ambiguity anywhere in the correlation keeps the whole run
// fail-closed: the reopen is refused with the same sentinel as before B2,
// nothing gets authorized, and a manual unblock would be re-blocked by the
// barrier -- the exact behavior that exposed the missing primitive.
func TestOneUnresolvableAmbiguityKeepsTheRunFailClosed(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition"}},
	})
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `"],` +
			`"evidence":[{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"}]}`
	task, _ := ambiguousAdjudication(t, fixture)
	detail, _ := fixture.tasks.GetTask(context.Background(), task.ID)
	detail.TaskClass = "coordination.some_future_tool_class"
	fixture.tasks.tasks[task.ID] = detail
	root := fixture.rootRecord(t)

	if _, err := fixture.tasks.BlockTask(context.Background(), root.ID, "model_outcome_ambiguous",
		"requires explicit inspection", "service", orchestratorWorkerID); err != nil {
		t.Fatal(err)
	}

	run, err := fixture.orchestrator.ResumeDurable(context.Background(), root.ID)
	if !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("expected the ambiguity wall, got run=%+v err=%v", run, err)
	}
	if run.State != StateBlocked {
		t.Fatalf("an unresolvable ambiguity let the run proceed: %+v", run)
	}
	if rows := resolutionRows(t, fixture, task.ID); len(rows) != 0 {
		t.Fatalf("nothing may authorize an unknown effect: %+v", rows[0].Metadata)
	}
}
