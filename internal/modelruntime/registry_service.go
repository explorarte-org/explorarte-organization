package modelruntime

import (
	"context"
	"fmt"
)

// ProviderWalletProvisionChecker is an OPTIONAL capability
// RegistryService may use to confirm every provider a sync is about to
// make dispatchable already has a funded provider_wallets row (G2-001),
// without RegistryService importing internal/costledger directly --
// mirroring modelruntime.ContextSnapshotSelectorReader's own
// optional-capability shape (internal/modelruntime/postgres owning an analogous
// pattern for its own context-snapshot table).
type ProviderWalletProvisionChecker interface {
	ProvisionedProviderIDs(ctx context.Context) (map[string]bool, error)
}

type RegistryService struct {
	canonicalDir   string
	organizationID string
	catalog        OrganizationCatalog
	store          RegistryStore
	// walletChecker is nil by default -- Sync behaves exactly as before
	// (no provisioning check performed) until SetWalletChecker wires one.
	walletChecker ProviderWalletProvisionChecker
}

// SetWalletChecker wires the OPTIONAL provisioning-consistency check
// Sync performs (G2-001). A setter, not a constructor parameter, for the
// same reason internal/modelruntime/postgres.Store.SetContextSnapshotReader
// is one: most callers of NewRegistryService never call Sync in a context
// where this matters and have no reason to construct or thread a
// costledger reader through just to satisfy an unused parameter.
func (s *RegistryService) SetWalletChecker(checker ProviderWalletProvisionChecker) {
	s.walletChecker = checker
}

func NewRegistryService(canonicalDir, organizationID string, catalog OrganizationCatalog, store RegistryStore) (*RegistryService, error) {
	if catalog == nil {
		return nil, fmt.Errorf("registry service requires organization catalog")
	}
	if store == nil {
		return nil, fmt.Errorf("registry service requires store")
	}
	return &RegistryService{canonicalDir: canonicalDir, organizationID: organizationID, catalog: catalog, store: store}, nil
}
func (s *RegistryService) Validate(ctx context.Context) (CanonicalRouting, error) {
	routing, err := LoadCanonicalRouting(s.canonicalDir)
	if err != nil {
		return CanonicalRouting{}, err
	}
	org, err := s.catalog.CurrentOrganization(ctx, s.organizationID)
	if err != nil {
		return CanonicalRouting{}, err
	}
	roles, err := s.catalog.ListRoles(ctx, s.organizationID)
	if err != nil {
		return CanonicalRouting{}, err
	}
	plan, err := BuildRegistryPlan(roles, org, routing)
	if err != nil {
		return CanonicalRouting{}, err
	}
	_ = applyR21CompiledAvailability(plan)
	if err = s.store.RecordRegistryValidated(ctx, org.ID, routing.Hash); err != nil {
		return CanonicalRouting{}, err
	}
	return routing, nil
}
func (s *RegistryService) Plan(ctx context.Context) (RegistryPlan, error) {
	routing, err := LoadCanonicalRouting(s.canonicalDir)
	if err != nil {
		return RegistryPlan{}, err
	}
	org, err := s.catalog.CurrentOrganization(ctx, s.organizationID)
	if err != nil {
		return RegistryPlan{}, err
	}
	roles, err := s.catalog.ListRoles(ctx, s.organizationID)
	if err != nil {
		return RegistryPlan{}, err
	}
	plan, err := BuildRegistryPlan(roles, org, routing)
	if err != nil {
		return RegistryPlan{}, err
	}
	return applyR21CompiledAvailability(plan), nil
}
func (s *RegistryService) Diff(ctx context.Context) (RegistryDiff, error) {
	plan, err := s.Plan(ctx)
	if err != nil {
		return RegistryDiff{}, err
	}
	status, err := s.store.RegistryStatus(ctx, plan.OrganizationID, plan.OrganizationRevisionID, plan.CanonicalHash)
	if err != nil {
		return RegistryDiff{}, err
	}
	synchronized := status.Synchronized && status.Providers == len(plan.Providers) && status.Profiles == len(plan.Profiles) && status.ProfileVersions == len(plan.Versions) && status.Bindings == len(plan.Bindings)
	return RegistryDiff{CanonicalHash: plan.CanonicalHash, MaterializedHash: status.MaterializedHash, Synchronized: synchronized, Providers: len(plan.Providers), Profiles: len(plan.Profiles), Versions: len(plan.Versions), Bindings: len(plan.Bindings)}, nil
}
func (s *RegistryService) Sync(ctx context.Context, apply bool, outboxMax int) (RegistrySyncResult, error) {
	plan, err := s.Plan(ctx)
	if err != nil {
		return RegistrySyncResult{}, err
	}
	status, err := s.store.RegistryStatus(ctx, plan.OrganizationID, plan.OrganizationRevisionID, plan.CanonicalHash)
	if err != nil {
		return RegistrySyncResult{}, err
	}
	missingWallets, err := s.missingProviderWallets(ctx, plan)
	if err != nil {
		return RegistrySyncResult{}, err
	}
	if status.Synchronized && status.Providers == len(plan.Providers) && status.Profiles == len(plan.Profiles) && status.ProfileVersions == len(plan.Versions) && status.Bindings == len(plan.Bindings) {
		return RegistrySyncResult{NoOp: true, CanonicalHash: plan.CanonicalHash, OrganizationRevisionID: plan.OrganizationRevisionID, Providers: len(plan.Providers), Profiles: len(plan.Profiles), Versions: len(plan.Versions), Bindings: len(plan.Bindings), MissingProviderWallets: missingWallets}, nil
	}
	if !apply {
		return RegistrySyncResult{CanonicalHash: plan.CanonicalHash, OrganizationRevisionID: plan.OrganizationRevisionID, Providers: len(plan.Providers), Profiles: len(plan.Profiles), Versions: len(plan.Versions), Bindings: len(plan.Bindings), MissingProviderWallets: missingWallets}, nil
	}
	result, err := s.store.ApplyRegistry(ctx, plan, outboxMax)
	if err != nil {
		return RegistrySyncResult{}, err
	}
	result.MissingProviderWallets = missingWallets
	return result, nil
}

