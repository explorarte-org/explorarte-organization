package modeldispatch

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAuthorizer struct {
	denyCapability map[string]bool
	err            error
}

func (f fakeAuthorizer) Authorize(_ context.Context, _ string, _ int64, _, capability string) error {
	if f.err != nil {
		return f.err
	}
	if f.denyCapability[capability] {
		return errors.New("denied by fixture")
	}
	return nil
}

type fakeCatalog struct {
	revision int64
	roles    map[string]RoleRef
}

func (f fakeCatalog) CurrentRevision(context.Context, string) (int64, error) { return f.revision, nil }
func (f fakeCatalog) GetRole(_ context.Context, _, roleID string) (RoleRef, error) {
	role, ok := f.roles[roleID]
	if !ok {
		return RoleRef{}, ErrNotFound
	}
	return role, nil
}

type fakeTaskReader struct {
	ref TaskAttemptRef
	err error
}

func (f fakeTaskReader) GetTaskAttempt(context.Context, int64, int64) (TaskAttemptRef, error) {
	return f.ref, f.err
}

type fakePrincipalResolver struct {
	principal ExecutionPrincipal
	err       error
}

func (f fakePrincipalResolver) ResolveByKey(context.Context, string, string) (ExecutionPrincipal, error) {
	return f.principal, f.err
}

func (f fakePrincipalResolver) ResolveActiveForRole(context.Context, string, string) (ExecutionPrincipal, error) {
	return f.principal, f.err
}

type fakePrincipalStore struct {
	fakePrincipalResolver
	registerCalls int
	lastPrepared  PreparedRegisterPrincipal
	registerFn    func(PreparedRegisterPrincipal) (RegisterPrincipalResult, error)
	disableFn     func(int64, string, string) (ExecutionPrincipal, error)
}

func (f *fakePrincipalStore) RegisterPrincipal(_ context.Context, p PreparedRegisterPrincipal) (RegisterPrincipalResult, error) {
	f.registerCalls++
	f.lastPrepared = p
	if f.registerFn != nil {
		return f.registerFn(p)
	}
	return RegisterPrincipalResult{Principal: ExecutionPrincipal{ID: 1, OrganizationID: p.Command.OrganizationID, PrincipalKey: p.Command.PrincipalKey, DispatchActorRoleID: p.Command.DispatchActorRoleID, PrincipalKind: p.Command.PrincipalKind, Status: PrincipalActive, RequestHash: p.RequestHash}}, nil
}
func (f *fakePrincipalStore) GetPrincipal(context.Context, int64) (ExecutionPrincipal, error) {
	return f.principal, nil
}
func (f *fakePrincipalStore) ListPrincipals(context.Context, string, int) ([]ExecutionPrincipal, error) {
	return []ExecutionPrincipal{f.principal}, nil
}
func (f *fakePrincipalStore) DisablePrincipal(_ context.Context, id int64, actor, reason string) (ExecutionPrincipal, error) {
	if f.disableFn != nil {
		return f.disableFn(id, actor, reason)
	}
	return ExecutionPrincipal{ID: id, Status: PrincipalDisabled, DisabledByRoleID: actor, DisableReasonCode: reason}, nil
}

