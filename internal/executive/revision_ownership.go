package executive

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Checkpoint E -- Revision Ownership + Department Consistency Gate.
//
// R16's convergence audit located the loss precisely: the round-2 plan gave
// TWO parallel workers the same central question (MaxDepartmentReplans
// granularity), they answered it with opposite claims, and the department
// review voted accept over internally contradictory deliverables. The
// contradiction then escaped into the DESIGN loop and cost another full
// adversarial-plus-adjudication round.
//
// The two contracts that were missing are now host-owned:
//
//  1. IDENTITY. Every required change of a design round carries a durable,
//     reproducible id -- RC:<adjudication-round>:<ordinal> -- assigned by the
//     HOST from the adjudication's own required_changes array. The planner is
//     told the ids; it never invents them.
//
//  2. EXCLUSIVE OWNERSHIP. A department plan for that round must bind every
//     id to exactly ONE proposed task. Two owners for one decision, an owner
//     for an unknown decision, or a decision left unowned are all refused at
//     plan-contract time -- before any worker exists, before any money is
//     spent. One worker may own several changes; support tasks may own none;
//     what is forbidden is parallel authority over one decision.
//
// And on the way back, the reviewer answers PER required change whether the
// deliverables collectively resolved it. accept is only representable when
// every change says resolved: R16's shape -- contradictory deliverables under
// an accept verdict -- now leaves as needs_replan, which routes to
// MaxDepartmentReplans, the bound that actually owns departmental redoing.
// MaxDesignRounds keeps governing only the design loop.

// RequiredChange is one host-identified demand from an adjudication.
type RequiredChange struct {
	ID   string
	Text string
}

// requiredChangeID formats the host-owned identity: the adjudication round
// that produced the demand plus its 1-based ordinal in that adjudication's
// own required_changes order (deterministic: the durable result's array).
func requiredChangeID(round, ordinal int) string {
	return fmt.Sprintf("RC:%d:%d", round, ordinal)
}

// requiredChangesWithIDs reads an adjudication's required_changes and names
// each one. The ordinal comes from the DURABLE ARRAY ORDER, never from any
// re-sorting: the same completed adjudication always yields the same ids.
func (o *Orchestrator) requiredChangesWithIDs(ctx context.Context, all []TaskRecord, rootID int64, adjudicationRound int) ([]RequiredChange, error) {
	texts, err := o.requiredChangesOf(ctx, all, rootID, adjudicationRound)
	if err != nil {
		return nil, err
	}
	changes := make([]RequiredChange, 0, len(texts))
	for index, text := range texts {
		changes = append(changes, RequiredChange{
			ID:   requiredChangeID(adjudicationRound, index+1),
			Text: text,
		})
	}
	return changes, nil
}

// outstandingRevisionChanges returns the required changes a round-N
// department plan must assign ownership for: those of the round-(N-1)
// adjudication. Round 1 has no prior adjudication, so nothing is outstanding
// and the plan's ownership list must be empty.
func (o *Orchestrator) outstandingRevisionChanges(ctx context.Context, all []TaskRecord, rootID int64, round int) ([]RequiredChange, error) {
	if round <= 1 {
		return nil, nil
	}
	return o.requiredChangesWithIDs(ctx, all, rootID, round-1)
}

