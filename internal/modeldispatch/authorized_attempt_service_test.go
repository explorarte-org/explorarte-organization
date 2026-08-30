package modeldispatch

import (
	"context"
	"errors"
	"testing"
	"time"
)

type authorizationCall struct {
	role       string
	capability string
	resourceID string
}

type recordingAuthorizer struct {
	calls []authorizationCall
	deny  map[string]bool
}

func (a *recordingAuthorizer) Authorize(_ context.Context, _ string, _ int64, role, capability string) error {
	a.calls = append(a.calls, authorizationCall{role: role, capability: capability})
	if a.deny[role+"|"+capability] {
		return errors.New("denied")
	}
	return nil
}

func (a *recordingAuthorizer) AuthorizeResource(_ context.Context, _ string, _ int64, role, capability, _, resourceID, _ string) error {
	a.calls = append(a.calls, authorizationCall{role: role, capability: capability, resourceID: resourceID})
	if a.deny[role+"|"+capability] {
		return errors.New("denied")
	}
	return nil
}

type lineageReader struct {
	tasks map[int64]TaskLineageRef
}

func (r *lineageReader) GetTaskLineage(_ context.Context, taskID int64) (TaskLineageRef, error) {
	task, ok := r.tasks[taskID]
	if !ok {
		return TaskLineageRef{}, ErrNotFound
	}
	return task, nil
}

type bindingReader struct {
	binding RoleModelBindingRef
	err     error
}

func (r *bindingReader) GetActiveRoleModelBinding(context.Context, string, int64, string) (RoleModelBindingRef, error) {
	return r.binding, r.err
}

type authorizedAttemptFixture struct {
	service    *AuthorizedAttemptProvisioner
	authorizer *recordingAuthorizer
	lineage    *lineageReader
	binding    *bindingReader
	store      *fakeAssignmentStore
	now        time.Time
	attempt    TaskAttemptRef
	principal  ExecutionPrincipal
}

