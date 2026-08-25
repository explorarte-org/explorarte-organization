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
	// ReasonDesignRoundsExhausted ends the revision loop. A design that has
	// been sent back the allowed number of times and still is not settled is
	// waiting on a human, not on another round: the reviewer has said the
	// same kind of thing twice and a third attempt spends budget to hear it
	// again.
	ReasonDesignRoundsExhausted = "design_rounds_exhausted"
	ReasonDesignRejected        = "design_rejected"
	// ReasonAdversarialReviewUnavailable covers every way the reviewer cannot
	// be dispatched at all -- role absent, disabled, not executable, or its
	// provider unconfigured. It fails the run closed. There is deliberately
	// no branch that substitutes another role or another model.
	ReasonAdversarialReviewUnavailable = "adversarial_review_unavailable"
)

// The candidate design is the set of deliverables the departments produced,
// not the verdicts their leaders returned about them.
//
// Those were confused for a long time and nothing could tell, because the
// artifact only ever surfaced as identities and digests. The moment the body
// became readable the adjudicator read it and said so in one line: replace
// the department-review verdict with a textual design artifact. It had been
// judging the review summary and calling it the design.
//
// A completed department review still gates which units contribute -- it is
// what says a department has finished and been judged. It is simply not the
// thing the department made.
//
// designArtifact is the host'"'"'s own definition of "the candidate design": the
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
	// CarriedFromRound is nonzero when this unit did not produce new work
	// in the artifact's round -- its sheet claimed no required change --
	// and the deliverable named here is its last accepted one from that
	// earlier round, standing in verbatim. A design round asks some
	// departments for changes and nothing of the rest; dropping the
	// un-asked ones would silently shrink the design under review.
	CarriedFromRound int `json:"carried_from_round,omitempty"`
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

// DesignBaseSHAReference is where a campaign's pinned commit lives.
const DesignBaseSHAReference = "design-base-sha://"

// ReasonWorldChangedSinceFreeze stops a run whose promotion target moved
// between the design being frozen and the mission being provisioned.
//
// It is never a licence to retarget. A design was decided about ONE version of
// the repository -- its evidence cites that version, its reviewer read that
// version, its adjudicator ruled on that version -- so implementing it against
// a different one would silently convert a decision about S0 into work on S1.
// That the two are usually compatible is exactly what makes it dangerous:
// it would be right often enough to be trusted, and wrong without a signal.
const ReasonWorldChangedSinceFreeze = "world_changed_since_freeze"

