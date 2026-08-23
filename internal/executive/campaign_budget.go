package executive

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
)

// The ceilings that govern a campaign are decided once, at submission, and
// recorded durably with the campaign's root. They are a property of the
// campaign, not of whatever process happened to submit it.
//
// That was not true before. Submit created the root budget from limits the
// runtime had read out of its own environment, and CreateRootBudget is
// ON CONFLICT (task_id) DO NOTHING -- first writer wins, permanently. So a
// campaign submitted by a CLI whose environment lacked the ceilings was born
// with the package defaults and kept them, while the Executive runtime that
// went on to drive it had been configured with entirely different ones. The
// two processes did not disagree loudly; the campaign simply ran under
// whichever one got there first, and nothing in the system recorded that a
// choice had been made at all.
//
// Two representations of one concept is the defect. The fix is not to make
// both processes read the same environment -- that is the same defect with a
// deployment note attached. It is to leave exactly one: the stated budget,
// carried in the submission, resolved here, and written to the durable row
// that every later decision already reads.

// CampaignBudget is the owner's statement of what a campaign may spend.
//
// It is agentbudget.Limits rather than a parallel struct of the same seven
// numbers. The executive package usually keeps its own port-level types and
// lets adapters map (TaskRecord and tasks.Task are separate on purpose), but
// that convention earns its keep where the two shapes genuinely differ. Here
// they do not: these ARE the ledger's ceilings, and a mapped copy would mean
// that adding one dimension requires editing two structs that express exactly
// the same property.
type CampaignBudget = agentbudget.Limits

// DefaultCampaignBudget is what a campaign runs under when its submission
// states nothing.
//
// It is deliberately a documented constant of the domain rather than
// deployment configuration. A default read from the environment would put the
// divergence straight back: two processes, two environments, and whichever
// submitted first would silently decide. An unstated budget must mean the same
// thing everywhere, so it means this.
func DefaultCampaignBudget() CampaignBudget { return agentbudget.DefaultLimits() }

// resolveCampaignBudget answers one question -- what ceilings govern this
// campaign -- and is the only place that answers it.
//
// A stated budget is validated here rather than at the ledger, because a
// campaign born under an invalid ceiling is a campaign that cannot run, and
// the submission is the last point at which saying so is still useful.
func resolveCampaignBudget(stated *CampaignBudget) (CampaignBudget, error) {
	if stated == nil {
		return DefaultCampaignBudget(), nil
	}
	if err := stated.Validate(); err != nil {
		return CampaignBudget{}, fmt.Errorf("%w: campaign budget: %s", ErrInvalidInput, err)
	}
	return *stated, nil
}
