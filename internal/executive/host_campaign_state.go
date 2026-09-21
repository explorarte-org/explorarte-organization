package executive

import "strings"

// A promoted campaign's goal opens with a statement the HOST wrote from durable
// records -- that the campaign is approved and why -- delimited by these markers.
// It exists for the planners: a proposal is drafted before its approval, and a
// model that drafted it writes "draft until approved" into it; without the host's
// statement a planner reads that as an approval still owed, and blocks the root
// asking the owner for the approval they already gave.
//
// The statement is not searched for. It is recognised by exact markers the
// campaign builder places around it, so nothing here interprets free text: a
// goal the owner really wrote about a "draft" is never touched.
const (
	HostCampaignStateBegin = "[host-campaign-state]"
	HostCampaignStateEnd   = "[/host-campaign-state]"
)

// WrapHostCampaignState delimits a host-authored statement of campaign state.
func WrapHostCampaignState(statement string) string {
	return HostCampaignStateBegin + "\n" + strings.TrimSpace(statement) + "\n" + HostCampaignStateEnd
}

// withoutHostCampaignState removes the delimited statement, leaving what the
// campaign is about. The statement is the same shape in every campaign, so it
// says nothing about what THIS design should look at; left in the retrieval
// query it would compete for the search budget with the symbols the goal names
// (the same damage host guidance did, see withoutHostGuidance).
func withoutHostCampaignState(instructions string) string {
	for {
		start := strings.Index(instructions, HostCampaignStateBegin)
		if start < 0 {
			return strings.TrimSpace(instructions)
		}
		end := strings.Index(instructions[start:], HostCampaignStateEnd)
		if end < 0 {
			// An unterminated marker removes nothing: never truncate a goal on
			// a marker that was not closed.
			return strings.TrimSpace(instructions)
		}
		instructions = instructions[:start] + instructions[start+end+len(HostCampaignStateEnd):]
	}
}
