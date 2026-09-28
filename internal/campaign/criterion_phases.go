package campaign

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// External audit A4 (2026-09-27): promotion classified every acceptance criterion of a proposal as
// implementation. In root 1848 "The design names exactly one defect..." -- a criterion about the design
// artifact -- was handed to the implementation phase, and the typed contract lost the phase the text
// still implied. The proposal now declares each criterion's phase, and promotion carries it unchanged:
// nothing after approval infers or moves a phase.

// ValidateCriterionPhases requires one valid phase per criterion, in order.
func ValidateCriterionPhases(criteria, phases []string) error {
	if len(phases) != len(criteria) {
		return fmt.Errorf("acceptance_criterion_phases must have one phase per acceptance criterion (%d criteria, %d phases)", len(criteria), len(phases))
	}
	for i, phase := range phases {
		switch executive.AcceptancePhase(phase) {
		case executive.AcceptanceDesign, executive.AcceptanceImplementation, executive.AcceptancePromotion:
		default:
			return fmt.Errorf("acceptance_criterion_phases[%d] is %q; it must be design, implementation or promotion", i, phase)
		}
	}
	return nil
}

// criterionPhase is the phase promotion gives criterion i: the declared one, or, for a proposal made
// before phases were declared, implementation, the historical mapping it was approved under.
func criterionPhase(proposal CampaignProposal, i int) executive.AcceptancePhase {
	if len(proposal.AcceptanceCriterionPhases) == len(proposal.AcceptanceCriteria) {
		return executive.AcceptancePhase(proposal.AcceptanceCriterionPhases[i])
	}
	return executive.AcceptanceImplementation
}
