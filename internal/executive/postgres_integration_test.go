//go:build integration

package executive_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	agentmessagingpostgres "github.com/Mireuz13/explorarte-organization/internal/agentmessaging/postgres"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/completion"
	completionpostgres "github.com/Mireuz13/explorarte-organization/internal/completion/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/canonical"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisiongraphpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

type integrationHarness struct {
	ctx        context.Context
	cancel     context.CancelFunc
	store      *platformpostgres.Store
	registry   *registry.PostgresRepository
	tasks      *tasks.Service
	authz      executive.AuthorizationGate
	completion executive.CompletionGate
	decisions  executive.DecisionRecorder
	authorizer agentmessaging.CapabilityAuthorizer
	principals executive.RoleBoundPrincipalResolver
	dispatch   *dispatchpostgres.Store
}

func newIntegrationHarness(t *testing.T) *integrationHarness {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	canonicalDir := filepath.Join("..", "..", "docs", "canonical")
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "24",
			"ORG_DATABASE_MIN_CONNS": "0",
			"ORG_CANONICAL_DIR":      canonicalDir,
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "executive-integration-test")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		store.Close()
		cancel()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		store.Close()
		cancel()
		t.Fatalf("migrate: %v", err)
	}
	if err := testdbguard.RequireDestructive(ctx, databaseURL, store.Pool()); err != nil {
		store.Close()
		cancel()
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err = store.Pool().Exec(ctx, `
TRUNCATE outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,
         task_requirements,task_dependencies,tasks,organization_reporting_lines,
         organization_registry_revision_documents,organization_roles,organizational_units,
         organizations,organization_registry_revisions,audit_events,
         decision_records,decision_budget_events,decision_verifications,decision_observations,
         decision_node_executions,decision_graph_budgets,decision_branch_events,decision_graph_edges,
         decision_graph_nodes,decision_graph_versions,decision_graph_runs RESTART IDENTITY CASCADE`); err != nil {
		store.Close()
		cancel()
		t.Fatalf("reset schema: %v", err)
	}
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, "explorarte", 30*time.Second)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	if syncResult, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || !syncResult.Applied {
		store.Close()
		cancel()
		t.Fatalf("sync registry: result=%+v err=%v", syncResult, syncErr)
	}
	catalog, err := registryadapter.New(registryRepo)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	taskDB, err := taskpostgres.New(store)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	taskService, err := tasks.NewService(taskDB, catalog, tasks.Config{
		OrganizationID:       "explorarte",
		DefaultMaxAttempts:   5,
		DefaultLeaseDuration: time.Minute,
		MaxLeaseDuration:     15 * time.Minute,
		RetryPolicy:          tasks.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute},
		OutboxMaxAttempts:    3,
		OutboxClaimDuration:  time.Minute,
	})
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	authRuntime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	completionReader, err := completionpostgres.New(store, "explorarte")
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	completionService, err := completion.NewService(completionReader, completionReader, completionReader, completionReader, completionReader, nil)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	decisionGraphStore, err := decisiongraphpostgres.New(store, "explorarte")
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	decisionGraphService, err := decisiongraph.NewService(decisionGraphStore, decisiongraph.SystemClock{})
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	canonicalProvider, err := canonical.New(canonicalDir, int64(cfg.Context.MaxTotalBytes))
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	dispatchStore, err := dispatchpostgres.New(store)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	// The role-bound principal resolver is the real one, against the real
	// dispatch principal store: lease holder identity is exactly what this
	// migration moved, so faking it here would hide the thing under test.
	roleBoundResolver, err := runtimeadapter.NewRoleBoundPrincipalResolver(dispatchStore, "explorarte")
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	return &integrationHarness{
		ctx: ctx, cancel: cancel, store: store, registry: registryRepo, tasks: taskService,
		principals: runtimeadapter.RoleBoundPrincipals{Resolver: roleBoundResolver},
		dispatch:   dispatchStore,
		authz:      runtimeadapter.Authorization{Service: authRuntime.Service, OrganizationID: "explorarte"},
		authorizer: authRuntime.Authorizer,
		completion: runtimeadapter.Completion{Service: completionService},
		decisions: runtimeadapter.DecisionGraph{
			Service: decisionGraphService, Canonical: canonicalProvider,
			Limits: executive.DefaultLimits(), Clock: executive.ClockFunc(time.Now),
		},
	}
}

