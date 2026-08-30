package endtoendfixtures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/completion"
	completionpostgres "github.com/Mireuz13/explorarte-organization/internal/completion/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/canonical"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisiongraphpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
)

const (
	fixtureOrganization  = "explorarte"
	researchWorkerRole   = "investigacion/research_worker_hourly"
	researchAuditRole    = "investigacion/auditor_cerebro_empresa"
	departmentLeaderRole = "ingenieria_ia/orquestador"
	departmentWorkerRole = "ingenieria_ia/qa"
)

// harness wires the exact same real production components
// internal/executive's own postgres integration test uses (registry,
// tasks, authorization, completion, decisiongraph) — see
// internal/executive/postgres_integration_test.go's newIntegrationHarness,
// which this mirrors. Canonical registry sync happens here too: every
// productive orgctl command already expects the canonical registry to be
// synced against the target database before it runs, so a fixture replay
// against a fresh disposable database must do the same instead of
// assuming another test suite already did it.
type harness struct {
	store      *platformpostgres.Store
	registry   *registry.PostgresRepository
	tasks      *tasks.Service
	authz      executive.AuthorizationGate
	completion executive.CompletionGate
	decisions  executive.DecisionRecorder
	authorizer agentmessaging.CapabilityAuthorizer
}

func newHarness(ctx context.Context, store *platformpostgres.Store) (*harness, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		return nil, err
	}
	loader, err := registry.NewLoader(cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, err
	}
	registryService, err := registry.NewService(loader, registryRepo, fixtureOrganization, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if existing, err := registryRepo.GetCurrentRevision(ctx, fixtureOrganization); err != nil || existing == nil {
		if syncResult, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || !syncResult.Applied {
			return nil, fmt.Errorf("sync canonical registry: applied=%v err=%v", syncResult.Applied, syncErr)
		}
	}
	catalog, err := registryadapter.New(registryRepo)
	if err != nil {
		return nil, err
	}
	taskDB, err := taskpostgres.New(store)
	if err != nil {
		return nil, err
	}
	taskService, err := tasks.NewService(taskDB, catalog, tasks.Config{
		OrganizationID: fixtureOrganization, DefaultMaxAttempts: 5, DefaultLeaseDuration: time.Minute,
		MaxLeaseDuration: 15 * time.Minute, RetryPolicy: tasks.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute},
		OutboxMaxAttempts: 3, OutboxClaimDuration: time.Minute,
	})
	if err != nil {
		return nil, err
	}
	authRuntime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		return nil, err
	}
	completionReader, err := completionpostgres.New(store, fixtureOrganization)
	if err != nil {
		return nil, err
	}
	completionService, err := completion.NewService(completionReader, completionReader, completionReader, completionReader, completionReader, nil)
	if err != nil {
		return nil, err
	}
	decisionGraphStore, err := decisiongraphpostgres.New(store, fixtureOrganization)
	if err != nil {
		return nil, err
	}
	decisionGraphService, err := decisiongraph.NewService(decisionGraphStore, decisiongraph.SystemClock{})
	if err != nil {
		return nil, err
	}
	canonicalProvider, err := canonical.New(cfg.Registry.CanonicalDir, int64(cfg.Context.MaxTotalBytes))
	if err != nil {
		return nil, err
	}
	return &harness{
		store: store, registry: registryRepo, tasks: taskService,
		authz:      runtimeadapter.Authorization{Service: authRuntime.Service, OrganizationID: fixtureOrganization},
		authorizer: authRuntime.Authorizer,
		completion: runtimeadapter.Completion{Service: completionService},
		decisions: runtimeadapter.DecisionGraph{
			Service: decisionGraphService, Canonical: canonicalProvider,
			Limits: executive.DefaultLimits(), Clock: executive.ClockFunc(time.Now),
		},
	}, nil
}

// fakeContext is the ContextCoordinator stub the real integration test
// uses (an incrementing snapshot id) — real context assembly is a
// separate concern this fixture does not exercise; internal/contextengine
// has its own coverage.
type fakeContext struct{ next int64 }

