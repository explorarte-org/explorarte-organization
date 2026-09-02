package modelruntime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// fakeWalletChecker is a deterministic ProviderWalletProvisionChecker
// double: provisioned names every provider_id a real provider_wallets row
// would exist for.
type fakeWalletChecker struct {
	provisioned map[string]bool
	err         error
}

func (f fakeWalletChecker) ProvisionedProviderIDs(context.Context) (map[string]bool, error) {
	return f.provisioned, f.err
}

// TestSyncFlagsAProviderWithNoWalletRow proves G2-001's fix: a provider
// bound to an enabled role but missing entirely from provider_wallets is
// named in RegistrySyncResult.MissingProviderWallets, distinctly from
// Sync's other, unrelated fields -- without SetWalletChecker ever being
// called, Sync's behavior is unchanged (nil slice, no error).
func TestSyncFlagsAProviderWithNoWalletRow(t *testing.T) {
	canonicalDir := filepath.Join("..", "..", "docs", "canonical")
	catalog := fakeCatalog{
		org:  OrganizationRef{ID: "explorarte", RevisionID: 7},
		role: RoleRef{ID: "ingenieria_ia/orquestador", ModelPolicy: "department.leader", Enabled: true, Executable: true},
	}
	store := &fakeStore{}

	service, err := NewRegistryService(canonicalDir, "explorarte", catalog, store)
	if err != nil {
		t.Fatal(err)
	}

	// Before SetWalletChecker: identical to Sync's behavior before G2-001.
	result, err := service.Sync(t.Context(), true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.MissingProviderWallets != nil {
		t.Fatalf("no checker wired: expected nil MissingProviderWallets, got %v", result.MissingProviderWallets)
	}

	// department.leader routes to gemini (docs/canonical/model-routing.yaml)
	// but this fake checker has never heard of it -- exactly the xai
	// scenario this finding is named after (a real provider, priced and
	// routed, with zero provider_wallets rows).
	service.SetWalletChecker(fakeWalletChecker{provisioned: map[string]bool{}})
	result, err = service.Sync(t.Context(), true, 10)
	if err != nil {
		t.Fatal(err)
	}
	// plan.Providers reflects every provider docs/canonical/model-routing.yaml
	// names across ALL routing policies (executive.ceo, department.leader,
	// department.worker, research.*) -- not merely the one role this fixture
	// binds -- so an empty wallet checker flags every one of them, a superset
	// of this finding's own minimum bar ("bound to at least one enabled
	// role"), never a subset.
	wantMissing := map[string]bool{"gemini": true, "xai": true, "deepseek": true, "openai_compatible": true, "openai_responses": true}
	if len(result.MissingProviderWallets) != len(wantMissing) {
		t.Fatalf("expected %d missing providers, got %v", len(wantMissing), result.MissingProviderWallets)
	}
	for _, id := range result.MissingProviderWallets {
		if !wantMissing[id] {
			t.Fatalf("unexpected provider %q reported missing: %v", id, result.MissingProviderWallets)
		}
	}

	// A funded wallet for every dispatch-enabled provider in the plan
	// clears the flag entirely.
	service.SetWalletChecker(fakeWalletChecker{provisioned: map[string]bool{"gemini": true, "openai_responses": true, "xai": true, "deepseek": true, "openai_compatible": true}})
	result, err = service.Sync(t.Context(), true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.MissingProviderWallets) != 0 {
		t.Fatalf("expected no missing wallets once every provider is funded, got %v", result.MissingProviderWallets)
	}
}

// TestSyncPropagatesWalletCheckerFailure proves a wallet-check transport
// error fails Sync loudly (never silently treated as "nothing missing") --
// mirroring how every other Sync dependency failure (Plan, RegistryStatus)
// already propagates.
func TestSyncPropagatesWalletCheckerFailure(t *testing.T) {
	canonicalDir := filepath.Join("..", "..", "docs", "canonical")
	catalog := fakeCatalog{
		org:  OrganizationRef{ID: "explorarte", RevisionID: 7},
		role: RoleRef{ID: "ingenieria_ia/orquestador", ModelPolicy: "department.leader", Enabled: true, Executable: true},
	}
	service, err := NewRegistryService(canonicalDir, "explorarte", catalog, &fakeStore{})
	if err != nil {
		t.Fatal(err)
	}
	service.SetWalletChecker(fakeWalletChecker{err: errors.New("wallet check transport failure")})

	if _, err := service.Sync(t.Context(), true, 10); err == nil {
		t.Fatal("expected Sync to fail when the wallet checker itself fails")
	}
}