func (h *integrationHarness) close() {
	h.store.Close()
	h.cancel()
}

type integrationContext struct{ next int64 }

func (f *integrationContext) Build(context.Context, executive.ContextRequest) (executive.ContextSnapshot, error) {
	f.next++
	content := fmt.Sprintf("integration context snapshot %d", f.next)
	digest := sha256.Sum256([]byte(content))
	return executive.ContextSnapshot{
		ID: f.next, Version: "1", Digest: hex.EncodeToString(digest[:]), Content: content,
	}, nil
}

type integrationAssignments struct{ fail bool }

func (f integrationAssignments) ResolveAssignment(_ context.Context, taskID, attemptID int64, role string) (executive.AssignmentRef, error) {
	if f.fail {
		return executive.AssignmentRef{}, errors.New("assignment unavailable")
	}
	return executive.AssignmentRef{
		ID: 1000 + attemptID, OrganizationRevisionID: 1, TaskID: taskID, AttemptID: attemptID,
		SubjectRoleID: role, ExecutionPrincipalID: 77, DispatchActorRoleID: "ingenieria_ia/code-runner",
		ValidUntil: time.Now().Add(time.Hour),
	}, nil
}

type integrationModelRuntime struct {
	nextID             int64
	byAttempt          map[string][]executive.InvocationRecord
	results            map[int64]executive.InvocationResult
	ensureCalls        int
	ambiguousPurpose   string
	invalidCEOOverride bool
	crossDepartment    bool
	runs               []executive.HarnessRunCommand
}

func newIntegrationModelRuntime() *integrationModelRuntime {
	return &integrationModelRuntime{nextID: 9000, byAttempt: map[string][]executive.InvocationRecord{}, results: map[int64]executive.InvocationResult{}}
}

func modelAttemptKey(taskID, attemptID int64) string { return fmt.Sprintf("%d/%d", taskID, attemptID) }

// Execute is the Execution Harness stand-in. Everything around it in these
// tests is real -- real PostgreSQL tasks, leases, attempts, evidence,
// completion verification and decision graph -- and the one thing faked here
// is the billed provider call, exactly as before the migration. It leaves the
// same durable trace a real run leaves: one invocation row per attempt, its
// result, and a verdict that references it.
func (f *integrationModelRuntime) Execute(_ context.Context, command executive.HarnessRunCommand) (executive.HarnessRunOutcome, error) {
	f.runs = append(f.runs, command)
	purpose := command.Purpose.LegacyPurpose()
	key := modelAttemptKey(command.TaskID, command.AttemptID)
	if existing := f.byAttempt[key]; len(existing) > 0 {
		return f.outcomeFor(existing[0]), nil
	}
	f.ensureCalls++
	f.nextID++
	status := "succeeded"
	if purpose == f.ambiguousPurpose {
		status = "ambiguous"
	}
	invocation := executive.InvocationRecord{
		ID: f.nextID, TaskID: command.TaskID, AttemptID: command.AttemptID, SubjectRoleID: command.RoleID,
		Status: status, CorrelationID: command.CorrelationID, CausationID: command.CausationID,
	}
	f.byAttempt[key] = []executive.InvocationRecord{invocation}
	if status == "succeeded" {
		body := f.output(purpose)
		hash := sha256.Sum256(body)
		f.results[invocation.ID] = executive.InvocationResult{
			InvocationID: invocation.ID, JSONOutput: body, ResponseHash: hex.EncodeToString(hash[:]), ResponseBytes: len(body),
		}
	}
	return f.outcomeFor(invocation), nil
}

