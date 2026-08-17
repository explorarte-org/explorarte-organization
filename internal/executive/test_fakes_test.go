package executive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// memoryTasks is mutex-guarded because the lease keeper heartbeats from its
// own goroutine while the main flow is still driving the same task: an
// unguarded fake would report data races that the real, transactional task
// service does not have.
type memoryTasks struct {
	mu              sync.Mutex
	heartbeatErr    error
	heartbeatActors []string
	nextID          int64
	nextAttempt     int64
	tasks           map[int64]TaskRecord
	keys            map[string]int64
	createCalls     []CreateTaskCommand
	claims          []ClaimTaskCommand
	workerIDs       map[int64]string
	finalized       []int64
	blocked         []int64
	failed          []string
	heartbeats      int
	evidence        []EvidenceCommand
}

func newMemoryTasks() *memoryTasks {
	return &memoryTasks{nextID: 1, nextAttempt: 100, tasks: map[int64]TaskRecord{}, keys: map[string]int64{}, workerIDs: map[int64]string{}}
}

// ErrLeaseMismatch stands in for tasks.ErrLeaseMismatch, which the executive
// package deliberately cannot import.
var ErrLeaseMismatch = errors.New("fake task engine: lease actor mismatch")

// fakePrincipals is the canonical role-bound identity source for unit tests:
// one stable numeric principal per role, exactly like the real resolver.
type fakePrincipals struct {
	ids      map[string]string
	err      error
	resolves int
}

func newFakePrincipals() *fakePrincipals {
	return &fakePrincipals{ids: map[string]string{}}
}

func (f *fakePrincipals) ResolveRoleBoundPrincipal(_ context.Context, roleID string) (ExecutionPrincipalRef, error) {
	f.resolves++
	if f.err != nil {
		return ExecutionPrincipalRef{}, f.err
	}
	id, ok := f.ids[roleID]
	if !ok {
		id = fmt.Sprintf("%d", 7000+len(f.ids)+1)
		f.ids[roleID] = id
	}
	return ExecutionPrincipalRef{ID: id, RoleID: roleID}, nil
}

func (m *memoryTasks) CreateTask(_ context.Context, command CreateTaskCommand) (TaskRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hash := actionDigest(command.Title, command.Instructions, fmt.Sprint(command.AcceptanceCriteria), command.AssignedRoleID)
	if id, ok := m.keys[command.IdempotencyKey]; ok {
		existing := m.tasks[id]
		if existing.RequestHash != hash {
			return TaskRecord{}, false, errors.New("idempotency conflict")
		}
		return existing, true, nil
	}
	id := m.nextID
	m.nextID++
	requirements := make([]RequirementRecord, 0, len(command.Requirements))
	for i, requirement := range command.Requirements {
		requirements = append(requirements, RequirementRecord{
			ID: int64(id*100 + int64(i+1)), Key: requirement.Key, Type: requirement.Type,
			Required: requirement.Required, Status: "pending",
		})
	}
	record := TaskRecord{
		ID: id, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		RequestedByRoleID: command.RequestedByRoleID, AssignedRoleID: command.AssignedRoleID,
		IdempotencyKey: command.IdempotencyKey, RequestHash: hash, Title: command.Title,
		Instructions: command.Instructions, AcceptanceCriteria: append([]string(nil), command.AcceptanceCriteria...),
		Status: "ready", Priority: command.Priority, MaxAttempts: command.MaxAttempts,
		CorrelationID: command.CorrelationID, CausationID: command.CausationID, Requirements: requirements,
	}
	m.tasks[id] = record
	m.keys[command.IdempotencyKey] = id
	m.createCalls = append(m.createCalls, command)
	return record, false, nil
}

func (m *memoryTasks) AddDependency(context.Context, int64, int64) error { return nil }

func (m *memoryTasks) GetTask(_ context.Context, id int64) (TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.tasks[id]
	if !ok {
		return TaskRecord{}, errors.New("not found")
	}
	return value, nil
}

func (m *memoryTasks) ListByCorrelation(_ context.Context, correlation string) ([]TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []TaskRecord{}
	for _, task := range m.tasks {
		if task.CorrelationID == correlation {
			out = append(out, task)
		}
	}
	return out, nil
}