func newAuthorizedAttemptFixture(t *testing.T) *authorizedAttemptFixture {
	t.Helper()
	now := mustTime("2026-01-01T00:00:00Z")
	attempt := TaskAttemptRef{
		TaskID: 12, AttemptID: 34, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		AssignedRoleID: "empresa/ceo", TaskStatus: "running", AttemptStatus: "running",
		LeaseHolderID: "41", LeaseExpiresAt: now.Add(30 * time.Minute),
	}
	principal := ExecutionPrincipal{
		ID: 81, OrganizationID: "explorarte", PrincipalKey: "oracle-01/model-runtime-01",
		DispatchActorRoleID: "ingenieria_ia/code-runner", Status: PrincipalActive,
	}
	authorizer := &recordingAuthorizer{}
	catalog := fakeCatalog{revision: 7, roles: map[string]RoleRef{
		"ingenieria_ia/code-runner": {ID: "ingenieria_ia/code-runner", Enabled: true, Executable: true, AuthorityClass: "execution_service"},
	}}
	store := &fakeAssignmentStore{}
	assignments, err := NewAssignmentService(
		"explorarte", authorizer, catalog, fakeTaskReader{ref: attempt},
		fakePrincipalResolver{principal: principal}, store, ClockFunc(func() time.Time { return now }),
		15*time.Minute, time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	lineage := &lineageReader{tasks: map[int64]TaskLineageRef{
		12: {TaskID: 12, OrganizationID: "explorarte", OrganizationRevisionID: 7, RequestedByRoleID: "empresa/ceo", AssignedRoleID: "empresa/ceo", CorrelationID: "executive:campaign", CausationID: "task:4"},
		4:  {TaskID: 4, OrganizationID: "explorarte", OrganizationRevisionID: 7, RequestedByRoleID: "empresa/human", AssignedRoleID: "empresa/ceo", CorrelationID: "executive:campaign", CausationID: "owner:campaign-r17"},
	}}
	binding := &bindingReader{binding: RoleModelBindingRef{
		OrganizationID: "explorarte", OrganizationRevisionID: 7, RoleID: "empresa/ceo",
		ProfileID: "ceo-primary", ModelProfileVersionID: 8,
		BindingHash: "bf7b45e7e18cf02ff98a4562537c16b21767fb321bf6a87a48bc2ba5ab24f669", Active: true,
	}}
	service, err := NewAuthorizedAttemptProvisioner(assignments, lineage, binding, principal.PrincipalKey)
	if err != nil {
		t.Fatal(err)
	}
	return &authorizedAttemptFixture{service: service, authorizer: authorizer, lineage: lineage, binding: binding, store: store, now: now, attempt: attempt, principal: principal}
}

func TestAuthorizedAttemptProvisionerDerivesAndSeparatesAuthorities(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	result, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil || result.Reused || result.Assignment.ID == 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(fixture.authorizer.calls) != 3 {
		t.Fatalf("authorization calls=%+v", fixture.authorizer.calls)
	}
	want := []authorizationCall{
		{role: "ingenieria_ia/code-runner", capability: capabilityProvisionAuthorizedAttempt, resourceID: "task:12/attempt:34"},
		{role: "empresa/human", capability: capabilityAssignmentCreate, resourceID: "task:12/attempt:34"},
		{role: "empresa/human", capability: capabilityAssignmentCreate},
	}
	for i := range want {
		if fixture.authorizer.calls[i] != want[i] {
			t.Fatalf("authorization call %d=%+v want %+v", i, fixture.authorizer.calls[i], want[i])
		}
	}
	if fixture.store.created[0].CreatedByRoleID != "empresa/human" {
		t.Fatalf("created_by=%q, want persisted root requester", fixture.store.created[0].CreatedByRoleID)
	}
}

func TestAuthorizedAttemptProvisionerRejectsBrokenOrForgedAncestry(t *testing.T) {
	tests := map[string]func(*authorizedAttemptFixture){
		"missing parent": func(f *authorizedAttemptFixture) {
			delete(f.lineage.tasks, 4)
		},
		"cross organization parent": func(f *authorizedAttemptFixture) {
			parent := f.lineage.tasks[4]
			parent.OrganizationID = "foreign"
			f.lineage.tasks[4] = parent
		},
		"correlation splice": func(f *authorizedAttemptFixture) {
			parent := f.lineage.tasks[4]
			parent.CorrelationID = "executive:other"
			f.lineage.tasks[4] = parent
		},
		"cycle": func(f *authorizedAttemptFixture) {
			parent := f.lineage.tasks[4]
			parent.CausationID = "task:12"
			f.lineage.tasks[4] = parent
		},
		"forged owner marker without requester": func(f *authorizedAttemptFixture) {
			parent := f.lineage.tasks[4]
			parent.RequestedByRoleID = ""
			f.lineage.tasks[4] = parent
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newAuthorizedAttemptFixture(t)
			mutate(fixture)
			_, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
			if err == nil || (!errors.Is(err, ErrTaskAttemptRejected) && !errors.Is(err, ErrAuthorizationDenied)) {
				t.Fatalf("expected fail-closed ancestry rejection, got %v", err)
			}
			if len(fixture.store.created) != 0 {
				t.Fatal("broken ancestry reached assignment creation")
			}
		})
	}
}

func TestAuthorizedAttemptProvisionerRejectsMissingBinding(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	fixture.binding.err = ErrNotFound
	_, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if !errors.Is(err, ErrTaskAttemptRejected) {
		t.Fatalf("expected missing binding rejection, got %v", err)
	}
	if len(fixture.store.created) != 0 {
		t.Fatal("missing binding reached assignment creation")
	}
}

func TestAuthorizedAttemptProvisionerReplayRequiresSameEffectiveBinding(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	first, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil || !second.Reused || second.Assignment.ID != first.Assignment.ID || len(fixture.store.created) != 1 {
		t.Fatalf("exact replay result=%+v err=%v creates=%d", second, err, len(fixture.store.created))
	}

	fixture.binding.binding.ModelProfileVersionID++
	fixture.binding.binding.BindingHash = "af7b45e7e18cf02ff98a4562537c16b21767fb321bf6a87a48bc2ba5ab24f669"
	_, err = fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected explicit binding replay conflict, got %v", err)
	}
	if len(fixture.store.created) != 1 {
		t.Fatalf("divergent replay created another assignment: %d", len(fixture.store.created))
	}
}
