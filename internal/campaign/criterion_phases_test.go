package campaign_test

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// External audit A4: promotion used to give every owner criterion the implementation phase; in root
// 1848 a criterion about the design artifact went to the implementation phase. The phases the proposal
// declares reach Executive unchanged.
func TestPromotionCarriesTheDeclaredCriterionPhases(t *testing.T) {
	_, submitter, svc, _, _, approval := setupPromotionFixture(t, func(p *campaign.CanonicalPayload) {
		p.AcceptanceCriterionPhases = []string{"design", "implementation"}
	})
	if _, err := svc.PromoteToExecutive(context.Background(), campaign.PromoteToExecutiveParams{
		OrganizationID: "org-1", OwnerApprovalID: approval.ID, PromotedByRoleID: "empresa/human",
		ConversationID: 1, ToolCallID: "call-phases", IdempotencyKey: "prom-phases",
	}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	criteria := submitter.lastRequest.Goal.AcceptanceCriteria
	if len(criteria) != 3 || criteria[1].Phase != executive.AcceptanceDesign || criteria[2].Phase != executive.AcceptanceImplementation {
		t.Fatalf("submitted criteria %+v, want the host criterion, then design, then implementation", criteria)
	}
}

// A proposal made before phases existed keeps the hash it was approved under and the historical
// mapping.
func TestAProposalWithoutPhasesKeepsItsHashAndMapping(t *testing.T) {
	legacy := campaign.CanonicalPayload{Title: "T", Goal: "G", AcceptanceCriteria: []string{"A"}}
	withEmpty := legacy
	withEmpty.AcceptanceCriterionPhases = []string{}
	a, _ := campaign.ComputeCanonicalHash(legacy)
	b, _ := campaign.ComputeCanonicalHash(withEmpty)
	if a != b {
		t.Fatalf("a proposal without phases changed hash: %s vs %s", a, b)
	}
	withPhases := legacy
	withPhases.AcceptanceCriterionPhases = []string{"design"}
	if c, _ := campaign.ComputeCanonicalHash(withPhases); c == a {
		t.Fatal("declared phases are not part of the proposal's identity")
	}
}

func TestCriterionPhasesAreRequiredOnePerCriterion(t *testing.T) {
	criteria := []string{"a", "b"}
	for _, phases := range [][]string{nil, {"design"}, {"design", "later"}} {
		if err := campaign.ValidateCriterionPhases(criteria, phases); err == nil {
			t.Errorf("phases %v were accepted for %d criteria", phases, len(criteria))
		}
	}
	if err := campaign.ValidateCriterionPhases(criteria, []string{"design", "promotion"}); err != nil {
		t.Fatalf("valid phases refused: %v", err)
	}
}