// missingProviderWallets names every DISTINCT, dispatch-enabled provider
// in plan.Providers that has no provider_wallets row at all (G2-001).
// Returns nil (not an error, not a mutation) when no checker is wired --
// Sync's prior behavior is preserved exactly until SetWalletChecker is
// called. This is read-only validation: it never writes a wallet row
// itself (that would just move the same silent failure one step later,
// per this finding's own DO_NOT_FIX_WITH) and never touches
// organization_revision_id.
func (s *RegistryService) missingProviderWallets(ctx context.Context, plan RegistryPlan) ([]string, error) {
	if s.walletChecker == nil {
		return nil, nil
	}
	provisioned, err := s.walletChecker.ProvisionedProviderIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("check provider wallet provisioning: %w", err)
	}
	seen := make(map[string]bool, len(plan.Providers))
	var missing []string
	for _, provider := range plan.Providers {
		if !provider.DispatchEnabled || seen[provider.ID] {
			continue
		}
		seen[provider.ID] = true
		if !provisioned[provider.ID] {
			missing = append(missing, provider.ID)
		}
	}
	return missing, nil
}
func (s *RegistryService) Status(ctx context.Context) (RegistryStatus, error) {
	plan, err := s.Plan(ctx)
	if err != nil {
		return RegistryStatus{}, err
	}
	status, err := s.store.RegistryStatus(ctx, s.organizationID, plan.OrganizationRevisionID, plan.CanonicalHash)
	if err != nil {
		return RegistryStatus{}, err
	}
	status.Synchronized = status.Synchronized && status.Providers == len(plan.Providers) && status.Profiles == len(plan.Profiles) && status.ProfileVersions == len(plan.Versions) && status.Bindings == len(plan.Bindings)
	return status, nil
}
