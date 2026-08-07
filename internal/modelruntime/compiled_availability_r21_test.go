package modelruntime

import "testing"

func TestR21CompiledAvailabilityEnablesProviderAndCEOOnly(t *testing.T) {
	plan := RegistryPlan{
		Providers: []Provider{
			{ID: alibabaTokenPlanProviderID, Transport: TransportCLI, DirectHTTPForbidden: true, AdapterStatus: AdapterUnavailable},
			{ID: "other_cli", Transport: TransportCLI, DirectHTTPForbidden: true, AdapterStatus: AdapterUnavailable},
		},
		Versions: []ProfileVersion{
			{ProfileID: r21CEOProfileID, ProviderID: alibabaTokenPlanProviderID, Transport: TransportCLI, AdapterStatus: AdapterUnavailable},
			{ProfileID: "executive.observer", ProviderID: alibabaTokenPlanProviderID, Transport: TransportCLI, AdapterStatus: AdapterUnavailable},
			{ProfileID: "research.audit", ProviderID: alibabaTokenPlanProviderID, Transport: TransportCLI, AdapterStatus: AdapterUnavailable},
			{ProfileID: "other", ProviderID: "other_cli", Transport: TransportCLI, AdapterStatus: AdapterUnavailable},
		},
	}
	got := applyR21CompiledAvailability(plan)
	if got.Providers[0].AdapterStatus != AdapterAvailable || !got.Providers[0].DispatchEnabled {
		t.Fatalf("Alibaba provider not enabled: %+v", got.Providers[0])
	}
	if got.Versions[0].AdapterStatus != AdapterAvailable || !got.Versions[0].DispatchEnabled {
		t.Fatalf("CEO version not enabled: %+v", got.Versions[0])
	}
	for _, index := range []int{1, 2, 3} {
		if got.Versions[index].AdapterStatus != AdapterUnavailable || got.Versions[index].DispatchEnabled {
			t.Fatalf("non-CEO CLI version unexpectedly enabled: %+v", got.Versions[index])
		}
	}
	if got.Providers[1].AdapterStatus != AdapterUnavailable || got.Providers[1].DispatchEnabled {
		t.Fatalf("unknown CLI provider unexpectedly enabled: %+v", got.Providers[1])
	}
}

func TestR21AvailabilityRequiresDirectHTTPForbiddenOnProvider(t *testing.T) {
	plan := RegistryPlan{Providers: []Provider{{ID: alibabaTokenPlanProviderID, Transport: TransportCLI, DirectHTTPForbidden: false, AdapterStatus: AdapterUnavailable}}}
	got := applyR21CompiledAvailability(plan)
	if got.Providers[0].AdapterStatus != AdapterUnavailable || got.Providers[0].DispatchEnabled {
		t.Fatalf("Alibaba provider enabled without direct_http_forbidden: %+v", got.Providers[0])
	}
}
