package modelegress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type serviceOrganizationCatalog struct{ value OrganizationRef }

func (c serviceOrganizationCatalog) CurrentOrganization(context.Context, string) (OrganizationRef, error) {
	return c.value, nil
}

type serviceProviderCatalog struct{ providers []string }

func (c serviceProviderCatalog) ProviderIDs(string) ([]string, error) {
	return append([]string(nil), c.providers...), nil
}

type serviceRegistryStore struct {
	status        RegistryStatus
	validated     int
	statusCalls   int
	applyCalls    int
	resolved      ResolvedPolicy
	validationErr error
}

func (s *serviceRegistryStore) RecordValidated(context.Context, string, int64, string) error {
	s.validated++
	return s.validationErr
}
func (s *serviceRegistryStore) Status(context.Context, RegistryPlan) (RegistryStatus, error) {
	s.statusCalls++
	return s.status, nil
}
func (s *serviceRegistryStore) Apply(_ context.Context, plan RegistryPlan) (RegistrySyncResult, error) {
	s.applyCalls++
	return RegistrySyncResult{Applied: true, OrganizationRevisionID: plan.OrganizationRevisionID, PolicyVersionID: 41, PolicyID: plan.Policy.PolicyID, PolicyVersion: plan.Policy.PolicyVersion, CanonicalHash: plan.CanonicalHash, Rules: len(plan.Policy.HardDenies) + len(plan.Policy.Rules)}, nil
}
func (s *serviceRegistryStore) ResolveForRevision(context.Context, string, int64) (ResolvedPolicy, error) {
	return s.resolved, nil
}

func writeServicePolicy(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	body := `schema_version: 0.1.0
document_status: branch_09_candidate
policy_id: model-egress
policy_version: 1
default_action: deny
hard_denies:
- data_classification: clinical
  reason_code: clinical_egress_forbidden
- data_classification: secret
  reason_code: secret_egress_forbidden
rules:
- provider_id: deepseek
  data_classification: organizational
  effect: deny
  reason_code: organizational_egress_not_approved
`
	if err := os.WriteFile(filepath.Join(dir, PolicyFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := LoadCanonicalPolicy(dir, LoadOptions{KnownProviders: []string{"deepseek"}})
	if err != nil {
		t.Fatal(err)
	}
	return dir, policy.CanonicalHash
}

func TestServiceCommandsRespectReadWriteSemantics(t *testing.T) {
	dir, hash := writeServicePolicy(t)
	store := &serviceRegistryStore{status: RegistryStatus{Synchronized: false}}
	service, err := NewService(dir, "explorarte", serviceOrganizationCatalog{value: OrganizationRef{ID: "explorarte", RevisionID: 9, PolicyDocumentHash: hash}}, serviceProviderCatalog{providers: []string{"deepseek"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = service.Validate(ctx); err != nil || store.validated != 1 || store.applyCalls != 0 {
		t.Fatalf("validate writes validated=%d apply=%d err=%v", store.validated, store.applyCalls, err)
	}
	if _, err = service.Diff(ctx); err != nil || store.statusCalls != 1 || store.applyCalls != 0 {
		t.Fatalf("diff status=%d apply=%d err=%v", store.statusCalls, store.applyCalls, err)
	}
	if _, err = service.Status(ctx); err != nil || store.statusCalls != 2 || store.applyCalls != 0 {
		t.Fatalf("status calls=%d apply=%d err=%v", store.statusCalls, store.applyCalls, err)
	}
	plan, err := service.Sync(ctx, false)
	if err != nil || plan.Applied || plan.NoOp || store.applyCalls != 0 {
		t.Fatalf("dry sync=%+v apply=%d err=%v", plan, store.applyCalls, err)
	}
	applied, err := service.Sync(ctx, true)
	if err != nil || !applied.Applied || store.applyCalls != 1 {
		t.Fatalf("apply=%+v calls=%d err=%v", applied, store.applyCalls, err)
	}
}

func TestServiceRejectsOrganizationRegistryHashDrift(t *testing.T) {
	dir, _ := writeServicePolicy(t)
	service, err := NewService(dir, "explorarte", serviceOrganizationCatalog{value: OrganizationRef{ID: "explorarte", RevisionID: 9, PolicyDocumentHash: SHA256Bytes([]byte("different"))}}, serviceProviderCatalog{providers: []string{"deepseek"}}, &serviceRegistryStore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Diff(context.Background()); !errors.Is(err, ErrPolicyStale) {
		t.Fatalf("error=%v", err)
	}
}