// departmentOfPlanKey recovers the unit a round plan task belongs to from
// its key (leader-plan:<unit>[:design-round:N]).
func departmentOfPlanKey(key string) string {
	marker := "leader-plan:"
	index := strings.Index(key, marker)
	if index < 0 {
		return ""
	}
	rest := key[index+len(marker):]
	if end := strings.Index(rest, ":"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// validateOwnershipProposal checks ONE department's round claim sheet --
// structurally, against the round's full roster and that sheet's own tasks:
// every claimed id must be a required change of this round, every owner must
// be a task this very plan proposes, and no id may be claimed twice inside
// the same sheet. What it deliberately does NOT check is the CROSS-department
// union -- whether some other department also claimed an id, or whether
// between them the plans cover the roster at all. That judgment needs every
// department's proposal in hand, so it lives where all of them are visible:
// the last completing plan carries it (validateRoundPartition).
//
// A sheet claiming NOTHING is legal -- that is a department the round asks
// nothing of -- but it then may not propose work either: its previous
// deliverable stands, carried forward as-is into the candidate.
func validateOwnershipProposal(roster []RequiredChange, plan DepartmentPlan) error {
	if len(roster) == 0 {
		if len(plan.RevisionOwnership) > 0 {
			return fmt.Errorf("%w: revision_ownership lists %d entries but this round has no outstanding required changes",
				ErrContractRejected, len(plan.RevisionOwnership))
		}
		return nil
	}
	if len(plan.RevisionOwnership) == 0 {
		if len(plan.Tasks) > 0 {
			return fmt.Errorf("%w: this plan claims no required change, so it proposes no work; "+
				"an unclaiming department is carried forward, not re-staffed", ErrContractRejected)
		}
		return nil
	}
	rosterIDs := make(map[string]RequiredChange, len(roster))
	for _, change := range roster {
		rosterIDs[change.ID] = change
	}
	knownKeys := make(map[string]bool, len(plan.Tasks))
	for _, task := range plan.Tasks {
		knownKeys[task.ClientKey] = true
	}
	seen := make(map[string]string, len(plan.RevisionOwnership))
	for _, ownership := range plan.RevisionOwnership {
		change, known := rosterIDs[ownership.RequiredChangeID]
		if !known {
			return fmt.Errorf("%w: revision_ownership names %q, which is not a required change of this design round",
				ErrContractRejected, ownership.RequiredChangeID)
		}
		if !knownKeys[ownership.OwnerClientKey] {
			return fmt.Errorf("%w: revision_ownership assigns %q to client_key %q, which this plan does not propose",
				ErrContractRejected, change.ID, ownership.OwnerClientKey)
		}
		if previous, taken := seen[ownership.RequiredChangeID]; taken {
			return fmt.Errorf("%w: required change %q (%s) has two owners (%q and %q); one decision, one owner",
				ErrContractRejected, ownership.RequiredChangeID, truncate(change.Text, 120), previous, ownership.OwnerClientKey)
		}
		seen[ownership.RequiredChangeID] = ownership.OwnerClientKey
	}
	return nil
}

// planClaims narrows a department's claim sheet to the round's roster,
// returned in roster order -- the assigned subset every downstream consumer
// (review table, followup gate) reads for that department.
func planClaims(plan DepartmentPlan, roster []RequiredChange) []RequiredChange {
	claimed := make(map[string]bool, len(plan.RevisionOwnership))
	for _, ownership := range plan.RevisionOwnership {
		claimed[ownership.RequiredChangeID] = true
	}
	claims := make([]RequiredChange, 0, len(plan.RevisionOwnership))
	for _, change := range roster {
		if claimed[change.ID] {
			claims = append(claims, change)
		}
	}
	if len(claims) == 0 {
		return nil
	}
	return claims
}

// unitRoundPlan is one department's completed round plan beside its unit id.
type unitRoundPlan struct {
	unit string
	plan DepartmentPlan
}

// completedRoundPlans parses every requested department's COMPLETED round-N
// plan. Units whose plan has not finished are reported separately -- their
// proposals do not exist yet, which is exactly what defers the coverage half
// of the partition check until the last plan completes.
func (o *Orchestrator) completedRoundPlans(ctx context.Context, all []TaskRecord, rootID int64, requests []DepartmentRequest, round int) ([]unitRoundPlan, []string, error) {
	done := make([]unitRoundPlan, 0, len(requests))
	pending := make([]string, 0, len(requests))
	for _, req := range requests {
		task, found := findTaskByKey(all, childKey(rootID, "leader-plan:"+req.UnitID+designRoundSuffix(round)))
		if !found || task.Status != "completed" {
			pending = append(pending, req.UnitID)
			continue
		}
		result, ok := o.resultForCompletedTask(ctx, task)
		if !ok {
			return nil, nil, fmt.Errorf("%w: department %s round %d plan completed with no durable result",
				ErrContractRejected, req.UnitID, round)
		}
		parsed, err := ParseDepartmentPlan(result.JSONOutput, o.limits)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: department %s round %d plan no longer parses: %v",
				ErrContractRejected, req.UnitID, round, err)
		}
		done = append(done, unitRoundPlan{unit: req.UnitID, plan: parsed})
	}
	return done, pending, nil
}

