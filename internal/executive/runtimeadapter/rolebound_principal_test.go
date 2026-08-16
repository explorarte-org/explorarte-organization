package runtimeadapter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

// principalRows behaves like the durable store the resolver depends on:
// RegisterPrincipal is idempotent by key and there is at most one active
// principal per role, which is what migration 000048 enforces in PostgreSQL.
type principalRows struct {
	mu         sync.Mutex
	byRole     map[string]modeldispatch.ExecutionPrincipal
	byKey      map[string]modeldispatch.ExecutionPrincipal
	registered int
	resolveErr error
	nextID     int64
}

func newPrincipalRows() *principalRows {
	return &principalRows{byRole: map[string]modeldispatch.ExecutionPrincipal{}, byKey: map[string]modeldispatch.ExecutionPrincipal{}}
}

func (s *principalRows) ResolveActiveForRole(_ context.Context, organizationID, roleID string) (modeldispatch.ExecutionPrincipal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolveErr != nil {
		return modeldispatch.ExecutionPrincipal{}, s.resolveErr
	}
	principal, ok := s.byRole[organizationID+"|"+roleID]
	if !ok {
		return modeldispatch.ExecutionPrincipal{}, modeldispatch.ErrNotFound
	}
	return principal, nil
}

func (s *principalRows) RegisterPrincipal(_ context.Context, prepared modeldispatch.PreparedRegisterPrincipal) (modeldispatch.RegisterPrincipalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.byKey[prepared.Command.IdempotencyKey]; ok {
		return modeldispatch.RegisterPrincipalResult{Principal: existing, Reused: true}, nil
	}
	s.nextID++
	s.registered++
	principal := modeldispatch.ExecutionPrincipal{
		ID: s.nextID, OrganizationID: prepared.Command.OrganizationID,
		PrincipalKey: prepared.Command.PrincipalKey, DispatchActorRoleID: prepared.Command.DispatchActorRoleID,
		PrincipalKind: prepared.Command.PrincipalKind, Status: modeldispatch.PrincipalActive,
		IdempotencyKey: prepared.Command.IdempotencyKey, RequestHash: prepared.RequestHash,
		RegisteredByRoleID: prepared.RegisteredByRoleID,
	}
	s.byKey[prepared.Command.IdempotencyKey] = principal
	s.byRole[prepared.Command.OrganizationID+"|"+prepared.Command.DispatchActorRoleID] = principal
	return modeldispatch.RegisterPrincipalResult{Principal: principal}, nil
}

func newResolver(t *testing.T, store RoleBoundPrincipalStore) RoleBoundPrincipalResolver {
	t.Helper()
	resolver, err := NewRoleBoundPrincipalResolver(store, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

// An existing active principal must be reused exactly, never re-provisioned:
// a second row for the same role is the state migration 000048 forbids.
func TestRoleBoundResolverReusesTheExistingActivePrincipal(t *testing.T) {
	store := newPrincipalRows()
	store.byRole["explorarte|empresa/ceo"] = modeldispatch.ExecutionPrincipal{
		ID: 41, OrganizationID: "explorarte", DispatchActorRoleID: "empresa/ceo", Status: modeldispatch.PrincipalActive,
	}

	principal, err := newResolver(t, store).Resolve(context.Background(), "empresa/ceo")
	if err != nil {
		t.Fatal(err)
	}
	if principal.ID != 41 {
		t.Fatalf("principal=%d want the existing 41", principal.ID)
	}
	if store.registered != 0 {
		t.Fatalf("provisioned %d principals for a role that already had one", store.registered)
	}
}

// First use provisions deterministically: the key, the idempotency key and the
// role binding are all functions of (organization, role) and nothing else.
func TestRoleBoundResolverProvisionsDeterministicallyOnFirstUse(t *testing.T) {
	store := newPrincipalRows()
	resolver := newResolver(t, store)

	principal, err := resolver.Resolve(context.Background(), "ingenieria_ia/code-runner")
	if err != nil {
		t.Fatal(err)
	}
	if principal.DispatchActorRoleID != "ingenieria_ia/code-runner" || principal.Status != modeldispatch.PrincipalActive {
		t.Fatalf("provisioned principal is not an active role binding: %+v", principal)
	}
	if !strings.HasSuffix(principal.PrincipalKey, "ingenieria_ia/code-runner") ||
		!strings.HasPrefix(principal.PrincipalKey, modeldispatch.RoleBoundPrincipalKeyPrefix) {
		t.Fatalf("principal key %q is not the deterministic role-bound key", principal.PrincipalKey)
	}
	if principal.IdempotencyKey != "role-bound-principal:explorarte:ingenieria_ia/code-runner" {
		t.Fatalf("idempotency key=%q is not a function of organization and role alone", principal.IdempotencyKey)
	}
	if principal.RegisteredByRoleID != roleBoundProvisionerRoleID {
		t.Fatalf("provisioning was attributed to %q", principal.RegisteredByRoleID)
	}

	// A second resolve now finds the row instead of provisioning again.
	again, err := resolver.Resolve(context.Background(), "ingenieria_ia/code-runner")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != principal.ID || store.registered != 1 {
		t.Fatalf("second resolve produced %d (registered=%d)", again.ID, store.registered)
	}
}

// The race the comment in AgentMessages asserted is now a test: concurrent
// callers converge on one principal, never on two.
func TestRoleBoundResolverConcurrentCallersConvergeOnOnePrincipal(t *testing.T) {
	store := newPrincipalRows()
	resolver := newResolver(t, store)

	const callers = 16
	ids := make([]int64, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			principal, err := resolver.Resolve(context.Background(), "empresa/ceo")
			ids[index], errs[index] = principal.ID, err
		}(i)
	}
	close(start)
	wg.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("caller %d failed: %v", index, err)
		}
	}
	for index, id := range ids {
		if id != ids[0] {
			t.Fatalf("caller %d resolved principal %d while caller 0 resolved %d: the role has two identities", index, id, ids[0])
		}
	}
	if store.registered != 1 {
		t.Fatalf("%d principals were registered for one role", store.registered)
	}
}

// A store failure that is not "absent" must not be mistaken for absence and
// silently turned into provisioning a new identity.
func TestRoleBoundResolverDoesNotProvisionOnStoreFailure(t *testing.T) {
	store := newPrincipalRows()
	store.resolveErr = errors.New("principal store unreachable")

	_, err := newResolver(t, store).Resolve(context.Background(), "empresa/ceo")
	if !errors.Is(err, ErrNoActivePrincipal) {
		t.Fatalf("error=%v want ErrNoActivePrincipal", err)
	}
	if store.registered != 0 {
		t.Fatal("a store failure provisioned a new principal")
	}
}

func TestRoleBoundResolverRequiresItsDependencies(t *testing.T) {
	if _, err := NewRoleBoundPrincipalResolver(nil, "explorarte"); err == nil {
		t.Fatal("a resolver without a principal store was accepted")
	}
	if _, err := NewRoleBoundPrincipalResolver(newPrincipalRows(), ""); err == nil {
		t.Fatal("a resolver without an organization was accepted")
	}
}
