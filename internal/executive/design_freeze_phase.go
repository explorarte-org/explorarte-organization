package executive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
	"github.com/Mireuz13/explorarte-organization/internal/designreview"
)

// The design-freeze phase sits between the department phase and CEO closure,
// and it is opt-in per run: it engages only when the owner attached a
// design-freeze requirement to the root goal. A run without that requirement
// behaves exactly as before, which is why this is additive rather than a
// change to how existing runs complete.
//
// The phase is driven here, in the Executive, and not in a package of its own.
// Every other cognitive execution in this system goes through driveTypedTask
// -- lease, prior-execution barrier, dispatch assignment, budget, Harness,
// gated completion -- and an adversarial review that took a different path
// would be a second execution model with its own resume and failure
// semantics. internal/designreview keeps the parts that are genuinely policy
// (who may review, what they may see); the ordering stays here.

const (
	// AdversarialReviewerRoleID is the only role the host will dispatch an
	// adversarial review to. It is resolved through the registry like any
	// other role; naming it here is an authority statement, not a routing one
	// -- which provider serves it remains entirely a canonical-routing
	// decision this package cannot see.
	AdversarialReviewerRoleID = "investigacion/revisor_adversarial"

	TaskClassCoordinationAdversarialReview  = "coordination.adversarial_review"
	TaskClassCoordinationDesignAdjudication = "coordination.design_adjudication"

	// ReasonDesignRevisionRequired and ReasonDesignRejected are executive
	// decisions, not transient faults. Resume deliberately does NOT
	// auto-unblock them: a design sent back for changes is waiting on a
	// human, and silently retrying it would spend the reviewer's budget
	// re-deciding something already decided.
	ReasonDesignRevisionRequired = "design_revision_required"
	ReasonDesignRejected         = "design_rejected"
	// ReasonAdversarialReviewUnavailable covers every way the reviewer cannot
	// be dispatched at all -- role absent, disabled, not executable, or its
	// provider unconfigured. It fails the run closed. There is deliberately
	// no branch that substitutes another role or another model.
	ReasonAdversarialReviewUnavailable = "adversarial_review_unavailable"
)

// designArtifact is the host's own definition of "the candidate design": the
// ordered durable deliverables the department phase produced. It is computed
// from durable task state only, so the same run always yields the same digest
// and a changed deliverable necessarily yields a different one.
type designArtifact struct {
	RootTaskID int64           `json:"root_task_id"`
	Round      int             `json:"round"`
	Units      []designUnitRef `json:"units"`
}

type designUnitRef struct {
	UnitID       string `json:"unit_id"`
	TaskID       int64  `json:"task_id"`
	InvocationID int64  `json:"invocation_id"`
	ResultHash   string `json:"result_hash"`
}

// driveDesignFreeze returns done=true when the run must stop at this phase --
// because an execution is still in flight, or because the executive decided
// the design is not frozen. done=false means the phase has nothing to hold up
// and the run may proceed to closure.
// designAdjudicationPreamble is everything the adjudicator is told before the
// bundle itself.
//
// It deliberately does not ask for the design identity back. The host binds
// design_id, design_version and design_digest onto the verdict from its own
// record, and the output schema has no place to put them, so an instruction
// to echo them commands the model to return something with nowhere to go. It
// obeyed: one campaign died with the identity written out as prose inside a
// findings array, rejected as an invalid finding reference.
//
// Removing the fields from the schema and leaving the sentence that demanded
// them was half a change. This is the other half.
const designAdjudicationPreamble = "Adjudicate the adversarial review of this candidate design and return DesignAdjudication JSON. " +
	"The design identity is bound by the host and must not be restated; return only the fields the schema declares. " +
	"Only verdict=freeze settles the design.\n\n"

