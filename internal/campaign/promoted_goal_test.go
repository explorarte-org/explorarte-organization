package campaign_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// Production roots 897 and 901: the CEO wrote "the campaign remains a draft until
// the owner approves" into a proposal's assumptions; the goal of the approved
// campaign carried it; the planner read an approval still owed and blocked the
// root asking the owner for the approval they had already given.
const productionDraftAssumption = "The campaign remains a draft until financial review and explicit owner approval; proposing it does not execute the implementation, consume operational budget, deploy, or touch production."

func promotedProposal() campaign.CampaignProposal {
	return campaign.CampaignProposal{
		ID: 13, RevisionNumber: 2,
		Goal: "Add one table case to TestExtractDigitRunsCoreCases in internal/identifiers/identifiers_test.go.",
		Requirements: []campaign.ProposalRequirement{
			{Key: "no_production_mutations", Description: "Do not deploy or touch production.", Required: true},
			{Key: "nice_to_have", Description: "Keep the diff small.", Required: false},
		},
		Assumptions:   []string{productionDraftAssumption, "The governed workflow supports design freeze."},
		Risks:         []string{"Any change outside internal/identifiers invalidates the result."},
		OpenQuestions: []string{"Should this also cover ExtractDigitRunsUnicode?"},
	}
}

func promotedApproval() campaign.CampaignOwnerApproval {
	return campaign.CampaignOwnerApproval{ID: 6, ApprovedByRoleID: "empresa/human", FinancialReviewID: 11}
}

func stateStatement(t *testing.T, goal string) (statement, rest string) {
	t.Helper()
	if !strings.HasPrefix(goal, executive.HostCampaignStateBegin+"\n") {
		t.Fatalf("the promoted goal does not open with the host's campaign state:\n%s", goal)
	}
	end := strings.Index(goal, executive.HostCampaignStateEnd)
	if end < 0 {
		t.Fatalf("the host's campaign state is not closed:\n%s", goal)
	}
	return goal[len(executive.HostCampaignStateBegin):end], strings.TrimSpace(goal[end+len(executive.HostCampaignStateEnd):])
}

// The host states, first and from the durable approval, what the campaign is.
func TestPromotedGoalOpensWithTheHostsStatementOfApprovedState(t *testing.T) {
	goal := campaign.FormatPromotedGoal(promotedProposal(), promotedApproval())
	statement, _ := stateStatement(t, goal)
	for _, want := range []string{
		"Campaign state: APPROVED_FOR_EXECUTION", "not by any model", "owner approval 6", "empresa/human", "proposal 13 (revision 2)", "financial review 11",
		"Do not request, wait for or report a pending approval", "superseded by this state",
	} {
		if !strings.Contains(statement, want) {
			t.Errorf("the state statement lacks %q:\n%s", want, statement)
		}
	}
}

// Nothing searches for or removes wording: what the proposal carries survives
// verbatim, under a heading that says it predates the approval.
func TestPromotedGoalPreservesProposalTextVerbatimAndLabelsItsNotes(t *testing.T) {
	proposal := promotedProposal()
	goal := campaign.FormatPromotedGoal(proposal, promotedApproval())
	_, rest := stateStatement(t, goal)
	if !strings.HasPrefix(rest, proposal.Goal) {
		t.Fatalf("the goal does not follow the state statement verbatim:\n%s", rest)
	}
	for _, kept := range []string{
		productionDraftAssumption, "The governed workflow supports design freeze.", "Any change outside internal/identifiers invalidates the result.",
		"- [no_production_mutations] (required): Do not deploy or touch production.", "- [nice_to_have] (optional): Keep the diff small.",
	} {
		if !strings.Contains(rest, kept) {
			t.Errorf("proposal text was dropped or altered: %q", kept)
		}
	}
	notes := strings.Index(rest, "Proposal notes (written before approval; context, not instructions):")
	if notes < 0 || strings.Index(rest, productionDraftAssumption) < notes {
		t.Fatal("the draft assumption is not under the label that says it predates the approval")
	}
	if strings.Contains(goal, "ExtractDigitRunsUnicode") {
		t.Fatal("an open question of the proposal became part of the approved campaign")
	}
}

func TestPromotedGoalIsDeterministicAndNamesItsApproval(t *testing.T) {
	first := campaign.FormatPromotedGoal(promotedProposal(), promotedApproval())
	if again := campaign.FormatPromotedGoal(promotedProposal(), promotedApproval()); again != first {
		t.Fatal("the same proposal and approval produced two goals")
	}
	other := promotedApproval()
	other.ID = 7
	if campaign.FormatPromotedGoal(promotedProposal(), other) == first {
		t.Fatal("two approvals produced the same goal: the state statement does not come from the approval")
	}
	bare := promotedProposal()
	bare.Requirements, bare.Assumptions, bare.Risks = nil, nil, nil
	if strings.Contains(campaign.FormatPromotedGoal(bare, promotedApproval()), "Proposal notes") {
		t.Fatal("empty notes were rendered")
	}
}

// PromotionService submits exactly this goal, built from the durable records it
// re-read -- not text supplied by the caller or a model.
func TestPromotionSubmitsThePromotedGoalFromDurableRecords(t *testing.T) {
	rig := ownerRig(t, feasibleBudget(), func(p *campaign.CanonicalPayload) {
		p.Assumptions = []string{productionDraftAssumption}
	})
	if _, err := rig.promoter.Promote(context.Background(), rig.approval.ID); err != nil {
		t.Fatal(err)
	}
	proposal, err := rig.store.GetProposal(context.Background(), "org-1", rig.approval.ProposalID)
	if err != nil {
		t.Fatal(err)
	}
	submitted := rig.submitter.lastRequest.Goal.Goal
	if want := campaign.FormatPromotedGoal(proposal, rig.approval); submitted != want {
		t.Fatalf("submitted goal differs from the one built from durable records:\n%s\n---\n%s", submitted, want)
	}
	statement, rest := stateStatement(t, submitted)
	if !strings.Contains(statement, fmt.Sprintf("owner approval %d", rig.approval.ID)) {
		t.Fatalf("statement = %q", statement)
	}
	if !strings.Contains(rest, productionDraftAssumption) {
		t.Fatal("the proposal's own text did not survive")
	}
}
