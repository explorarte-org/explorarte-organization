package workflowruntime_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging/topologyfixture"
	"github.com/Mireuz13/explorarte-organization/internal/workflowruntime"
	"github.com/Mireuz13/explorarte-organization/internal/workflowruntime/accessadapter"
)

const correlationID = "workflow:test-001"

type fakeTasks struct {
	nextID int64
	tasks  map[int64]workflowruntime.Snapshot
}

func newFakeTasks() *fakeTasks {
	return &fakeTasks{nextID: 1, tasks: map[int64]workflowruntime.Snapshot{}}
}

func (f *fakeTasks) Initiate(_ context.Context, work workflowruntime.WorkRequest, actor workflowruntime.Actor) (workflowruntime.Snapshot, bool, error) {
	for _, existing := range f.tasks {
		if existing.OrganizationID == work.OrganizationID && eventKey(existing) == work.IdempotencyKey {
			return clone(existing), true, nil
		}
	}
	id := f.nextID
	f.nextID++
	snapshot := workflowruntime.Snapshot{
		TaskID: id, OrganizationID: work.OrganizationID, Status: workflowruntime.StatusReady,
		AssignedRoleID: work.AssignedRoleID, AssignedUnitID: strings.Split(work.AssignedRoleID, "/")[0],
		RequestedByRoleID: work.RequestedByRoleID, CorrelationID: work.CorrelationID, CausationID: work.CausationID,
	}
	for index, spec := range work.Requirements {
		snapshot.Requirements = append(snapshot.Requirements, workflowruntime.Requirement{
			ID: int64(index + 1), Key: spec.Key, Type: spec.Type, Description: spec.Description, Required: spec.Required, Status: "pending",
		})
	}
	f.appendEvent(&snapshot, "task.created:"+work.IdempotencyKey, "", workflowruntime.StatusReady, actor)
	f.tasks[id] = snapshot
	return clone(snapshot), false, nil
}

func eventKey(snapshot workflowruntime.Snapshot) string {
	if len(snapshot.Events) == 0 {
		return ""
	}
	return strings.TrimPrefix(snapshot.Events[0].EventType, "task.created:")
}

func (f *fakeTasks) Observe(_ context.Context, id int64) (workflowruntime.Snapshot, error) {
	value, ok := f.tasks[id]
	if !ok {
		return workflowruntime.Snapshot{}, errors.New("task not found")
	}
	return clone(value), nil
}

func (f *fakeTasks) StartExecution(_ context.Context, command workflowruntime.ExecutionCommand) (workflowruntime.Snapshot, error) {
	value := f.tasks[command.TaskID]
	from := value.Status
	value.Status = workflowruntime.StatusRunning
	value.Attempts = append(value.Attempts, workflowruntime.Attempt{ID: command.AttemptID, Ordinal: len(value.Attempts) + 1, State: "running"})
	f.appendEvent(&value, "attempt.started", from, value.Status, command.Actor)
	f.tasks[value.TaskID] = value
	return clone(value), nil
}

func (f *fakeTasks) RecordOutcome(_ context.Context, command workflowruntime.OutcomeCommand) (workflowruntime.Snapshot, error) {
	value := f.tasks[command.TaskID]
	from := value.Status
	switch command.Outcome {
	case workflowruntime.OutcomeSucceeded:
		value.Status = workflowruntime.StatusAwaitingVerification
	case workflowruntime.OutcomeRetryableFailure:
		value.Status = workflowruntime.StatusRetryWait
	case workflowruntime.OutcomeNonRetryableFailure:
		value.Status = workflowruntime.StatusFailed
	case workflowruntime.OutcomeCancelled:
		value.Status = workflowruntime.StatusCancelled
	}
	if len(value.Attempts) > 0 {
		value.Attempts[len(value.Attempts)-1].State = "finished"
	}
	f.appendEvent(&value, "attempt."+string(command.Outcome), from, value.Status, command.Actor)
	f.tasks[value.TaskID] = value
	return clone(value), nil
}

func (f *fakeTasks) RecordEvidence(_ context.Context, command workflowruntime.EvidenceCommand) (workflowruntime.Snapshot, error) {
	value := f.tasks[command.TaskID]
	evidence := workflowruntime.Evidence{
		ID: int64(len(value.Evidence) + 1), RequirementID: command.RequirementID, Type: command.Type,
		Reference: command.Reference, Digest: command.Digest, RecordedBy: command.Actor.DurableActorID(), Metadata: command.Metadata,
	}
	value.Evidence = append(value.Evidence, evidence)
	if command.Satisfies {
		for index := range value.Requirements {
			if value.Requirements[index].ID == command.RequirementID {
				value.Requirements[index].Status = "satisfied"
			}
		}
	}
	f.appendEvent(&value, "evidence.recorded", value.Status, value.Status, command.Actor)
	f.tasks[value.TaskID] = value
	return clone(value), nil
}

func (f *fakeTasks) FinalizeCompleted(_ context.Context, command workflowruntime.CompleteCommand) (workflowruntime.Snapshot, error) {
	value := f.tasks[command.TaskID]
	if value.Status.Terminal() {
		return workflowruntime.Snapshot{}, workflowruntime.ErrTerminalReplay
	}
	from := value.Status
	value.Status = workflowruntime.StatusCompleted
	f.appendEvent(&value, "task.completed", from, value.Status, command.Actor)
	f.tasks[value.TaskID] = value
	return clone(value), nil
}