func TestPrincipalServiceRegisterRequiresEligibleRole(t *testing.T) {
	authorizer := fakeAuthorizer{}
	catalog := fakeCatalog{revision: 7, roles: map[string]RoleRef{
		"ingenieria_ia/code-runner": {ID: "ingenieria_ia/code-runner", Enabled: true, Executable: true, AuthorityClass: "execution_service"},
		"ingenieria_ia/frontend":    {ID: "ingenieria_ia/frontend", Enabled: true, Executable: true, AuthorityClass: "specialist"},
	}}
	store := &fakePrincipalStore{}
	service, err := NewPrincipalService("explorarte", authorizer, catalog, store, ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	valid := RegisterPrincipalCommand{PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner", PrincipalKind: PrincipalLocalProcess, IdempotencyKey: "idem-1"}
	if _, err = service.Register(context.Background(), "empresa/human", valid); err != nil {
		t.Fatalf("eligible role rejected: %v", err)
	}
	if store.registerCalls != 1 {
		t.Fatalf("expected one store call, got %d", store.registerCalls)
	}
	ineligible := valid
	ineligible.DispatchActorRoleID = "ingenieria_ia/frontend"
	ineligible.IdempotencyKey = "idem-2"
	if _, err = service.Register(context.Background(), "empresa/human", ineligible); !errors.Is(err, ErrRoleNotEligible) {
		t.Fatalf("expected role eligibility rejection, got %v", err)
	}
	if store.registerCalls != 1 {
		t.Fatalf("ineligible role must not reach the store: calls=%d", store.registerCalls)
	}
}

func TestPrincipalServiceRegisterRequiresAuthorization(t *testing.T) {
	authorizer := fakeAuthorizer{denyCapability: map[string]bool{capabilityPrincipalRegister: true}}
	catalog := fakeCatalog{revision: 7, roles: map[string]RoleRef{"ingenieria_ia/code-runner": {Enabled: true, Executable: true, AuthorityClass: "execution_service"}}}
	store := &fakePrincipalStore{}
	service, err := NewPrincipalService("explorarte", authorizer, catalog, store, ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	command := RegisterPrincipalCommand{PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner", PrincipalKind: PrincipalLocalProcess, IdempotencyKey: "idem-1"}
	if _, err = service.Register(context.Background(), "ingenieria_ia/code-runner", command); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expected authorization denial, got %v", err)
	}
	if store.registerCalls != 0 {
		t.Fatalf("denied actor must not reach the store: calls=%d", store.registerCalls)
	}
}

func TestPrincipalServiceDisableRequiresAuthorization(t *testing.T) {
	authorizer := fakeAuthorizer{denyCapability: map[string]bool{capabilityPrincipalDisable: true}}
	catalog := fakeCatalog{revision: 7}
	store := &fakePrincipalStore{}
	service, err := NewPrincipalService("explorarte", authorizer, catalog, store, ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Disable(context.Background(), "ingenieria_ia/code-runner", 1, "retired"); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expected authorization denial, got %v", err)
	}
}

type fakeAssignmentStore struct {
	createFn func(PreparedCreateAssignment) (CreateAssignmentResult, error)
	created  []PreparedCreateAssignment
}

func (f *fakeAssignmentStore) CreateAssignment(_ context.Context, p PreparedCreateAssignment) (CreateAssignmentResult, error) {
	f.created = append(f.created, p)
	if f.createFn != nil {
		return f.createFn(p)
	}
	return CreateAssignmentResult{Assignment: DispatcherAssignment{ID: 1, Status: AssignmentActive}}, nil
}
func (f *fakeAssignmentStore) GetAssignment(context.Context, int64) (DispatcherAssignment, error) {
	return DispatcherAssignment{}, nil
}
func (f *fakeAssignmentStore) ListAssignments(context.Context, string, int) ([]DispatcherAssignment, error) {
	return nil, nil
}
func (f *fakeAssignmentStore) RevokeAssignment(context.Context, int64, string, string) (DispatcherAssignment, error) {
	return DispatcherAssignment{Status: AssignmentRevoked}, nil
}
func (f *fakeAssignmentStore) ExpireAssignments(context.Context, string, int, time.Time) (ExpireResult, error) {
	return ExpireResult{}, nil
}
func (f *fakeAssignmentStore) ResolveActive(context.Context, string, int64, int64, string) (ResolvedAssignment, error) {
	return ResolvedAssignment{}, ErrNotFound
}
func (f *fakeAssignmentStore) GetByID(context.Context, string, int64) (ResolvedAssignment, error) {
	return ResolvedAssignment{}, ErrNotFound
}

func assignmentServiceFixture(t *testing.T, now time.Time) (*AssignmentService, *fakeAssignmentStore) {
	t.Helper()
	authorizer := fakeAuthorizer{}
	catalog := fakeCatalog{revision: 7, roles: map[string]RoleRef{"ingenieria_ia/code-runner": {Enabled: true, Executable: true, AuthorityClass: "execution_service"}}}
	tasks := fakeTaskReader{ref: TaskAttemptRef{TaskID: 3, AttemptID: 4, OrganizationID: "explorarte", OrganizationRevisionID: 7, AssignedRoleID: "ingenieria_ia/code-runner", TaskStatus: "running", AttemptStatus: "running", LeaseHolderID: "worker-1", LeaseExpiresAt: now.Add(time.Hour)}}
	principals := fakePrincipalResolver{principal: ExecutionPrincipal{ID: 21, PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner", Status: PrincipalActive}}
	store := &fakeAssignmentStore{}
	service, err := NewAssignmentService("explorarte", authorizer, catalog, tasks, principals, store, ClockFunc(func() time.Time { return now }), 15*time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func TestAssignmentServiceCreateValidatesLeaseAndQuota(t *testing.T) {
	now := mustTime("2026-01-01T00:00:00Z")
	service, store := assignmentServiceFixture(t, now)
	command := CreateAssignmentCommand{TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner", ExecutionPrincipalKey: "oracle-01/model-runtime-01", MaxInvocations: 1, IdempotencyKey: "assign-1"}
	if _, err := service.Create(context.Background(), "empresa/human", command); err != nil {
		t.Fatalf("valid assignment rejected: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected one create call, got %d", len(store.created))
	}

	longTTL := 2 * time.Hour
	tooLong := command
	tooLong.TTL = &longTTL
	tooLong.IdempotencyKey = "assign-2"
	if _, err := service.Create(context.Background(), "empresa/human", tooLong); err == nil {
		t.Fatal("expected vigency exceeding the lease to be rejected")
	}
	if len(store.created) != 1 {
		t.Fatalf("lease-violating assignment must not reach the store: calls=%d", len(store.created))
	}
}

func TestAssignmentServiceCreateRejectsDisabledPrincipal(t *testing.T) {
	now := mustTime("2026-01-01T00:00:00Z")
	authorizer := fakeAuthorizer{}
	catalog := fakeCatalog{revision: 7, roles: map[string]RoleRef{"ingenieria_ia/code-runner": {Enabled: true, Executable: true, AuthorityClass: "execution_service"}}}
	tasks := fakeTaskReader{ref: TaskAttemptRef{TaskID: 3, AttemptID: 4, OrganizationID: "explorarte", OrganizationRevisionID: 7, AssignedRoleID: "ingenieria_ia/code-runner", TaskStatus: "running", AttemptStatus: "running", LeaseHolderID: "worker-1", LeaseExpiresAt: now.Add(time.Hour)}}
	principals := fakePrincipalResolver{principal: ExecutionPrincipal{ID: 21, PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner", Status: PrincipalDisabled}}
	store := &fakeAssignmentStore{}
	service, err := NewAssignmentService("explorarte", authorizer, catalog, tasks, principals, store, ClockFunc(func() time.Time { return now }), 15*time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	command := CreateAssignmentCommand{TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner", ExecutionPrincipalKey: "oracle-01/model-runtime-01", MaxInvocations: 1, IdempotencyKey: "assign-1"}
	if _, err = service.Create(context.Background(), "empresa/human", command); !errors.Is(err, ErrPrincipalDisabled) {
		t.Fatalf("expected disabled principal rejection, got %v", err)
	}
	if len(store.created) != 0 {
		t.Fatalf("disabled principal must not reach the store: calls=%d", len(store.created))
	}
}

func TestAssignmentServiceCreateRejectsForeignSubject(t *testing.T) {
	now := mustTime("2026-01-01T00:00:00Z")
	service, store := assignmentServiceFixture(t, now)
	command := CreateAssignmentCommand{TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/frontend", ExecutionPrincipalKey: "oracle-01/model-runtime-01", MaxInvocations: 1, IdempotencyKey: "assign-1"}
	if _, err := service.Create(context.Background(), "empresa/human", command); !errors.Is(err, ErrTaskAttemptRejected) {
		t.Fatalf("expected subject/assigned-role mismatch rejection, got %v", err)
	}
	if len(store.created) != 0 {
		t.Fatalf("foreign subject must not reach the store: calls=%d", len(store.created))
	}
}

func TestAssignmentServiceRevokeRequiresAuthorization(t *testing.T) {
	now := mustTime("2026-01-01T00:00:00Z")
	authorizer := fakeAuthorizer{denyCapability: map[string]bool{capabilityAssignmentRevoke: true}}
	catalog := fakeCatalog{revision: 7}
	tasks := fakeTaskReader{}
	principals := fakePrincipalResolver{}
	store := &fakeAssignmentStore{}
	service, err := NewAssignmentService("explorarte", authorizer, catalog, tasks, principals, store, ClockFunc(func() time.Time { return now }), 15*time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Revoke(context.Background(), "ingenieria_ia/code-runner", 1, "superseded"); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expected authorization denial, got %v", err)
	}
}
