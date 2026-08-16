package tasksauthority

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

type canonicalPrincipalStore struct {
	value       modeldispatch.ExecutionPrincipal
	requestedID int64
	calls       int
}

func (s *canonicalPrincipalStore) GetPrincipal(_ context.Context, id int64) (modeldispatch.ExecutionPrincipal, error) {
	s.requestedID = id
	s.calls++
	return s.value, nil
}

func TestCanonicalPrincipalReaderMapsActiveOrganizationRoleBinding(t *testing.T) {
	store := &canonicalPrincipalStore{value: modeldispatch.ExecutionPrincipal{
		ID: 41, OrganizationID: "org-1", DispatchActorRoleID: "research/worker", Status: modeldispatch.PrincipalActive,
	}}
	reader, err := NewCanonicalPrincipalReader(store)
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
	// The parsed identity must be the one actually looked up: without this the
	// double would answer the same principal for any ID, and a reader that
	// queried a constant would still pass.
	if store.calls != 1 || store.requestedID != 41 {
		t.Fatalf("canonical store queried with calls=%d id=%d, want calls=1 id=41", store.calls, store.requestedID)
	}
}

func TestCanonicalPrincipalReaderPreservesDisabledStateForFailClosedAuthority(t *testing.T) {
	reader, err := NewCanonicalPrincipalReader(&canonicalPrincipalStore{value: modeldispatch.ExecutionPrincipal{
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
	store := &canonicalPrincipalStore{}
	reader, err := NewCanonicalPrincipalReader(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ResolveExecutionPrincipal(context.Background(), "org-1", "role-bound/worker"); err == nil {
		t.Fatal("non-numeric principal identity crossed canonical reader")
	}
	if store.calls != 0 {
		t.Fatalf("canonical store was queried %d times for a non-numeric identity, want 0", store.calls)
	}
}