// outcomeFor mirrors what the real adapter reports: an ambiguous provider
// outcome is a model failure as far as the Harness can tell, and the durable
// invocation row is what actually knows it was ambiguous.
func (f *integrationModelRuntime) outcomeFor(invocation executive.InvocationRecord) executive.HarnessRunOutcome {
	if invocation.Status != "succeeded" {
		return executive.HarnessRunOutcome{
			Status: executive.HarnessRunFailed, Failure: executive.HarnessFailureModelError,
			InvocationID: invocation.ID, TerminationReason: "model execution failed",
		}
	}
	return executive.HarnessRunOutcome{
		Status: executive.HarnessRunSucceeded, InvocationID: invocation.ID,
		FinalOutput: string(f.results[invocation.ID].JSONOutput),
	}
}

func (f *integrationModelRuntime) GetInvocation(_ context.Context, id int64) (executive.InvocationRecord, error) {
	for _, values := range f.byAttempt {
		for _, value := range values {
			if value.ID == id {
				return value, nil
			}
		}
	}
	return executive.InvocationRecord{}, errors.New("invocation not found")
}

func (f *integrationModelRuntime) FindTaskAttemptInvocations(_ context.Context, taskID, attemptID int64) ([]executive.InvocationRecord, error) {
	return append([]executive.InvocationRecord(nil), f.byAttempt[modelAttemptKey(taskID, attemptID)]...), nil
}

func (f *integrationModelRuntime) GetResult(_ context.Context, invocationID int64) (executive.InvocationResult, error) {
	value, ok := f.results[invocationID]
	if !ok {
		return executive.InvocationResult{}, errors.New("result not found")
	}
	return value, nil
}

func (f *integrationModelRuntime) output(purpose string) json.RawMessage {
	switch purpose {
	case "executive_ceo_plan":
		if f.invalidCEOOverride {
			return json.RawMessage(`{"schema_version":"executive-plan/v1","objective":"analyze","department_requests":[{"unit_id":"ingenieria_ia","objective":"inspect","deliverable":"report","priority":10,"constraints":[],"provider":"unapproved-vendor"}],"global_constraints":[],"success_criteria":["verified"],"owner_decisions_required":[]}`)
		}
		return json.RawMessage(`{"schema_version":"executive-plan/v1","objective":"analyze","department_requests":[{"unit_id":"ingenieria_ia","objective":"inspect","deliverable":"report","priority":10,"constraints":[]}],"global_constraints":[],"success_criteria":["verified"],"owner_decisions_required":[]}`)
	case "department_plan":
		role := "ingenieria_ia/qa"
		if f.crossDepartment {
			role = "marketing/estratega_crecimiento"
		}
		return json.RawMessage(fmt.Sprintf(`{"schema_version":"department-plan/v1","department_id":"ingenieria_ia","tasks":[{"client_key":"inspect","assigned_role_id":%q,"title":"Inspect state","instructions":"Inspect the bounded task context and report findings.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":5}],"review_criteria":["findings verified"],"unresolved":[]}`, role))
	case "department_worker":
		return json.RawMessage(`{"schema_version":"worker-result/v1","summary":"bounded findings","evidence_refs":["integration:evidence:1"]}`)
	case "department_review":
		return json.RawMessage(`{"schema_version":"department-review/v1","verdict":"accept","findings":["criteria satisfied"],"unsatisfied_criteria":[],"evidence_refs":["integration:evidence:1"],"proposed_followup_tasks":[]}`)
	case "executive_ceo_closure":
		return json.RawMessage(`{"schema_version":"executive-closure/v1","status":"completed","answer_to_owner":"The requested area was analyzed with verified evidence.","completed_items":["engineering analysis"],"blocked_items":[],"unresolved_decisions":[],"evidence_refs":["integration:evidence:1"]}`)
	default:
		return json.RawMessage(`{"schema_version":"worker-result/v1","summary":"unknown","evidence_refs":[]}`)
	}
}

type countingCompletion struct {
	delegate executive.CompletionGate
	calls    int
	forceAt  int
}

func (c *countingCompletion) Verify(ctx context.Context, taskID, attemptID int64) (executive.CompletionResult, error) {
	c.calls++
	if c.forceAt > 0 && c.calls == c.forceAt {
		return executive.CompletionResult{Verdict: executive.CompletionInconclusive, Detail: "injected inconclusive"}, nil
	}
	return c.delegate.Verify(ctx, taskID, attemptID)
}