func (f *fakeTasks) Block(_ context.Context, request workflowruntime.BranchRequest, action workflowruntime.BranchAction) (workflowruntime.Snapshot, error) {
	value := f.tasks[request.TaskID]
	from := value.Status
	value.Status = workflowruntime.StatusBlocked
	f.appendEvent(&value, "task.blocked:"+action.ReasonCode, from, value.Status, request.Actor)
	f.tasks[value.TaskID] = value
	return clone(value), nil
}

func (f *fakeTasks) appendEvent(snapshot *workflowruntime.Snapshot, eventType string, from, to workflowruntime.Status, actor workflowruntime.Actor) {
	event := workflowruntime.DurableEvent{
		ID: int64(len(snapshot.Events) + 1), Sequence: int64(len(snapshot.Events) + 1), EventType: eventType,
		FromStatus: from, ToStatus: to, ActorType: actor.ActorType, ActorID: actor.DurableActorID(),
		CorrelationID: snapshot.CorrelationID, CausationID: snapshot.CausationID,
		OccurredAt: time.Date(2026, 8, 16, 12, 0, len(snapshot.Events), 0, time.UTC),
	}
	snapshot.Events = append(snapshot.Events, event)
	last := event
	snapshot.LastTransition = &last
}

func clone(value workflowruntime.Snapshot) workflowruntime.Snapshot {
	copyValue := value
	copyValue.Requirements = append([]workflowruntime.Requirement(nil), value.Requirements...)
	copyValue.Evidence = append([]workflowruntime.Evidence(nil), value.Evidence...)
	copyValue.Attempts = append([]workflowruntime.Attempt(nil), value.Attempts...)
	copyValue.Events = append([]workflowruntime.DurableEvent(nil), value.Events...)
	if len(copyValue.Events) > 0 {
		last := copyValue.Events[len(copyValue.Events)-1]
		copyValue.LastTransition = &last
	}
	return copyValue
}

type fakeCompletion struct{}

func (fakeCompletion) Verify(_ context.Context, taskID, attemptID int64) (workflowruntime.CompletionDecision, error) {
	return workflowruntime.CompletionDecision{TaskID: taskID, AttemptID: attemptID, Disposition: workflowruntime.CompletionAllow, Provenance: "fake:independent"}, nil
}

type requirementCompletion struct{ tasks *fakeTasks }

func (g requirementCompletion) Verify(_ context.Context, taskID, attemptID int64) (workflowruntime.CompletionDecision, error) {
	value := g.tasks.tasks[taskID]
	for _, requirement := range value.Requirements {
		if requirement.Required && requirement.Status != "satisfied" {
			return workflowruntime.CompletionDecision{
				TaskID: taskID, AttemptID: attemptID, Disposition: workflowruntime.CompletionDeny,
				Reason: "required obligation is not satisfied", Provenance: "fake:requirements",
			}, nil
		}
	}
	return workflowruntime.CompletionDecision{TaskID: taskID, AttemptID: attemptID, Disposition: workflowruntime.CompletionAllow, Provenance: "fake:requirements"}, nil
}

type fakeDecision struct{ action workflowruntime.BranchAction }

func (f fakeDecision) Evaluate(_ context.Context, request workflowruntime.BranchRequest) (workflowruntime.BranchDecision, error) {
	return workflowruntime.BranchDecision{
		TaskID: request.TaskID, CorrelationID: request.CorrelationID, CausationID: request.CausationID,
		SelectedBranch: "branch:blocked", DecisionRef: "decision_graph_run:7", Action: f.action,
	}, nil
}

type allowAllAuthorization struct{}

func (allowAllAuthorization) AuthorizeInitiation(context.Context, workflowruntime.Actor, workflowruntime.WorkRequest) error {
	return nil
}

func (allowAllAuthorization) AuthorizeTaskAccess(context.Context, workflowruntime.Actor, workflowruntime.Snapshot, workflowruntime.TaskAccess) error {
	return nil
}

type fakePrincipalReader map[string]accessadapter.PrincipalIdentity

func (f fakePrincipalReader) GetPrincipal(_ context.Context, id string) (accessadapter.PrincipalIdentity, error) {
	principal, ok := f[id]
	if !ok {
		return accessadapter.PrincipalIdentity{}, errors.New("principal not found")
	}
	return principal, nil
}

func activePrincipal(id, organizationID, roleID string) accessadapter.PrincipalIdentity {
	return accessadapter.PrincipalIdentity{ID: id, OrganizationID: organizationID, RoleID: roleID, Active: true}
}

func strictAuthorization(t *testing.T, principals fakePrincipalReader) workflowruntime.AuthorizationPort {
	t.Helper()
	authorization, err := accessadapter.New(topologyfixture.NewReader(), principals)
	if err != nil {
		t.Fatal(err)
	}
	return authorization
}

type principal struct {
	organizationID string
	roleID         string
	active         bool
}

type topologyCoordination struct {
	principals map[string]principal
	records    []workflowruntime.CoordinationRecord
	keys       map[string]int64
}

func newTopologyCoordination() *topologyCoordination {
	return &topologyCoordination{principals: map[string]principal{}, keys: map[string]int64{}}
}

