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
// one terminal-ambiguous invocation on its expired attempt. The invocation
// carries the driver-declared purpose the real row will have once the
// identity threads through; its TaskClass is deliberately an ordinary worker
// class to prove classification never reads it. The fabricated task carries
// an idempotency key the freeze phase never looks up, so it stays exactly
// what this checkpoint is about -- durable ambiguity state -- without
// disturbing the scripted pipeline around it.
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
		Purpose:       PurposeDesignAdjudication.LegacyPurpose(),
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

// The frontier, stated exactly as reviewed: classification reads the
// invocation's declared identity, never TaskClass. Every current Executive
// purpose is pure_model -- worker classes are deliberately free-form
// (memory.discovery, engineering.review, ...), so a class-based frontier
// would lock out precisely the executions B2 exists to recover. A purpose
// outside the closed set is fail-closed, whatever its task looks like.
func TestEffectClassificationFollowsThePurposeNeverTheTaskClass(t *testing.T) {
	for _, purpose := range []ExecutionPurpose{
		PurposeCEOPlan, PurposeDepartmentPlan, PurposeDepartmentWorker,
		PurposeDepartmentReview, PurposeCEOClosure, PurposeAdversarialReview,
		PurposeDesignAdjudication, PurposeImplementationPlan,
	} {
		class := executionEffectClass(InvocationRecord{Purpose: purpose.LegacyPurpose()})
		if class != EffectPureModel {
			t.Errorf("purpose %q must classify pure_model, got %q", purpose.LegacyPurpose(), class)
		}
	}

	// Free-form worker classes on a department-worker invocation: the exact
	// world that would have been locked out under a TaskClass frontier. The
	// class is written onto the task so the guard proves the classifier never
	// reads it.
	for _, taskClass := range []string{"memory.discovery", "engineering.review", "general.work", "anything.valid_syntax"} {
		fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
		task, _ := ambiguousAdjudication(t, fixture)
		detail, err := fixture.tasks.GetTask(context.Background(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		detail.TaskClass = taskClass
		fixture.tasks.tasks[task.ID] = detail
		invocation := InvocationRecord{
			ID: 341, TaskID: task.ID, AttemptID: 384, Status: "ambiguous",
			CorrelationID: detail.CorrelationID,
			Purpose:       PurposeDepartmentWorker.LegacyPurpose(),
		}
		fixture.harness.models.setInvocations(task.ID, 384, invocation)
		handled, _, err := fixture.orchestrator.priorExecutionBarrier(context.Background(), fixture.rootRecord(t), detail)
		if err != nil || handled {
			t.Fatalf("task class %q with a cognitive purpose must not block: handled=%v err=%v", taskClass, handled, err)
		}
		if rows := resolutionRows(t, fixture, task.ID); len(rows) != 1 {
			t.Fatalf("task class %q: want exactly one resolution, got %d", taskClass, len(rows))
		}
	}

	// An unrecognized purpose fails closed regardless of what the task says
	// it is; nothing may be authorized or written.
	for _, purpose := range []string{"", "future_tool_purpose", "execution harness turn not-a-digest"} {
		fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
		task, _ := ambiguousAdjudication(t, fixture)
		detail, err := fixture.tasks.GetTask(context.Background(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		detail.TaskClass = TaskClassGeneralWork
		fixture.tasks.tasks[task.ID] = detail
		invocation := InvocationRecord{
			ID: 342, TaskID: task.ID, AttemptID: 384, Status: "ambiguous",
			CorrelationID: detail.CorrelationID,
			Purpose:       purpose,
		}
		fixture.harness.models.setInvocations(task.ID, 384, invocation)

		handled, blocked, err := fixture.orchestrator.priorExecutionBarrier(
			context.Background(), fixture.rootRecord(t), detail)
		if !handled || !errors.Is(err, ErrModelOutcomeAmbiguous) {
			t.Fatalf("purpose %q must block as before B2: handled=%v err=%v", purpose, handled, err)
		}
		if blocked.Status == "" {
			t.Fatal("barrier returned no blocked task record")
		}
		if rows := resolutionRows(t, fixture, task.ID); len(rows) != 0 {
			t.Fatalf("purpose %q: nothing may authorize an unclassified effect: %+v", purpose, rows[0].Metadata)
		}
	}
}

// The frozen legacy format: every ambiguity row created before the driver's
// identity reached the purpose column carries it, including R14's real
// invocation. It is recognized as pure_model because within this scan domain
// it could only have been produced by the same tool-free adapter.
func TestLegacyHarnessTurnPurposesArePureModel(t *testing.T) {
	legacy := "execution harness turn b6a3f1e05d9c4728ba0fe61d3c5a2974e8b10c6df24937a58c0d41e92fb6730a"
	if class := executionEffectClass(InvocationRecord{Purpose: legacy}); class != EffectPureModel {
		t.Fatalf("the frozen legacy format must classify pure_model, got %q", class)
	}
	// Almost-right formats do not: the recognizer is exact.
	for _, almost := range []string{
		"execution harness turn B6A3F1E05D9C4728BA0FE61D3C5A2974E8B10C6DF24937A58C0D41E92FB6730A",
		"execution harness turn b6a3f1e0",
		"execution harness turn ",
	} {
		if class := executionEffectClass(InvocationRecord{Purpose: almost}); class != EffectUnknown {
			t.Errorf("purpose %q must be unknown, got %q", almost, class)
		}
	}
}

// Two ambiguities are two decisions: each pure-model invocation gets its own
// ambiguity-resolution://<id> row, and the barrier only stands down once BOTH
// are reconciled. One resolution must never stand down for the next one.
func TestTwoPureModelAmbiguitiesGetTwoResolutionsAndStandDown(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	task, _ := ambiguousAdjudication(t, fixture)
	detail, _ := fixture.tasks.GetTask(context.Background(), task.ID)
	second := InvocationRecord{
		ID: 341, TaskID: task.ID, AttemptID: 385, Status: "ambiguous",
		CorrelationID: detail.CorrelationID,
		Purpose:       PurposeDepartmentWorker.LegacyPurpose(),
	}
	detail.Attempts = append(detail.Attempts, AttemptRecord{ID: 385, State: "lease_expired"})
	fixture.tasks.tasks[task.ID] = detail
	fixture.harness.models.setInvocations(task.ID, 385, second)

	handled, _, err := fixture.orchestrator.priorExecutionBarrier(context.Background(), fixture.rootRecord(t), detail)
	if err != nil || handled {
		t.Fatalf("both reconciled ambiguities must let the drive proceed: handled=%v err=%v", handled, err)
	}

	first := resolutionRowsFor(t, fixture, task.ID, 340)
	if len(first) != 1 {
		t.Fatalf("invocation 340 has %d resolutions, want exactly its own", len(first))
	}
	rows := resolutionRows(t, fixture, task.ID)
	if len(rows) != 2 {
		t.Fatalf("two ambiguities produced %d resolutions, want two", len(rows))
	}
}

// The race the reviewer called out: an OLD already-authorized ambiguity must
// never stand the barrier down in front of a NEWER unreconciled one. The
// unknown-purpose invocation keeps the wall up, no claim may be created, and
// the old resolution is left as the only fact on file.
func TestResolvedOldAmbiguityCannotHideANewUnknownOne(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	task, invocation := ambiguousAdjudication(t, fixture)
	detail, _ := fixture.tasks.GetTask(context.Background(), task.ID)

	// The old ambiguity was inspected and authorized in an earlier pass.
	resolved, err := fixture.orchestrator.reconcileAmbiguousInvocation(context.Background(), detail, invocation)
	if err != nil || !resolved {
		t.Fatalf("fixture precondition: old ambiguity must authorize cleanly (resolved=%v err=%v)", resolved, err)
	}

	// Time passes; a fresh attempt produces a NEW ambiguity the policy
	// cannot resolve.
	newer := InvocationRecord{
		ID: 341, TaskID: task.ID, AttemptID: 385, Status: "ambiguous",
		CorrelationID: detail.CorrelationID,
		Purpose:       "some.future.externally_effectful_purpose",
	}
	detail.Attempts = append(detail.Attempts, AttemptRecord{ID: 385, State: "lease_expired"})
	fixture.tasks.tasks[task.ID] = detail
	fixture.harness.models.setInvocations(task.ID, 385, newer)

	before := len(fixture.tasks.claims)
	handled, blocked, err := fixture.orchestrator.priorExecutionBarrier(
		context.Background(), fixture.rootRecord(t), detail)
	if !handled || !errors.Is(err, ErrModelOutcomeAmbiguous) {
		t.Fatalf("the new ambiguity must keep the wall up: handled=%v err=%v", handled, err)
	}
	if blocked.Status == "" {
		t.Fatal("barrier returned no blocked task record")
	}
	if len(fixture.tasks.claims) != before {
		t.Fatal("a claim was created beside an unreconciled ambiguity")
	}
	rows := resolutionRows(t, fixture, task.ID)
	if len(rows) != 1 {
		t.Fatalf("the unknown new ambiguity must not gain a resolution, rows=%d", len(rows))
	}
}

func resolutionRowsFor(t *testing.T, fixture *wiringFixture, taskID, invocationID int64) []EvidenceRecord {
	t.Helper()
	want := ambiguityResolutionReference(invocationID)
	detail, err := fixture.tasks.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	rows := []EvidenceRecord{}
	for _, evidence := range detail.Evidence {
		if evidence.Reference == want {
			rows = append(rows, evidence)
		}
	}
	return rows
}

// An execution whose purpose is unknown still blocks forever, whatever its
// task claims to be -- the barrier behaves exactly as before B2 and writes
// no resolution for an effect it cannot prove pure.
func TestAnUnknownEffectClassStillBlocksForever(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), nil)
	task, invocation := ambiguousAdjudication(t, fixture)
	detail, _ := fixture.tasks.GetTask(context.Background(), task.ID)
	unclassified := invocation
	unclassified.ID = 999
	unclassified.Purpose = "some.future.externally_effectful_purpose"
	fixture.harness.models.setInvocations(task.ID, 384, unclassified)
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
	task, invocation := ambiguousAdjudication(t, fixture)
	// The frontier is the invocation's declared identity: a purpose outside
	// the closed set is unresolvable no matter what the task's class says.
	unclassified := invocation
	unclassified.Purpose = "some.future.externally_effectful_purpose"
	fixture.harness.models.setInvocations(task.ID, 384, unclassified)
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
