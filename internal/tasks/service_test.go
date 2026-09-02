package tasks

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeCatalog struct {
	revision RevisionRef
	roles    map[string]RoleRef
	err      error
}

func (f fakeCatalog) CurrentRevision(context.Context, string) (RevisionRef, error) {
	if f.err != nil {
		return RevisionRef{}, f.err
	}
	return f.revision, nil
}
func (f fakeCatalog) GetRole(_ context.Context, _, id string) (RoleRef, error) {
	if f.err != nil {
		return RoleRef{}, f.err
	}
	role, ok := f.roles[id]
	if !ok {
		return RoleRef{}, ErrNotFound
	}
	return role, nil
}

type fakePersistence struct {
	prepared PreparedCreate
	created  Task
	check    AssigneeCheck
}

func (f *fakePersistence) GetTask(context.Context, int64) (TaskDetail, error) {
	return TaskDetail{Task: f.created}, nil
}
func (f *fakePersistence) ListTasks(context.Context, TaskFilter) ([]Task, error)   { return nil, nil }
func (f *fakePersistence) ListEvents(context.Context, int64, int) ([]Event, error) { return nil, nil }
func (f *fakePersistence) ListAttempts(context.Context, int64) ([]Attempt, error)  { return nil, nil }
func (f *fakePersistence) ListDeadLetters(context.Context, int) ([]DeadLetter, error) {
	return nil, nil
}
func (f *fakePersistence) GetDeadLetter(context.Context, int64) (DeadLetter, error) {
	return DeadLetter{}, nil
}
func (f *fakePersistence) Create(_ context.Context, input PreparedCreate) (Task, bool, error) {
	f.prepared = input
	value := f.created
	if value.ID == 0 {
		value = Task{ID: 1, OrganizationID: input.Request.OrganizationID, OrganizationRevisionID: input.OrganizationRevisionID, AssignedRoleID: input.Request.AssignedRoleID, AssignedUnitID: input.AssignedUnitID, Status: input.InitialStatus, MaxAttempts: input.Request.MaxAttempts}
	}
	return value, true, nil
}
func (f *fakePersistence) AddDependency(context.Context, AddDependencyCommand, int) error { return nil }
func (f *fakePersistence) AddRequirement(context.Context, AddRequirementCommand, int) (Requirement, error) {
	return Requirement{}, nil
}
func (f *fakePersistence) RecordEvidence(context.Context, RecordEvidenceCommand, int) (Evidence, error) {
	return Evidence{}, nil
}
func (f *fakePersistence) RecordRequirementVerification(context.Context, RequirementVerificationCommand, int) (Evidence, error) {
	return Evidence{}, nil
}
func (f *fakePersistence) VerifyActiveExecutionLease(context.Context, VerifyExecutionLeaseCommand) (ExecutionLeaseContext, error) {
	return ExecutionLeaseContext{}, nil
}
func (f *fakePersistence) Finalize(context.Context, FinalizeCommand, int) (Task, error) {
	return f.created, nil
}
func (f *fakePersistence) Block(context.Context, BlockCommand, int) (Task, error) {
	return f.created, nil
}
func (f *fakePersistence) Unblock(_ context.Context, _ UnblockCommand, check AssigneeCheck, _ int) (Task, error) {
	f.check = check
	return f.created, nil
}
func (f *fakePersistence) ReleaseCoordinationHold(_ context.Context, _ ReleaseCoordinationHoldCommand, check AssigneeCheck, _ int) (Task, error) {
	f.check = check
	return f.created, nil
}
func (f *fakePersistence) Cancel(context.Context, CancelCommand, int) (Task, error) {
	return f.created, nil
}
func (f *fakePersistence) Claim(_ context.Context, _ ClaimRequest, validate AssigneeValidator, _ int) ([]ClaimedTask, error) {
	check, err := validate(context.Background(), f.created)
	f.check = check
	return nil, err
}
func (f *fakePersistence) StartAttempt(context.Context, LeaseCommand, int) (Task, error) {
	return f.created, nil
}
func (f *fakePersistence) Heartbeat(context.Context, LeaseCommand, time.Duration) (Lease, error) {
	return Lease{}, nil
}
func (f *fakePersistence) RecordAttemptResult(context.Context, RecordAttemptResultCommand, RetryPolicy, int) (Task, error) {
	return f.created, nil
}
func (f *fakePersistence) Reconcile(_ context.Context, _ int, validate AssigneeValidator, _ RetryPolicy, _ int) (ReconcileResult, error) {
	check, err := validate(context.Background(), f.created)
	f.check = check
	return ReconcileResult{}, err
}
func (f *fakePersistence) ClaimOutbox(context.Context, OutboxClaimRequest) ([]ClaimedOutboxEvent, error) {
	return nil, nil
}
func (f *fakePersistence) AckOutbox(context.Context, OutboxDisposition) error  { return nil }
func (f *fakePersistence) NackOutbox(context.Context, OutboxDisposition) error { return nil }
func (f *fakePersistence) RecoverOutbox(context.Context, int) (int, error)     { return 0, nil }
func (f *fakePersistence) OutboxStats(context.Context) (OutboxStats, error) {
	return OutboxStats{}, nil
}