func newOrchestrator(t *testing.T, h *integrationHarness, models *integrationModelRuntime, assignments integrationAssignments, completionGate executive.CompletionGate, opts ...executive.OrchestratorOption) *executive.Orchestrator {
	t.Helper()
	value, err := executive.NewOrchestrator(executive.Dependencies{
		OrganizationID: "explorarte",
		Registry:       runtimeadapter.Registry{Reader: h.registry, OrganizationID: "explorarte"},
		Tasks:          runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"},
		Contexts:       &integrationContext{},
		Assignments:    assignments,
		Principals:     h.principals,
		Models:         models,
		Harness:        models,
		Budget: runtimeadapter.ModelCallBudget{
			Models: models,
			Tasks:  runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"},
			Limits: executive.DefaultLimits(),
		},
		Completion:    completionGate,
		Decisions:     h.decisions,
		Authorization: h.authz,
		Limits:        executive.DefaultLimits(),
		Clock:         executive.ClockFunc(time.Now),
	}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func runUntilTerminalOrError(t *testing.T, ctx context.Context, orchestrator *executive.Orchestrator, rootID int64, max int) (executive.Run, error) {
	t.Helper()
	var run executive.Run
	var err error
	for i := 0; i < max; i++ {
		run, err = orchestrator.Resume(ctx, rootID)
		if err != nil || run.State == executive.StateCompleted || run.State == executive.StateFailed || run.State == executive.StateBlocked {
			return run, err
		}
	}
	return run, fmt.Errorf("executive run did not converge after %d resumes", max)
}

func TestExecutivePostgreSQL17EndToEndAndRestart(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "integration-executive-normal",
		Goal: executive.OwnerGoal{Goal: "Analyze the organization and return a one-area plan without external actions.", AcceptanceCriteria: []string{"one department reviewed", "closure verified"}},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}
	rootID := run.RootTaskID
	for i := 0; i < 3; i++ {
		if _, err = orchestrator.Resume(h.ctx, rootID); err != nil {
			t.Fatalf("pre-restart resume %d: %v", i, err)
		}
	}
	orchestrator = newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	run, err = runUntilTerminalOrError(t, h.ctx, orchestrator, rootID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != executive.StateCompleted || run.AnswerToOwner == "" {
		t.Fatalf("run=%+v", run)
	}
	if models.ensureCalls != executive.NormalExpectedCalls(1, 1) {
		t.Fatalf("model calls=%d want=%d", models.ensureCalls, executive.NormalExpectedCalls(1, 1))
	}
	if completionGate.calls != 6 {
		t.Fatalf("completion verifier calls=%d want=6", completionGate.calls)
	}
	root, err := h.tasks.GetTask(h.ctx, rootID)
	if err != nil || root.Task.Status != tasks.StatusCompleted {
		t.Fatalf("root=%+v err=%v", root.Task, err)
	}
	correlated, err := h.tasks.ListTasks(h.ctx, tasks.TaskFilter{OrganizationID: "explorarte", CorrelationID: *root.Task.CorrelationID, Limit: 100})
	if err != nil || len(correlated) != 6 {
		t.Fatalf("correlated tasks=%d err=%v", len(correlated), err)
	}
	for _, task := range correlated {
		if task.CorrelationID == nil || *task.CorrelationID != *root.Task.CorrelationID {
			t.Fatalf("correlation drift task=%+v", task)
		}
		if task.ID != rootID && (task.CausationID == nil || *task.CausationID == "") {
			t.Fatalf("missing causation task=%+v", task)
		}
	}
}

// TestExecutivePostgreSQL17AgentBudgetsAndMessagingAreWiredThroughDelegation
// runs the same real CEO->leader->worker flow with
// WithAgentBudgets/WithAgentMessaging configured, and asserts the root task
// gets a real agent_budgets row, both delegation hops (CEO->leader,
// leader->worker) get a real durable delegation message, and the delegated
// tasks resolve to the root's shared budget rather than a budget of their
// own — the default "no explicit allocation" path.
func TestExecutivePostgreSQL17AgentBudgetsAndMessagingAreWiredThroughDelegation(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}

	budgetLedger, err := agentbudgetpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	messageLedger, err := agentmessagingpostgres.New(h.store, h.registry, h.authorizer, 200, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// EXEC-PRINCIPAL-001: no principal registration here. AgentMessages
	// resolves the role-bound principal for each hop's sender internally,
	// lazily provisioning one on first use -- the exact mechanism
	// internal/executive/bootstrap/runtime.go wires in production. This
	// fixture exercises the real runtime contract, not a fixture-only
	// shortcut: it must pass because role-based resolution genuinely works
	// across the CEO->leader and leader->worker hops below, not because the
	// fixture pre-arranged a principal whose role happens to match one hop.
	dispatchStore, err := dispatchpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate,
		executive.WithAgentBudgets(runtimeadapter.AgentBudgets{Ledger: budgetLedger, Limits: agentbudget.DefaultLimits()}),
		executive.WithAgentMessaging(runtimeadapter.AgentMessages{
			Ledger: messageLedger, MaxAttempts: 10,
			PrincipalStore: dispatchStore, OrganizationID: "explorarte",
		}),
	)

	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "integration-executive-agent-coordination",
		Goal: executive.OwnerGoal{Goal: "Analyze the organization and return a one-area plan without external actions.", AcceptanceCriteria: []string{"one department reviewed", "closure verified"}},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}
	rootID := run.RootTaskID
	run, err = runUntilTerminalOrError(t, h.ctx, orchestrator, rootID, 20)
	if err != nil || run.State != executive.StateCompleted {
		t.Fatalf("run=%+v err=%v", run, err)
	}

	var rootBudgetID int64
	var parentBudgetID *int64
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT id, parent_budget_id FROM agent_budgets WHERE task_id=$1`, rootID).Scan(&rootBudgetID, &parentBudgetID); err != nil {
		t.Fatalf("root budget: %v", err)
	}
	if parentBudgetID != nil {
		t.Fatalf("root budget must have no parent: %v", *parentBudgetID)
	}

	root, err := h.tasks.GetTask(h.ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	correlated, err := h.tasks.ListTasks(h.ctx, tasks.TaskFilter{OrganizationID: "explorarte", CorrelationID: *root.Task.CorrelationID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var leaderTask, workerTask *tasks.Task
	for i := range correlated {
		task := correlated[i]
		if task.AssignedRoleID == "ingenieria_ia/orquestador" {
			leaderTask = &task
		}
		if task.AssignedRoleID == "ingenieria_ia/qa" {
			workerTask = &task
		}
	}
	if leaderTask == nil || workerTask == nil {
		t.Fatalf("expected a leader and a worker task among correlated tasks: leader=%v worker=%v", leaderTask, workerTask)
	}

	for _, delegatedTaskID := range []int64{leaderTask.ID, workerTask.ID} {
		var budgetID int64
		if err := h.store.Pool().QueryRow(h.ctx, `SELECT budget_id FROM task_budgets WHERE task_id=$1`, delegatedTaskID).Scan(&budgetID); err != nil {
			t.Fatalf("task_budgets for %d: %v", delegatedTaskID, err)
		}
		if budgetID != rootBudgetID {
			t.Fatalf("task %d shares a different budget than root: got=%d want=%d", delegatedTaskID, budgetID, rootBudgetID)
		}
		var messageType, recipientRoleID string
		if err := h.store.Pool().QueryRow(h.ctx, `SELECT message_type, recipient_role_id FROM agent_messages WHERE recipient_task_id=$1`, delegatedTaskID).Scan(&messageType, &recipientRoleID); err != nil {
			t.Fatalf("agent_messages for %d: %v", delegatedTaskID, err)
		}
		if messageType != "delegation" {
			t.Fatalf("task %d message_type=%s want delegation", delegatedTaskID, messageType)
		}
	}

	var ceoToLeader int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages WHERE sender_task_id=$1 AND recipient_task_id=$2 AND message_type='delegation'`, rootID, leaderTask.ID).Scan(&ceoToLeader); err != nil {
		t.Fatal(err)
	}
	if ceoToLeader != 1 {
		t.Fatalf("ceo->leader delegation messages=%d want 1", ceoToLeader)
	}
	var leaderToWorker int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages WHERE sender_task_id=$1 AND recipient_task_id=$2 AND message_type='delegation'`, leaderTask.ID, workerTask.ID).Scan(&leaderToWorker); err != nil {
		t.Fatal(err)
	}
	if leaderToWorker != 1 {
		t.Fatalf("leader->worker delegation messages=%d want 1", leaderToWorker)
	}
}

// TestExecutivePostgreSQL17RecordsDecisionGraphTraceForEveryAttempt is the
// Rama 25 producer smoke test: it runs one real executive task to completion
// against real PostgreSQL 17 and asserts every finished attempt left a
// durable, queryable decision-graph trace — not just that gatedComplete
// didn't error. The trace's shape (goal/candidate_action/decision nodes, a
// verified terminal decision pointing at the task/attempt that produced it)
// is checked directly against the tables, matching how R23/R24 verified
// behavior against real Postgres rather than fakes.
func TestExecutivePostgreSQL17RecordsDecisionGraphTraceForEveryAttempt(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "integration-decision-trace",
		Goal: executive.OwnerGoal{Goal: "Analyze the organization and return a one-area plan without external actions.", AcceptanceCriteria: []string{"one department reviewed", "closure verified"}},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}
	run, err = runUntilTerminalOrError(t, h.ctx, orchestrator, run.RootTaskID, 20)
	if err != nil || run.State != executive.StateCompleted {
		t.Fatalf("run=%+v err=%v", run, err)
	}

	rows, queryErr := h.store.Pool().Query(h.ctx, `