func (f *fakeContext) Build(context.Context, executive.ContextRequest) (executive.ContextSnapshot, error) {
	f.next++
	content := fmt.Sprintf("fixture context snapshot %d", f.next)
	digest := sha256.Sum256([]byte(content))
	return executive.ContextSnapshot{
		ID: f.next, Version: "1", Digest: hex.EncodeToString(digest[:]), Content: content,
	}, nil
}

// fakeAssignments stubs DispatchProvisioner — real provider dispatch is
// internal/modelruntime's concern, out of scope for an evaluation harness
// that must never make a real, billed model call.
type fakeAssignments struct{}

func (fakeAssignments) EnsureAuthorizedAssignmentForRunningAttempt(_ context.Context, taskID, attemptID int64) (executive.AssignmentRef, error) {
	return executive.AssignmentRef{ID: 1_000_000 + attemptID, TaskID: taskID, AttemptID: attemptID}, nil
}

func (fakeAssignments) ResolveAssignment(_ context.Context, taskID, attemptID int64, role string) (executive.AssignmentRef, error) {
	return executive.AssignmentRef{
		ID: 1_000_000 + attemptID, OrganizationRevisionID: 1, TaskID: taskID, AttemptID: attemptID,
		SubjectRoleID: role, ExecutionPrincipalID: 1, DispatchActorRoleID: "ingenieria_ia/code-runner",
		ValidUntil: time.Now().Add(time.Hour),
	}, nil
}

// fakeModelRuntime stands in for internal/modelruntime — the fixture
// never makes a real, billed LLM call. Its worker-purpose output embeds
// the evidence this runner sourced for real from rag.Manager/
// memory.Manager (see research.go), so a real invariant can later
// re-parse the persisted invocation bytes and confirm the sourced
// evidence actually flowed into what the orchestrator dispatched, not
// just that both halves independently exist.
type fakeModelRuntime struct {
	evidence  researchEvidence
	byAttempt map[string][]executive.InvocationRecord
	results   map[int64]executive.InvocationResult
	nextID    int64
}

func newFakeModelRuntime(evidence researchEvidence) *fakeModelRuntime {
	return &fakeModelRuntime{evidence: evidence, byAttempt: map[string][]executive.InvocationRecord{}, results: map[int64]executive.InvocationResult{}, nextID: 9000}
}

func modelAttemptKey(taskID, attemptID int64) string { return fmt.Sprintf("%d/%d", taskID, attemptID) }

// Execute stands in for the Execution Harness. It performs the one thing a
// real run performs that this fixture must never do -- call a billed provider
// -- and leaves behind exactly what a real run leaves behind: a durable
// invocation row for the attempt, its result, and a verdict referencing it.
func (f *fakeModelRuntime) Execute(_ context.Context, command executive.HarnessRunCommand) (executive.HarnessRunOutcome, error) {
	key := modelAttemptKey(command.TaskID, command.AttemptID)
	if existing := f.byAttempt[key]; len(existing) > 0 {
		result := f.results[existing[0].ID]
		return executive.HarnessRunOutcome{
			Status: executive.HarnessRunSucceeded, FinalOutput: string(result.JSONOutput),
			InvocationID: existing[0].ID,
		}, nil
	}
	f.nextID++
	invocation := executive.InvocationRecord{
		ID: f.nextID, TaskID: command.TaskID, AttemptID: command.AttemptID, SubjectRoleID: command.RoleID,
		Status: "succeeded", CorrelationID: command.CorrelationID, CausationID: command.CausationID,
	}
	f.byAttempt[key] = []executive.InvocationRecord{invocation}
	body := f.output(command.Purpose.LegacyPurpose())
	hash := sha256.Sum256(body)
	f.results[invocation.ID] = executive.InvocationResult{
		InvocationID: invocation.ID, JSONOutput: body, ResponseHash: hex.EncodeToString(hash[:]), ResponseBytes: len(body),
	}
	return executive.HarnessRunOutcome{
		Status: executive.HarnessRunSucceeded, FinalOutput: string(body), InvocationID: invocation.ID,
	}, nil
}

// ProviderFailureRetryable answers false: this fixture's model runtime never
// records a transient provider outcome, so nothing it produces is worth
// retrying. Returning true would make failure paths retry against a fake that
// always fails the same way.
func (f *fakeModelRuntime) ProviderFailureRetryable(context.Context, int64) (bool, error) {
	return false, nil
}

