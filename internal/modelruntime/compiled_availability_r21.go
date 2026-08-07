package modelruntime

const (
	alibabaTokenPlanProviderID = "alibaba_token_plan_via_claude_code"
	r21CEOProfileID            = "ceo-primary"
)

// applyR21CompiledAvailability decorates a canonical registry plan with the
// Alibaba CLI adapter compiled by R21. Provider availability and profile
// dispatchability are deliberately separate: R21 makes the transport known to
// the binary, but only the owner-confirmed CEO profile becomes dispatchable.
// executive.observer and research.audit keep their canonical candidate status
// and remain unavailable until their own governance decisions are resolved.
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
		if version.ProfileID == r21CEOProfileID && version.ProviderID == alibabaTokenPlanProviderID && version.Transport == TransportCLI {
			version.AdapterStatus = AdapterAvailable
			version.DispatchEnabled = true
		}
	}
	return plan
}
