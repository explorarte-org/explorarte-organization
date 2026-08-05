package modelruntime

import (
	"context"
	"fmt"
)

type RegistryService struct {
	canonicalDir   string
	organizationID string
	catalog        OrganizationCatalog
	store          RegistryStore
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
	_, err = BuildRegistryPlan(roles, org, routing)
	if err != nil {
		return CanonicalRouting{}, err
	}
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
	return BuildRegistryPlan(roles, org, routing)
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
	if status.Synchronized && status.Providers == len(plan.Providers) && status.Profiles == len(plan.Profiles) && status.ProfileVersions == len(plan.Versions) && status.Bindings == len(plan.Bindings) {
		return RegistrySyncResult{NoOp: true, CanonicalHash: plan.CanonicalHash, OrganizationRevisionID: plan.OrganizationRevisionID, Providers: len(plan.Providers), Profiles: len(plan.Profiles), Versions: len(plan.Versions), Bindings: len(plan.Bindings)}, nil
	}
	if !apply {
		return RegistrySyncResult{CanonicalHash: plan.CanonicalHash, OrganizationRevisionID: plan.OrganizationRevisionID, Providers: len(plan.Providers), Profiles: len(plan.Profiles), Versions: len(plan.Versions), Bindings: len(plan.Bindings)}, nil
	}
	return s.store.ApplyRegistry(ctx, plan, outboxMax)
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