func (f *fakeModelRuntime) GetInvocation(_ context.Context, id int64) (executive.InvocationRecord, error) {
	for _, values := range f.byAttempt {
		for _, value := range values {
			if value.ID == id {
				return value, nil
			}
		}
	}
	return executive.InvocationRecord{}, errors.New("invocation not found")
}

func (f *fakeModelRuntime) FindTaskAttemptInvocations(_ context.Context, taskID, attemptID int64) ([]executive.InvocationRecord, error) {
	return append([]executive.InvocationRecord(nil), f.byAttempt[modelAttemptKey(taskID, attemptID)]...), nil
}

func (f *fakeModelRuntime) GetResult(_ context.Context, invocationID int64) (executive.InvocationResult, error) {
	value, ok := f.results[invocationID]
	if !ok {
		return executive.InvocationResult{}, errors.New("result not found")
	}
	return value, nil
}

func (f *fakeModelRuntime) output(purpose string) json.RawMessage {
	switch purpose {
	case "executive_ceo_plan":
		return json.RawMessage(`{"schema_version":"executive-plan/v1","objective":"analyze the r30-14 synthetic case","department_requests":[{"unit_id":"ingenieria_ia","objective":"inspect the sourced research evidence","deliverable":"report","priority":10,"constraints":[]}],"global_constraints":[],"success_criteria":["verified"],"owner_decisions_required":[]}`)
	case "department_plan":
		return json.RawMessage(`{"schema_version":"department-plan/v1","department_id":"ingenieria_ia","tasks":[{"client_key":"inspect","assigned_role_id":"ingenieria_ia/qa","title":"Inspect sourced evidence","instructions":"Inspect the bounded task context and cite the sourced research evidence.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":5}],"review_criteria":["findings verified"],"unresolved":[]}`)
	case "department_worker":
		refs, _ := json.Marshal([]string{f.evidence.ragEvidenceRef(), f.evidence.memoryEvidenceRef()})
		return json.RawMessage(fmt.Sprintf(`{"schema_version":"worker-result/v1","summary":"findings grounded in real R30.1 research evidence","evidence_refs":%s}`, refs))
	case "department_review":
		refs, _ := json.Marshal([]string{f.evidence.ragEvidenceRef(), f.evidence.memoryEvidenceRef()})
		return json.RawMessage(fmt.Sprintf(`{"schema_version":"department-review/v1","verdict":"accept","findings":["criteria satisfied"],"unsatisfied_criteria":[],"evidence_refs":%s,"proposed_followup_tasks":[]}`, refs))
	case "executive_ceo_closure":
		refs, _ := json.Marshal([]string{f.evidence.ragEvidenceRef(), f.evidence.memoryEvidenceRef()})
		return json.RawMessage(fmt.Sprintf(`{"schema_version":"executive-closure/v1","status":"completed","answer_to_owner":"El caso sintético r30-14 fue analizado con evidencia real de investigación aprobada.","completed_items":["engineering analysis"],"blocked_items":[],"unresolved_decisions":[],"evidence_refs":%s}`, refs))
	default:
		return json.RawMessage(`{"schema_version":"worker-result/v1","summary":"unknown","evidence_refs":[]}`)
	}
}

// allowRAGGate/allowMemoryGate are permissive AuthorizationGates for the
// research-evidence-sourcing phase only — cross-namespace/cross-role
// authorization denial already has its own dedicated coverage in
// internal/retrievalfixtures (r30-06); re-testing it here would duplicate,
// not compose, existing coverage.
type allowRAGGate struct{}

func (allowRAGGate) Authorize(context.Context, rag.AuthorizationRequest) error { return nil }

type allowMemoryGate struct{}

func (allowMemoryGate) Authorize(context.Context, memory.AuthorizationRequest) error { return nil }

type namespaceResolver struct{ namespaceID string }

func (r namespaceResolver) ResolveNamespace(context.Context, string, string, rag.NamespaceKind) (string, error) {
	return r.namespaceID, nil
}

func stableSuffix(subjectID string) string {
	sum := sha256.Sum256([]byte("endtoend-runner-v1|" + subjectID))
	return hex.EncodeToString(sum[:6])
}
