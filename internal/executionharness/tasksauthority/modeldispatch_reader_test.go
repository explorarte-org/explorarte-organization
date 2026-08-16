package tasksauthority

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

type canonicalPrincipalStore struct {
	value modeldispatch.ExecutionPrincipal
}

func (s canonicalPrincipalStore) GetPrincipal(context.Context, int64) (modeldispatch.ExecutionPrincipal, error) {
	return s.value, nil
}

func TestCanonicalPrincipalReaderMapsActiveOrganizationRoleBinding(t *testing.T) {
	reader, err := NewCanonicalPrincipalReader(canonicalPrincipalStore{value: modeldispatch.ExecutionPrincipal{
		ID: 41, OrganizationID: "org-1", DispatchActorRoleID: "research/worker", Status: modeldispatch.PrincipalActive,
	}})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := reader.ResolveExecutionPrincipal(context.Background(), "org-1", "41")
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Active || principal.ID != "41" || principal.OrganizationID != "org-1" || principal.RoleID != "research/worker" {
		t.Fatalf("unexpected canonical principal mapping: %+v", principal)
	}
}

func TestCanonicalPrincipalReaderPreservesDisabledStateForFailClosedAuthority(t *testing.T) {
	reader, err := NewCanonicalPrincipalReader(canonicalPrincipalStore{value: modeldispatch.ExecutionPrincipal{
		ID: 41, OrganizationID: "org-1", DispatchActorRoleID: "research/worker", Status: modeldispatch.PrincipalDisabled,
	}})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := reader.ResolveExecutionPrincipal(context.Background(), "org-1", "41")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Active {
		t.Fatal("disabled canonical principal was mapped as active")
	}
}

func TestCanonicalPrincipalReaderRejectsNonNumericIdentity(t *testing.T) {
	reader, err := NewCanonicalPrincipalReader(canonicalPrincipalStore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ResolveExecutionPrincipal(context.Background(), "org-1", "role-bound/worker"); err == nil {
		t.Fatal("non-numeric principal identity crossed canonical reader")
	}
}