// validateRoundPartition is the GLOBAL half of checkpoint E's ownership rule,
// evaluated once every department's proposal is in hand: across ALL of them,
// every required change of the round must be claimed EXACTLY once. The host
// never decides WHO takes a change -- the departments' own sheets say that --
// but it refuses any world where a decision has two departments or none,
// before a single worker of the round exists.
func validateRoundPartition(done []unitRoundPlan, roster []RequiredChange) error {
	holders := make(map[string]string, len(roster))
	texts := make(map[string]string, len(roster))
	var problems []string
	for _, up := range done {
		for _, ownership := range up.plan.RevisionOwnership {
			if previous, taken := holders[ownership.RequiredChangeID]; taken {
				problems = append(problems, ownership.RequiredChangeID+
					" is claimed by both "+previous+" and "+up.unit)
				continue
			}
			holders[ownership.RequiredChangeID] = up.unit
		}
	}
	for _, change := range roster {
		texts[change.ID] = change.Text
		if _, claimed := holders[change.ID]; !claimed {
			problems = append(problems, change.ID+" ("+truncate(change.Text, 80)+") is claimed by no department")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%w: the round's ownership partition is not exact: %s",
			ErrContractRejected, strings.Join(problems, "; "))
	}
	return nil
}

// renderOwnershipRoster is the planner-facing rendering: id + demanded text,
// with the exclusivity rule stated where the ids are stated (the header in
// the plan instructions carries it). Owners do not exist yet at planning
// time -- the plan itself creates them.
func renderOwnershipRoster(changes []RequiredChange) string {
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, change.ID+" "+change.Text)
	}
	return joinLines(lines)
}

// renderOwnershipTable is the reviewer-facing rendering: id, the ONE owner
// client key the accepted plan assigned, and the demanded text. A change with
// no recorded owner renders as UNASSIGNED so the gap is visible instead of
// silent.
func renderOwnershipTable(changes []RequiredChange, owners map[string]string) string {
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		owner, ok := owners[change.ID]
		if !ok || strings.TrimSpace(owner) == "" {
			owner = "UNASSIGNED"
		}
		lines = append(lines, change.ID+" [owner: "+owner+"] "+change.Text)
	}
	return joinLines(lines)
}