// designBaseSHA returns the commit this campaign's design reasons about,
// pinning it durably the first time it is needed.
//
// It is resolved ONCE per campaign and never again. Every design round, every
// adversarial review and every adjudication has to be discussing the same
// repository: if a round could re-resolve the target, the adversarial reviewer might criticise one
// version while Luna rules on another, and neither would be wrong.
//
// This makes a design episode a transaction over a snapshot. The target moving
// afterwards is a concurrency event, not a design error -- and it is the
// mission phase, not this one, that decides what to do about it.
func (o *Orchestrator) designBaseSHA(ctx context.Context, root TaskRecord) (string, error) {
	reference := DesignBaseSHAReference + fmt.Sprint(root.ID)
	detail, err := o.tasks.GetTask(ctx, root.ID)
	if err != nil {
		return "", err
	}
	for _, evidence := range detail.Evidence {
		if evidence.Reference != reference {
			continue
		}
		pinned, _ := evidence.Metadata["design_base_sha"].(string)
		if pinned == "" {
			return "", fmt.Errorf("%w: pinned design base sha is empty", ErrContractRejected)
		}
		return pinned, nil
	}
	// A deployment that only decides designs has no promotion target, and
	// therefore no repository for a design to be about. That is a supported
	// shape (see missionProvisioningOptions), so there is simply nothing to
	// pin -- and the mission phase, which DOES require a pin, fails closed on
	// its absence rather than inventing one.
	if o.programTarget == nil {
		return "", nil
	}
	head, err := o.programTarget.ResolveProgramTargetSHA(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(head) == "" {
		return "", fmt.Errorf("%w: promotion target resolved to no commit", ErrContractRejected)
	}
	// The digest is a SHA-256 of the FACT being recorded, not the commit id.
	// A git SHA is 40 hex characters and the Tasks Engine accepts only 64:
	// writing the commit here would have been refused by the real store while
	// passing every test, because the fakes did not reproduce that boundary.
	// The commit itself lives in metadata, where it is read from.
	factDigest := sha256.Sum256([]byte("design_base_sha\x00" + head))
	if err = o.tasks.RecordEvidence(ctx, EvidenceCommand{
		TaskID: root.ID, Type: "result", Reference: reference,
		Digest: hex.EncodeToString(factDigest[:]), RecordedBy: orchestratorWorkerID,
		Metadata: map[string]any{"design_base_sha": head},
	}); err != nil {
		return "", err
	}
	return head, nil
}

// frozenDesignBaseSHA reads back the commit a frozen design was decided about.
//
// It READS the pin and never creates one. That distinction is the whole
// safety property: a mission that resolved its own base would be free to pick
// a commit nobody designed against, which is exactly the substitution this
// change exists to make impossible. No pin means the world this design was
// decided about is unknown, and unknown fails closed.
func (o *Orchestrator) frozenDesignBaseSHA(ctx context.Context, root TaskRecord) (string, error) {
	reference := DesignBaseSHAReference + fmt.Sprint(root.ID)
	detail, err := o.tasks.GetTask(ctx, root.ID)
	if err != nil {
		return "", err
	}
	for _, evidence := range detail.Evidence {
		if evidence.Reference != reference {
			continue
		}
		if pinned, _ := evidence.Metadata["design_base_sha"].(string); pinned != "" {
			return pinned, nil
		}
	}
	return "", fmt.Errorf("%w: the design carries no pinned base commit, so the repository it was decided about is unknown", ErrContractRejected)
}

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

	round := o.activeDesignRound(ctx, all, root.ID)
	if round > o.limits.MaxDesignRounds {
		run, blockErr := o.blockRoot(ctx, root, ReasonDesignRoundsExhausted,
			fmt.Sprintf("the design was sent back %d times and is still not settled", o.limits.MaxDesignRounds))
		return run, true, blockErr
	}
	artifact, units, ok, err := o.candidateDesign(ctx, root, all, round)
	if err != nil {
		return Run{}, true, err
	}
	if !ok {
		if round > 1 {
			// A later round exists because a revise asked for one, and its
			// deliverables are not in yet. That is ordinary progress, not a
			// missing design: the departments are working on the changes.
			return o.driveInProgress(ctx, root)
		}
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
	bundle, err := o.reviewBundle(ctx, root, design, artifact)
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
			if err := AssertFindingsExist(parsed, reviewIDs); err != nil {
				return err
			}
			// A revise binds the NEXT round to whatever it demands, so the
			// demand is probed against the pinned world while there is still
			// an attempt to correct: an unsupplyable slot is a contract
			// rejection with measured feedback, and a broken sensor is
			// infrastructure -- never Luna's fault.
			return o.probeAdjudicationRequirements(ctx, root, parsed.EvidenceRequirements)
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

	// Already pinned before the first cognitive call; read back here so the
	// freeze records the commit the whole decision was actually made about.
	pinnedBaseSHA, err := o.designBaseSHA(ctx, root)
	if err != nil {
		return Run{}, true, err
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
			// The commit the whole decision was made about. Empty only for
			// a deployment with no promotion target at all.
			"design_base_sha": pinnedBaseSHA,
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
func (o *Orchestrator) candidateDesign(ctx context.Context, root TaskRecord, all []TaskRecord, round int) (designArtifact, []string, bool, error) {
	artifact := designArtifact{RootTaskID: root.ID, Round: round}
	units := make([]string, 0, len(all))
	seenContributors := make(map[string]bool)
	for _, task := range all {
		// A completed department review is what says this department has
		// finished and been judged by its leader. It gates which units
		// contribute -- it is not itself the thing they produced.
		if task.TaskClass != TaskClassCoordinationDeptReview || task.Status != "completed" {
			continue
		}
		if designRoundOf(task.IdempotencyKey) != round {
			// A round's design is the work that round produced. Carrying an
			// earlier round's deliverables forward would put the design that
			// was sent back for changes in front of the reviewer again,
			// alongside the one meant to replace it.
			continue
		}
		unit := task.AssignedUnitID
		if seenContributors[unit] {
			continue
		}
		seenContributors[unit] = true
		superseded, err := o.supersededWorkerKeys(ctx, all, root.ID, unit, round)
		if err != nil {
			return designArtifact{}, nil, false, err
		}
		contributed := false
		for _, worker := range departmentWorkerTasks(all, root.ID, unit) {
			if designRoundOf(worker.IdempotencyKey) != round {
				continue
			}
			// Only completed workers. A failed one produced no
			// deliverable, and the leader review already weighed its
			// failure; presenting nothing as part of the design would
			// be worse than presenting less.
			if worker.Status != "completed" {
				continue
			}
			// The frontier rule (checkpoint E): a worker whose authority a
			// follow-up took over is SUPERSEDED. Its resolution was judged
			// and redone inside the department loop; re-presenting it here
			// would hand the adversarial reviewer the very contradiction
			// the replan just settled, next to the answer that settled it.
			if superseded[workerBaseClientKey(worker.IdempotencyKey, root.ID, unit, round)] {
				continue
			}
			result, ok := o.resultForCompletedTask(ctx, worker)
			if !ok {
				continue
			}
			artifact.Units = append(artifact.Units, designUnitRef{
				UnitID: unit, TaskID: worker.ID,
				InvocationID: result.InvocationID, ResultHash: result.ResponseHash,
			})
			contributed = true
		}
		if contributed {
			units = append(units, unit)
		}
	}
	// Carry-forward pass. A department whose round sheet claimed NO
	// required change was asked for nothing; its last accepted deliverable
	// is still part of this design, and the candidate must say so instead
	// of letting the component vanish because nobody redid it this round.
	if round > 1 {
		contributedUnits := make(map[string]bool, len(units))
		for _, unit := range units {
			contributedUnits[unit] = true
		}
		for _, task := range all {
			if task.TaskClass != TaskClassCoordinationDeptPlan || task.Status != "completed" {
				continue
			}
			if designRoundOf(task.IdempotencyKey) != round {
				continue
			}
			unit := departmentOfPlanKey(task.IdempotencyKey)
			if unit == "" || contributedUnits[unit] {
				continue
			}
			result, ok := o.resultForCompletedTask(ctx, task)
			if !ok {
				continue
			}
			sheet, err := ParseDepartmentPlan(result.JSONOutput, o.limits)
			if err != nil || len(sheet.RevisionOwnership) > 0 {
				continue // claimed work, or unreadable -- never invent a carry
			}
			refs, fromRound, ok := o.carriedContribution(ctx, all, root.ID, unit, round)
			if !ok {
				continue // nothing accepted earlier to carry
			}
			for _, ref := range refs {
				ref.CarriedFromRound = fromRound
				artifact.Units = append(artifact.Units, ref)
			}
			units = append(units, unit)
			contributedUnits[unit] = true
		}
	}
	if len(artifact.Units) == 0 {
		return designArtifact{}, nil, false, nil
	}
	sort.Slice(artifact.Units, func(i, j int) bool {
		if artifact.Units[i].UnitID != artifact.Units[j].UnitID {
			return artifact.Units[i].UnitID < artifact.Units[j].UnitID
		}
		return artifact.Units[i].TaskID < artifact.Units[j].TaskID
	})
	return artifact, units, true, nil
}

// supersededWorkerKeys replays a department's completed reviews of this
// round in replan order and returns the client keys whose authority a
// follow-up TOOK OVER. The authority map starts as the round sheet's own
// claims; every needs_replan verdict re-binds its open changes to the
// follow-ups it proposed, and each re-binding supersedes exactly one
// previous holder -- that is what followup_ownership MEANS. Whoever ends up
// in this set was judged and redone inside the department loop; their
// deliverable is history, not part of the design's effective frontier.
func (o *Orchestrator) supersededWorkerKeys(ctx context.Context, all []TaskRecord, rootID int64, unit string, round int) (map[string]bool, error) {
	superseded := make(map[string]bool)
	authority := make(map[string]string)
	if planTask, found := findTaskByKey(all, childKey(rootID, "leader-plan:"+unit+designRoundSuffix(round))); found && planTask.Status == "completed" {
		if result, ok := o.resultForCompletedTask(ctx, planTask); ok {
			if sheet, err := ParseDepartmentPlan(result.JSONOutput, o.limits); err == nil {
				for _, ownership := range sheet.RevisionOwnership {
					authority[ownership.RequiredChangeID] = ownership.OwnerClientKey
				}
			}
		}
	}
	type replayedReview struct {
		ordinal int
		task    TaskRecord
	}
	var reviews []replayedReview
	reviewPrefix := childKey(rootID, "leader-review:"+unit+designRoundSuffix(round))
	for _, task := range all {
		if task.TaskClass != TaskClassCoordinationDeptReview || task.Status != "completed" {
			continue
		}
		if !strings.HasPrefix(task.IdempotencyKey, reviewPrefix) {
			continue
		}
		reviews = append(reviews, replayedReview{ordinal: reviewReplanOrdinal(task.IdempotencyKey), task: task})
	}
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].ordinal < reviews[j].ordinal })
	for _, r := range reviews {
		result, ok := o.resultForCompletedTask(ctx, r.task)
		if !ok {
			continue
		}
		review, err := ParseDepartmentReview(result.JSONOutput, o.limits)
		if err != nil {
			// An unreadable body carries no bindings to replay. Results are
			// schema-validated when they complete, so this is tolerance for
			// legacy fixtures, not a door for live corruption.
			continue
		}
		if review.Verdict != ReviewNeedsReplan {
			continue
		}
		for _, binding := range review.FollowupOwnership {
			if previous, taken := authority[binding.RequiredChangeID]; taken && previous != "" {
				superseded[previous] = true
			}
			authority[binding.RequiredChangeID] = binding.OwnerClientKey
		}
	}
	return superseded, nil
}

