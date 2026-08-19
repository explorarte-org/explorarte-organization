package executive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// defaultHarnessBody is a valid, schema-shaped answer for the department-plan
// purpose most unit tests drive. Tests that care about the content set their
// own.
var defaultHarnessBody = json.RawMessage(`{"schema_version":"department-plan/v1","department_id":"ingenieria_ia","tasks":[],"review_criteria":[],"unresolved":[]}`)

// fakeHarness stands in for the Execution Harness. It records what it was
// asked to run, persists the durable invocation a real run would leave behind,
// and returns a scripted verdict.
type fakeHarness struct {
	mu          sync.Mutex
	models      *fakeModels
	body        json.RawMessage
	toolIntents int
	// invocationStatus is the durable Model Runtime status the run leaves
	// behind. "" means no invocation row at all (the run never reached the
	// provider).
	invocationStatus string
	failure          HarnessRunFailure
	execErr          error
	// duringRun runs while the "provider call" is in flight, which is where a
	// lease can be lost underneath a run that is about to succeed.
	duringRun func(command HarnessRunCommand)
	calls     int
	commands  []HarnessRunCommand
}

func newFakeHarness(models *fakeModels) *fakeHarness {
	return &fakeHarness{models: models, body: defaultHarnessBody, invocationStatus: "succeeded"}
}

func (h *fakeHarness) Execute(_ context.Context, command HarnessRunCommand) (HarnessRunOutcome, error) {
	h.mu.Lock()
	h.calls++
	h.commands = append(h.commands, command)
	models, body, status, toolIntents := h.models, h.body, h.invocationStatus, h.toolIntents
	failure, execErr, during := h.failure, h.execErr, h.duringRun
	h.mu.Unlock()

	var invocation InvocationRecord
	if status != "" {
		recordedBody := body
		if status != "succeeded" {
			recordedBody = nil
		}
		invocation = models.recordDurableInvocation(command, status, recordedBody, toolIntents)
	}
	if during != nil {
		during(command)
	}
	if execErr != nil {
		return HarnessRunOutcome{}, execErr
	}
	if failure != HarnessFailureNone {
		return HarnessRunOutcome{
			Status: HarnessRunFailed, Failure: failure, InvocationID: invocation.ID,
			Retryable:         failure == HarnessFailureAuthorityUnavailable,
			TerminationReason: string(failure),
		}, nil
	}
	return HarnessRunOutcome{
		Status: HarnessRunSucceeded, FinalOutput: string(body), InvocationID: invocation.ID,
	}, nil
}

func (h *fakeHarness) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func (h *fakeHarness) lastCommand() HarnessRunCommand {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.commands) == 0 {
		return HarnessRunCommand{}
	}
	return h.commands[len(h.commands)-1]
}

// countingBudget is the pre-Harness model-call gate. It delegates to whatever
// the test wants and records that it was consulted BEFORE the harness ran.
type countingBudget struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (b *countingBudget) AuthorizeModelCall(context.Context, ModelCallBudgetRequest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	return b.err
}

func (b *countingBudget) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

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
		// TaskClass and AssignedUnitID are durable columns in the real store.
		// Dropping them here made the fake claim every task was classless,
		// which any phase that selects by task class cannot work against.
		TaskClass: command.TaskClass, AssignedUnitID: unitOf(command.AssignedRoleID),
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

// unitOf mirrors how the registry derives a role's unit from its id.
func unitOf(roleID string) string {
	if index := strings.Index(roleID, "/"); index > 0 {
		return roleID[:index]
	}
	return ""
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

type fakeContexts struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeContexts) Build(context.Context, ContextRequest) (ContextSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	content := fmt.Sprintf("context snapshot %d", f.calls)
	digest := sha256.Sum256([]byte(content))
	return ContextSnapshot{ID: int64(f.calls), Version: "1", Digest: hex.EncodeToString(digest[:]), Content: content}, nil
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

// fakeModels is Model Runtime's READ side. It deliberately has no method that
// creates an invocation: if the productive Executive ever grew a bypass, it
// would have nothing here to call.
type fakeModels struct {
	mu          sync.Mutex
	nextID      int64
	invocations map[string][]InvocationRecord
	results     map[int64]InvocationResult
}

func newFakeModels() *fakeModels {
	return &fakeModels{nextID: 500, invocations: map[string][]InvocationRecord{}, results: map[int64]InvocationResult{}}
}

func invocationKey(taskID, attemptID int64) string { return fmt.Sprintf("%d/%d", taskID, attemptID) }

// recordDurableInvocation is what the real Model Runtime does underneath a
// Harness turn: it persists an invocation row for the attempt and, when the
// provider answered, its result.
func (f *fakeModels) recordDurableInvocation(command HarnessRunCommand, status string, body json.RawMessage, toolIntents int) InvocationRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := invocationKey(command.TaskID, command.AttemptID)
	if existing := f.invocations[key]; len(existing) > 0 {
		return existing[0]
	}
	f.nextID++
	invocation := InvocationRecord{
		ID: f.nextID, TaskID: command.TaskID, AttemptID: command.AttemptID, SubjectRoleID: command.RoleID,
		Status: status, CorrelationID: command.CorrelationID, CausationID: command.CausationID,
	}
	f.invocations[key] = []InvocationRecord{invocation}
	if len(body) > 0 {
		hash := sha256.Sum256(body)
		f.results[invocation.ID] = InvocationResult{
			InvocationID: invocation.ID, JSONOutput: append(json.RawMessage(nil), body...),
			ToolIntents: toolIntents, ResponseHash: hex.EncodeToString(hash[:]), ResponseBytes: len(body),
		}
	}
	return invocation
}

func (f *fakeModels) setInvocations(taskID, attemptID int64, values ...InvocationRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invocations[invocationKey(taskID, attemptID)] = values
}

func (f *fakeModels) setResult(id int64, result InvocationResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[id] = result
}

func (f *fakeModels) invocationCount(taskID, attemptID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.invocations[invocationKey(taskID, attemptID)])
}

func (f *fakeModels) GetInvocation(_ context.Context, id int64) (InvocationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]InvocationRecord(nil), f.invocations[invocationKey(taskID, attemptID)]...), nil
}

func (f *fakeModels) GetResult(_ context.Context, id int64) (InvocationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
