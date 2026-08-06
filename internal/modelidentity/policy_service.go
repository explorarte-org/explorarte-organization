package modelidentity

import (
	"context"
	"fmt"
)

type PolicyService struct {
	canonicalDir   string
	organizationID string
	store          PolicyStore
}

func NewPolicyService(canonicalDir, organizationID string, store PolicyStore) (*PolicyService, error) {
	if store == nil {
		return nil, fmt.Errorf("model identity policy store is required")
	}
	return &PolicyService{canonicalDir: canonicalDir, organizationID: organizationID, store: store}, nil
}

func (s *PolicyService) Validate(context.Context) (CanonicalPolicy, error) {
	return LoadCanonicalPolicy(s.canonicalDir)
}

func (s *PolicyService) Status(ctx context.Context) (RegistryStatus, error) {
	policy, err := LoadCanonicalPolicy(s.canonicalDir)
	if err != nil {
		return RegistryStatus{}, err
	}
	return s.store.Status(ctx, s.organizationID, policy)
}

func (s *PolicyService) Diff(ctx context.Context) (RegistryStatus, error) { return s.Status(ctx) }

func (s *PolicyService) Sync(ctx context.Context, apply bool) (RegistrySyncResult, error) {
	policy, err := LoadCanonicalPolicy(s.canonicalDir)
	if err != nil {
		return RegistrySyncResult{}, err
	}
	status, err := s.store.Status(ctx, s.organizationID, policy)
	if err != nil {
		return RegistrySyncResult{}, err
	}
	if status.Synchronized {
		return RegistrySyncResult{NoOp: true, PolicyVersionID: status.PolicyVersionID, PolicyID: policy.PolicyID, PolicyVersion: policy.PolicyVersion, CanonicalHash: policy.CanonicalHash}, nil
	}
	if !apply {
		return RegistrySyncResult{PolicyID: policy.PolicyID, PolicyVersion: policy.PolicyVersion, CanonicalHash: policy.CanonicalHash}, nil
	}
	return s.store.Apply(ctx, s.organizationID, policy)
}