func (f *topologyCoordination) Send(ctx context.Context, command workflowruntime.CoordinationCommand, sender, recipient workflowruntime.Snapshot) (workflowruntime.CoordinationRecord, bool, error) {
	bound, ok := f.principals[command.Actor.ExecutionPrincipalID]
	if !ok || !bound.active || bound.organizationID != command.OrganizationID || bound.roleID != command.Actor.RoleID || bound.roleID != sender.AssignedRoleID {
		return workflowruntime.CoordinationRecord{}, false, errors.New("execution principal denied")
	}
	validator := agentmessaging.NewTopologyValidator(topologyfixture.NewReader(), command.OrganizationID)
	if err := validator.ValidateEdge(ctx, sender.AssignedRoleID, recipient.AssignedRoleID); err != nil {
		return workflowruntime.CoordinationRecord{}, false, err
	}
	if id, reused := f.keys[command.IdempotencyKey]; reused {
		return f.records[id-1], true, nil
	}
	record := workflowruntime.CoordinationRecord{
		ID: int64(len(f.records) + 1), OrganizationID: command.OrganizationID,
		SenderRoleID: sender.AssignedRoleID, SenderTaskID: sender.TaskID,
		RecipientRoleID: recipient.AssignedRoleID, RecipientTaskID: recipient.TaskID,
		CorrelationID: command.CorrelationID, CausationID: command.CausationID,
		Kind: command.Kind, IdempotencyKey: command.IdempotencyKey,
		DurableProvenance: fmt.Sprintf("agent_messages:%d", len(f.records)+1),
	}
	f.records = append(f.records, record)
	f.keys[command.IdempotencyKey] = record.ID
	return record, false, nil
}

type fakeExecutive struct{ tasks *fakeTasks }

func (f fakeExecutive) Start(ctx context.Context, request workflowruntime.GoalRequest) (workflowruntime.ExecutiveStart, error) {
	correlation := "executive:" + request.IdempotencyKey
	root, reused, err := f.tasks.Initiate(ctx, workflowruntime.WorkRequest{
		OrganizationID: request.Actor.OrganizationID, RequestedByRoleID: request.Actor.RoleID,
		AssignedRoleID: topologyfixture.RoleCEO, IdempotencyKey: request.IdempotencyKey,
		Title: "Owner goal", Instructions: request.Goal, AcceptanceCriteria: goalCriterionTexts(request.AcceptanceCriteria),
		MaxAttempts: 3, CorrelationID: correlation, Requirements: request.Requirements,
	}, request.Actor)
	return workflowruntime.ExecutiveStart{RootTaskID: root.TaskID, CorrelationID: correlation, LegacyState: "accepted", Reused: reused}, err
}

