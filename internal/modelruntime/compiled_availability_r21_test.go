package modelruntime

import "testing"

func TestR21CompiledAvailabilityRetiresEveryAlibabaRoute(t *testing.T) {
	plan := RegistryPlan{
		Providers: []Provider{
			{ID: alibabaTokenPlanProviderID, Transport: TransportCLI, DirectHTTPForbidden: true, AdapterStatus: AdapterAvailable, DispatchEnabled: true},
			{ID: "other_cli", Transport: TransportCLI, DirectHTTPForbidden: true, AdapterStatus: AdapterUnavailable},
		},
		Versions: []ProfileVersion{
			{ProfileID: "ceo-primary", ProviderID: alibabaTokenPlanProviderID, Transport: TransportCLI, AdapterStatus: AdapterAvailable, DispatchEnabled: true},
			{ProfileID: "executive.observer", ProviderID: alibabaTokenPlanProviderID, Transport: TransportCLI, AdapterStatus: AdapterAvailable, DispatchEnabled: true},
			{ProfileID: "research.audit", ProviderID: alibabaTokenPlanProviderID, Transport: TransportCLI, AdapterStatus: AdapterAvailable, DispatchEnabled: true},
			{ProfileID: "other", ProviderID: "other_cli", Transport: TransportCLI, AdapterStatus: AdapterUnavailable},
		},
	}
	got := applyR21CompiledAvailability(plan)
	if got.Providers[0].AdapterStatus != AdapterUnavailable || got.Providers[0].DispatchEnabled {
		t.Fatalf("retired Alibaba provider remained enabled: %+v", got.Providers[0])
	}
	for _, index := range []int{0, 1, 2, 3} {
		if got.Versions[index].AdapterStatus != AdapterUnavailable || got.Versions[index].DispatchEnabled {
			t.Fatalf("CLI version unexpectedly enabled: %+v", got.Versions[index])
		}
	}
	if got.Providers[1].AdapterStatus != AdapterUnavailable || got.Providers[1].DispatchEnabled {
		t.Fatalf("unknown CLI provider unexpectedly enabled: %+v", got.Providers[1])
	}
}

func TestR21RetirementDoesNotDependOnLegacyDirectHTTPFlag(t *testing.T) {
	plan := RegistryPlan{Providers: []Provider{{ID: alibabaTokenPlanProviderID, Transport: TransportCLI, DirectHTTPForbidden: false, AdapterStatus: AdapterAvailable, DispatchEnabled: true}}}
	got := applyR21CompiledAvailability(plan)
	if got.Providers[0].AdapterStatus != AdapterUnavailable || got.Providers[0].DispatchEnabled {
		t.Fatalf("retired Alibaba provider was re-enabled: %+v", got.Providers[0])
	}
}