SELECT r.id, r.task_id, r.attempt_id, r.status,
       (SELECT count(*) FROM decision_graph_nodes n WHERE n.run_id = r.id) AS node_count,
       d.decision_node_id, d.selected_candidate_node_id, d.verification_label
FROM decision_graph_runs r
JOIN decision_records d ON d.run_id = r.id
WHERE r.organization_id = 'explorarte'
ORDER BY r.id`)
	if queryErr != nil {
		t.Fatalf("query decision graph runs: %v", queryErr)
	}
	defer rows.Close()

	type traceRow struct {
		runID, taskID, attemptID, nodeCount, decisionNodeID, candidateNodeID int64
		status, label                                                        string
	}
	var traces []traceRow
	for rows.Next() {
		var tr traceRow
		if err := rows.Scan(&tr.runID, &tr.taskID, &tr.attemptID, &tr.status, &tr.nodeCount, &tr.decisionNodeID, &tr.candidateNodeID, &tr.label); err != nil {
			t.Fatalf("scan decision graph row: %v", err)
		}
		traces = append(traces, tr)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate decision graph rows: %v", err)
	}

	// Every finished attempt in this run went through completion.Verify
	// exactly once (countingCompletion.calls, asserted below matches the
	// existing end-to-end test's expectation of 6), so exactly 6 runs with a
	// recorded terminal decision are expected — one per attempt, no more, no
	// fewer, and none left as an orphaned trace with no terminal decision.
	if completionGate.calls != 6 {
		t.Fatalf("completion verifier calls=%d want=6", completionGate.calls)
	}
	if len(traces) != completionGate.calls {
		t.Fatalf("decision graph traces=%d want=%d (traces=%+v)", len(traces), completionGate.calls, traces)
	}
	seenTasks := map[int64]bool{}
	for _, tr := range traces {
		if tr.status != "succeeded" {
			t.Fatalf("run %d status=%s want=succeeded", tr.runID, tr.status)
		}
		if tr.nodeCount != 3 {
			t.Fatalf("run %d node_count=%d want=3 (goal, candidate_action, decision)", tr.runID, tr.nodeCount)
		}
		if tr.label != "verified" {
			t.Fatalf("run %d verification_label=%s want=verified (every attempt in this scenario passes completion verification)", tr.runID, tr.label)
		}
		if tr.decisionNodeID == tr.candidateNodeID {
			t.Fatalf("run %d decision node and candidate node must be distinct, got both=%d", tr.runID, tr.decisionNodeID)
		}
		if tr.taskID <= 0 || tr.attemptID <= 0 {
			t.Fatalf("run %d has invalid task/attempt linkage: task=%d attempt=%d", tr.runID, tr.taskID, tr.attemptID)
		}
		seenTasks[tr.taskID] = true
	}
	if len(seenTasks) != 6 {
		t.Fatalf("distinct tasks with a recorded decision trace=%d want=6 (traces=%+v)", len(seenTasks), traces)
	}
}

func TestExecutivePostgreSQL17AmbiguousBlocksWithoutSecondInvocation(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	models.ambiguousPurpose = "department_worker"
	gate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, gate)
	run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "integration-ambiguous", Goal: executive.OwnerGoal{Goal: "Analyze one area.", AcceptanceCriteria: []string{"verified"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runUntilTerminalOrError(t, h.ctx, orchestrator, run.RootTaskID, 20)
	if !errors.Is(err, executive.ErrModelOutcomeAmbiguous) {
		t.Fatalf("err=%v run=%+v", err, run)
	}
	if run.State != executive.StateBlocked || run.ReasonCode != "model_outcome_ambiguous" {
		t.Fatalf("run=%+v", run)
	}
	before := models.ensureCalls
	_, secondErr := orchestrator.Resume(h.ctx, run.RootTaskID)
	if !errors.Is(secondErr, executive.ErrModelOutcomeAmbiguous) || models.ensureCalls != before {
		t.Fatalf("second resume err=%v calls=%d before=%d", secondErr, models.ensureCalls, before)
	}
}

func TestExecutivePostgreSQL17CompletionInconclusiveNeverCompletes(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	gate := &countingCompletion{delegate: h.completion, forceAt: 3}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, gate)
	run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "integration-inconclusive", Goal: executive.OwnerGoal{Goal: "Analyze one area.", AcceptanceCriteria: []string{"verified"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runUntilTerminalOrError(t, h.ctx, orchestrator, run.RootTaskID, 20)
	if !errors.Is(err, executive.ErrCompletionInconclusive) || run.State != executive.StateBlocked {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	root, getErr := h.tasks.GetTask(h.ctx, run.RootTaskID)
	if getErr != nil || root.Task.Status == tasks.StatusCompleted {
		t.Fatalf("root=%+v err=%v", root.Task, getErr)
	}
}

func TestExecutivePostgreSQL17RejectsModelOverridesAndCrossDepartmentDelegation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*integrationModelRuntime)
	}{
		{name: "provider override", mutate: func(m *integrationModelRuntime) { m.invalidCEOOverride = true }},
		{name: "cross department", mutate: func(m *integrationModelRuntime) { m.crossDepartment = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newIntegrationHarness(t)
			defer h.close()
			models := newIntegrationModelRuntime()
			tc.mutate(models)
			orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, &countingCompletion{delegate: h.completion})
			run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "integration-reject-" + tc.name, Goal: executive.OwnerGoal{Goal: "Analyze one area.", AcceptanceCriteria: []string{"verified"}}})
			if err != nil {
				t.Fatal(err)
			}
			run, err = runUntilTerminalOrError(t, h.ctx, orchestrator, run.RootTaskID, 20)
			if err == nil || run.State != executive.StateBlocked {
				t.Fatalf("run=%+v err=%v", run, err)
			}
		})
	}
}

// TestExecutivePostgreSQL17ReconcileGatedCompletionsRecoversOrphanedTask
// simulates the exact crash window ReconcileGatedCompletions exists to
// close: RecordAttemptDecision commits (decisiongraph's own transaction),
// then the process dies before gatedComplete's second half — finalizing the
// task — runs (a separate transaction in a separate subsystem; there is no
// shared transaction spanning both). A real end-to-end run establishes a
// genuinely committed, terminal decision_graph_run; the task's own status is
// then surgically rewound to StatusAwaitingVerification (the one and only
// state that means "attempt finished, decision not yet acted on") to
// reproduce that exact window without needing to hand-roll the task
// lifecycle machinery the orchestrator already exercises correctly elsewhere
// in this file.
func TestExecutivePostgreSQL17ReconcileGatedCompletionsRecoversOrphanedTask(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	completionGate := &countingCompletion{delegate: h.completion}
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, completionGate)
	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "integration-reconcile-gating",
		Goal: executive.OwnerGoal{Goal: "Analyze the organization and return a one-area plan without external actions.", AcceptanceCriteria: []string{"one department reviewed", "closure verified"}},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}
	run, err = runUntilTerminalOrError(t, h.ctx, orchestrator, run.RootTaskID, 20)
	if err != nil || run.State != executive.StateCompleted {
		t.Fatalf("run=%+v err=%v", run, err)
	}

	var decisionGraphRunsBefore, graphVersionsBefore int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM decision_graph_runs WHERE organization_id='explorarte'`).Scan(&decisionGraphRunsBefore); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM decision_graph_versions v JOIN decision_graph_runs r ON r.id=v.run_id WHERE r.organization_id='explorarte'`).Scan(&graphVersionsBefore); err != nil {
		t.Fatal(err)
	}

	// Rewind only the root task's own status — the decision graph run for
	// it is already genuinely committed as terminal from the real run
	// above. This reproduces "decision recorded, task never finalized"
	// without touching decisiongraph state at all.
	if _, err := h.store.Pool().Exec(h.ctx, `UPDATE tasks SET status='awaiting_verification', terminal_at=NULL WHERE id=$1`, run.RootTaskID); err != nil {
		t.Fatal(err)
	}
	rewound, err := h.tasks.GetTask(h.ctx, run.RootTaskID)
	if err != nil || rewound.Task.Status != tasks.StatusAwaitingVerification {
		t.Fatalf("rewind root task: task=%+v err=%v", rewound.Task, err)
	}

	result, err := orchestrator.ReconcileGatedCompletions(h.ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Found == 0 || result.Reconciled == 0 || len(result.Failed) != 0 {
		t.Fatalf("reconcile result=%+v", result)
	}

	recovered, err := h.tasks.GetTask(h.ctx, run.RootTaskID)
	if err != nil || recovered.Task.Status != tasks.StatusCompleted {
		t.Fatalf("recovered root task=%+v err=%v", recovered.Task, err)
	}

	// The idempotency fix from RecordAttemptDecision must have made
	// reconciliation a true no-op on the decisiongraph side: no new run, no
	// extra graph version appended to the existing one.
	var decisionGraphRunsAfter, graphVersionsAfter int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM decision_graph_runs WHERE organization_id='explorarte'`).Scan(&decisionGraphRunsAfter); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM decision_graph_versions v JOIN decision_graph_runs r ON r.id=v.run_id WHERE r.organization_id='explorarte'`).Scan(&graphVersionsAfter); err != nil {
		t.Fatal(err)
	}
	if decisionGraphRunsAfter != decisionGraphRunsBefore || graphVersionsAfter != graphVersionsBefore {
		t.Fatalf("reconciliation was not a decisiongraph no-op: runs %d->%d, versions %d->%d", decisionGraphRunsBefore, decisionGraphRunsAfter, graphVersionsBefore, graphVersionsAfter)
	}
}

func TestExecutivePostgreSQL17MissingDispatchAssignmentBlocks(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{fail: true}, &countingCompletion{delegate: h.completion})
	run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "integration-assignment", Goal: executive.OwnerGoal{Goal: "Analyze one area.", AcceptanceCriteria: []string{"verified"}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		run, err = orchestrator.Resume(h.ctx, run.RootTaskID)
		if errors.Is(err, executive.ErrDispatchAssignmentRequired) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(err, executive.ErrDispatchAssignmentRequired) || run.State != executive.StateBlocked || run.ReasonCode != "dispatch_assignment_required" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if models.ensureCalls != 0 {
		t.Fatalf("model invocation created without assignment: %d", models.ensureCalls)
	}
}