func (o *Orchestrator) driveDesignFreeze(ctx context.Context, root TaskRecord, all []TaskRecord) (Run, bool, error) {
	requirement, found := findRequirementByKey(root.Requirements, designfreeze.RequirementKey)
	if !found {
		// This run is not governed by a design freeze at all.
		return Run{}, false, nil
	}
	if requirement.Status == "satisfied" {
		return Run{}, false, nil
	}

	reviewer, err := o.registry.GetRole(ctx, AdversarialReviewerRoleID)
	if err != nil || !reviewer.Enabled || !reviewer.Executable {
		run, blockErr := o.blockRoot(ctx, root, ReasonAdversarialReviewUnavailable,
			fmt.Sprintf("adversarial reviewer %s is not dispatchable (%s)", AdversarialReviewerRoleID, designreview.ProviderUnavailableReason))
		return run, true, blockErr
	}
	adjudicator, err := o.registry.GetRole(ctx, CEORoleID)
	if err != nil {
		return Run{}, true, err
	}

	artifact, units, ok := o.candidateDesign(ctx, root, all)
	if !ok {
		run, blockErr := o.blockRoot(ctx, root, "candidate_design_missing",
			"no completed department deliverable is available to review")
		return run, true, blockErr
	}
	if err = designreview.ValidateIndependence(
		designreview.Participant{RoleID: reviewer.ID, UnitID: reviewer.UnitID, Enabled: reviewer.Enabled, Executable: reviewer.Executable},
		designreview.Participant{RoleID: adjudicator.ID, UnitID: adjudicator.UnitID, Enabled: adjudicator.Enabled, Executable: adjudicator.Executable},
		units,
	); err != nil {
		run, blockErr := o.blockRoot(ctx, root, "adversarial_review_not_independent", err.Error())
		if blockErr != nil {
			return run, true, blockErr
		}
		return run, true, nil
	}

	design := designfreeze.Design{
		ID:      fmt.Sprintf("design:root:%d", root.ID),
		Version: "v" + strconv.Itoa(artifact.Round),
		Digest:  artifactDigest(artifact),
	}
	bundle, err := o.reviewBundle(root, design, artifact)
	if err != nil {
		run, blockErr := o.blockRoot(ctx, root, "adversarial_review_bundle_rejected", err.Error())
		if blockErr != nil {
			return run, true, blockErr
		}
		return run, true, nil
	}

	suffix := ":round:" + strconv.Itoa(artifact.Round)

	// ---- adversarial review -------------------------------------------
	reviewTask, ok := findTaskByKey(all, childKey(root.ID, "design-review"+suffix))
	if !ok {
		reviewTask, _, err = o.tasks.CreateTask(ctx, CreateTaskCommand{
			RequestedByRoleID: CEORoleID, AssignedRoleID: reviewer.ID,
			TaskClass:      TaskClassCoordinationAdversarialReview,
			IdempotencyKey: childKey(root.ID, "design-review"+suffix),
			Title:          "Adversarial design review: " + design.ID + " " + design.Version,
			Instructions: "Review this sanitized candidate design adversarially and return AdversarialReview JSON. " +
				"You publish findings only: you do not approve, adjudicate, freeze, or propose tasks.\n\n" + string(bundle),
			AcceptanceCriteria: []string{
				"Return strict AdversarialReview JSON",
				"Every finding must be falsifiable and name the requirement it affects",
				"Do not propose follow-up tasks or approvals",
			},
			Priority: 90, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID),
			Requirements: []RequirementProposal{{Key: "typed_adversarial_review", Type: "result", Description: "Validated AdversarialReview invocation result", Required: true}},
		})
		if err != nil {
			return Run{}, true, err
		}
	}
	if reviewTask.Status != "completed" {
		if _, err = o.driveTypedTask(ctx, root, reviewTask, AdversarialReviewOutputSchema(), PurposeAdversarialReview, func(result InvocationResult) error {
			_, parseErr := ParseAdversarialReview(result.JSONOutput, o.limits)
			return parseErr
		}); err != nil {
			run, phaseErr := o.handlePhaseError(ctx, root, reviewTask, err)
			return run, true, phaseErr
		}
		run, statusErr := o.Status(ctx, root.ID)
		return run, true, statusErr
	}
	reviewResult, ok := o.resultForCompletedTask(ctx, reviewTask)
	if !ok {
		run, blockErr := o.blockRoot(ctx, root, "adversarial_review_result_missing", "completed adversarial review has no durable invocation result")
		return run, true, blockErr
	}
	review, err := ParseAdversarialReview(reviewResult.JSONOutput, o.limits)
	if err != nil {
		run, blockErr := o.blockRoot(ctx, root, "adversarial_review_invalid", err.Error())
		return run, true, blockErr
	}

	// ---- adjudication ---------------------------------------------------
	adjudicationTask, ok := findTaskByKey(all, childKey(root.ID, "design-adjudication"+suffix))
	if !ok {
		adjudicationTask, _, err = o.tasks.CreateTask(ctx, CreateTaskCommand{
			RequestedByRoleID: OwnerRoleID, AssignedRoleID: CEORoleID,
			TaskClass:      TaskClassCoordinationDesignAdjudication,
			IdempotencyKey: childKey(root.ID, "design-adjudication"+suffix),
			Title:          "Design adjudication: " + design.ID + " " + design.Version,
			Instructions: designAdjudicationPreamble +
				string(bundle) + "\n\nADVERSARIAL REVIEW:\n" + string(reviewResult.JSONOutput),
			AcceptanceCriteria: []string{
				"Return strict DesignAdjudication JSON",
				"A freeze verdict may not carry required changes or unresolved owner decisions",
			},
			Priority: 95, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID),
			Requirements: []RequirementProposal{{Key: "typed_design_adjudication", Type: "result", Description: "Validated DesignAdjudication invocation result", Required: true}},
		})
		if err != nil {
			return Run{}, true, err
		}
	}
	expected := DesignIdentity{DesignID: design.ID, DesignVersion: design.Version, DesignDigest: design.Digest}
	if adjudicationTask.Status != "completed" {
		// The adjudicator may only cite findings this review actually
		// raised, so the schema is built from the review in hand rather
		// than from a constant that cannot know what is in it.
		reviewIDs := reviewFindingIDs(reviewResult.JSONOutput, o.limits)
		if _, err = o.driveTypedTask(ctx, root, adjudicationTask, DesignAdjudicationOutputSchemaFor(reviewIDs), PurposeDesignAdjudication, func(result InvocationResult) error {
			parsed, parseErr := ParseDesignAdjudication(result.JSONOutput, expected, o.limits)
			if parseErr != nil {
				return parseErr
			}
			return AssertFindingsExist(parsed, reviewIDs)
		}); err != nil {
			run, phaseErr := o.handlePhaseError(ctx, root, adjudicationTask, err)
			return run, true, phaseErr
		}
		run, statusErr := o.Status(ctx, root.ID)
		return run, true, statusErr
	}
	adjudicationResult, ok := o.resultForCompletedTask(ctx, adjudicationTask)
	if !ok {
		run, blockErr := o.blockRoot(ctx, root, "design_adjudication_result_missing", "completed adjudication has no durable invocation result")
		return run, true, blockErr
	}
	adjudication, err := ParseDesignAdjudication(adjudicationResult.JSONOutput, expected, o.limits)
	if err == nil {
		err = AssertFindingsExist(adjudication, reviewFindingIDs(reviewResult.JSONOutput, o.limits))
	}
	if err != nil {
		run, blockErr := o.blockRoot(ctx, root, "design_adjudication_invalid", err.Error())
		return run, true, blockErr
	}

	// ---- gate -----------------------------------------------------------
	decision := designfreeze.Evaluate(designfreeze.Request{
		Design: design,
		Review: designfreeze.ExecutionRef{
			TaskID: reviewTask.ID, AttemptID: finishedAttemptID(reviewTask), InvocationID: reviewResult.InvocationID,
			ResultDigest: reviewResult.ResponseHash, Verdict: string(review.Verdict),
		},
		ReviewDesign: design,
		Adjudication: designfreeze.ExecutionRef{
			TaskID: adjudicationTask.ID, AttemptID: finishedAttemptID(adjudicationTask), InvocationID: adjudicationResult.InvocationID,
			ResultDigest: adjudicationResult.ResponseHash, Verdict: string(adjudication.Verdict),
		},
		AdjudicationDesign: design,
	})
	if !decision.Satisfied {
		code := ReasonDesignRevisionRequired
		if adjudication.Verdict == AdjudicationReject {
			code = ReasonDesignRejected
		}
		run, blockErr := o.blockRoot(ctx, root, code, fmt.Sprintf("design %s %s not frozen: %s", design.ID, design.Version, decision.ReasonCode))
		return run, true, blockErr
	}

	payload, err := designfreeze.EvidencePayload(decision.Record)
	if err != nil {
		return Run{}, true, err
	}
	if err = o.tasks.RecordEvidence(ctx, EvidenceCommand{
		TaskID: root.ID, RequirementID: requirement.ID, Type: "approval",
		Reference:  fmt.Sprintf("task:%d:model-invocation:%d", adjudicationTask.ID, adjudicationResult.InvocationID),
		Digest:     decision.Record.Digest,
		RecordedBy: orchestratorWorkerID,
		Metadata: map[string]any{
			"design_id": design.ID, "design_version": design.Version, "design_digest": design.Digest,
			"adversarial_review_task_id": reviewTask.ID, "design_adjudication_task_id": adjudicationTask.ID,
			"design_freeze_record": string(payload),
		},
		Satisfies: true,
	}); err != nil {
		return Run{}, true, err
	}
	// The freeze is recorded. Nothing else happens here on purpose: a frozen
	// design settles WHAT to build, and creates no task, no mission and no
	// eligibility to build it.
	return Run{}, false, nil
}