// workerBaseClientKey recovers the proposing client_key from a worker task's
// durable key, stripping the replan suffix a materialized follow-up carries:
// executive:<root>:worker:<unit>[:design-round:N]:<key>[-replan:M] -> <key>.
func workerBaseClientKey(key string, rootID int64, unit string, round int) string {
	rest := strings.TrimPrefix(key, childKey(rootID, "worker:"+unit+designRoundSuffix(round)+":"))
	if index := strings.LastIndex(rest, "-replan:"); index >= 0 {
		tail := rest[index+len("-replan:"):]
		numeric := tail != ""
		for _, r := range tail {
			if r < '0' || r > '9' {
				numeric = false
				break
			}
		}
		if numeric {
			rest = rest[:index]
		}
	}
	return rest
}

// carriedContribution returns a department's last ACCEPTED deliverable from
// rounds before `round`: its most recent earlier round whose review completed
// and produced completed workers with durable results. That is what an
// un-asked department contributes verbatim -- accepted work, never a draft.
func (o *Orchestrator) carriedContribution(ctx context.Context, all []TaskRecord, rootID int64, unit string, round int) ([]designUnitRef, int, bool) {
	for from := round - 1; from >= 1; from-- {
		accepted := false
		for _, task := range all {
			if task.TaskClass == TaskClassCoordinationDeptReview && task.Status == "completed" &&
				designRoundOf(task.IdempotencyKey) == from && task.AssignedUnitID == unit {
				accepted = true
				break
			}
		}
		if !accepted {
			continue
		}
		var refs []designUnitRef
		for _, worker := range departmentWorkerTasks(all, rootID, unit) {
			if designRoundOf(worker.IdempotencyKey) != from || worker.Status != "completed" {
				continue
			}
			result, ok := o.resultForCompletedTask(ctx, worker)
			if !ok {
				continue
			}
			refs = append(refs, designUnitRef{
				UnitID: unit, TaskID: worker.ID,
				InvocationID: result.InvocationID, ResultHash: result.ResponseHash,
			})
		}
		if len(refs) > 0 {
			return refs, from, true
		}
	}
	return nil, 0, false
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
// designRoundSuffix scopes a task to the design round that produced it.
//
// Round 1 carries no suffix, so every key predating design rounds keeps the
// exact identity it already had durably. Later rounds are separate tasks with
// separate keys, which is what makes a round immutable: nothing is reopened
// or overwritten, and round N's work stays exactly as it was judged.
// activeDesignRound is the round the run is working on now.
//
// It advances for exactly one reason: the previous round's adjudication
// returned revise. Not because the artifact changed, not because a task was
// retried, and never on its own -- an earlier revision derived the round from
// a count and advanced on every Resume, which created a fresh review and
// adjudication pair forever over the most expensive execution in the system.
//
// A revise is a governed decision that this design must change, so the work
// that follows belongs to a new round with its own keys. Round N stays
// exactly as it was judged.
func (o *Orchestrator) activeDesignRound(ctx context.Context, all []TaskRecord, rootID int64) int {
	round := designRound(all, rootID)
	adjudication, ok := findTaskByKey(all, childKey(rootID, "design-adjudication:round:"+strconv.Itoa(round)))
	if !ok || adjudication.Status != "completed" {
		return round
	}
	result, ok := o.resultForCompletedTask(ctx, adjudication)
	if !ok {
		return round
	}
	verdict, err := adjudicationVerdictOf(result.JSONOutput)
	if err != nil || verdict != AdjudicationRevise {
		return round
	}
	return round + 1
}

// adjudicationVerdictOf reads only the verdict, without the host identity
// binding a full parse requires. The round decision needs the verdict alone,
// and demanding the whole contract here would make an unparseable adjudication
// silently stop the round from advancing.
func adjudicationVerdictOf(body []byte) (AdjudicationVerdict, error) {
	var envelope struct {
		Verdict AdjudicationVerdict `json:"verdict"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", err
	}
	return envelope.Verdict, nil
}

func designRoundSuffix(round int) string {
	if round <= 1 {
		return ""
	}
	return ":design-round:" + strconv.Itoa(round)
}

// designRoundOf recovers the round a task belongs to from its key.
//
// The marker is not always last. A review key ends with it
// (leader-review:<unit>:design-round:2) but a worker key continues past it
// (worker:<unit>:design-round:2:<client-key>), so reading to the end of the
// string parsed "2:design-1" and silently fell back to round 1 -- which put
// every later round's worker in round 1 and left round 2 with no design at
// all. Only the digits up to the next separator belong to the round.
func designRoundOf(key string) int {
	marker := ":design-round:"
	index := strings.LastIndex(key, marker)
	if index < 0 {
		return 1
	}
	rest := key[index+len(marker):]
	if end := strings.Index(rest, ":"); end >= 0 {
		rest = rest[:end]
	}
	round, err := strconv.Atoi(rest)
	if err != nil || round < 1 {
		return 1
	}
	return round
}

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

// candidateBody resolves the exact durable deliverable behind each unit of
// the artifact and renders it as an inspectable design.
//
// Every unit's body is fetched by its own invocation id and then checked
// against the result hash the artifact recorded. That check is the whole
// point: what the reviewer reads must be provably the same object the
// artifact says is under review. Without it, "here is the design" and "here
// is its digest" would be two independent claims, and an inspectable body
// would have bought readability at the cost of provenance.
//
// Nothing else is read. Not task instructions, not repository files, not
// whole task contexts -- only the durable results the artifact already names.
func (o *Orchestrator) candidateBody(ctx context.Context, artifact designArtifact) (string, error) {
	sections := make([]string, 0, len(artifact.Units))
	for _, unit := range artifact.Units {
		result, err := o.models.GetResult(ctx, unit.InvocationID)
		if err != nil {
			return "", fmt.Errorf("resolving deliverable for %s (task:%d invocation:%d): %w", unit.UnitID, unit.TaskID, unit.InvocationID, err)
		}
		if result.ResponseHash != unit.ResultHash {
			return "", fmt.Errorf("%w: deliverable for %s (task:%d invocation:%d) hashes %s but the artifact records %s",
				ErrContractRejected, unit.UnitID, unit.TaskID, unit.InvocationID, result.ResponseHash, unit.ResultHash)
		}
		body := strings.TrimSpace(result.TextOutput)
		if body == "" {
			body = strings.TrimSpace(string(result.JSONOutput))
		}
		if body == "" {
			return "", fmt.Errorf("%w: deliverable for %s (task:%d invocation:%d) is empty",
				ErrContractRejected, unit.UnitID, unit.TaskID, unit.InvocationID)
		}
		if limit := o.limits.MaxStringBytes; limit > 0 && len(body) > limit {
			body = body[:limit]
		}
		carried := ""
		if unit.CarriedFromRound > 0 {
			carried = fmt.Sprintf(" [carried forward unchanged from design round %d; this round asked this department for no changes]", unit.CarriedFromRound)
		}
		sections = append(sections, fmt.Sprintf("%s%s (task:%d model-invocation:%d result:%s)\n%s",
			unit.UnitID, carried, unit.TaskID, unit.InvocationID, unit.ResultHash, body))
	}
	return joinLines(sections), nil
}

// verifiedDesignCitations confirms which repository citations in a candidate
// design were really in front of the model that wrote it.
//
// Each deliverable is checked against ITS OWN invocation's context snapshot,
// not against a shared or rebuilt one. Two workers in the same round can see
// different excerpts -- the selection reads each task's own instructions -- so
// checking one design's citations against another's context would verify
// claims their author never had grounds for.
func (o *Orchestrator) verifiedDesignCitations(ctx context.Context, root TaskRecord, artifact designArtifact) ([]designreview.DeliverableCitations, []OrganizationalSource, error) {
	if o.programTarget == nil && o.snapshotSources == nil {
		// Nothing was grounded and nothing can be read back: there are no
		// repository claims to verify and no source that could have been
		// shown to a worker.
		return nil, nil, nil
	}
	baseSHA, err := o.frozenDesignBaseSHA(ctx, root)
	if err != nil || baseSHA == "" {
		// A campaign with no pinned world has no repository claims to
		// ground, and nothing to verify them against.
		return nil, nil, nil
	}
	// A repository-grounded campaign with no way to read back what the
	// workers were shown is not an ungrounded campaign, it is an
	// unobservable one: citations could not be verified and the candidate
	// could not be checked for source it must not carry. The same
	// distinction the grounding path makes between "no repository" and "a
	// repository I cannot read" applies here, and for the same reason --
	// the second must never degrade into the first.
	//
	// The condition is programTarget, not the pin: a governed design freeze
	// is pinned even where no repository is wired, and there a worker never
	// saw source, so there is nothing to read back. Refusing on the pin
	// alone would make an unrelated dependency of every governed campaign.
	if o.programTarget != nil && o.snapshotSources == nil {
		return nil, nil, fmt.Errorf("%w: design cites a pinned world but no snapshot reader is wired", ErrContractRejected)
	}
	organizational := make([]OrganizationalSource, 0, 8)
	deliverables := make([]designreview.DeliverableCitations, 0, len(artifact.Units))
	for _, unit := range artifact.Units {
		invocation, readErr := o.models.GetInvocation(ctx, unit.InvocationID)
		if readErr != nil {
			return nil, nil, readErr
		}
		// Authorization is the tuple (task, invocation, result digest,
		// reference). Every check below binds one element of it, and each
		// one was missing: the tuple was assembled from labels the artifact
		// asserted rather than from facts the host confirmed.
		//
		// An invocation belonging to another task cannot ground this
		// deliverable's claims, however genuine its own citations are.
		if invocation.TaskID != unit.TaskID {
			return nil, nil, fmt.Errorf("%w: deliverable claims task %d but invocation %d belongs to task %d",
				ErrContractRejected, unit.TaskID, unit.InvocationID, invocation.TaskID)
		}
		// No snapshot means there is no record of what this model was shown.
		// Skipping it left the deliverable out of deliverables[] while its
		// text stayed in the candidate design -- claims with no owner beside
		// other deliverables' references, which is the laundering this
		// structure exists to prevent, arriving through omission.
		if invocation.ContextSnapshotID == 0 {
			return nil, nil, fmt.Errorf("%w: invocation %d records no context snapshot, so what it was shown is unknown",
				ErrContractRejected, unit.InvocationID)
		}
		result, resultErr := o.models.GetResult(ctx, unit.InvocationID)
		if resultErr != nil {
			return nil, nil, resultErr
		}
		// The text verified must be the text the artifact recorded. Without
		// this, citations found in whatever GetResult returns today would be
		// published under a digest describing different bytes -- the reviewer
		// would be told that D1 was entitled to references extracted from
		// something that is not D1.
		if result.ResponseHash != unit.ResultHash {
			return nil, nil, fmt.Errorf("%w: deliverable for task %d hashes %s but the artifact records %s",
				ErrContractRejected, unit.TaskID, result.ResponseHash, unit.ResultHash)
		}
		body := strings.TrimSpace(result.TextOutput)
		if body == "" {
			body = strings.TrimSpace(string(result.JSONOutput))
		}
		shown, shownErr := o.snapshotSources.SnapshotSources(ctx, invocation.ContextSnapshotID)
		if shownErr != nil {
			return nil, nil, shownErr
		}
		for _, source := range shown {
			if source.Kind == "repository_evidence" && source.Included && source.Content != "" {
				organizational = append(organizational, OrganizationalSource{Reference: source.Reference, Content: source.Content})
			}
		}
		verified, verifyErr := o.VerifyRepositoryCitations(ctx, o.snapshotSources,
			invocation.ContextSnapshotID, baseSHA, body, unit.TaskID, unit.InvocationID, result.ResponseHash)
		if verifyErr != nil {
			return nil, nil, verifyErr
		}
		// The entry is emitted even with no verified references. Silence and
		// "this deliverable grounded nothing" are different facts, and the
		// reviewer needs the second one to judge a claim that cites nothing.
		refs := make([]string, 0, len(verified))
		for _, citation := range verified {
			// Belt and braces on the tuple: a citation that came back
			// describing another deliverable must never be published under
			// this one.
			if citation.TaskID != unit.TaskID || citation.InvocationID != unit.InvocationID || citation.ResultDigest != unit.ResultHash {
				return nil, nil, fmt.Errorf("%w: verified citation %s does not belong to task %d invocation %d",
					ErrContractRejected, citation.Reference, unit.TaskID, unit.InvocationID)
			}
			refs = append(refs, citation.Reference)
		}
		deliverables = append(deliverables, designreview.DeliverableCitations{
			TaskID: unit.TaskID, InvocationID: unit.InvocationID,
			ResultDigest: result.ResponseHash, VerifiedRepositoryRefs: refs,
		})
	}
	return deliverables, organizational, nil
}

func (o *Orchestrator) reviewBundle(ctx context.Context, root TaskRecord, design designfreeze.Design, artifact designArtifact) ([]byte, error) {
	evidence := make([]string, 0, len(artifact.Units))
	for _, unit := range artifact.Units {
		evidence = append(evidence, fmt.Sprintf("task:%d:model-invocation:%d", unit.TaskID, unit.InvocationID))
	}
	// Repository citations the host has confirmed were really in front of the
	// designer that used them.
	//
	// References only, never the source behind them. The reviewer's context
	// admits public and sanitized data, and repository evidence is
	// organizational: widening that so it could read code would be an egress
	// decision taken as a side effect of a convenience. What it gets instead
	// is exactly what it needs -- the set of claims it may treat as grounded.
	deliverables, organizational, err := o.verifiedDesignCitations(ctx, root, artifact)
	if err != nil {
		return nil, err
	}
	body, err := o.candidateBody(ctx, artifact)
	if err != nil {
		return nil, err
	}
	// The candidate is model text written by designers that read
	// organizational source. Before any of it leaves for a reviewer whose
	// context admits only public and sanitized data, the host establishes
	// that it carries claims about the code and not the code.
	//
	// Against the union of everything the contributing deliverables were
	// shown, not only what the author of a given passage saw: egress is not
	// a property of the author.
	if err = DeclassifyCandidate(body, organizational); err != nil {
		return nil, err
	}
	// Only the criteria the owner assigned to the design phase. The rest
	// describe what the built change must demonstrate, and asking a design
	// reviewer to verify them is asking it to verify the future -- which
	// it refused, correctly, on every campaign that got this far.
	recorded, err := o.acceptance.Acceptance(ctx, root.ID)
	if err != nil {
		return nil, fmt.Errorf("read acceptance phases for root %d: %w", root.ID, err)
	}
	if len(recorded) == 0 {
		return nil, fmt.Errorf("%w: root %d has no recorded acceptance phases; it predates phase ownership and must be resubmitted",
			ErrContractRejected, root.ID)
	}
	designRequirements := AcceptanceForPhase(recorded, AcceptanceDesign)
	if len(designRequirements) == 0 {
		return nil, fmt.Errorf("%w: root %d has no design-phase acceptance criterion to judge the design against",
			ErrContractRejected, root.ID)
	}
	bundle := designreview.Bundle{
		OwnerRequirements: designRequirements,
		CandidateDesign:   body,
		ArchitectureConstraints: []string{
			// The old text claimed the reviewer saw only identities and
			// digests. It was true, and it made the review impossible:
			// a reviewer that cannot read the design can only report
			// that it could not verify anything, which is what it did,
			// every time. This states the property we actually want --
			// readable AND provably the object under review.
			"The reviewer sees the sanitized candidate design bound to the durable deliverable identities and result digests listed in this bundle.",
			// The rule that makes repository grounding worth anything. A
			// design may now look at code, so a claim about code can be
			// checked -- and one that cites nothing is no longer merely
			// unsupported, it is a claim the designer had every means to
			// ground and did not.
			"Any claim about concrete repository structure, files, symbols, existing behavior or implementation must cite a repository:// reference listed under the SAME deliverable that makes the claim, in deliverables[].verified_repository_refs.",
			"A reference authorized for one deliverable does not authorize a claim made by another: each deliverable saw different code.",
			"A claim of that kind with no authorized repository citation is a finding of kind unverifiable_repository_claim.",
			"Do not infer that a repository citation exists merely because the candidate design names a file or path.",
		},
		AuthorityConstraints: []string{
			"The reviewer publishes findings; it does not approve, adjudicate or freeze.",
			"Only empresa/ceo may adjudicate, and only verdict=freeze settles the design.",
		},
		UnresolvedDecisions: nil,
		EvidenceRefs:        evidence,
		Deliverables:        deliverables,
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