func (m *memoryTasks) ListAwaitingGating(_ context.Context, limit int) ([]TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []TaskRecord{}
	for _, task := range m.tasks {
		if task.Status == "awaiting_verification" {
			out = append(out, task)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ClaimTask mirrors the durable contract: task_attempts.worker_id records the
// operational worker, task_leases.holder_id records the security principal,
// and they are stored separately because they are separate identities.
func (m *memoryTasks) ClaimTask(_ context.Context, command ClaimTaskCommand) (TaskRecord, AttemptRecord, LeaseRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(command.HolderPrincipalID) == "" {
		return TaskRecord{}, AttemptRecord{}, LeaseRecord{}, errors.New("claim without a holder principal")
	}
	task := m.tasks[command.TaskID]
	m.nextAttempt++
	attempt := AttemptRecord{ID: m.nextAttempt, Ordinal: task.AttemptCount + 1, State: "leased"}
	task.AttemptCount++
	task.Status = "leased"
	task.Attempts = append(task.Attempts, attempt)
	lease := LeaseRecord{
		TaskID: command.TaskID, AttemptID: attempt.ID, HolderID: command.HolderPrincipalID,
		LeaseToken: fmt.Sprintf("lease-%d", attempt.ID), ExpiresAt: time.Now().Add(command.LeaseDuration),
	}
	task.ActiveLease = &lease
	m.claims = append(m.claims, command)
	m.workerIDs[attempt.ID] = command.WorkerID
	m.tasks[command.TaskID] = task
	return task, attempt, lease, nil
}

// verifyLeaseActor is the in-memory equivalent of the task engine's
// `lease.holder_id != command.ActorID -> ErrLeaseMismatch`. Without it a fake
// would happily accept the worker name as an actor and the unit tests would
// stop being able to see the defect this migration exists to prevent.
func (m *memoryTasks) verifyLeaseActor(lease LeaseRecord, actor string) error {
	task, ok := m.tasks[lease.TaskID]
	if !ok || task.ActiveLease == nil {
		return ErrLeaseMismatch
	}
	if task.ActiveLease.HolderID != actor || task.ActiveLease.AttemptID != lease.AttemptID {
		return ErrLeaseMismatch
	}
	return nil
}

func (m *memoryTasks) StartAttempt(_ context.Context, lease LeaseRecord, actor string) (TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.verifyLeaseActor(lease, actor); err != nil {
		return TaskRecord{}, err
	}
	task := m.tasks[lease.TaskID]
	task.Status = "running"
	for i := range task.Attempts {
		if task.Attempts[i].ID == lease.AttemptID {
			task.Attempts[i].State = "running"
		}
	}
	m.tasks[task.ID] = task
	return task, nil
}

func (m *memoryTasks) Heartbeat(_ context.Context, lease LeaseRecord, actor string, duration time.Duration) (LeaseRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.heartbeatActors = append(m.heartbeatActors, actor)
	if m.heartbeatErr != nil {
		return LeaseRecord{}, m.heartbeatErr
	}
	if err := m.verifyLeaseActor(lease, actor); err != nil {
		return LeaseRecord{}, err
	}
	m.heartbeats++
	lease.ExpiresAt = time.Now().Add(duration)
	return lease, nil
}

func (m *memoryTasks) failHeartbeats(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.heartbeatErr = err
}

func (m *memoryTasks) heartbeatActorLog() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.heartbeatActors...)
}

func (m *memoryTasks) RecordAttemptSucceeded(_ context.Context, lease LeaseRecord, actor string, _ string) (TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.verifyLeaseActor(lease, actor); err != nil {
		return TaskRecord{}, err
	}
	task := m.tasks[lease.TaskID]
	task.Status = "awaiting_verification"
	task.ActiveLease = nil
	for i := range task.Attempts {
		if task.Attempts[i].ID == lease.AttemptID {
			task.Attempts[i].State = "finished"
		}
	}
	m.tasks[task.ID] = task
	return task, nil
}

func (m *memoryTasks) RecordAttemptFailed(_ context.Context, lease LeaseRecord, actor string, code, reason string, _ bool) (TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.verifyLeaseActor(lease, actor); err != nil {
		return TaskRecord{}, err
	}
	m.failed = append(m.failed, code)
	task := m.tasks[lease.TaskID]
	task.Status = "failed"
	task.ReasonCode = code
	task.Reason = reason
	task.ActiveLease = nil
	m.tasks[task.ID] = task
	return task, nil
}

func (m *memoryTasks) RecordEvidence(_ context.Context, command EvidenceCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[command.TaskID]
	for i := range task.Requirements {
		if task.Requirements[i].ID == command.RequirementID && command.Satisfies {
			task.Requirements[i].Status = "satisfied"
		}
	}
	m.tasks[task.ID] = task
	m.evidence = append(m.evidence, command)
	return nil
}

func (m *memoryTasks) FinalizeCompleted(_ context.Context, id int64, _, _ string) (TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[id]
	task.Status = "completed"
	m.tasks[id] = task
	m.finalized = append(m.finalized, id)
	return task, nil
}

func (m *memoryTasks) FinalizeFailed(_ context.Context, id int64, code, reason, _, _ string) (TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[id]
	task.Status = "failed"
	task.ReasonCode = code
	task.Reason = reason
	m.tasks[id] = task
	return task, nil
}

func (m *memoryTasks) BlockTask(_ context.Context, id int64, code, reason, _, _ string) (TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[id]
	task.Status = "blocked"
	task.ReasonCode = code
	task.Reason = reason
	m.tasks[id] = task
	m.blocked = append(m.blocked, id)
	return task, nil
}

func (m *memoryTasks) UnblockTask(_ context.Context, id int64, _, _ string) (TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[id]
	task.Status = "ready"
	task.ReasonCode = ""
	task.Reason = ""
	m.tasks[id] = task
	return task, nil
}

func (m *memoryTasks) Reconcile(context.Context, int) error { return nil }

type fakeContexts struct{ calls int }

func (f *fakeContexts) Build(context.Context, ContextRequest) (int64, error) {
	f.calls++
	return int64(f.calls), nil
}

type fakeAssignments struct{ err error }

func (f fakeAssignments) ResolveAssignment(_ context.Context, taskID, attemptID int64, role string) (AssignmentRef, error) {
	if f.err != nil {
		return AssignmentRef{}, f.err
	}
	return AssignmentRef{
		ID: 1, OrganizationRevisionID: 7, TaskID: taskID, AttemptID: attemptID,
		SubjectRoleID: role, ExecutionPrincipalID: 9, DispatchActorRoleID: "ingenieria_ia/code-runner",
		ValidUntil: time.Now().Add(time.Hour),
	}, nil
}

type fakeModels struct {
	invocations map[string][]InvocationRecord
	results     map[int64]InvocationResult
	ensure      InvocationRecord
	ensureCalls int
}

func newFakeModels() *fakeModels {
	return &fakeModels{invocations: map[string][]InvocationRecord{}, results: map[int64]InvocationResult{}}
}

func invocationKey(taskID, attemptID int64) string { return fmt.Sprintf("%d/%d", taskID, attemptID) }

func (f *fakeModels) EnsureInvocation(_ context.Context, command InvocationCommand) (InvocationRecord, bool, error) {
	f.ensureCalls++
	value := f.ensure
	if value.ID == 0 {
		value.ID = 500
	}
	value.TaskID = command.TaskID
	value.AttemptID = command.AttemptID
	value.SubjectRoleID = command.SubjectRoleID
	value.CorrelationID = command.CorrelationID
	value.CausationID = command.CausationID
	f.invocations[invocationKey(command.TaskID, command.AttemptID)] = []InvocationRecord{value}
	return value, false, nil
}

func (f *fakeModels) GetInvocation(_ context.Context, id int64) (InvocationRecord, error) {
	for _, values := range f.invocations {
		for _, value := range values {
			if value.ID == id {
				return value, nil
			}
		}
	}
	return InvocationRecord{}, errors.New("not found")
}

func (f *fakeModels) FindTaskAttemptInvocations(_ context.Context, taskID, attemptID int64) ([]InvocationRecord, error) {
	return append([]InvocationRecord(nil), f.invocations[invocationKey(taskID, attemptID)]...), nil
}

func (f *fakeModels) GetResult(_ context.Context, id int64) (InvocationResult, error) {
	value, ok := f.results[id]
	if !ok {
		return InvocationResult{}, errors.New("result not found")
	}
	return value, nil
}

type fakeCompletion struct {
	verdict CompletionVerdict
	calls   int
}

func (f *fakeCompletion) Verify(context.Context, int64, int64) (CompletionResult, error) {
	f.calls++
	return CompletionResult{Verdict: f.verdict, Detail: "test"}, nil
}

type fakeDecisionRecorder struct {
	records []AttemptDecisionRecord
	err     error
}

func (f *fakeDecisionRecorder) RecordAttemptDecision(_ context.Context, record AttemptDecisionRecord) error {
	if f.err != nil {
		return f.err
	}
	f.records = append(f.records, record)
	return nil
}