// candidateDesign builds the artifact from completed department review
// deliverables. It returns the contributing unit ids alongside it so the
// independence rule can be evaluated against every author, not just the lead.
func (o *Orchestrator) candidateDesign(ctx context.Context, root TaskRecord, all []TaskRecord) (designArtifact, []string, bool) {
	artifact := designArtifact{RootTaskID: root.ID, Round: designRound(all, root.ID)}
	units := make([]string, 0, len(all))
	for _, task := range all {
		if task.TaskClass != TaskClassCoordinationDeptReview || task.Status != "completed" {
			continue
		}
		result, ok := o.resultForCompletedTask(ctx, task)
		if !ok {
			continue
		}
		artifact.Units = append(artifact.Units, designUnitRef{
			UnitID: task.AssignedUnitID, TaskID: task.ID,
			InvocationID: result.InvocationID, ResultHash: result.ResponseHash,
		})
		units = append(units, task.AssignedUnitID)
	}
	if len(artifact.Units) == 0 {
		return designArtifact{}, nil, false
	}
	sort.Slice(artifact.Units, func(i, j int) bool {
		if artifact.Units[i].UnitID != artifact.Units[j].UnitID {
			return artifact.Units[i].UnitID < artifact.Units[j].UnitID
		}
		return artifact.Units[i].TaskID < artifact.Units[j].TaskID
	})
	return artifact, units, true
}