func (f *fakePersistence) PruneOutbox(context.Context, OutboxPruneRequest) (OutboxPruneResult, error) {
	return OutboxPruneResult{}, nil
}

// TestPruneOutboxValidatesRetentionFloors (G5-001): the floor checks run
// BEFORE anything reaches persistence -- a fat-fingered short duration or
// an empty status selection must never reach SQL.
func TestPruneOutboxValidatesRetentionFloors(t *testing.T) {
	service, _, _ := serviceFixture(t)
	ctx := context.Background()

	if _, err := service.PruneOutbox(ctx, OutboxPruneRequest{OlderThan: time.Hour, IncludeDead: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for sub-24h older-than, got %v", err)
	}
	if _, err := service.PruneOutbox(ctx, OutboxPruneRequest{OlderThan: 24 * time.Hour, IncludePending: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for pending prune under 30d, got %v", err)
	}
	if _, err := service.PruneOutbox(ctx, OutboxPruneRequest{OlderThan: 90 * 24 * time.Hour}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput when no status is selected, got %v", err)
	}
	if _, err := service.PruneOutbox(ctx, OutboxPruneRequest{OlderThan: 90 * 24 * time.Hour, IncludeDead: true, Limit: 50000}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for an oversized limit, got %v", err)
	}
	if _, err := service.PruneOutbox(ctx, OutboxPruneRequest{OlderThan: 90 * 24 * time.Hour, IncludeDead: true}); err != nil {
		t.Fatalf("expected a valid request to pass validation, got %v", err)
	}
}

func serviceFixture(t *testing.T) (*Service, *fakePersistence, *fakeCatalog) {
	t.Helper()
	persistence := &fakePersistence{created: Task{ID: 1, OrganizationID: "explorarte", OrganizationRevisionID: 7, AssignedRoleID: "ingenieria_ia/qa", AssignedUnitID: "ingenieria_ia", Status: StatusReady}}
	catalog := &fakeCatalog{revision: RevisionRef{ID: 7}, roles: map[string]RoleRef{
		"ingenieria_ia/qa": {ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true},
		"empresa/human":    {ID: "empresa/human", UnitID: "empresa", Enabled: true, Executable: false},
	}}
	service, err := NewService(persistence, catalog, Config{
		OrganizationID: "explorarte", DefaultMaxAttempts: 5, DefaultLeaseDuration: time.Minute,
		MaxLeaseDuration: 10 * time.Minute, RetryPolicy: RetryPolicy{BaseDelay: 5 * time.Second, MaxDelay: 10 * time.Minute},
		OutboxMaxAttempts: 10, OutboxClaimDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, persistence, catalog
}

// ORG-AUDIT-012 regression: the postgres Store has no organization_id bound
// at construction (unlike memory/rag), so GetTask(id) alone cannot tell one
// organization's task from another's if this ever ran multi-org. Service is
// scoped to Config.OrganizationID and must reject a task belonging to a
// different one rather than returning it.
func TestGetTaskRejectsTaskFromAnotherOrganization(t *testing.T) {
	service, persistence, _ := serviceFixture(t)
	persistence.created.OrganizationID = "a-different-organization"
	if _, err := service.GetTask(context.Background(), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTask across organizations: err=%v, want ErrNotFound", err)
	}
}

func TestCreateTaskDerivesRegistryFieldsAndDefaults(t *testing.T) {
	service, persistence, _ := serviceFixture(t)
	request := validCreateRequest()
	request.OrganizationID = ""
	request.MaxAttempts = 0
	request.RequestedByRoleID = "empresa/human"
	value, created, err := service.CreateTask(context.Background(), request, "human", "eduardo")
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if !created || value.ID != 1 {
		t.Fatalf("result = %+v created=%v", value, created)
	}
	if persistence.prepared.OrganizationRevisionID != 7 || persistence.prepared.AssignedUnitID != "ingenieria_ia" {
		t.Fatalf("prepared = %+v", persistence.prepared)
	}
	if persistence.prepared.Request.MaxAttempts != 5 || persistence.prepared.RequestHash == "" {
		t.Fatalf("defaults/hash not applied: %+v", persistence.prepared)
	}
}

func TestCreateTaskRejectsUnavailableAssignees(t *testing.T) {
	cases := []RoleRef{
		{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: false, Executable: true},
		{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: false},
		{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true, Retired: true},
	}
	for index, role := range cases {
		t.Run(time.Duration(index).String(), func(t *testing.T) {
			service, _, catalog := serviceFixture(t)
			catalog.roles["ingenieria_ia/qa"] = role
			_, _, err := service.CreateTask(context.Background(), validCreateRequest(), "human", "eduardo")
			if !errors.Is(err, ErrAssigneeUnavailable) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCreateTaskWithDependencyStartsPending(t *testing.T) {
	service, persistence, _ := serviceFixture(t)
	request := validCreateRequest()
	request.Dependencies = []int64{9}
	if _, _, err := service.CreateTask(context.Background(), request, "human", "eduardo"); err != nil {
		t.Fatal(err)
	}
	if persistence.prepared.InitialStatus != StatusPending {
		t.Fatalf("initial status = %s", persistence.prepared.InitialStatus)
	}
}

func TestClaimRevalidatesAssignee(t *testing.T) {
	service, persistence, catalog := serviceFixture(t)
	catalog.roles["ingenieria_ia/qa"] = RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: false, Executable: false}
	_, err := service.ClaimTasks(context.Background(), ClaimRequest{WorkerID: "worker-1", AssignedRoleID: "ingenieria_ia/qa"})
	if err != nil {
		t.Fatalf("ClaimTasks() error = %v", err)
	}
	if persistence.check.Available {
		t.Fatal("unavailable assignee passed claim validation")
	}
}

func TestLeaseCommandValidation(t *testing.T) {
	service, _, _ := serviceFixture(t)
	commands := []LeaseCommand{
		{},
		{TaskID: 1, AttemptID: 1, ActorID: "w"},
		{TaskID: 1, AttemptID: 1, LeaseToken: "token"},
	}
	for _, command := range commands {
		if _, err := service.StartAttempt(context.Background(), command); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("command %+v error = %v", command, err)
		}
	}
}

func TestAttemptResultRequiresFailureCode(t *testing.T) {
	service, _, _ := serviceFixture(t)
	_, err := service.RecordAttemptResult(context.Background(), RecordAttemptResultCommand{
		LeaseCommand: LeaseCommand{TaskID: 1, AttemptID: 1, LeaseToken: "token", ActorID: "worker"},
		Result:       AttemptResult{Outcome: OutcomeRetryableFailure},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v", err)
	}
}

func TestFinalizeReasonRules(t *testing.T) {
	service, _, _ := serviceFixture(t)
	_, err := service.FinalizeTask(context.Background(), FinalizeCommand{TaskID: 1, Outcome: FinalNoAction, ActorID: "human"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v", err)
	}
	if _, err := service.FinalizeTask(context.Background(), FinalizeCommand{TaskID: 1, Outcome: FinalCompleted, ActorID: "human"}); err != nil {
		t.Fatalf("completed validation error = %v", err)
	}
}

func TestServiceConfigurationValidation(t *testing.T) {
	valid := Config{OrganizationID: "explorarte", DefaultMaxAttempts: 5, DefaultLeaseDuration: time.Minute, MaxLeaseDuration: 2 * time.Minute, RetryPolicy: RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute}, OutboxMaxAttempts: 5, OutboxClaimDuration: time.Minute}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	invalid := []Config{
		{},
		{OrganizationID: "bad/id", DefaultMaxAttempts: 5, DefaultLeaseDuration: time.Minute, MaxLeaseDuration: 2 * time.Minute, RetryPolicy: RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute}, OutboxMaxAttempts: 5, OutboxClaimDuration: time.Minute},
		{OrganizationID: "explorarte", DefaultMaxAttempts: 0, DefaultLeaseDuration: time.Minute, MaxLeaseDuration: 2 * time.Minute, RetryPolicy: RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute}, OutboxMaxAttempts: 5, OutboxClaimDuration: time.Minute},
		{OrganizationID: "explorarte", DefaultMaxAttempts: 5, DefaultLeaseDuration: 3 * time.Minute, MaxLeaseDuration: 2 * time.Minute, RetryPolicy: RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute}, OutboxMaxAttempts: 5, OutboxClaimDuration: time.Minute},
	}
	for _, cfg := range invalid {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}
