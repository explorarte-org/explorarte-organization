package modelruntime

const (
	alibabaTokenPlanProviderID = "alibaba_token_plan_via_claude_code"
)

// applyR21CompiledAvailability now acts as a retirement barrier for the legacy
// Alibaba Token Plan route. The adapter package remains in the repository for
// historical compatibility, but productive plans must never make its provider
// or profile versions dispatchable again.
func applyR21CompiledAvailability(plan RegistryPlan) RegistryPlan {
	for i := range plan.Providers {
		provider := &plan.Providers[i]
		if provider.ID == alibabaTokenPlanProviderID {
			provider.AdapterStatus = AdapterUnavailable
			provider.DispatchEnabled = false
		}
	}
	for i := range plan.Versions {
		version := &plan.Versions[i]
		if version.ProviderID == alibabaTokenPlanProviderID {
			version.AdapterStatus = AdapterUnavailable
			version.DispatchEnabled = false
		}
	}
	return plan
}
