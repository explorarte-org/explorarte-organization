package ceochat

import "github.com/Mireuz13/explorarte-organization/internal/campaign"

// permissiveExecutionRequirements is a floor every fixture budget clears, for
// tests that are not about execution-budget feasibility.
func permissiveExecutionRequirements() campaign.ExecutionRequirementsProvider {
	return campaign.FixedExecutionRequirements{Requirements: campaign.ExecutionBudgetRequirements{
		MinUSD: 1, MinTokens: 1, MinModelCalls: 1, MinWallTimeMS: 1, MinDepth: 1, MinRetries: 1, MinSubagents: 1,
	}}
}
