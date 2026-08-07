package modelruntime

const alibabaTokenPlanProviderID = "alibaba_token_plan_via_claude_code"

// applyR21CompiledAvailability decorates a canonical registry plan with
// adapters that are compiled by branches newer than the original routing
// parser. RegistryService is the productive entry point for every
// validate/plan/diff/sync/status operation, so persisted availability is
// always derived here before it reaches PostgreSQL.
func applyR21CompiledAvailability(plan RegistryPlan) RegistryPlan {
	for i := range plan.Providers {
		provider := &plan.Providers[i]
		if provider.ID == alibabaTokenPlanProviderID && provider.Transport == TransportCLI && provider.DirectHTTPForbidden {
			provider.AdapterStatus = AdapterAvailable
			provider.DispatchEnabled = true
		}
	}
	for i := range plan.Versions {
		version := &plan.Versions[i]
		if version.ProviderID == alibabaTokenPlanProviderID && version.Transport == TransportCLI {
			version.AdapterStatus = AdapterAvailable
			version.DispatchEnabled = true
		}
	}
	return plan
}
