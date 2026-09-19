package campaign_test

import "github.com/Mireuz13/explorarte-organization/internal/campaign"

// permissiveRequirements is a floor every fixture budget clears, for tests that
// are not about feasibility.
func permissiveRequirements() campaign.ExecutionRequirementsProvider {
	return campaign.FixedExecutionRequirements{Requirements: campaign.ExecutionBudgetRequirements{
		MinUSD: 1, MinTokens: 1, MinModelCalls: 1, MinWallTimeMS: 1, MinDepth: 1, MinRetries: 1, MinSubagents: 1,
	}}
}
