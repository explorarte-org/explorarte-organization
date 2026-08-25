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

// validateRevisionOwnership enforces exclusive ownership at plan-contract
// time, before materializeWorkerTasks creates anyone. The refusals map one to
// one to R16's failure shape and to the reviewer's guard list: unknown ids,
// unowned changes, and two owners for one change are all contract
// rejections; one worker owning SEVERAL changes is fine; support tasks that
// own nothing are fine.
func validateRevisionOwnership(outstanding []RequiredChange, plan DepartmentPlan) error {
	if len(outstanding) == 0 {
		if len(plan.RevisionOwnership) > 0 {
			return fmt.Errorf("%w: revision_ownership lists %d entries but this round has no outstanding required changes",
				ErrContractRejected, len(plan.RevisionOwnership))
		}
		return nil
	}
	outstandingIDs := make(map[string]RequiredChange, len(outstanding))
	for _, change := range outstanding {
		outstandingIDs[change.ID] = change
	}
	knownKeys := make(map[string]bool, len(plan.Tasks))
	for _, task := range plan.Tasks {
		knownKeys[task.ClientKey] = true
	}
	owners := make(map[string]string, len(outstanding))
	for _, ownership := range plan.RevisionOwnership {
		change, known := outstandingIDs[ownership.RequiredChangeID]
		if !known {
			return fmt.Errorf("%w: revision_ownership names %q, which is not a required change of this design round",
				ErrContractRejected, ownership.RequiredChangeID)
		}
		if !knownKeys[ownership.OwnerClientKey] {
			return fmt.Errorf("%w: revision_ownership assigns %q to client_key %q, which this plan does not propose",
				ErrContractRejected, change.ID, ownership.OwnerClientKey)
		}
		if previous, taken := owners[ownership.RequiredChangeID]; taken {
			return fmt.Errorf("%w: required change %q (%s) has two owners (%q and %q); one decision, one owner",
				ErrContractRejected, ownership.RequiredChangeID, truncate(change.Text, 120), previous, ownership.OwnerClientKey)
		}
		owners[ownership.RequiredChangeID] = ownership.OwnerClientKey
	}
	unowned := make([]string, 0, len(outstanding))
	for _, change := range outstanding {
		if _, owned := owners[change.ID]; !owned {
			unowned = append(unowned, change.ID+" ("+truncate(change.Text, 80)+")")
		}
	}
	if len(unowned) > 0 {
		sort.Strings(unowned)
		return fmt.Errorf("%w: required changes without exactly one owner: %s",
			ErrContractRejected, strings.Join(unowned, "; "))
	}
	return nil
}

// renderOwnershipRoster is the planner-facing rendering: id + demanded text,
// with the exclusivity rule stated where the ids are stated. It is also the
// table handed to the reviewer, so both sides of the round speak about the
// same named decisions.
func renderOwnershipRoster(changes []RequiredChange) string {
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, change.ID+" "+change.Text)
	}
	return joinLines(lines)
}

// validateRevisionOutcomes gates the COMPLETED review: per outstanding
// required change there must be exactly one well-formed outcome, and an
// accept verdict is only consistent when every outcome says resolved. This
// runs inside the attempt callback, so a refusal is retryable feedback the
// reviewer can act on -- not a permanent wall.
func validateRevisionOutcomes(outstanding []RequiredChange, review DepartmentReview) error {
	if len(outstanding) == 0 {
		return nil
	}
	outcomes := make(map[string]RevisionOutcome, len(review.RevisionOutcomes))
	for _, outcome := range review.RevisionOutcomes {
		if _, duplicate := outcomes[outcome.RequiredChangeID]; duplicate {
			return fmt.Errorf("%w: revision_outcomes states %q twice", ErrContractRejected, outcome.RequiredChangeID)
		}
		outcomes[outcome.RequiredChangeID] = outcome
	}
	var problems []string
	for _, change := range outstanding {
		outcome, stated := outcomes[change.ID]
		if !stated {
			problems = append(problems, change.ID+" not addressed by revision_outcomes")
			continue
		}
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
			}
		case RevisionUnresolved:
			if review.Verdict == ReviewAccept {
				statusProblem = "is unresolved under an accept verdict (" +
					truncate(outcome.CanonicalResolution, 100) + ")"
			}
		}
		if statusProblem != "" {
			problems = append(problems, change.ID+" "+statusProblem)
		}
	}
	if review.Verdict == ReviewAccept && len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%w: accept requires every required change resolved; %s",
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

// outstandingChangesForReview computes the outstanding table for a review of
// the given design round, tolerating rounds without a prior adjudication.
func (o *Orchestrator) outstandingChangesForReview(ctx context.Context, all []TaskRecord, rootID int64, round int) ([]RequiredChange, error) {
	return o.outstandingRevisionChanges(ctx, all, rootID, round)
}

var _ = strconv.Itoa
