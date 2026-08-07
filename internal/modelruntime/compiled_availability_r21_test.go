package modelruntime

import "testing"

func TestR21CompiledAvailabilityEnablesOnlyCanonicalAlibabaCLI(t *testing.T) {
	plan := RegistryPlan{
		Providers: []Provider{
			{ID: alibabaTokenPlanProviderID, Transport: TransportCLI, DirectHTTPForbidden: true, AdapterStatus: AdapterUnavailable},
			{ID: "other_cli", Transport: TransportCLI, DirectHTTPForbidden: true, AdapterStatus: AdapterUnavailable},
		},
		Versions: []ProfileVersion{
			{ProviderID: alibabaTokenPlanProviderID, Transport: TransportCLI, AdapterStatus: AdapterUnavailable},
			{ProviderID: "other_cli", Transport: TransportCLI, AdapterStatus: AdapterUnavailable},
		},
	}
	got := applyR21CompiledAvailability(plan)
	if got.Providers[0].AdapterStatus != AdapterAvailable || !got.Providers[0].DispatchEnabled {
		t.Fatalf("Alibaba provider not enabled: %+v", got.Providers[0])
	}
	if got.Versions[0].AdapterStatus != AdapterAvailable || !got.Versions[0].DispatchEnabled {
		t.Fatalf("Alibaba version not enabled: %+v", got.Versions[0])
	}
	if got.Providers[1].AdapterStatus != AdapterUnavailable || got.Providers[1].DispatchEnabled {
		t.Fatalf("unknown CLI provider unexpectedly enabled: %+v", got.Providers[1])
	}
	if got.Versions[1].AdapterStatus != AdapterUnavailable || got.Versions[1].DispatchEnabled {
		t.Fatalf("unknown CLI version unexpectedly enabled: %+v", got.Versions[1])
	}
}

func TestR21AvailabilityRequiresDirectHTTPForbiddenOnProvider(t *testing.T) {
	plan := RegistryPlan{Providers: []Provider{{ID: alibabaTokenPlanProviderID, Transport: TransportCLI, DirectHTTPForbidden: false, AdapterStatus: AdapterUnavailable}}}
	got := applyR21CompiledAvailability(plan)
	if got.Providers[0].AdapterStatus != AdapterUnavailable || got.Providers[0].DispatchEnabled {
		t.Fatalf("Alibaba provider enabled without direct_http_forbidden: %+v", got.Providers[0])
	}
}