// designRound resolves the round this phase is currently working on: the
// highest round that already has tasks, or 1 when none do.
//
// It deliberately does NOT advance on its own. An earlier revision derived the
// round from the count of completed adjudications, which meant every Resume
// after a completed round computed a higher number, created a fresh review
// and adjudication pair, and never evaluated the gate for the round that had
// just finished. That is an unbounded loop over the most expensive execution
// in the system, and the freeze it was supposed to record never landed.
//
// A new round exists only when someone creates its tasks. Since a revise or
// reject verdict blocks the root and Resume does not auto-unblock those
// reasons, no new round can appear without a deliberate act.
func designRound(all []TaskRecord, rootID int64) int {
	prefix := childKey(rootID, "design-review:round:")
	highest := 0
	for _, task := range all {
		if !strings.HasPrefix(task.IdempotencyKey, prefix) {
			continue
		}
		value, err := strconv.Atoi(strings.TrimPrefix(task.IdempotencyKey, prefix))
		if err == nil && value > highest {
			highest = value
		}
	}
	if highest == 0 {
		return 1
	}
	return highest
}

func (o *Orchestrator) reviewBundle(root TaskRecord, design designfreeze.Design, artifact designArtifact) ([]byte, error) {
	evidence := make([]string, 0, len(artifact.Units))
	summary := make([]string, 0, len(artifact.Units))
	for _, unit := range artifact.Units {
		evidence = append(evidence, fmt.Sprintf("task:%d:model-invocation:%d", unit.TaskID, unit.InvocationID))
		summary = append(summary, fmt.Sprintf("%s -> task:%d result:%s", unit.UnitID, unit.TaskID, unit.ResultHash))
	}
	bundle := designreview.Bundle{
		OwnerRequirements: root.AcceptanceCriteria,
		CandidateDesign:   "Durable department deliverables under review:\n" + joinLines(summary),
		ArchitectureConstraints: []string{
			"The reviewer sees only durable deliverable identities and their result digests.",
		},
		AuthorityConstraints: []string{
			"The reviewer publishes findings; it does not approve, adjudicate or freeze.",
			"Only empresa/ceo may adjudicate, and only verdict=freeze settles the design.",
		},
		UnresolvedDecisions: nil,
		EvidenceRefs:        evidence,
		Design:              design,
	}
	return bundle.Encode()
}

func joinLines(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += "\n"
		}
		out += value
	}
	return out
}

func artifactDigest(artifact designArtifact) string {
	body, err := json.Marshal(artifact)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// finishedAttemptID is the durable attempt the completed result belongs to.
// The freeze record binds task, attempt AND invocation, so a result recovered
// from a different attempt of the same task cannot be presented as this one.
func finishedAttemptID(task TaskRecord) int64 {
	highest := int64(0)
	for _, attempt := range task.Attempts {
		if attempt.ID > highest {
			highest = attempt.ID
		}
	}
	return highest
}

func findRequirementByKey(requirements []RequirementRecord, key string) (RequirementRecord, bool) {
	for _, requirement := range requirements {
		if requirement.Key == key {
			return requirement, true
		}
	}
	return RequirementRecord{}, false
}

// reviewFindingIDs recovers the identifiers the adversarial review raised.
//
// A review that will not parse yields no identifiers, which pins the
// adjudicator's finding lists empty. That is the right failure: an
// adjudication cannot cite findings from a review nobody could read, and the
// unparseable review is a separate problem that surfaces on its own.
func reviewFindingIDs(body []byte, limits Limits) []string {
	review, err := ParseAdversarialReview(body, limits)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(review.Findings))
	for _, finding := range review.Findings {
		ids = append(ids, finding.ID)
	}
	return ids
}