func newRuntime(t *testing.T, tasks *fakeTasks, completion workflowruntime.CompletionPort, coordination *topologyCoordination) *workflowruntime.Runtime {
	t.Helper()
	runtime, err := workflowruntime.New(tasks, completion, fakeDecision{action: workflowruntime.BranchAction{
		Kind: workflowruntime.BranchActionBlock, ReasonCode: "branch_blocked", Reason: "selected branch requires more evidence",
	}}, allowAllAuthorization{}, coordination, fakeExecutive{tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func newRuntimeWithAuthorization(t *testing.T, tasks *fakeTasks, authorization workflowruntime.AuthorizationPort) *workflowruntime.Runtime {
	t.Helper()
	runtime, err := workflowruntime.New(tasks, fakeCompletion{}, fakeDecision{action: workflowruntime.BranchAction{
		Kind: workflowruntime.BranchActionBlock, ReasonCode: "branch_blocked", Reason: "selected branch requires more evidence",
	}}, authorization, newTopologyCoordination(), fakeExecutive{tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestRuntimeConstructionFailsClosedWithoutAuthorization(t *testing.T) {
	tasks := newFakeTasks()
	_, err := workflowruntime.New(tasks, fakeCompletion{}, fakeDecision{action: workflowruntime.BranchAction{
		Kind: workflowruntime.BranchActionBlock, ReasonCode: "blocked", Reason: "blocked",
	}}, nil, newTopologyCoordination(), fakeExecutive{tasks: tasks})
	if err == nil {
		t.Fatal("runtime accepted a nil authorization port")
	}
}

func actor(role, principalID string) workflowruntime.Actor {
	return workflowruntime.Actor{OrganizationID: topologyfixture.OrganizationID, RoleID: role, ExecutionPrincipalID: principalID, ActorType: "execution_principal", ActorID: principalID}
}

func mustInitiate(t *testing.T, runtime *workflowruntime.Runtime, request workflowruntime.WorkRequest, actor workflowruntime.Actor) workflowruntime.Snapshot {
	t.Helper()
	value, _, err := runtime.Initiate(context.Background(), workflowruntime.InitiateCommand{Actor: actor, Work: request})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func childRequest(requester, assignee, key, causation string, requirements ...workflowruntime.RequirementSpec) workflowruntime.WorkRequest {
	return workflowruntime.WorkRequest{
		OrganizationID: topologyfixture.OrganizationID, RequestedByRoleID: requester, AssignedRoleID: assignee,
		IdempotencyKey: key, Title: key, Instructions: "perform bounded organizational work",
		AcceptanceCriteria: []string{"return evidence"}, MaxAttempts: 3,
		CorrelationID: correlationID, CausationID: causation, Requirements: requirements,
	}
}

func TestInitiateAuthorizesAssignmentBeforeDurableTaskCreation(t *testing.T) {
	tests := []struct {
		name      string
		actorRole string
		assignee  string
		principal accessadapter.PrincipalIdentity
	}{
		{
			name: "worker to peer worker", actorRole: topologyfixture.RoleEngineeringA,
			assignee:  topologyfixture.RoleEngineeringB,
			principal: activePrincipal("1", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringA),
		},
		{
			name: "worker direct to CEO", actorRole: topologyfixture.RoleEngineeringA,
			assignee:  topologyfixture.RoleCEO,
			principal: activePrincipal("1", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringA),
		},
		{
			name: "leader to another department worker", actorRole: topologyfixture.RoleEngineeringLead,
			assignee:  topologyfixture.RoleFinanceWorker,
			principal: activePrincipal("1", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringLead),
		},
		{
			name: "disabled principal", actorRole: topologyfixture.RoleEngineeringLead,
			assignee: topologyfixture.RoleEngineeringA,
			principal: func() accessadapter.PrincipalIdentity {
				value := activePrincipal("1", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringLead)
				value.Active = false
				return value
			}(),
		},
		{
			name: "cross organization principal", actorRole: topologyfixture.RoleEngineeringLead,
			assignee:  topologyfixture.RoleEngineeringA,
			principal: activePrincipal("1", "other-org", topologyfixture.RoleEngineeringLead),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tasks := newFakeTasks()
			runtime := newRuntimeWithAuthorization(t, tasks, strictAuthorization(t, fakePrincipalReader{"1": test.principal}))
			work := childRequest(test.actorRole, test.assignee, "unauthorized", "cause")
			_, _, err := runtime.Initiate(context.Background(), workflowruntime.InitiateCommand{
				Actor: actor(test.actorRole, "1"), Work: work,
			})
			if !errors.Is(err, workflowruntime.ErrAuthorizationDenied) {
				t.Fatalf("Initiate err=%v, want authorization denial", err)
			}
			if len(tasks.tasks) != 0 || tasks.nextID != 1 {
				t.Fatalf("denied initiation created durable work: tasks=%d nextID=%d", len(tasks.tasks), tasks.nextID)
			}
		})
	}
}

func TestInitiateAllowsSameRoleAndAuthorizedDelegation(t *testing.T) {
	principals := fakePrincipalReader{
		"1": activePrincipal("1", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringA),
		"2": activePrincipal("2", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringLead),
	}
	tasks := newFakeTasks()
	runtime := newRuntimeWithAuthorization(t, tasks, strictAuthorization(t, principals))

	if _, _, err := runtime.Initiate(context.Background(), workflowruntime.InitiateCommand{
		Actor: actor(topologyfixture.RoleEngineeringA, "1"),
		Work:  childRequest(topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringA, "same-role-authorized", "cause"),
	}); err != nil {
		t.Fatalf("same-role initiation: %v", err)
	}
	if _, _, err := runtime.Initiate(context.Background(), workflowruntime.InitiateCommand{
		Actor: actor(topologyfixture.RoleEngineeringLead, "2"),
		Work:  childRequest(topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringB, "delegation-authorized", "cause"),
	}); err != nil {
		t.Fatalf("authorized delegation: %v", err)
	}
	if len(tasks.tasks) != 2 {
		t.Fatalf("created tasks=%d want 2", len(tasks.tasks))
	}
}

func TestObserveUsesExplicitRoleAndPrincipalPolicy(t *testing.T) {
	tasks := newFakeTasks()
	target, _, err := tasks.Initiate(context.Background(), childRequest(
		topologyfixture.RoleCEO, topologyfixture.RoleEngineeringA, "observable", "cause",
	), actor(topologyfixture.RoleCEO, "seed"))
	if err != nil {
		t.Fatal(err)
	}
	disabled := activePrincipal("6", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringA)
	disabled.Active = false
	principals := fakePrincipalReader{
		"1": activePrincipal("1", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringA),
		"2": activePrincipal("2", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringLead),
		"3": activePrincipal("3", topologyfixture.OrganizationID, topologyfixture.RoleCEO),
		"4": activePrincipal("4", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringB),
		"5": activePrincipal("5", topologyfixture.OrganizationID, topologyfixture.RoleFinanceLead),
		"6": disabled,
		"7": activePrincipal("7", "other-org", topologyfixture.RoleEngineeringA),
	}
	runtime := newRuntimeWithAuthorization(t, tasks, strictAuthorization(t, principals))
	tests := []struct {
		name      string
		actor     workflowruntime.Actor
		wantAllow bool
	}{
		{"assignee", actor(topologyfixture.RoleEngineeringA, "1"), true},
		{"own department leader", actor(topologyfixture.RoleEngineeringLead, "2"), true},
		{"CEO descendant visibility", actor(topologyfixture.RoleCEO, "3"), true},
		{"peer worker", actor(topologyfixture.RoleEngineeringB, "4"), false},
		{"other department leader", actor(topologyfixture.RoleFinanceLead, "5"), false},
		{"disabled principal", actor(topologyfixture.RoleEngineeringA, "6"), false},
		{"cross organization principal", actor(topologyfixture.RoleEngineeringA, "7"), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observed, observeErr := runtime.Observe(context.Background(), workflowruntime.ObserveCommand{Actor: test.actor, TaskID: target.TaskID})
			if test.wantAllow {
				if observeErr != nil || observed.TaskID != target.TaskID {
					t.Fatalf("Observe=%+v err=%v", observed, observeErr)
				}
				return
			}
			if !errors.Is(observeErr, workflowruntime.ErrAuthorizationDenied) || !reflect.DeepEqual(observed, workflowruntime.Snapshot{}) {
				t.Fatalf("unauthorized Observe leaked snapshot=%+v err=%v", observed, observeErr)
			}
		})
	}
}

func TestMutationRejectsInactivePrincipalWithoutChangingTask(t *testing.T) {
	tasks := newFakeTasks()
	target, _, err := tasks.Initiate(context.Background(), childRequest(
		topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringA, "inactive-mutation", "cause",
	), actor(topologyfixture.RoleEngineeringA, "seed"))
	if err != nil {
		t.Fatal(err)
	}
	disabled := activePrincipal("1", topologyfixture.OrganizationID, topologyfixture.RoleEngineeringA)
	disabled.Active = false
	runtime := newRuntimeWithAuthorization(t, tasks, strictAuthorization(t, fakePrincipalReader{"1": disabled}))
	_, err = runtime.StartExecution(context.Background(), workflowruntime.ExecutionCommand{
		Actor: actor(topologyfixture.RoleEngineeringA, "1"), TaskID: target.TaskID, AttemptID: 1,
		LeaseToken: "lease", CorrelationID: correlationID, CausationID: "cause",
	})
	if !errors.Is(err, workflowruntime.ErrAuthorizationDenied) {
		t.Fatalf("StartExecution err=%v, want authorization denial", err)
	}
	if got := tasks.tasks[target.TaskID]; got.Status != workflowruntime.StatusReady || len(got.Events) != 1 {
		t.Fatalf("denied mutation changed task: %+v", got)
	}
}

func TestWorkflowRuntimeComposesDurableMultiHopFlow(t *testing.T) {
	tasks := newFakeTasks()
	coordination := newTopologyCoordination()
	coordination.principals = map[string]principal{
		"p-ceo":    {topologyfixture.OrganizationID, topologyfixture.RoleCEO, true},
		"p-leader": {topologyfixture.OrganizationID, topologyfixture.RoleEngineeringLead, true},
		"p-worker": {topologyfixture.OrganizationID, topologyfixture.RoleEngineeringA, true},
	}
	runtime := newRuntime(t, tasks, requirementCompletion{tasks: tasks}, coordination)

	started, err := runtime.StartGoal(context.Background(), workflowruntime.GoalRequest{
		Actor: actor(topologyfixture.RoleOwner, "p-owner"), Goal: "Ship a verified result",
		AcceptanceCriteria: []workflowruntime.GoalAcceptanceCriterion{{Text: "evidence exists", Phase: "design"}}, IdempotencyKey: "goal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	root := started.Snapshot
	if root.Status != workflowruntime.StatusReady || root.CorrelationID != started.Executive.CorrelationID {
		t.Fatalf("root=%+v", root)
	}
	// Use the executive-generated correlation for every descendant.
	rootCorrelation := root.CorrelationID

	leaderRequest := childRequest(topologyfixture.RoleCEO, topologyfixture.RoleEngineeringLead, "leader-work", fmt.Sprintf("task:%d", root.TaskID))
	leaderRequest.CorrelationID = rootCorrelation
	leader := mustInitiate(t, runtime, leaderRequest, actor(topologyfixture.RoleCEO, "p-ceo"))
	if _, _, err = runtime.Coordinate(context.Background(), workflowruntime.CoordinationCommand{
		Actor: actor(topologyfixture.RoleCEO, "p-ceo"), OrganizationID: topologyfixture.OrganizationID,
		SenderTaskID: root.TaskID, RecipientTaskID: leader.TaskID, CorrelationID: rootCorrelation,
		CausationID: leader.CausationID, Kind: workflowruntime.CoordinationDelegation, IdempotencyKey: "delegate-leader",
	}); err != nil {
		t.Fatal(err)
	}

	workerRequest := childRequest(topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringA, "worker-work", fmt.Sprintf("task:%d", leader.TaskID),
		workflowruntime.RequirementSpec{Key: "result", Type: "result", Description: "worker evidence", Required: true})
	workerRequest.CorrelationID = rootCorrelation
	worker := mustInitiate(t, runtime, workerRequest, actor(topologyfixture.RoleEngineeringLead, "p-leader"))
	if _, _, err = runtime.Coordinate(context.Background(), workflowruntime.CoordinationCommand{
		Actor: actor(topologyfixture.RoleEngineeringLead, "p-leader"), OrganizationID: topologyfixture.OrganizationID,
		SenderTaskID: leader.TaskID, RecipientTaskID: worker.TaskID, CorrelationID: rootCorrelation,
		CausationID: worker.CausationID, Kind: workflowruntime.CoordinationDelegation, IdempotencyKey: "delegate-worker",
	}); err != nil {
		t.Fatal(err)
	}

	workerActor := actor(topologyfixture.RoleEngineeringA, "p-worker")
	execution := workflowruntime.ExecutionCommand{Actor: workerActor, TaskID: worker.TaskID, AttemptID: 31, LeaseToken: "lease-31", CorrelationID: rootCorrelation, CausationID: worker.CausationID}
	if _, err = runtime.StartExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}
	if len(coordination.records) != 2 {
		t.Fatalf("same-role start emitted coordination: %d", len(coordination.records))
	}
	if _, err = runtime.RecordEvidence(context.Background(), workflowruntime.EvidenceCommand{
		Actor: workerActor, TaskID: worker.TaskID, RequirementID: 1, Type: "result", Reference: "artifact:worker-result",
		Satisfies: true, CorrelationID: rootCorrelation, CausationID: worker.CausationID,
	}); err != nil {
		t.Fatal(err)
	}
	awaiting, err := runtime.RecordOutcome(context.Background(), workflowruntime.OutcomeCommand{ExecutionCommand: execution, Outcome: workflowruntime.OutcomeSucceeded, Summary: "done"})
	if err != nil || awaiting.Status != workflowruntime.StatusAwaitingVerification || awaiting.Completion.Disposition != workflowruntime.CompletionAllow {
		t.Fatalf("awaiting=%+v err=%v", awaiting, err)
	}
	completed, err := runtime.Complete(context.Background(), workflowruntime.CompleteCommand{
		Actor: workerActor, TaskID: worker.TaskID, AttemptID: 31, CorrelationID: rootCorrelation, CausationID: worker.CausationID,
	})
	if err != nil || completed.Snapshot.Status != workflowruntime.StatusCompleted {
		t.Fatalf("complete=%+v err=%v", completed, err)
	}
	if _, _, err = runtime.Coordinate(context.Background(), workflowruntime.CoordinationCommand{
		Actor: workerActor, OrganizationID: topologyfixture.OrganizationID,
		SenderTaskID: worker.TaskID, RecipientTaskID: leader.TaskID, CorrelationID: rootCorrelation,
		CausationID: worker.CausationID, Kind: workflowruntime.CoordinationCompletion, IdempotencyKey: "complete-worker",
	}); err != nil {
		t.Fatal(err)
	}

	leaderActor := actor(topologyfixture.RoleEngineeringLead, "p-leader")
	leaderExecution := workflowruntime.ExecutionCommand{Actor: leaderActor, TaskID: leader.TaskID, AttemptID: 21, LeaseToken: "lease-21", CorrelationID: rootCorrelation, CausationID: leader.CausationID}
	if _, err = runtime.StartExecution(context.Background(), leaderExecution); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.RecordOutcome(context.Background(), workflowruntime.OutcomeCommand{ExecutionCommand: leaderExecution, Outcome: workflowruntime.OutcomeSucceeded}); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Complete(context.Background(), workflowruntime.CompleteCommand{Actor: leaderActor, TaskID: leader.TaskID, AttemptID: 21, CorrelationID: rootCorrelation, CausationID: leader.CausationID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = runtime.Coordinate(context.Background(), workflowruntime.CoordinationCommand{
		Actor: leaderActor, OrganizationID: topologyfixture.OrganizationID,
		SenderTaskID: leader.TaskID, RecipientTaskID: root.TaskID, CorrelationID: rootCorrelation,
		CausationID: leader.CausationID, Kind: workflowruntime.CoordinationCompletion, IdempotencyKey: "complete-leader",
	}); err != nil {
		t.Fatal(err)
	}

	if len(coordination.records) != 4 {
		t.Fatalf("coordination records=%d want 4", len(coordination.records))
	}
	gotEdges := [][2]string{}
	for _, record := range coordination.records {
		gotEdges = append(gotEdges, [2]string{record.SenderRoleID, record.RecipientRoleID})
		if record.CorrelationID != rootCorrelation || record.DurableProvenance == "" {
			t.Fatalf("coordination lost provenance: %+v", record)
		}
	}
	wantEdges := [][2]string{
		{topologyfixture.RoleCEO, topologyfixture.RoleEngineeringLead},
		{topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringA},
		{topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringLead},
		{topologyfixture.RoleEngineeringLead, topologyfixture.RoleCEO},
	}
	if !reflect.DeepEqual(gotEdges, wantEdges) {
		t.Fatalf("edges=%v want=%v", gotEdges, wantEdges)
	}
}

func TestCompletionGateOwnsPermissionButTasksOwnTerminalTruth(t *testing.T) {
	tasks := newFakeTasks()
	coordination := newTopologyCoordination()
	runtime := newRuntime(t, tasks, requirementCompletion{tasks: tasks}, coordination)
	workerActor := actor(topologyfixture.RoleEngineeringA, "p-worker")
	work := mustInitiate(t, runtime, childRequest(topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringA, "completion", "cause",
		workflowruntime.RequirementSpec{Key: "required", Type: "result", Description: "required evidence", Required: true}), workerActor)
	execution := workflowruntime.ExecutionCommand{Actor: workerActor, TaskID: work.TaskID, AttemptID: 1, LeaseToken: "lease", CorrelationID: correlationID, CausationID: "cause"}
	_, _ = runtime.StartExecution(context.Background(), execution)
	_, _ = runtime.RecordOutcome(context.Background(), workflowruntime.OutcomeCommand{ExecutionCommand: execution, Outcome: workflowruntime.OutcomeSucceeded})

	denied, err := runtime.Complete(context.Background(), workflowruntime.CompleteCommand{Actor: workerActor, TaskID: work.TaskID, AttemptID: 1, CorrelationID: correlationID, CausationID: "cause"})
	if err != nil || denied.Decision.Disposition != workflowruntime.CompletionDeny || denied.Snapshot.Status != workflowruntime.StatusAwaitingVerification {
		t.Fatalf("denied=%+v err=%v", denied, err)
	}
	_, _ = runtime.RecordEvidence(context.Background(), workflowruntime.EvidenceCommand{Actor: workerActor, TaskID: work.TaskID, RequirementID: 1, Type: "result", Reference: "artifact:x", Satisfies: true, CorrelationID: correlationID, CausationID: "cause"})
	allowed, err := runtime.Complete(context.Background(), workflowruntime.CompleteCommand{Actor: workerActor, TaskID: work.TaskID, AttemptID: 1, CorrelationID: correlationID, CausationID: "cause"})
	if err != nil || allowed.Decision.Disposition != workflowruntime.CompletionAllow || allowed.Snapshot.Status != workflowruntime.StatusCompleted {
		t.Fatalf("allowed=%+v err=%v", allowed, err)
	}
	if _, err = runtime.Complete(context.Background(), workflowruntime.CompleteCommand{Actor: workerActor, TaskID: work.TaskID, AttemptID: 1, CorrelationID: correlationID, CausationID: "cause"}); !errors.Is(err, workflowruntime.ErrTerminalReplay) {
		t.Fatalf("duplicate terminal transition err=%v", err)
	}
}

func TestCoordinationNegativeAuthorizationMatrix(t *testing.T) {
	cases := []struct {
		name          string
		senderRole    string
		recipientRole string
		principalRole string
		principalOrg  string
		active        bool
	}{
		{"worker to worker", topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringB, topologyfixture.RoleEngineeringA, topologyfixture.OrganizationID, true},
		{"worker direct to CEO", topologyfixture.RoleEngineeringA, topologyfixture.RoleCEO, topologyfixture.RoleEngineeringA, topologyfixture.OrganizationID, true},
		{"leader to other department worker", topologyfixture.RoleEngineeringLead, topologyfixture.RoleFinanceWorker, topologyfixture.RoleEngineeringLead, topologyfixture.OrganizationID, true},
		{"actor role differs from principal", topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringA, topologyfixture.RoleCEO, topologyfixture.OrganizationID, true},
		{"disabled principal", topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringLead, topologyfixture.OrganizationID, false},
		{"cross-org principal", topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringLead, "other-org", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tasks := newFakeTasks()
			coordination := newTopologyCoordination()
			coordination.principals["p"] = principal{testCase.principalOrg, testCase.principalRole, testCase.active}
			runtime := newRuntime(t, tasks, fakeCompletion{}, coordination)
			sender := mustInitiate(t, runtime, childRequest(testCase.senderRole, testCase.senderRole, "sender", "cause-sender"), actor(testCase.senderRole, "sender-creator"))
			recipient := mustInitiate(t, runtime, childRequest(testCase.senderRole, testCase.recipientRole, "recipient", "cause-recipient"), actor(testCase.senderRole, "sender-creator"))
			_, _, err := runtime.Coordinate(context.Background(), workflowruntime.CoordinationCommand{
				Actor: actor(testCase.senderRole, "p"), OrganizationID: topologyfixture.OrganizationID,
				SenderTaskID: sender.TaskID, RecipientTaskID: recipient.TaskID, CorrelationID: correlationID,
				CausationID: recipient.CausationID, Kind: workflowruntime.CoordinationDelegation, IdempotencyKey: "negative",
			})
			if err == nil {
				t.Fatal("unauthorized coordination was allowed")
			}
			if len(coordination.records) != 0 {
				t.Fatalf("denied coordination persisted %d records", len(coordination.records))
			}
		})
	}
}

func TestCoordinationRejectsTaskAndCausalBindingDrift(t *testing.T) {
	tasks := newFakeTasks()
	coordination := newTopologyCoordination()
	coordination.principals["p-ceo"] = principal{topologyfixture.OrganizationID, topologyfixture.RoleCEO, true}
	runtime := newRuntime(t, tasks, fakeCompletion{}, coordination)
	sender := mustInitiate(t, runtime, childRequest(topologyfixture.RoleCEO, topologyfixture.RoleCEO, "sender", "sender-cause"), actor(topologyfixture.RoleCEO, "creator"))
	recipient := mustInitiate(t, runtime, childRequest(topologyfixture.RoleCEO, topologyfixture.RoleEngineeringLead, "recipient", "recipient-cause"), actor(topologyfixture.RoleCEO, "creator"))

	for _, mutate := range []func(*workflowruntime.CoordinationCommand){
		func(c *workflowruntime.CoordinationCommand) { c.CorrelationID = "wrong" },
		func(c *workflowruntime.CoordinationCommand) { c.CausationID = "wrong" },
		func(c *workflowruntime.CoordinationCommand) { c.OrganizationID = "other-org" },
	} {
		command := workflowruntime.CoordinationCommand{
			Actor: actor(topologyfixture.RoleCEO, "p-ceo"), OrganizationID: topologyfixture.OrganizationID,
			SenderTaskID: sender.TaskID, RecipientTaskID: recipient.TaskID, CorrelationID: correlationID,
			CausationID: recipient.CausationID, Kind: workflowruntime.CoordinationDelegation, IdempotencyKey: "binding",
		}
		mutate(&command)
		if _, _, err := runtime.Coordinate(context.Background(), command); err == nil {
			t.Fatal("binding drift was accepted")
		}
	}
	if len(coordination.records) != 0 {
		t.Fatalf("binding drift persisted %d records", len(coordination.records))
	}
}

func TestCoordinationRejectsTaskFromAnotherOrganization(t *testing.T) {
	tasks := newFakeTasks()
	coordination := newTopologyCoordination()
	coordination.principals["p-ceo"] = principal{topologyfixture.OrganizationID, topologyfixture.RoleCEO, true}
	runtime := newRuntime(t, tasks, fakeCompletion{}, coordination)
	sender := mustInitiate(t, runtime, childRequest(topologyfixture.RoleCEO, topologyfixture.RoleCEO, "sender", "sender-cause"), actor(topologyfixture.RoleCEO, "creator"))

	foreign, _, err := tasks.Initiate(context.Background(), workflowruntime.WorkRequest{
		OrganizationID: "other-org", RequestedByRoleID: topologyfixture.RoleCEO,
		AssignedRoleID: topologyfixture.RoleEngineeringLead, IdempotencyKey: "foreign",
		Title: "foreign", Instructions: "foreign", CorrelationID: correlationID,
		CausationID: "foreign-cause",
	}, workflowruntime.Actor{OrganizationID: "other-org", RoleID: topologyfixture.RoleCEO, ActorID: "foreign-creator"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runtime.Coordinate(context.Background(), workflowruntime.CoordinationCommand{
		Actor: actor(topologyfixture.RoleCEO, "p-ceo"), OrganizationID: topologyfixture.OrganizationID,
		SenderTaskID: sender.TaskID, RecipientTaskID: foreign.TaskID, CorrelationID: correlationID,
		CausationID: foreign.CausationID, Kind: workflowruntime.CoordinationDelegation, IdempotencyKey: "cross-org-task",
	})
	if !errors.Is(err, workflowruntime.ErrTaskBinding) {
		t.Fatalf("cross-org task err=%v", err)
	}
	if len(coordination.records) != 0 {
		t.Fatalf("cross-org task persisted %d records", len(coordination.records))
	}
}

func TestRetryAppendsAndNeverRewritesPriorDurableEvents(t *testing.T) {
	tasks := newFakeTasks()
	runtime := newRuntime(t, tasks, fakeCompletion{}, newTopologyCoordination())
	workerActor := actor(topologyfixture.RoleEngineeringA, "p-worker")
	work := mustInitiate(t, runtime, childRequest(topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringA, "retry", "cause"), workerActor)
	execution := workflowruntime.ExecutionCommand{Actor: workerActor, TaskID: work.TaskID, AttemptID: 1, LeaseToken: "lease", CorrelationID: correlationID, CausationID: "cause"}
	_, _ = runtime.StartExecution(context.Background(), execution)
	first, err := runtime.RecordOutcome(context.Background(), workflowruntime.OutcomeCommand{ExecutionCommand: execution, Outcome: workflowruntime.OutcomeRetryableFailure, FailureCode: "transient"})
	if err != nil {
		t.Fatal(err)
	}
	historical := append([]workflowruntime.DurableEvent(nil), first.Events...)
	retryExecution := execution
	retryExecution.AttemptID = 2
	retryExecution.LeaseToken = "lease-retry"
	if _, err = runtime.StartExecution(context.Background(), retryExecution); err != nil {
		t.Fatal(err)
	}
	second, err := runtime.RecordOutcome(context.Background(), workflowruntime.OutcomeCommand{ExecutionCommand: retryExecution, Outcome: workflowruntime.OutcomeSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != len(historical)+2 || !reflect.DeepEqual(second.Events[:len(historical)], historical) || len(second.Attempts) != 2 {
		t.Fatalf("history was rewritten: before=%+v after=%+v", historical, second.Events)
	}
}

func TestDecisionBranchCausesTaskTransitionNotIndependentCompletion(t *testing.T) {
	tasks := newFakeTasks()
	runtime := newRuntime(t, tasks, fakeCompletion{}, newTopologyCoordination())
	workerActor := actor(topologyfixture.RoleEngineeringA, "p-worker")
	work := mustInitiate(t, runtime, childRequest(topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringA, "branch", "cause"), workerActor)
	result, decision, err := runtime.ApplyDecision(context.Background(), workflowruntime.BranchRequest{
		Actor: workerActor, TaskID: work.TaskID, AttemptID: 1, CorrelationID: correlationID, CausationID: "cause", InputRef: "decision_graph_run:7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != workflowruntime.StatusBlocked || result.Status.Terminal() || decision.DecisionRef != "decision_graph_run:7" {
		t.Fatalf("result=%+v decision=%+v", result, decision)
	}
	if result.LastTransition == nil || !strings.HasPrefix(result.LastTransition.EventType, "task.blocked:") {
		t.Fatalf("decision did not produce a durable task transition: %+v", result.LastTransition)
	}
}

func TestSameRoleProgressionNeverUsesCoordinationPort(t *testing.T) {
	tasks := newFakeTasks()
	coordination := newTopologyCoordination()
	runtime := newRuntime(t, tasks, fakeCompletion{}, coordination)
	workerActor := actor(topologyfixture.RoleEngineeringA, "p-worker")
	work := mustInitiate(t, runtime, childRequest(topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringA, "same-role", "cause"), workerActor)
	execution := workflowruntime.ExecutionCommand{Actor: workerActor, TaskID: work.TaskID, AttemptID: 1, LeaseToken: "lease", CorrelationID: correlationID, CausationID: "cause"}
	_, _ = runtime.StartExecution(context.Background(), execution)
	_, _ = runtime.RecordOutcome(context.Background(), workflowruntime.OutcomeCommand{ExecutionCommand: execution, Outcome: workflowruntime.OutcomeSucceeded})
	if len(coordination.records) != 0 {
		t.Fatalf("same-role progression emitted %d messages", len(coordination.records))
	}
	if _, _, err := runtime.Coordinate(context.Background(), workflowruntime.CoordinationCommand{
		Actor: workerActor, OrganizationID: topologyfixture.OrganizationID, SenderTaskID: work.TaskID,
		RecipientTaskID: work.TaskID, CorrelationID: correlationID, CausationID: "cause",
		Kind: workflowruntime.CoordinationCompletion, IdempotencyKey: "self",
	}); !errors.Is(err, workflowruntime.ErrSameRoleCoordination) {
		t.Fatalf("same-role message err=%v", err)
	}
}

// goalCriterionTexts drops the phase for the fake task store, which records
// requirements as prose. The phase is the Executive's concern; this fake
// stands in for the task layer, which never had it.
func goalCriterionTexts(criteria []workflowruntime.GoalAcceptanceCriterion) []string {
	out := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		out = append(out, criterion.Text)
	}
	return out
}