// validateRevisionOutcomes gates the COMPLETED review: per outstanding
// required change there must be exactly one well-formed outcome; conflicted
// or unresolved outcomes are OPEN answers, representable only under a
// needs_replan verdict; accept is only consistent when every outcome says
// resolved. This runs inside the attempt callback, so a refusal is retryable
// feedback the reviewer can act on -- not a permanent wall.
func validateRevisionOutcomes(outstanding []RequiredChange, review DepartmentReview) error {
	outcomes := make(map[string]RevisionOutcome, len(review.RevisionOutcomes))
	var problems []string
	for _, outcome := range review.RevisionOutcomes {
		if _, duplicate := outcomes[outcome.RequiredChangeID]; duplicate {
			problems = append(problems, outcome.RequiredChangeID+" stated more than once")
			continue
		}
		outcomes[outcome.RequiredChangeID] = outcome
	}
	open := 0
	for _, change := range outstanding {
		outcome, stated := outcomes[change.ID]
		if !stated {
			problems = append(problems, change.ID+" not addressed by revision_outcomes")
			continue
		}
		delete(outcomes, change.ID)
		statusProblem := ""
		switch outcome.Status {
		case RevisionResolved:
			if strings.TrimSpace(outcome.CanonicalResolution) == "" {
				statusProblem = "claims resolved without stating the canonical resolution"
			}
		case RevisionConflicted:
			if len(outcome.ConflictingTaskRefs) == 0 {
				statusProblem = "claims conflicted without naming the conflicting tasks"
			} else if review.Verdict == ReviewAccept {
				// Well-formed, and still fatal under accept: conflicted means
				// the deliverables assert incompatible resolutions. R16's
				// department review accepted exactly this shape.
				statusProblem = "is conflicted (" +
					strings.Join(outcome.ConflictingTaskRefs, ", ") + ") under an accept verdict"
			} else {
				open++
			}
		case RevisionUnresolved:
			// Unresolved is structurally well-formed and OPEN. Under
			// needs_replan it is the honest answer -- exactly what routes the
			// redo; only an accept verdict contradicts it. Making the status
			// itself a problem would reject the very answer the consistency
			// rule tells the reviewer to give when deliverables leave a
			// change unanswered.
			if review.Verdict == ReviewAccept {
				statusProblem = "is unresolved under an accept verdict"
			}
			open++
		default:
			statusProblem = "has an unknown status " + outcome.Status
		}
		if statusProblem != "" {
			problems = append(problems, change.ID+" "+statusProblem)
		}
	}
	for id := range outcomes {
		problems = append(problems, "revision_outcomes names "+id+", which is not a required change of this design round")
	}
	// CLOSED WORLD + verdict compatibility. accept is only representable when
	// every required change says resolved; needs_replan is only honest when
	// at least one of them is still open; blocked/fail end the run either
	// way, so their statuses stay unconstrained beyond well-formedness.
	switch review.Verdict {
	case ReviewAccept:
		if open > 0 {
			problems = append(problems, "accept requires every required change resolved")
		}
	case ReviewNeedsReplan:
		if outstanding != nil && open == 0 && len(problems) == 0 {
			problems = append(problems, "needs_replan requires at least one conflicted or unresolved required change")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		return fmt.Errorf("%w: revision outcomes refused: %s",
			ErrContractRejected, strings.Join(problems, "; "))
	}
	return nil
}

// validateFollowupOwnership is checkpoint E1 on the REDO side, in its
// worker-atomic form. Authority over required changes lives per change, but
// a redo replaces a WHOLE deliverable: when a follow-up takes over one
// change, it must take over every change the replaced artifact still
// governs -- otherwise part of a judged-and-redone result would survive as
// authority while the rest of that same result is being redone, and the
// candidate frontier (which drops superseded workers whole) would silently
// lose the still-valid resolutions.
//
// `authority` is the CURRENT change->owner map for this department and
// round (roundOwnershipReplay). The rule set:
//
//   - every binding must name a proposed follow-up and a change this
//     department owns; no change may be bound twice;
//   - CLOSURE: binding any change of owner P forces binding ALL changes P
//     currently owns (a resolved sibling cannot stay with a half-replaced
//     artifact);
//   - COVERAGE: every OPEN (conflicted/unresolved) change must be bound.
func validateFollowupOwnership(universe []RequiredChange, authority map[string]string, review DepartmentReview) error {
	followupKeys := make(map[string]bool, len(review.ProposedFollowupTasks))
	for _, task := range review.ProposedFollowupTasks {
		followupKeys[task.ClientKey] = true
	}
	universeIDs := make(map[string]bool, len(universe))
	openIDs := make(map[string]bool, len(universe))
	outcomes := make(map[string]RevisionOutcome, len(review.RevisionOutcomes))
	for _, outcome := range review.RevisionOutcomes {
		outcomes[outcome.RequiredChangeID] = outcome
	}
	for _, change := range universe {
		universeIDs[change.ID] = true
		if outcome, ok := outcomes[change.ID]; !ok || outcome.Status != RevisionResolved {
			openIDs[change.ID] = true
		}
	}
	bindings := make(map[string]string)
	var problems []string
	for _, ownership := range review.FollowupOwnership {
		id := ownership.RequiredChangeID
		if !followupKeys[ownership.OwnerClientKey] {
			problems = append(problems, id+" names follow-up client_key "+ownership.OwnerClientKey+", which this review does not propose")
			continue
		}
		if previous, taken := bindings[id]; taken {
			problems = append(problems, id+" has two follow-up owners ("+previous+" and "+ownership.OwnerClientKey+")")
			continue
		}
		if !universeIDs[id] {
			problems = append(problems, id+" is not a required change this department owns")
			continue
		}
		bindings[id] = ownership.OwnerClientKey
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%w: followup_ownership invalid: %s",
			ErrContractRejected, strings.Join(problems, "; "))
	}

	// Atomic-takeover closure: from each bound change, walk to its current
	// owner and demand bindings for everything else that owner governs.
	texts := make(map[string]string, len(universe))
	ownedBy := make(map[string][]string)
	for _, change := range universe {
		texts[change.ID] = truncate(change.Text, 80)
		if owner := authority[change.ID]; owner != "" {
			ownedBy[owner] = append(ownedBy[owner], change.ID)
		}
	}
	required := make(map[string]bool)
	queue := make([]string, 0, len(bindings))
	for id := range bindings {
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if required[id] {
			continue
		}
		required[id] = true
		if owner := authority[id]; owner != "" {
			queue = append(queue, ownedBy[owner]...)
		}
	}
	bound := func(id string) bool { _, ok := bindings[id]; return ok }
	problems = problems[:0]
	for _, change := range universe {
		if required[change.ID] && !bound(change.ID) {
			problems = append(problems, change.ID+" ("+texts[change.ID]+") stays governed by "+
				authority[change.ID]+", whose whole deliverable this redo replaces; "+
				"bind every change that artifact still owns")
		}
	}
	for _, change := range universe {
		if openIDs[change.ID] {
			if _, bound := bindings[change.ID]; !bound {
				problems = append(problems, "open required change "+change.ID+" has no follow-up owner")
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%w: followup_ownership invalid: %s",
			ErrContractRejected, strings.Join(problems, "; "))
	}
	return nil
}

// departmentConsistencyGuidance is checkpoint E's rider on the department
// review run: compare deliverables AGAINST EACH OTHER per required change --
// the exact duty whose absence let R16's contradictory pair sail through as
// accept. Phrased conditionally because reviews of plans without ownership
// have nothing to reconcile.
const departmentConsistencyGuidance = `Consistency rule for this review: if the plan instructions listed required-change ids, you MUST state a revision_outcomes entry for each one, comparing the deliverables against each other on that change -- not merely checking each deliverable against its own criteria. Where two deliverables assert incompatible resolutions, status is conflicted and you must name both tasks; accept is only available when every required change says resolved with one canonical resolution. When deliverables leave a change unanswered, say unresolved and return needs_replan so the department redoes the work within its own replan bound.`

// departmentReviewDelegationScopeGuidance closes AUTONOMY-SMOKE-017-R17-V6's
// task 15763: a review proposed a follow-up task for
// investigacion/revisor_adversarial, a role in a different department.
// ValidateFollowups (a thin wrapper over ValidateDepartmentPlan) rejected it
// correctly -- the same-department rule has no carve-out for the
// adversarial reviewer, and none is needed: that role is dispatched only by
// the host's own driveDesignFreeze once the root carries the design-freeze
// requirement. Nothing had ever told the reviewer proposed_followup_tasks
// is department-scoped, or that this transition is automatic.
const departmentReviewDelegationScopeGuidance = `Every proposed_followup_tasks entry must target a role within your own reviewing department. Adversarial review and design-freeze are transitions the host orchestrates automatically once your review completes -- do not request them, or any role outside your department, through proposed_followup_tasks.`

var _ = strconv.Itoa
