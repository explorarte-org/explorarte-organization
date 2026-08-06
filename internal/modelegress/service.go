package modelegress

import (
	"context"
	"fmt"
	"sort"
)

type Service struct {
	canonicalDir   string
	organizationID string
	organizations  OrganizationCatalog
	providers      ProviderCatalog
	store          RegistryStore
}

func NewService(canonicalDir, organizationID string, organizations OrganizationCatalog, providers ProviderCatalog, store RegistryStore) (*Service, error) {
	if organizations == nil || providers == nil || store == nil {
		return nil, fmt.Errorf("model egress registry dependencies are incomplete")
	}
	return &Service{canonicalDir: canonicalDir, organizationID: organizationID, organizations: organizations, providers: providers, store: store}, nil
}

func (s *Service) load(ctx context.Context) (RegistryPlan, error) {
	organization, err := s.organizations.CurrentOrganization(ctx, s.organizationID)
	if err != nil {
		return RegistryPlan{}, err
	}
	providers, err := s.providers.ProviderIDs(s.canonicalDir)
	if err != nil {
		return RegistryPlan{}, err
	}
	sort.Strings(providers)
	policy, err := LoadCanonicalPolicy(s.canonicalDir, LoadOptions{KnownProviders: providers})
	if err != nil {
		return RegistryPlan{}, err
	}
	if organization.PolicyDocumentHash == "" || organization.PolicyDocumentHash != policy.CanonicalHash {
		return RegistryPlan{}, fmt.Errorf("%w: organization revision policy hash does not match canonical policy", ErrPolicyStale)
	}
	return RegistryPlan{OrganizationID: organization.ID, OrganizationRevisionID: organization.RevisionID, CanonicalHash: policy.CanonicalHash, Policy: policy}, nil
}

func (s *Service) Validate(ctx context.Context) (CanonicalPolicy, error) {
	plan, err := s.load(ctx)
	if err != nil {
		return CanonicalPolicy{}, err
	}
	if err = s.store.RecordValidated(ctx, plan.OrganizationID, plan.OrganizationRevisionID, plan.CanonicalHash); err != nil {
		return CanonicalPolicy{}, err
	}
	return plan.Policy, nil
}

func (s *Service) Diff(ctx context.Context) (RegistryDiff, error) {
	plan, err := s.load(ctx)
	if err != nil {
		return RegistryDiff{}, err
	}
	return s.store.Status(ctx, plan)
}

func (s *Service) Status(ctx context.Context) (RegistryStatus, error) {
	plan, err := s.load(ctx)
	if err != nil {
		return RegistryStatus{}, err
	}
	return s.store.Status(ctx, plan)
}

func (s *Service) Sync(ctx context.Context, apply bool) (RegistrySyncResult, error) {
	plan, err := s.load(ctx)
	if err != nil {
		return RegistrySyncResult{}, err
	}
	status, err := s.store.Status(ctx, plan)
	if err != nil {
		return RegistrySyncResult{}, err
	}
	if status.Synchronized {
		return RegistrySyncResult{NoOp: true, OrganizationRevisionID: plan.OrganizationRevisionID, PolicyVersionID: status.PolicyVersionID, PolicyID: plan.Policy.PolicyID, PolicyVersion: plan.Policy.PolicyVersion, CanonicalHash: plan.CanonicalHash, Rules: len(plan.Policy.Rules) + len(plan.Policy.HardDenies)}, nil
	}
	if !apply {
		return RegistrySyncResult{OrganizationRevisionID: plan.OrganizationRevisionID, PolicyID: plan.Policy.PolicyID, PolicyVersion: plan.Policy.PolicyVersion, CanonicalHash: plan.CanonicalHash, Rules: len(plan.Policy.Rules) + len(plan.Policy.HardDenies)}, nil
	}
	return s.store.Apply(ctx, plan)
}
