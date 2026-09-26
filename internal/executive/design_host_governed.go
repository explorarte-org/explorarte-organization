package executive

import "fmt"

// Requirements the host enforces are not design content.
//
// Roots 1223, 1351 and 1382 (2026-09-24 to 2026-09-26) each spent a design round, and 1382 its
// department's only replan, on demands that no design can satisfy or break: restate the budget
// guidance addressed to Finance, restate that the campaign uses one department, restate who implements
// the change. They reached the adjudicator through campaign_target, which is the owner's whole goal,
// and through the rule that everything the target specifies must appear in the candidate. In root 1382
// the adjudicator rejected the only adversarial finding and still returned revise with two such
// demands; one was never answered, the department review asked for a replan, and the run stopped.
//
// Budget, department count, the implementing actor and deployment are enforced by the host
// (Finance review and owner approval, the executive plan, the engineering mission, the absence of any
// deploy path from a design). A design that omits them omits nothing; one that contradicts them is
// still wrong, and that stays a finding.
const hostGovernedRequirementsConstraint = "Some requirements in campaign_target are enforced by the host, not by the design: the budget and any guidance addressed to Finance, " +
	"how many departments take part, which actor implements the change and when (the engineering mission, after design freeze), and deployment or production access. " +
	"The candidate need not restate them: their absence is never a finding and never a required change, and no design statement can change them. " +
	"A candidate that contradicts one (it plans to deploy, to touch production, or to have department workers edit files) is still a finding."

// AssertReviseRestsOnTheReview refuses a revise that answers nothing in the adversarial review.
//
// The adjudicator rules on the review. A revise that rejects every finding and asks for no evidence
// has decided the review raised nothing that stands, and then sends the design back anyway, for
// demands of its own that no reviewer raised and no finding records (root 1382, round 1). The design
// that survived its review settles: the adjudicator freezes it, or rejects it outright.
func AssertReviseRestsOnTheReview(adjudication DesignAdjudication) error {
	if adjudication.Verdict != AdjudicationRevise || len(adjudication.AcceptedFindings) > 0 || len(adjudication.EvidenceRequirements) > 0 {
		return nil
	}
	return fmt.Errorf("%w: this revise rejects every finding of the adversarial review and asks for no evidence, so none of its required changes "+
		"answers the review; accept the finding each required change answers, or, if no finding stands, return verdict freeze (or reject). "+
		"Requirements the host enforces (budget and Finance guidance, how many departments take part, who implements the change and when, "+
		"deployment) are never required changes", ErrContractRejected)
}
