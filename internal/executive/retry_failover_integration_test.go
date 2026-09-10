//go:build integration

package executive_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	executionharnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/tasksauthority"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egresspostgres "github.com/Mireuz13/explorarte-organization/internal/modelegress/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitypostgres "github.com/Mireuz13/explorarte-organization/internal/modelidentity/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelrouting"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
)

// ============================================================
// Model Capacity State / Dynamic Canonical Model Routing fixture -- a
// test-only pool policy plus a shared static test.fake policy, so every
// role this campaign drives (CEO, department leader, worker) resolves
// through test.fake/fake_adapter, never a real provider. Mirrors the
// established pool-fixture pattern from
// internal/modelruntime/postgres/dynamic_routing_integration_test.go and
// capacity_routing_e2e_test.go.
// ============================================================

const (
	retryFailoverStaticPolicyID = "retry-failover.static"
	retryFailoverPoolPolicyID   = "retry-failover.worker.pool"
	retryCandidateA             = "retry-a"
	retryCandidateB             = "retry-b"
	retryStaticModel            = "retry-static"

	retryCEORoleID    = executive.CEORoleID
	retryLeaderRoleID = "ingenieria_ia/orquestador"
	// retryWorkerRoleID is the SUBJECT role -- the cognitive department
	// worker whose task actually gets planned, retried and reassigned to a
	// pool candidate on failure. It must be a worker_agent/specialist role
	// (Executive refuses to assign a deterministic_executor/
	// execution_service role as a cognitive department worker).
	retryWorkerRoleID = "ingenieria_ia/qa"
	// retryDispatchActorRoleID is a SEPARATE identity: the technical
	// process principal's DispatchActorRoleID, which
	// eligibleDispatchActorRole (internal/modeldispatch) requires to be
	// execution_service -- ingenieria_ia/code-runner already is, in the
	// real role-catalog.yaml. It never itself executes a model call or
	// appears as a task's AssignedRoleID; it only authorizes dispatch on
	// the subject role's behalf, exactly as production does.
	retryDispatchActorRoleID = "ingenieria_ia/code-runner"
	retryOrganizationID      = "explorarte"
)

const retryFailoverPolicyYAML = `  retry-failover.static:
    provider: test.fake
    model: retry-static
    transport: fake_adapter
  retry-failover.worker.pool:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    candidates:
      - provider: test.fake
        model: retry-a
        transport: fake_adapter
        capacity_class: free_daily
        priority: 10
      - provider: test.fake
        model: retry-b
        transport: fake_adapter
        capacity_class: free_daily
        priority: 20
`

// retryFailoverEgressRulesYAML allows test.fake at exactly the three
// classifications DispatchService's egress evaluator ever needs for these
// non-secret, non-clinical test tasks. Appended to model-egress-policy.yaml
// so the real DefaultAction: deny gate does not blanket-reject every
// test.fake call.
const retryFailoverEgressRulesYAML = `- provider_id: test.fake
  data_classification: organizational
  effect: allow
  reason_code: retry_failover_test_fixture
- provider_id: test.fake
  data_classification: public
  effect: allow
  reason_code: retry_failover_test_fixture
- provider_id: test.fake
  data_classification: sanitized
  effect: allow
  reason_code: retry_failover_test_fixture
`

var retryCanonicalDocumentNames = []string{
	"organization.yaml", "role-catalog.yaml", "leader-worker-map.yaml",
	"model-routing.yaml", "model-egress-policy.yaml", "capability-matrix.yaml",
	"instruction-precedence.yaml", "decisions-required.yaml", "source-manifest.yaml",
}

// buildRetryFailoverCanonicalDir copies the REAL docs/canonical documents
// verbatim (the productive files on disk are never touched), then applies
// AT MOST ONE of two additions, selected by which flag is set --
// retryFailoverPolicyYAML appended into model-routing.yaml
// (modifyRouting), or the three test.fake allow rules appended into
// model-egress-policy.yaml (modifyEgress).
//
// The two are built into SEPARATE directories, never both into the same
// one, because modelruntime.LoadCanonicalRouting internally re-validates
// ALL 9 canonical documents through internal/organization/registry (to
// cross-check model-routing.yaml's semantic hash) -- and that package's
// OWN, separate, hardcoded productiveEgressAllowRules map (used only by
// its SynchronizeCanonical/Load path, never by modelegress.LoadCanonicalPolicy)
// has no test.fake entry, so a directory carrying BOTH modifications makes
// LoadCanonicalRouting fail on the egress document it never actually
// needed the content of. modelegress.LoadCanonicalPolicy is a fully
// separate, self-contained loader (its own strict decode, its own
// validation against the CALLER-SUPPLIED LoadOptions.ProductiveExplicitRules
// this test controls) and never touches internal/organization/registry at
// all, so loading it from a directory where ONLY the egress document is
// modified is safe.
func buildRetryFailoverCanonicalDir(t *testing.T, modifyRouting, modifyEgress bool) string {
	t.Helper()
	dir := t.TempDir()
	srcDir := filepath.Join("..", "..", "docs", "canonical")
	for _, name := range retryCanonicalDocumentNames {
		body, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			t.Fatalf("read real canonical document %s: %v", name, err)
		}
		if modifyRouting && name == "model-routing.yaml" {
			const marker = "routing_invariants:"
			idx := strings.Index(string(body), marker)
			if idx < 0 {
				t.Fatalf("model-routing.yaml missing %q marker", marker)
			}
			body = []byte(string(body)[:idx] + retryFailoverPolicyYAML + string(body)[idx:])
		}
		if modifyEgress && name == "model-egress-policy.yaml" {
			// rules: is the last top-level key in the real document (no
			// trailing section follows it), so appending more "- provider_id:
			// ..." entries at file end continues the same YAML list.
			body = append(append([]byte(nil), body...), []byte(retryFailoverEgressRulesYAML)...)
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatalf("write %s into temp canonical dir: %v", name, err)
		}
	}
	return dir
}

// retryCatalog is Model Runtime's OWN OrganizationCatalog for this test --
// deliberately separate from Executive's real registry.PostgresRepository
// (which newIntegrationHarness already synchronized from the real
// canonical documents). The three roles below are REAL roles that really
// exist in role-catalog.yaml (empresa/ceo, ingenieria_ia/orquestador,
// ingenieria_ia/code-runner); only their ModelPolicy is overridden here,
// to the test.fake fixture policies above, exactly the way
// dynamic_routing_integration_test.go's catalogFixture overrides
// ingenieria_ia/code-runner alone. AuthorityClass/UnitID are copied
// verbatim from the real role-catalog.yaml so Model Runtime's own
// authorization/capability checks see the same values production does.
type retryCatalog struct {
	organizationRevisionID int64
	egressHash             string
	capabilityHash         string
}

func (c retryCatalog) CurrentOrganization(context.Context, string) (modelruntime.OrganizationRef, error) {
	return modelruntime.OrganizationRef{
		ID: retryOrganizationID, RevisionID: c.organizationRevisionID,
		ModelEgressPolicyHash: c.egressHash, CapabilityMatrixHash: c.capabilityHash,
	}, nil
}

func (c retryCatalog) GetRole(_ context.Context, _, id string) (modelruntime.RoleRef, error) {
	switch id {
	case retryCEORoleID:
		return modelruntime.RoleRef{ID: retryCEORoleID, ModelPolicy: retryFailoverStaticPolicyID, Enabled: true, Executable: true, AuthorityClass: "executive", UnitID: "empresa"}, nil
	case retryLeaderRoleID:
		return modelruntime.RoleRef{ID: retryLeaderRoleID, ModelPolicy: retryFailoverStaticPolicyID, Enabled: true, Executable: true, AuthorityClass: "department_leadership", UnitID: "ingenieria_ia"}, nil
	case retryWorkerRoleID:
		return modelruntime.RoleRef{ID: retryWorkerRoleID, ModelPolicy: retryFailoverPoolPolicyID, Enabled: true, Executable: true, AuthorityClass: "specialist", UnitID: "ingenieria_ia"}, nil
	case retryDispatchActorRoleID:
		// Model Runtime's own DispatchService resolves the dispatch actor
		// role through this SAME catalog (not modeldispatch's separate
		// one), to confirm it too is enabled/executable -- it never
		// dispatches a model call of its own, so it is bound to the
		// static policy only so a lookup here never needs a model_policy
		// that does not exist.
		return modelruntime.RoleRef{ID: retryDispatchActorRoleID, ModelPolicy: retryFailoverStaticPolicyID, Enabled: true, Executable: true, AuthorityClass: "execution_service", UnitID: "ingenieria_ia"}, nil
	}
	return modelruntime.RoleRef{}, fmt.Errorf("retryCatalog: unknown role %q", id)
}

func (c retryCatalog) ListRoles(context.Context, string) ([]modelruntime.RoleRef, error) {
	ceo, _ := c.GetRole(context.Background(), retryOrganizationID, retryCEORoleID)
	leader, _ := c.GetRole(context.Background(), retryOrganizationID, retryLeaderRoleID)
	worker, _ := c.GetRole(context.Background(), retryOrganizationID, retryWorkerRoleID)
	return []modelruntime.RoleRef{ceo, leader, worker}, nil
}

// ============================================================
// retryFailoverAdapter is the ONE synthetic piece on the main path (per
// spec Section 1): test.fake/fake_adapter, deterministic, no HTTP. Its
// only controllable behavior is whether candidate A (retry-a) currently
// answers with a confirmed retryable HTTP 503 or a real success --
// flipped between orchestrator.Resume() calls, never mid-attempt, so each
// attempt's Harness.Execute() call is internally consistent. retry-b and
// retry-static always succeed.
// ============================================================

type retryFailoverAdapter struct {
	mu             sync.Mutex
	failCandidateA bool
	failCandidateB bool
	calls          []retryFailoverCall
	// departmentPlanOverride, when set, replaces the single hardcoded
	// worker task retryFailoverResponseBody's department-plan case
	// produces -- used only by the mixed-workers test, which needs two
	// worker tasks in the same department instead of one. Every other
	// test leaves this nil and gets the shared single-worker body.
	departmentPlanOverride []byte
	// failTaskID, when non-zero, makes Dispatch fail non-retryably any
	// call whose CanonicalRequest.TaskID matches it -- a deterministic
	// way to fail exactly one of several worker tasks in the same
	// department once its real, database-assigned task ID is known.
	failTaskID int64
	// failTaskIDRetryable, when non-zero, makes Dispatch fail with a
	// confirmed RETRYABLE 503 -- the same shape failCandidateA/B produce
	// -- for any call whose CanonicalRequest.TaskID matches it,
	// regardless of which candidate (A or B) is being dispatched. Unlike
	// failCandidateA/B (which fail a candidate for EVERY task routed
	// through it, since model_routing_capacity_state is keyed by
	// provider/model, not by task), this targets one specific worker
	// task's own attempts so a sibling worker in the same department can
	// keep succeeding normally -- CASE 10's multi-worker capacity-wait
	// scenario needs exactly this: one worker's real failures drive both
	// pool candidates into cooldown while another worker, dispatched
	// before that cooldown lands, is never touched.
	failTaskIDRetryable int64
}

type retryFailoverCall struct {
	ProviderModelID string
	IdempotencyKey  string
	InvocationID    int64
}

func (a *retryFailoverAdapter) ProviderID() string { return "test.fake" }

func (a *retryFailoverAdapter) Descriptor() modelruntime.AdapterDescriptor {
	return modelruntime.AdapterDescriptor{
		ProviderID: "test.fake", AdapterID: "retry-failover-test", AdapterVersion: 1,
		Transport: modelruntime.TransportFake, RequestSchemaVersion: "test.fake.request.v1",
		ResponseSchemaVersion: "test.fake.response.v1",
		EndpointFingerprint:   modelruntime.SHA256Bytes([]byte("test.fake:endpoint")),
		CredentialRefHash:     modelruntime.SHA256Bytes([]byte("test.fake:credential")),
	}
}

func (a *retryFailoverAdapter) Preflight(ctx context.Context, _ modelruntime.ProviderPreflightRequest) error {
	return ctx.Err()
}

func (a *retryFailoverAdapter) setFailCandidateA(fail bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failCandidateA = fail
}

func (a *retryFailoverAdapter) setFailCandidateB(fail bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failCandidateB = fail
}

func (a *retryFailoverAdapter) setDepartmentPlanOverride(body []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.departmentPlanOverride = body
}

func (a *retryFailoverAdapter) setFailTaskID(taskID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failTaskID = taskID
}

func (a *retryFailoverAdapter) setFailTaskIDRetryable(taskID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failTaskIDRetryable = taskID
}

func (a *retryFailoverAdapter) callCount(providerModelID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for _, call := range a.calls {
		if call.ProviderModelID == providerModelID {
			count++
		}
	}
	return count
}

func (a *retryFailoverAdapter) idempotencyKeysFor(providerModelID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var keys []string
	for _, call := range a.calls {
		if call.ProviderModelID == providerModelID {
			keys = append(keys, call.IdempotencyKey)
		}
	}
	return keys
}

func (a *retryFailoverAdapter) Dispatch(_ context.Context, req modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	a.mu.Lock()
	failA := a.failCandidateA
	failB := a.failCandidateB
	planOverride := a.departmentPlanOverride
	failTaskID := a.failTaskID
	failTaskIDRetryable := a.failTaskIDRetryable
	a.calls = append(a.calls, retryFailoverCall{ProviderModelID: req.ProviderModelID, IdempotencyKey: req.ProviderIdempotencyKey, InvocationID: req.InvocationID})
	a.mu.Unlock()

	if (req.ProviderModelID == retryCandidateA && failA) || (req.ProviderModelID == retryCandidateB && failB) ||
		(failTaskIDRetryable != 0 && req.TaskID == failTaskIDRetryable && (req.ProviderModelID == retryCandidateA || req.ProviderModelID == retryCandidateB)) {
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{
			Phase: modelruntime.AdapterFailureResponseReceived,
			Outcome: modelruntime.ProviderOutcome{
				OutcomeClassification: modelruntime.ProviderOutcomeRejected,
				HTTPStatus:            503, ErrorClass: "transport", ErrorCode: "http_error",
				Retryable:             true,
				ResponseHash:          modelruntime.SHA256Bytes([]byte("retry-failover-503-" + strconv.FormatInt(req.InvocationID, 10))),
				ResponseSchemaVersion: "test.fake.response.v1",
			},
			Cause: errors.New("retry failover test: candidate confirmed 503"),
		}
	}

	if failTaskID != 0 && req.TaskID == failTaskID {
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{
			Phase: modelruntime.AdapterFailureResponseReceived,
			Outcome: modelruntime.ProviderOutcome{
				OutcomeClassification: modelruntime.ProviderOutcomeRejected,
				HTTPStatus:            400, ErrorClass: "policy", ErrorCode: "rejected",
				Retryable:             false,
				ResponseHash:          modelruntime.SHA256Bytes([]byte("retry-failover-400-" + strconv.FormatInt(req.InvocationID, 10))),
				ResponseSchemaVersion: "test.fake.response.v1",
			},
			Cause: errors.New("retry failover test: marked worker task confirmed non-retryable 400"),
		}
	}

	if planOverride != nil && strings.Contains(string(req.OutputSchema), "assigned_role_id") && !strings.Contains(string(req.OutputSchema), "unsatisfied_criteria") {
		hash := modelruntime.SHA256Bytes(planOverride)
		return modelruntime.RawResponse{
			ProviderRequestID: "retry-failover-" + hash[:16],
			InputTokens:       int64(len(req.RenderedContext) / 4),
			OutputTokens:      16,
			Content:           planOverride,
			ProviderOutcome: modelruntime.ProviderOutcome{
				OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
				ProviderRequestID:     "retry-failover-" + hash[:16], HTTPStatus: 200,
				ResponseHash:          hash,
				ResponseSchemaVersion: "test.fake.response.v1",
			},
		}, nil
	}

	body := retryFailoverResponseBody(req.OutputSchema)
	hash := modelruntime.SHA256Bytes(body)
	return modelruntime.RawResponse{
		ProviderRequestID: "retry-failover-" + hash[:16],
		InputTokens:       int64(len(req.RenderedContext) / 4),
		OutputTokens:      16,
		ProviderReported:  false,
		Content:           body,
		ProviderOutcome: modelruntime.ProviderOutcome{
			OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
			ProviderRequestID:     "retry-failover-" + hash[:16], HTTPStatus: 200,
			ResponseHash:          hash,
			ResponseSchemaVersion: "test.fake.response.v1",
		},
	}, nil
}

// retryFailoverResponseBody picks the right typed JSON body for whichever
// Executive purpose is calling, detected from a marker field unique to
// that purpose's OutputSchema (the exact same shapes
// postgres_integration_test.go's integrationModelRuntime.output already
// proves valid against the real schemas -- reused verbatim here, now
// produced by a real provider dispatch instead of a fake Harness).
func retryFailoverResponseBody(schema json.RawMessage) []byte {
	s := string(schema)
	switch {
	case strings.Contains(s, "department_requests"):
		return []byte(`{"schema_version":"executive-plan/v1","objective":"analyze","department_requests":[{"unit_id":"ingenieria_ia","objective":"inspect","deliverable":"report","priority":10,"constraints":[]}],"global_constraints":[],"success_criteria":["verified"],"owner_decisions_required":[]}`)
	// department-review/v2's own schema (departmentReviewOutputSchema in
	// schemas.go) embeds taskOutputSchemaJSON verbatim for
	// proposed_followup_tasks.items, so its raw text ALSO contains
	// "assigned_role_id" -- this case must be checked before that one, or
	// every department-review call wrongly matches the department-plan
	// body below (missing "verdict", which is what a real schema-mismatch
	// error looked like before this reordering).
	case strings.Contains(s, "unsatisfied_criteria"):
		return []byte(`{"schema_version":"department-review/v2","verdict":"accept","findings":["criteria satisfied"],"unsatisfied_criteria":[],"evidence_refs":[],"proposed_followup_tasks":[],"revision_outcomes":[],"followup_ownership":[]}`)
	case strings.Contains(s, "assigned_role_id"):
		return []byte(`{"schema_version":"department-plan/v2","department_id":"ingenieria_ia","tasks":[{"client_key":"inspect","assigned_role_id":"ingenieria_ia/qa","task_class":"general.work","title":"Inspect state","instructions":"Inspect the bounded task context and report findings.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":5}],"review_criteria":["findings verified"],"unresolved":[],"revision_ownership":[]}`)
	case strings.Contains(s, "answer_to_owner"):
		return []byte(`{"schema_version":"executive-closure/v1","status":"completed","answer_to_owner":"The requested area was analyzed with verified evidence.","completed_items":["engineering analysis"],"blocked_items":[],"unresolved_decisions":[],"evidence_refs":["integration:evidence:1"]}`)
	default:
		return []byte(`{"schema_version":"worker-result/v1","summary":"bounded findings","evidence_refs":["integration:evidence:1"]}`)
	}
}

// ============================================================
// Model Dispatch mini-adapters -- local copies of the unexported
// catalogAdapter/taskAdapter internal/modeldispatch/bootstrap uses,
// reproduced here (not imported, they are unexported) so this test can
// construct modeldispatch.NewPrincipalService/NewAssignmentService/
// NewAuthorizedAttemptProvisioner directly instead of pulling in the full
// production bootstrap (which requires real provider credentials).
// ============================================================

type retryDispatchCatalog struct{ reader registry.Reader }

func (c retryDispatchCatalog) CurrentRevision(ctx context.Context, organizationID string) (int64, error) {
	revision, err := c.reader.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	if revision == nil {
		return 0, registry.ErrNotFound
	}
	return revision.ID, nil
}

func (c retryDispatchCatalog) GetRole(ctx context.Context, organizationID, roleID string) (modeldispatch.RoleRef, error) {
	r, err := c.reader.GetRole(ctx, organizationID, roleID)
	if err != nil {
		return modeldispatch.RoleRef{}, err
	}
	// Existence/enabled/executable/authority-class come from the real
	// registry, but model_policy is overridden to match this test's own
	// retryCatalog (modelruntime's own catalog) for the three roles this
	// fixture repoints -- the real role-catalog.yaml's static model_policy
	// for these roles is irrelevant here, only the pool/static assignment
	// this test constructed matters.
	policy := ""
	if r.ModelPolicy != nil {
		policy = *r.ModelPolicy
	}
	switch roleID {
	case retryCEORoleID, retryLeaderRoleID, retryDispatchActorRoleID:
		policy = retryFailoverStaticPolicyID
	case retryWorkerRoleID:
		policy = retryFailoverPoolPolicyID
	}
	return modeldispatch.RoleRef{ID: r.ID, ModelPolicy: policy, Enabled: r.Enabled, Executable: r.Executable, AuthorityClass: r.AuthorityClass}, nil
}

type retryDispatchTasks struct{ reader tasks.TaskReader }

func (a retryDispatchTasks) GetTaskAttempt(ctx context.Context, taskID, attemptID int64) (modeldispatch.TaskAttemptRef, error) {
	detail, err := a.reader.GetTask(ctx, taskID)
	if err != nil {
		return modeldispatch.TaskAttemptRef{}, err
	}
	var attempt *tasks.Attempt
	for i := range detail.Attempts {
		if detail.Attempts[i].ID == attemptID {
			attempt = &detail.Attempts[i]
			break
		}
	}
	if attempt == nil {
		return modeldispatch.TaskAttemptRef{}, tasks.ErrNotFound
	}
	if detail.ActiveLease == nil || detail.ActiveLease.AttemptID != attemptID {
		return modeldispatch.TaskAttemptRef{}, modeldispatch.ErrTaskAttemptRejected
	}
	return modeldispatch.TaskAttemptRef{
		TaskID: detail.Task.ID, AttemptID: attempt.ID, OrganizationID: detail.Task.OrganizationID,
		OrganizationRevisionID: detail.Task.OrganizationRevisionID, AssignedRoleID: detail.Task.AssignedRoleID,
		TaskStatus: string(detail.Task.Status), AttemptStatus: string(attempt.State),
		LeaseHolderID: detail.ActiveLease.HolderID, LeaseExpiresAt: detail.ActiveLease.ExpiresAt,
	}, nil
}

func (a retryDispatchTasks) GetTaskLineage(ctx context.Context, taskID int64) (modeldispatch.TaskLineageRef, error) {
	detail, err := a.reader.GetTask(ctx, taskID)
	if err != nil {
		return modeldispatch.TaskLineageRef{}, err
	}
	requester, correlation, causation := "", "", ""
	if detail.Task.RequestedByRoleID != nil {
		requester = *detail.Task.RequestedByRoleID
	}
	if detail.Task.CorrelationID != nil {
		correlation = *detail.Task.CorrelationID
	}
	if detail.Task.CausationID != nil {
		causation = *detail.Task.CausationID
	}
	return modeldispatch.TaskLineageRef{
		TaskID: detail.Task.ID, OrganizationID: detail.Task.OrganizationID,
		OrganizationRevisionID: detail.Task.OrganizationRevisionID, RequestedByRoleID: requester,
		AssignedRoleID: detail.Task.AssignedRoleID, CorrelationID: correlation, CausationID: causation,
	}, nil
}

// ============================================================
// retryFailoverRuntime bundles the REAL Model Runtime + REAL Execution
// Harness this test drives Executive through -- everything downstream of
// Executive's HarnessExecutor/ModelInvocationReader ports is real
// PostgreSQL-backed production code (InvocationService, DispatchService,
// Dynamic Canonical Model Routing, Model Capacity State V1, Execution
// Harness Runtime); only the provider adapter is synthetic.
// ============================================================

type retryFailoverRuntime struct {
	adapter      *retryFailoverAdapter
	store        *modelpostgres.Store
	invocations  *modelruntime.InvocationService
	revisionID   int64
	models       runtimeadapter.Models
	harness      runtimeadapter.Harness
	contexts     *retryContextRegistry
	executionKey string
	assignments  executive.DispatchProvisioner
}

func newRetryFailoverRuntime(t *testing.T, h *integrationHarness) *retryFailoverRuntime {
	t.Helper()
	ctx := h.ctx

	// newIntegrationHarness truncates the EXECUTIVE package's own tables
	// (organizations, organization_roles, organization_registry_revisions,
	// tasks, ...) but has no reason to know about the model-runtime/
	// model-dispatch tables this fixture alone populates -- so with
	// several retry-failover tests in this file, each running its own
	// fresh newIntegrationHarness, rows this function creates in one test
	// (routing_policies/routing_candidates/role_model_bindings/
	// model_dispatcher_assignments/model_execution_principals/... keyed by
	// an organization_revision_id that RESTART IDENTITY makes the NEXT
	// test's own revision reuse) silently collide with the next test's
	// attempt to materialize the same (organization, revision, policy)
	// tuple. Confirmed empirically: running this file's tests together
	// (not in isolation) broke on exactly this.
	if _, err := h.store.Pool().Exec(ctx, `
TRUNCATE model_dispatcher_assignment_uses,model_dispatcher_assignments,model_execution_principals,
         model_egress_evaluations,model_invocation_usage,model_invocation_results,model_dispatch_attempts,
         model_provider_outcomes,model_provider_requests,model_invocations,model_routing_capacity_state,
         model_egress_revision_bindings,model_egress_rules,model_egress_policy_versions,
         model_execution_identity_keys,routing_candidates,routing_policies,role_model_bindings,
         model_capability_snapshots,model_profile_versions,model_profiles,model_providers,
         execution_run_descriptors,context_segments,context_snapshots
RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset model-runtime/model-dispatch schema: %v", err)
	}

	modelStore, err := modelpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	egressStore, err := egresspostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	identityStore, err := identitypostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	taskLeaseStore, err := taskpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	harnessHistory, err := executionharnesspostgres.New(h.store, retryOrganizationID)
	if err != nil {
		t.Fatal(err)
	}

	// Two SEPARATE temp directories, each with exactly one of this test's
	// two canonical additions -- see buildRetryFailoverCanonicalDir's doc
	// comment for why they cannot be combined into one.
	tmpRouting := buildRetryFailoverCanonicalDir(t, true, false)
	routing, err := modelruntime.LoadCanonicalRouting(tmpRouting)
	if err != nil {
		t.Fatalf("LoadCanonicalRouting: %v", err)
	}
	tmpEgress := buildRetryFailoverCanonicalDir(t, false, true)
	egressOptions := modelegress.ProductiveLoadOptions([]string{"deepseek", "openai_compatible", "openai_responses", "gemini", "cloudflare_workers_ai", "xai", "mistral"})
	// test.fake is added directly (not through ProductiveLoadOptions, whose
	// callers are all production providers) -- this document is the ONLY
	// place the fixture asserts test.fake may answer organizational/public/
	// sanitized calls; internal/organization/registry's OWN separate,
	// hardcoded productiveEgressAllowRules map (used only by its own
	// SynchronizeCanonical path, which this test never calls) has no such
	// entry and stays untouched.
	egressOptions.KnownProviders = append(egressOptions.KnownProviders, "test.fake")
	egressOptions.ProductiveExplicitRules["test.fake"] = []modelegress.DataClassification{modelegress.ClassificationOrganizational, modelegress.ClassificationPublic, modelegress.ClassificationSanitized}
	egressPolicy, err := modelegress.LoadCanonicalPolicy(tmpEgress, egressOptions)
	if err != nil {
		t.Fatalf("LoadCanonicalPolicy: %v", err)
	}

	// A revision Model Runtime's OWN routing/egress stores own, entirely
	// separate from the real organization_registry_revisions row
	// newIntegrationHarness's canonical sync already produced --
	// InvocationService.Create resolves organization and revision purely
	// through retryCatalog, never through task.OrganizationRevisionID, so
	// the two are independent by construction (see route_resolver.go/
	// invocation_service.go: nothing in RunIdentity carries a revision id
	// at all). Model Runtime's registry stores (egress.Apply,
	// modelStore.ApplyRegistry) and Executive's own resource authorizer
	// BOTH gate on organizations.current_revision_id -- the authorizer
	// specifically cross-checks document_hashes['capability-matrix.yaml']
	// against what it independently computes from the real file, so that
	// entry (and every other unchanged document's) is copied from the
	// REAL current revision verbatim; only the two modified documents get
	// fresh hashes.
	var originalRevisionID int64
	var originalCanonicalHash string
	var originalDocumentHashesRaw []byte
	if err = h.store.Pool().QueryRow(ctx, `
SELECT r.id, r.canonical_hash, r.document_hashes
FROM organizations o JOIN organization_registry_revisions r ON r.id = o.current_revision_id
WHERE o.id = $1`, retryOrganizationID).Scan(&originalRevisionID, &originalCanonicalHash, &originalDocumentHashesRaw); err != nil {
		t.Fatal(err)
	}
	documentHashesMap := make(map[string]string)
	if err = json.Unmarshal(originalDocumentHashesRaw, &documentHashesMap); err != nil {
		t.Fatal(err)
	}
	documentHashesMap["model-routing.yaml"] = routing.Hash
	documentHashesMap["model-egress-policy.yaml"] = egressPolicy.CanonicalHash
	capabilityHash := documentHashesMap["capability-matrix.yaml"]
	documentHashesJSON, err := json.Marshal(documentHashesMap)
	if err != nil {
		t.Fatal(err)
	}
	var revisionID int64
	if err = h.store.Pool().QueryRow(ctx, `INSERT INTO organization_registry_revisions(canonical_hash,status,schema_versions,document_hashes,counts,diff,applied_at) VALUES($1,'applied','{}',$2::jsonb,'{}','{}',clock_timestamp()) RETURNING id`,
		originalCanonicalHash, documentHashesJSON).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$1,updated_at=clock_timestamp() WHERE id=$2`, revisionID, retryOrganizationID); err != nil {
		t.Fatal(err)
	}
	// A real org-registry sync (internal/organization/registry's
	// postgres_repository.go ApplyDiff) unconditionally upserts every
	// role's source_revision_id to the new revision on every sync, whether
	// or not that role's own data changed -- this fixture bumps
	// organizations.current_revision_id by hand instead of running a real
	// sync, so it must mirror that same unconditional bump here.
	//
	// This E2E intentionally substitutes test-only routing for the three
	// cognitive roles it drives plus its technical dispatch actor. Keep the
	// persisted authority equal to that substituted catalog: the dispatcher
	// derives authority from organization_roles rather than trusting the
	// catalog adapter's convenience override. A real canonical sync would
	// establish the same agreement through role-catalog.yaml before model
	// registry materialization. This is confined to the disposable test DB.
	// modeldispatch.Store.GetRoleRoutingAuthority requires
	// organization_roles.source_revision_id to equal the queried revision
	// exactly (a real deployment's roles are never stale relative to the
	// current revision), and without this every role would still show
	// source_revision_id from before this fixture's revision bump.
	if _, err = h.store.Pool().Exec(ctx, `
UPDATE organization_roles
SET source_revision_id=$1,
    model_policy=CASE id
        WHEN $3 THEN $4
        WHEN $5 THEN $4
        WHEN $6 THEN $7
        WHEN $8 THEN $4
        ELSE model_policy
    END
WHERE organization_id=$2`, revisionID, retryOrganizationID,
		retryCEORoleID, retryFailoverStaticPolicyID,
		retryLeaderRoleID, retryWorkerRoleID, retryFailoverPoolPolicyID,
		retryDispatchActorRoleID); err != nil {
		t.Fatal(err)
	}

	egressPlan := modelegress.RegistryPlan{
		OrganizationID: retryOrganizationID, OrganizationRevisionID: revisionID,
		CanonicalHash: egressPolicy.CanonicalHash, Policy: egressPolicy,
	}
	if _, err = egressStore.Apply(ctx, egressPlan); err != nil {
		t.Fatalf("apply fixture egress policy: %v", err)
	}

	// CAPACITY_EXHAUSTION_SCHEDULING_V1: wire the SAME kind of capacity
	// gate internal/executive/bootstrap wires in production
	// (newCapacityGate there), reimplemented here only because
	// package executive_test importing internal/executive/bootstrap's
	// unexported constructor is not possible across the package boundary
	// -- the logic itself is not a second retry engine, it is a read-only
	// peek that reuses modelrouting.LookupSelector(...).Select, the exact
	// function RouteResolver itself calls.
	h.tasks.SetCapacityGate(newTestCapacityGate(h.registry, modelStore, retryOrganizationID))
	identityCanonical, err := modelidentity.LoadCanonicalPolicy(filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = identityStore.Apply(ctx, retryOrganizationID, identityCanonical); err != nil {
		t.Fatal(err)
	}

	plan, err := modelruntime.BuildRegistryPlan(
		[]modelruntime.RoleRef{
			{ID: retryCEORoleID, ModelPolicy: retryFailoverStaticPolicyID, Enabled: true, Executable: true},
			{ID: retryLeaderRoleID, ModelPolicy: retryFailoverStaticPolicyID, Enabled: true, Executable: true},
			{ID: retryWorkerRoleID, ModelPolicy: retryFailoverPoolPolicyID, Enabled: true, Executable: true},
			{ID: retryDispatchActorRoleID, ModelPolicy: retryFailoverStaticPolicyID, Enabled: true, Executable: true},
		},
		modelruntime.OrganizationRef{ID: retryOrganizationID, RevisionID: revisionID},
		routing,
	)
	if err != nil {
		t.Fatalf("BuildRegistryPlan: %v", err)
	}
	if applied, applyErr := modelStore.ApplyRegistry(ctx, plan, 10); applyErr != nil || !applied.Applied {
		t.Fatalf("ApplyRegistry(plan) = %+v err=%v", applied, applyErr)
	}

	catalog := retryCatalog{organizationRevisionID: revisionID, egressHash: egressPolicy.CanonicalHash, capabilityHash: capabilityHash}

	dispatchCfg, err := modeldispatch.LoadDispatchConfig(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	dispatchCatalog := retryDispatchCatalog{reader: h.registry}
	dispatchTasks := retryDispatchTasks{reader: h.tasks}
	assignmentsService, err := modeldispatch.NewAssignmentService(retryOrganizationID, h.authorizer, dispatchCatalog, dispatchTasks, h.dispatch, h.dispatch, modeldispatch.ClockFunc(time.Now), dispatchCfg.AssignmentDefaultTTL, dispatchCfg.AssignmentMaxTTL)
	if err != nil {
		t.Fatal(err)
	}
	executionKey := "retry-failover-technical-principal"
	// The technical process principal AuthorizedAttemptProvisioner resolves
	// on every EnsureAuthorizedAssignmentForRunningAttempt call must already
	// exist -- it is looked up (principals.ResolveByKey), never lazily
	// created. Registered directly through the store (bypassing
	// PrincipalService.Register's own capability-authorization gate, which
	// production wiring satisfies through a real actor role this test has
	// no reason to reconstruct) -- the same pattern
	// modelruntime/postgres/integration_test.go's fixturePrincipalAndAssignment
	// uses. DispatchActorRoleID is retryDispatchActorRoleID, NOT the
	// subject worker role: eligibleDispatchActorRole requires an
	// execution_service role, and the cognitive worker
	// (ingenieria_ia/qa) is a specialist, not execution_service.
	principalHash, err := modeldispatch.PrincipalRequestHash(retryOrganizationID, executionKey, retryDispatchActorRoleID, modeldispatch.PrincipalLocalProcess, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	registeredPrincipal, err := h.dispatch.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{
		Command: modeldispatch.RegisterPrincipalCommand{
			OrganizationID: retryOrganizationID, PrincipalKey: executionKey,
			DispatchActorRoleID: retryDispatchActorRoleID, PrincipalKind: modeldispatch.PrincipalLocalProcess,
			IdempotencyKey: "retry-failover-technical-principal-registration",
		},
		RequestHash: principalHash, RegisteredByRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizedAssignments, err := modeldispatch.NewAuthorizedAttemptProvisioner(assignmentsService, dispatchTasks, h.dispatch, executionKey)
	if err != nil {
		t.Fatal(err)
	}

	tasksAdapter := retryTaskAttemptReader{reader: h.tasks}
	contexts := newRetryContextRegistry(h.store, revisionID)

	invocationService, err := modelruntime.NewInvocationService(retryOrganizationID, catalog, tasksAdapter, contexts, modelStore, egressStore, identityStore, h.dispatch, modelruntime.ClockFunc(time.Now), 10, false)
	if err != nil {
		t.Fatal(err)
	}

	providerAdapter := &retryFailoverAdapter{}
	// DispatchService.Dispatch unconditionally rejects with
	// ErrExecutionIdentityDenied once an invocation has a pinned execution
	// identity policy (InvocationService.Create always pins one) unless
	// ExecutionIdentityEnabled is true and a real, registered signing key
	// backs it -- there is no "identity enabled but unused" shortcut on
	// this path, so a real ed25519 key is registered for the technical
	// principal, same as the capacity-state/dynamic-routing rounds'
	// pattern.
	identityPublicKey, identityPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identityKeyDER, err := x509.MarshalPKCS8PrivateKey(identityPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	identityKeyFile := filepath.Join(t.TempDir(), "retry-failover-execution-identity.pem")
	if err = os.WriteFile(identityKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: identityKeyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	preparedIdentityKey := modelidentity.PreparedKey{
		OrganizationID: retryOrganizationID, ExecutionPrincipalID: registeredPrincipal.Principal.ID,
		PublicKey: identityPublicKey, PublicKeyFingerprint: modelidentity.PublicKeyFingerprint(identityPublicKey),
		SecretRef: "file://retry-failover/key-1", IdempotencyKey: "retry-failover-identity-key",
		CreatedByRoleID: "empresa/human",
	}
	preparedIdentityKey.RequestHash, err = modelidentity.KeyRequestHash(preparedIdentityKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = identityStore.RegisterKey(ctx, preparedIdentityKey); err != nil {
		t.Fatal(err)
	}
	identityService, err := modelidentity.NewChallengeService(identityStore, modelidentity.ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	cfg := modelruntime.RuntimeConfig{
		Enabled: true, CommandTimeout: 30 * time.Second, GlobalConcurrency: 4,
		MaxResponseBytes: 1 << 20, MaxToolIntents: 8, ClaimTTL: time.Minute,
		ReconcileBatchSize: 100, OutboxMaxAttempts: 10,
		ExecutionPrincipalKey: executionKey, ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: identityKeyFile,
	}
	dispatchService, err := modelruntime.NewDispatchService(retryOrganizationID, cfg, catalog, tasksAdapter, contexts, retryAllowEvaluator{matrixHash: capabilityHash}, egressStore, modelegress.NewEvaluator(), modelStore, h.dispatch, h.dispatch, identityService, modelStore, adapter.NewRegistry(providerAdapter), modelruntime.ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}

	principalReader, err := tasksauthority.NewCanonicalPrincipalReader(h.dispatch)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := tasksauthority.New(taskLeaseStore, principalReader)
	if err != nil {
		t.Fatal(err)
	}

	models := runtimeadapter.Models{Service: invocationService, OrganizationID: retryOrganizationID}
	harness := runtimeadapter.Harness{
		OrganizationID: retryOrganizationID,
		Authority:      authority,
		History:        harnessHistory,
		NewModelExecutor: func(config modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return modelruntimeadapter.New(invocationService, dispatchService, nil, config)
		},
		Clock: executive.ClockFunc(time.Now),
	}

	_ = assignmentsService

	return &retryFailoverRuntime{
		adapter: providerAdapter, store: modelStore, invocations: invocationService,
		revisionID: revisionID, models: models, harness: harness, contexts: contexts, executionKey: executionKey,
		assignments: runtimeadapter.Assignment{Resolver: h.dispatch, Provisioner: authorizedAssignments, OrganizationID: retryOrganizationID},
	}
}

type retryTaskAttemptReader struct{ reader *tasks.Service }

func (r retryTaskAttemptReader) GetTaskAttempt(ctx context.Context, taskID, attemptID int64) (modelruntime.TaskAttemptRef, error) {
	detail, err := r.reader.GetTask(ctx, taskID)
	if err != nil {
		return modelruntime.TaskAttemptRef{}, err
	}
	var attempt *tasks.Attempt
	for i := range detail.Attempts {
		if detail.Attempts[i].ID == attemptID {
			attempt = &detail.Attempts[i]
			break
		}
	}
	if attempt == nil {
		return modelruntime.TaskAttemptRef{}, tasks.ErrNotFound
	}
	holder, expires := "", time.Time{}
	if detail.ActiveLease != nil && detail.ActiveLease.AttemptID == attemptID {
		holder, expires = detail.ActiveLease.HolderID, detail.ActiveLease.ExpiresAt
	}
	return modelruntime.TaskAttemptRef{
		TaskID: detail.Task.ID, AttemptID: attempt.ID, OrganizationID: detail.Task.OrganizationID,
		OrganizationRevisionID: detail.Task.OrganizationRevisionID, AssignedRoleID: detail.Task.AssignedRoleID,
		TaskStatus: string(detail.Task.Status), AttemptStatus: string(attempt.State),
		LeaseHolderID: holder, LeaseExpiresAt: expires,
	}, nil
}

// retryContextRegistry is BOTH Executive's ContextService (Build) and Model
// Runtime's ContextReader (GetContextSnapshot/ValidateContextSnapshot/
// RenderContextSnapshot) for this test, sharing one map so the two sides
// agree on the facts Model Runtime's own validateContext cross-checks that
// a role/task-oblivious counter (postgres_integration_test.go's
// integrationContext) cannot supply: which role and which task a given
// snapshot ID was actually built for (ContextSnapshotRef.ActorRoleID must
// equal the invocation's SubjectRoleID, and TaskRef its bare TaskID, or
// Model Runtime rejects the context before ever reaching
// InvocationService.Create).
type retrySnapshotFixture struct {
	actorRole string
	taskRef   string
	content   string
}

type retryContextRegistry struct {
	mu         sync.Mutex
	store      *platformpostgres.Store
	snapshots  map[int64]retrySnapshotFixture
	revisionID int64
}

func newRetryContextRegistry(store *platformpostgres.Store, revisionID int64) *retryContextRegistry {
	return &retryContextRegistry{store: store, snapshots: map[int64]retrySnapshotFixture{}, revisionID: revisionID}
}

// Build inserts a REAL context_snapshots row -- model_invocations has a hard
// FK to it (model_invocations_context_snapshot_id_fkey), so an in-memory-only
// id is rejected the moment InvocationService.Create tries to persist.
// content/actor role/task ref are ALSO kept in the in-memory map below,
// read back by GetContextSnapshot/RenderContextSnapshot -- the row itself
// only needs to exist and satisfy the FK; this test does not exercise
// context assembly correctness.
func (r *retryContextRegistry) Build(ctx context.Context, request executive.ContextRequest) (executive.ContextSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// request.TaskRef arrives as "task:<id>" (see driveTypedTask); Model
	// Runtime's own validateContext compares against the bare TaskID
	// (strconv.FormatInt(c.TaskID, 10)), so the prefix is stripped here.
	// The SAME content is what RenderContextSnapshot returns below --
	// PrepareModelInput requires SHA256(rendered) == snapshot.RenderedHash,
	// so Build, GetContextSnapshot and RenderContextSnapshot must all agree
	// on one fixed string per id, never three independently-formatted ones.
	taskRef := strings.TrimPrefix(request.TaskRef, "task:")
	now := time.Now().UTC()
	var id int64
	if err := r.store.Pool().QueryRow(ctx, `
INSERT INTO context_snapshots(
    organization_id,organization_revision_id,actor_role_id,purpose,task_ref,
    idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,
    status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,created_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ready',1,0,0,0,0,$11)
RETURNING id`,
		retryOrganizationID, r.revisionID, request.ActorRoleID, request.Purpose, request.TaskRef,
		request.IdempotencyKey, modelruntime.SHA256Bytes([]byte(request.IdempotencyKey)),
		modelruntime.SHA256Bytes([]byte("precedence")), modelruntime.SHA256Bytes([]byte("bundle")),
		modelruntime.SHA256Bytes([]byte("placeholder")), now,
	).Scan(&id); err != nil {
		return executive.ContextSnapshot{}, err
	}
	content := fmt.Sprintf("retry failover integration context snapshot %d role %s task %s", id, request.ActorRoleID, request.TaskRef)
	r.snapshots[id] = retrySnapshotFixture{actorRole: request.ActorRoleID, taskRef: taskRef, content: content}
	digest := modelruntime.SHA256Bytes([]byte(content))
	return executive.ContextSnapshot{ID: id, Version: "1", Digest: digest, Content: content}, nil
}

func (r *retryContextRegistry) GetContextSnapshot(_ context.Context, id int64) (modelruntime.ContextSnapshotRef, error) {
	r.mu.Lock()
	fixture := r.snapshots[id]
	r.mu.Unlock()
	return modelruntime.ContextSnapshotRef{
		ID: id, OrganizationID: retryOrganizationID, OrganizationRevisionID: r.revisionID,
		ActorRoleID: fixture.actorRole, TaskRef: fixture.taskRef, Status: "ready",
		RenderedHash: modelruntime.SHA256Bytes([]byte(fixture.content)), DataClasses: []string{"organizational"},
	}, nil
}
func (*retryContextRegistry) ValidateContextSnapshot(context.Context, int64) error { return nil }
func (r *retryContextRegistry) RenderContextSnapshot(_ context.Context, id int64) ([]byte, error) {
	r.mu.Lock()
	fixture := r.snapshots[id]
	r.mu.Unlock()
	return []byte(fixture.content), nil
}

type retryAllowEvaluator struct{ matrixHash string }

func (r retryAllowEvaluator) EvaluateDispatch(context.Context, string, int64, string, string, string) (modelruntime.AuthorizationDecision, error) {
	return modelruntime.AuthorizationDecision{Effect: modelegress.AuthorizationAllow, Allowed: true, ReasonCode: "allowed_by_grant", MatrixHash: r.matrixHash}, nil
}

// ============================================================
// Orchestrator wiring + drive loop
// ============================================================

func newRetryFailoverOrchestrator(t *testing.T, h *integrationHarness, runtime *retryFailoverRuntime, limits executive.Limits, opts ...executive.OrchestratorOption) *executive.Orchestrator {
	t.Helper()
	value, err := executive.NewOrchestrator(executive.Dependencies{
		Acceptance:     newIntegrationAcceptance(),
		OrganizationID: retryOrganizationID,
		Registry:       runtimeadapter.Registry{Reader: h.registry, OrganizationID: retryOrganizationID},
		Tasks:          runtimeadapter.Tasks{Service: h.tasks, OrganizationID: retryOrganizationID},
		Contexts:       runtime.contexts,
		Assignments:    runtime.assignments,
		Principals:     h.principals,
		Models:         runtime.models,
		Harness:        runtime.harness,
		Budget: runtimeadapter.ModelCallBudget{
			Models: runtime.models,
			Tasks:  runtimeadapter.Tasks{Service: h.tasks, OrganizationID: retryOrganizationID},
			Limits: limits,
		},
		Completion:    h.completion,
		Decisions:     h.decisions,
		Authorization: h.authz,
		Limits:        limits,
		Clock:         executive.ClockFunc(time.Now),
	}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// driveRetryFailoverRun resumes the run until it converges or max is
// exhausted, reconciling before every resume so a task engine backoff
// (retry_wait -> ready once available_at passes) is picked up without
// depending on any autonomous timer -- this codebase has none; every
// recurring sweep is operator/cron-invoked (see
// internal/tasks/postgres/reconcile.go). The short sleep is real wall-clock
// time elapsing for a real backoff delay, not a workaround.
func driveRetryFailoverRun(t *testing.T, h *integrationHarness, runtime *retryFailoverRuntime, orchestrator *executive.Orchestrator, rootID int64, max int) (executive.Run, error) {
	t.Helper()
	var run executive.Run
	var err error
	for i := 0; i < max; i++ {
		if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
			t.Fatalf("reconcile: %v", reconcileErr)
		}
		run, err = orchestrator.Resume(h.ctx, rootID)
		// Resume intentionally surfaces the causal failure even when
		// ErrTaskRetryScheduled proves the Task Engine has durably moved this
		// task to retry_wait. It is not a terminal campaign error: reconcile
		// will make the next attempt ready, so keep driving this E2E. Every
		// other error is still an immediate failure for the harness.
		if errors.Is(err, executive.ErrTaskRetryScheduled) {
			state, stateErr := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateA)
			t.Logf("retry scheduled at drive iteration %d; A capacity=%+v capacity_err=%v", i, state, stateErr)
			err = nil
		}
		if err != nil || run.State == executive.StateCompleted || run.State == executive.StateFailed || run.State == executive.StateBlocked {
			return run, err
		}
		time.Sleep(1100 * time.Millisecond)
	}
	return run, fmt.Errorf("executive run did not converge after %d resumes", max)
}

func submitRetryFailoverGoal(t *testing.T, h *integrationHarness, orchestrator *executive.Orchestrator, idempotencyKey string) int64 {
	t.Helper()
	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: idempotencyKey,
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}
	return run.RootTaskID
}

// workerTask finds this campaign's ONE department-worker task -- the only
// task assigned to retryWorkerRoleID -- among the correlated tasks the
// root produced.
func workerTask(t *testing.T, h *integrationHarness, rootID int64) tasks.TaskDetail {
	t.Helper()
	root, err := h.tasks.GetTask(h.ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if root.Task.CorrelationID == nil {
		t.Fatalf("root task %d has no correlation id", rootID)
	}
	correlated, err := h.tasks.ListTasks(h.ctx, tasks.TaskFilter{OrganizationID: retryOrganizationID, CorrelationID: *root.Task.CorrelationID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range correlated {
		if task.AssignedRoleID == retryWorkerRoleID {
			detail, err := h.tasks.GetTask(h.ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			return detail
		}
	}
	t.Fatalf("no worker task found for root %d among %d correlated tasks", rootID, len(correlated))
	return tasks.TaskDetail{}
}

// workerTasksFor returns EVERY department-worker task (retryWorkerRoleID)
// among the campaign's correlated tasks, for scenarios with more than one
// worker in the same department (workerTask only ever returns the first).
func workerTasksFor(t *testing.T, h *integrationHarness, rootID int64) []tasks.TaskDetail {
	t.Helper()
	root, err := h.tasks.GetTask(h.ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if root.Task.CorrelationID == nil {
		t.Fatalf("root task %d has no correlation id", rootID)
	}
	correlated, err := h.tasks.ListTasks(h.ctx, tasks.TaskFilter{OrganizationID: retryOrganizationID, CorrelationID: *root.Task.CorrelationID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var out []tasks.TaskDetail
	for _, task := range correlated {
		if task.AssignedRoleID == retryWorkerRoleID {
			detail, err := h.tasks.GetTask(h.ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Task.ID < out[j].Task.ID })
	return out
}

// invocationForAttempt returns the single durable Invocation Model Runtime
// recorded for one task attempt, read back through the SAME real
// modelruntime.InvocationService this test's Harness dispatches through --
// full provenance, not Executive's narrower InvocationRecord projection.
func invocationForAttempt(t *testing.T, h *integrationHarness, runtime *retryFailoverRuntime, taskID, attemptID int64) modelruntime.Invocation {
	t.Helper()
	records, err := runtime.models.FindTaskAttemptInvocations(h.ctx, taskID, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("task=%d attempt=%d: want exactly 1 invocation, got %d: %+v", taskID, attemptID, len(records), records)
	}
	full, err := runtime.invocations.Get(h.ctx, records[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return full
}

// ============================================================
// Main E2E: candidate A fails retryable -> cooldown -> Attempt 2 -> B ->
// success. Covers Sections 3-9 and 16.A/17 of the spec.
//
// A retryable candidate-A rejection must leave the root executable while the
// Task Engine owns retry_wait/backoff/attempt limits. The driver treats the
// causal ErrTaskRetryScheduled as progress rather than terminal failure, then
// reconciles and resumes until candidate B completes the campaign.
// ============================================================

func TestExecutiveRetryFailoverPostgreSQL17CandidateAToCandidateB(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	runtime.adapter.setFailCandidateA(true)

	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, executive.DefaultLimits())
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-a-to-b")

	run, err := driveRetryFailoverRun(t, h, runtime, orchestrator, rootID, 30)
	if err != nil {
		t.Fatalf("run did not converge: %v run=%+v", err, run)
	}
	if run.State != executive.StateCompleted || run.AnswerToOwner == "" {
		t.Fatalf("run=%+v", run)
	}

	worker := workerTask(t, h, rootID)
	if len(worker.Attempts) != 2 {
		t.Fatalf("worker task must have exactly 2 attempts (A failed, B succeeded), got %d: %+v", len(worker.Attempts), worker.Attempts)
	}
	attempt1, attempt2 := worker.Attempts[0], worker.Attempts[1]
	if attempt1.ID == attempt2.ID {
		t.Fatalf("Attempt2.ID must differ from Attempt1.ID, both are %d", attempt1.ID)
	}
	if attempt1.State != tasks.AttemptFailed || attempt1.Retryable == nil || !*attempt1.Retryable {
		t.Fatalf("attempt 1 must be a retryable failure: %+v", attempt1)
	}
	if attempt2.State != tasks.AttemptFinished {
		t.Fatalf("attempt 2 must have finished successfully: %+v", attempt2)
	}

	invocationA := invocationForAttempt(t, h, runtime, worker.Task.ID, attempt1.ID)
	invocationB := invocationForAttempt(t, h, runtime, worker.Task.ID, attempt2.ID)

	// Section 3: Invocation A selected candidate A, via the pool.
	if invocationA.ProviderModelID != retryCandidateA || invocationA.RoutingMode != modelruntime.RoutingModePool {
		t.Fatalf("Invocation A = %+v, want ProviderModelID=%s RoutingMode=%s", invocationA, retryCandidateA, modelruntime.RoutingModePool)
	}

	// Section 4: candidate A really went into cooldown from the real
	// ProviderOutcome the 503 produced.
	stateA, err := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateA)
	if err != nil {
		t.Fatal(err)
	}
	if stateA.CooldownUntil == nil || !time.Now().Before(*stateA.CooldownUntil) {
		t.Fatalf("candidate A must be in an active cooldown after the real 503, got %+v", stateA)
	}
	if stateA.QuotaExhausted {
		t.Fatalf("a retryable 503 must never set QuotaExhausted: %+v", stateA)
	}

	// Section 7/9: Attempt 2's HarnessRunID differs from Attempt 1's, and
	// Attempt 2 froze a DIFFERENT Invocation -- never Invocation A mutated.
	if invocationB.ID == invocationA.ID {
		t.Fatal("Attempt 2 must never reuse Invocation A")
	}
	if invocationB.ProviderModelID != retryCandidateB || invocationB.RoutingMode != modelruntime.RoutingModePool {
		t.Fatalf("Invocation B = %+v, want ProviderModelID=%s RoutingMode=%s", invocationB, retryCandidateB, modelruntime.RoutingModePool)
	}

	// Section 8: re-read Invocation A from PostgreSQL and require its
	// frozen identity is byte-identical to what it was right after Attempt
	// 1 -- nothing about Attempt 2/Invocation B may have touched it.
	reReadA, err := runtime.invocations.Get(h.ctx, invocationA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reReadA.ProviderID != invocationA.ProviderID || reReadA.ProviderModelID != invocationA.ProviderModelID ||
		reReadA.ModelProfileID != invocationA.ModelProfileID || reReadA.ModelProfileVersionID != invocationA.ModelProfileVersionID ||
		reReadA.RequestHash != invocationA.RequestHash || reReadA.RoutingMode != invocationA.RoutingMode ||
		reReadA.RoutingPolicyID != invocationA.RoutingPolicyID || reReadA.RoutingSelectorID != invocationA.RoutingSelectorID ||
		reReadA.RoutingCandidateSetHash != invocationA.RoutingCandidateSetHash || reReadA.RoutingCandidateHash != invocationA.RoutingCandidateHash {
		t.Fatalf("Invocation A's frozen identity changed: before=%+v after=%+v", invocationA, reReadA)
	}

	// Section 17: distinct ProviderIdempotencyKey per attempt -- the
	// provider registry (real or test.fake) must never see Attempt 2's
	// call as a replay of Attempt 1's.
	keysA := runtime.adapter.idempotencyKeysFor(retryCandidateA)
	keysB := runtime.adapter.idempotencyKeysFor(retryCandidateB)
	if len(keysA) != 1 || len(keysB) != 1 {
		t.Fatalf("expected exactly one dispatch call per candidate, got A=%v B=%v", keysA, keysB)
	}
	if keysA[0] == "" || keysB[0] == "" || keysA[0] == keysB[0] {
		t.Fatalf("provider idempotency keys must be non-empty and distinct: A=%q B=%q", keysA[0], keysB[0])
	}

	// Section 10 (positive half): each attempt consumed exactly one
	// provider call -- failover is not free.
	if got := runtime.adapter.callCount(retryCandidateA); got != 1 {
		t.Fatalf("candidate A call count = %d, want 1", got)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != 1 {
		t.Fatalf("candidate B call count = %d, want 1", got)
	}

	// C1: Assignment 1 and 2 both authorize the same pool policy but are
	// two distinct rows -- model_dispatcher_assignments carries no
	// candidate/provider/model column at all (RouteResolver alone selects
	// one, per-invocation, inside internal/modelruntime).
	var assignmentID1, assignmentID2 int64
	if err = h.store.Pool().QueryRow(h.ctx, `SELECT id FROM model_dispatcher_assignments WHERE organization_id=$1 AND task_id=$2 AND attempt_id=$3`, retryOrganizationID, worker.Task.ID, attempt1.ID).Scan(&assignmentID1); err != nil {
		t.Fatal(err)
	}
	if err = h.store.Pool().QueryRow(h.ctx, `SELECT id FROM model_dispatcher_assignments WHERE organization_id=$1 AND task_id=$2 AND attempt_id=$3`, retryOrganizationID, worker.Task.ID, attempt2.ID).Scan(&assignmentID2); err != nil {
		t.Fatal(err)
	}
	if assignmentID1 == assignmentID2 {
		t.Fatal("Assignment 2 must differ from Assignment 1")
	}

	// C1: a new HarnessRun per attempt -- harnessRunID(org, taskID,
	// attemptID, purpose) is a pure function of the attempt's own
	// identity, so a new attempt always produces a distinct run.
	var harnessRunID1, harnessRunID2 string
	if err = h.store.Pool().QueryRow(h.ctx, `SELECT harness_run_id FROM execution_run_descriptors WHERE organization_id=$1 AND task_id=$2 AND attempt_id=$3`, retryOrganizationID, worker.Task.ID, attempt1.ID).Scan(&harnessRunID1); err != nil {
		t.Fatal(err)
	}
	if err = h.store.Pool().QueryRow(h.ctx, `SELECT harness_run_id FROM execution_run_descriptors WHERE organization_id=$1 AND task_id=$2 AND attempt_id=$3`, retryOrganizationID, worker.Task.ID, attempt2.ID).Scan(&harnessRunID2); err != nil {
		t.Fatal(err)
	}
	if harnessRunID1 == "" || harnessRunID2 == "" || harnessRunID1 == harnessRunID2 {
		t.Fatalf("HarnessRunID must be distinct per attempt: attempt1=%q attempt2=%q", harnessRunID1, harnessRunID2)
	}

	// Section 16.A: reentering the SAME attempt (a second Resume once the
	// run has already converged) must never create a second Invocation for
	// either attempt.
	if _, err = orchestrator.Resume(h.ctx, rootID); err != nil {
		t.Fatalf("resuming a completed run must be idempotent: %v", err)
	}
	if got := runtime.adapter.callCount(retryCandidateA); got != 1 {
		t.Fatalf("re-resuming the completed run must not re-dispatch candidate A: calls=%d", got)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != 1 {
		t.Fatalf("re-resuming the completed run must not re-dispatch candidate B: calls=%d", got)
	}
}

// ============================================================
// Section D / C3: capacity-exhaustion + worker-terminal-isolation
// characterization. Both are explicitly READ-ONLY per the spec ("NO
// cambies todavía esa semántica" / "No lo arregles automáticamente") --
// this test asserts nothing about what SHOULD happen, only logs exactly
// what DOES, for the final report's CAPACITY_EXHAUSTION_* and
// WORKER_TERMINAL_FAILURE_ISOLATION_GAP fields.
// ============================================================

func TestExecutiveRetryFailoverPostgreSQL17BothCandidatesExhausted(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	runtime.adapter.setFailCandidateA(true)
	runtime.adapter.setFailCandidateB(true)

	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, executive.DefaultLimits())
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-both-exhausted")

	run, err := driveRetryFailoverRun(t, h, runtime, orchestrator, rootID, 30)
	t.Logf("CAPACITY_EXHAUSTION_CURRENT_BEHAVIOR: run=%+v err=%v", run, err)

	worker := workerTask(t, h, rootID)
	var workerReasonCode, workerReason string
	if worker.Task.StatusReasonCode != nil {
		workerReasonCode = *worker.Task.StatusReasonCode
	}
	if worker.Task.StatusReason != nil {
		workerReason = *worker.Task.StatusReason
	}
	t.Logf("worker task status=%q reason_code=%q reason=%q attempt_count=%d max_attempts=%d",
		worker.Task.Status, workerReasonCode, workerReason, worker.Task.AttemptCount, worker.Task.MaxAttempts)
	for i, at := range worker.Attempts {
		var retryableStr string
		if at.Retryable != nil {
			retryableStr = strconv.FormatBool(*at.Retryable)
		} else {
			retryableStr = "<nil>"
		}
		var failureCode, resultSummary string
		if at.FailureCode != nil {
			failureCode = *at.FailureCode
		}
		if at.ResultSummary != nil {
			resultSummary = *at.ResultSummary
		}
		t.Logf("attempt[%d]: id=%d state=%q failure_code=%q retryable=%s summary=%q", i, at.ID, at.State, failureCode, retryableStr, resultSummary)
		if at.State == tasks.AttemptFinished {
			invocations, invErr := runtime.models.FindTaskAttemptInvocations(h.ctx, worker.Task.ID, at.ID)
			t.Logf("attempt[%d] invocations=%+v err=%v", i, invocations, invErr)
		}
	}

	var reviewCount int
	if scanErr := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM tasks WHERE organization_id=$1 AND correlation_id=$2 AND idempotency_key LIKE '%leader-review:%'`, retryOrganizationID, run.CorrelationID).Scan(&reviewCount); scanErr != nil {
		t.Fatal(scanErr)
	}
	t.Logf("WORKER_TERMINAL_FAILURE_ISOLATION_GAP check: department review tasks created for this correlation=%d (0 means the review phase was never reached)", reviewCount)

	var rootStatus, rootReasonCode, rootReason string
	if scanErr := h.store.Pool().QueryRow(h.ctx, `SELECT status, COALESCE(status_reason_code,''), COALESCE(status_reason,'') FROM tasks WHERE id=$1`, rootID).Scan(&rootStatus, &rootReasonCode, &rootReason); scanErr != nil {
		t.Fatal(scanErr)
	}
	t.Logf("root task status=%q reason_code=%q reason=%q", rootStatus, rootReasonCode, rootReason)

	stateA, errA := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateA)
	stateB, errB := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateB)
	t.Logf("capacity A=%+v err=%v", stateA, errA)
	t.Logf("capacity B=%+v err=%v", stateB, errB)
}

// ============================================================
// Section C2: a tight campaign-wide model-call budget must gate the
// SECOND provider call the same way it gates the first -- a retryable
// candidate-A failure must not bypass o.budget.AuthorizeModelCall on
// Attempt 2. CEO-plan + department-plan consume the first two calls, so
// MaxModelCalls=3 leaves exactly one more: Attempt 1 (candidate A).
// ============================================================

func TestExecutiveRetryFailoverPostgreSQL17BudgetPreventsSecondCall(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	runtime.adapter.setFailCandidateA(true)

	limits := executive.DefaultLimits()
	limits.MaxModelCalls = 3
	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, limits)
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-budget-gate")

	run, err := driveRetryFailoverRun(t, h, runtime, orchestrator, rootID, 30)
	t.Logf("budget-gated run=%+v err=%v", run, err)

	if got := runtime.adapter.callCount(retryCandidateA); got != 1 {
		t.Fatalf("candidate A call count = %d, want exactly 1", got)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != 0 {
		t.Fatalf("candidate B must never be called once the campaign-wide model-call budget is exhausted: calls=%d", got)
	}
}

// ============================================================
// CASE 6: mixed workers. One department, two workers: A completes
// normally, B fails non-retryably on its own first attempt. Neither
// worker's outcome may contaminate the other's, and the department must
// still reach review once both are terminal.
// ============================================================

var mixedWorkersDepartmentPlan = []byte(`{"schema_version":"department-plan/v2","department_id":"ingenieria_ia","tasks":[` +
	`{"client_key":"inspect-a","assigned_role_id":"ingenieria_ia/qa","task_class":"general.work","title":"Inspect state A","instructions":"Inspect the bounded task context and report findings for area A.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":5},` +
	`{"client_key":"inspect-b","assigned_role_id":"ingenieria_ia/qa","task_class":"general.work","title":"Inspect state B","instructions":"Inspect the bounded task context and report findings for area B.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":5}` +
	`],"review_criteria":["findings verified"],"unresolved":[],"revision_ownership":[]}`)

func TestExecutiveRetryFailoverPostgreSQL17MixedWorkersReachReview(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	runtime.adapter.setDepartmentPlanOverride(mixedWorkersDepartmentPlan)

	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, executive.DefaultLimits())
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-mixed-workers")

	// Drive one step at a time until both worker tasks exist and exactly
	// one of them is still un-attempted, then target THAT one's real,
	// database-assigned task ID for a non-retryable failure -- learning
	// it this way (rather than guessing an order) is what proves the two
	// workers' outcomes are independent regardless of which one runs
	// first.
	var targeted bool
	for i := 0; i < 30 && !targeted; i++ {
		if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
			t.Fatalf("reconcile: %v", reconcileErr)
		}
		if _, err := orchestrator.Resume(h.ctx, rootID); err != nil && !errors.Is(err, executive.ErrTaskRetryScheduled) {
			t.Fatalf("resume step %d: %v", i, err)
		}
		for _, w := range workerTasksFor(t, h, rootID) {
			if w.Task.Status != "completed" && w.Task.Status != "failed" {
				runtime.adapter.setFailTaskID(w.Task.ID)
				targeted = true
				break
			}
		}
		if !targeted {
			time.Sleep(1100 * time.Millisecond)
		}
	}
	if !targeted {
		t.Fatal("never observed a second, un-attempted worker task to target")
	}

	run, err := driveRetryFailoverRun(t, h, runtime, orchestrator, rootID, 30)
	t.Logf("mixed-workers run=%+v err=%v", run, err)
	if err != nil {
		t.Fatalf("run did not converge: %v run=%+v", err, run)
	}

	workers := workerTasksFor(t, h, rootID)
	if len(workers) != 2 {
		t.Fatalf("expected exactly 2 department workers, got %d: %+v", len(workers), workers)
	}
	var completed, failed *tasks.TaskDetail
	for i := range workers {
		switch workers[i].Task.Status {
		case "completed":
			completed = &workers[i]
		case "failed":
			failed = &workers[i]
		}
	}
	if completed == nil {
		t.Fatalf("expected one worker completed, got statuses: %q, %q", workers[0].Task.Status, workers[1].Task.Status)
	}
	if failed == nil {
		t.Fatalf("expected one worker failed, got statuses: %q, %q", workers[0].Task.Status, workers[1].Task.Status)
	}

	// The failed worker's own failure must stay exactly what it was --
	// never converted into a synthetic success -- while the completed
	// worker's own outcome is untouched by its sibling's failure.
	if len(failed.Attempts) != 1 || failed.Attempts[0].State != tasks.AttemptFailed || failed.Attempts[0].Retryable == nil || *failed.Attempts[0].Retryable {
		t.Fatalf("failed worker's attempt must be a single non-retryable failure: %+v", failed.Attempts)
	}
	if len(completed.Attempts) != 1 || completed.Attempts[0].State != tasks.AttemptFinished {
		t.Fatalf("completed worker's attempt must have finished normally: %+v", completed.Attempts)
	}

	var reviewCount int
	if scanErr := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM tasks WHERE organization_id=$1 AND correlation_id=$2 AND idempotency_key LIKE '%leader-review:%'`, retryOrganizationID, run.CorrelationID).Scan(&reviewCount); scanErr != nil {
		t.Fatal(scanErr)
	}
	if reviewCount == 0 {
		t.Fatal("mixed department (one completed, one failed worker) never reached department review")
	}
}

// ============================================================
// CAPACITY_EXHAUSTION_SCHEDULING_V1 (FASE G/I): both pool candidates
// confirm a real, retryable 503 (consuming the two real attempts the
// existing MODEL_RETRY_FAILOVER_V1 A->B failover mechanism already uses),
// then both cool down at the same time. The NEXT claim attempt must not
// consume the campaign's third and final MaxAttempts slot on a fictitious
// invocation -- it must durably wait for capacity instead, then wake and
// complete for real once a candidate recovers, with no DB recreation, no
// in-process timer, and no ad-hoc polling anywhere in Executive.
// ============================================================

// driveRetryFailoverRunThroughCapacityWait is driveRetryFailoverRun without
// the StateBlocked-means-stop assumption baked into that helper: every
// OTHER blocked reason this suite exercises (assignee_unavailable,
// dependency_unsatisfied/terminal) genuinely cannot resolve itself without
// an external actor, so stopping there was correct for those tests. A
// capacity wait is different -- Task Engine's own Reconcile cycle, called
// here exactly like production would call it, resolves it on its own once
// available_at passes. Nothing here is a bespoke timer or sleep loop
// standing in for Executive: this is the same Reconcile+Resume pair every
// other test in this file already drives with.
func driveRetryFailoverRunThroughCapacityWait(t *testing.T, h *integrationHarness, runtime *retryFailoverRuntime, orchestrator *executive.Orchestrator, rootID int64, max int, stopOnBlocked bool) (executive.Run, error) {
	t.Helper()
	var run executive.Run
	var err error
	for i := 0; i < max; i++ {
		if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
			t.Fatalf("reconcile: %v", reconcileErr)
		}
		run, err = orchestrator.Resume(h.ctx, rootID)
		if errors.Is(err, executive.ErrTaskRetryScheduled) {
			err = nil
		}
		if err != nil || run.State == executive.StateCompleted || run.State == executive.StateFailed {
			return run, err
		}
		if run.State == executive.StateBlocked && stopOnBlocked {
			return run, err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return run, fmt.Errorf("executive run did not converge after %d resumes (last state=%q)", max, run.State)
}

func TestExecutiveRetryFailoverPostgreSQL17CapacityExhaustionDurableWait(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	runtime.adapter.setFailCandidateA(true)
	runtime.adapter.setFailCandidateB(true)

	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, executive.DefaultLimits())
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-capacity-wait")

	// Drive until BOTH candidates have been tried for real (A then B, per
	// the existing MODEL_RETRY_FAILOVER_V1 failover order) and cool down,
	// which must durably block the worker on capacity rather than consume
	// its third and final attempt. Never stop early on StateBlocked here --
	// that is exactly the condition under test.
	run, err := driveRetryFailoverRunThroughCapacityWait(t, h, runtime, orchestrator, rootID, 40, false)
	if err != nil {
		if run.State != executive.StateBlocked {
			t.Fatalf("run did not reach a capacity wait: run=%+v err=%v", run, err)
		}
	}

	worker := workerTask(t, h, rootID)
	var reasonCode string
	if worker.Task.StatusReasonCode != nil {
		reasonCode = *worker.Task.StatusReasonCode
	}
	t.Logf("worker after both-exhausted: status=%q reason_code=%q attempt_count=%d max_attempts=%d",
		worker.Task.Status, reasonCode, worker.Task.AttemptCount, worker.Task.MaxAttempts)

	if worker.Task.Status != "blocked" || reasonCode != "capacity" {
		t.Fatalf("worker must be durably capacity-blocked (status=blocked, reason_code=capacity), got status=%q reason_code=%q", worker.Task.Status, reasonCode)
	}
	// NO_MAX_ATTEMPTS_CONSUMPTION_FROM_WAIT: A and B each got exactly one
	// real attempt (2 total); the wait itself must not have claimed a
	// third, even though MaxAttempts=3 would have allowed one more claim.
	if worker.Task.AttemptCount != 2 {
		t.Fatalf("capacity wait must not consume MaxAttempts: attempt_count=%d, want exactly 2 (one real try per candidate, zero for the wait)", worker.Task.AttemptCount)
	}
	if worker.Task.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts must be untouched by capacity handling, got %d", worker.Task.MaxAttempts)
	}
	for _, at := range worker.Attempts {
		if at.State != tasks.AttemptFailed {
			t.Fatalf("every real attempt here must be the genuine A/B provider failure, got state=%q", at.State)
		}
	}

	// NO_INVOCATION_WHILE_WAITING / NO_PROVIDER_CALL_WHILE_WAITING: exactly
	// one real dispatch per candidate so far -- the capacity wait itself
	// must not have produced a third dispatch to either.
	if got := runtime.adapter.callCount(retryCandidateA); got != 1 {
		t.Fatalf("candidate A provider call count = %d, want exactly 1 (the capacity wait must not call the provider)", got)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != 1 {
		t.Fatalf("candidate B provider call count = %d, want exactly 1 (the capacity wait must not call the provider)", got)
	}

	// DEPARTMENT_REVIEW_NOT_PREMATURE
	var reviewCount int
	if scanErr := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM tasks WHERE organization_id=$1 AND correlation_id=$2 AND idempotency_key LIKE '%leader-review:%'`, retryOrganizationID, run.CorrelationID).Scan(&reviewCount); scanErr != nil {
		t.Fatal(scanErr)
	}
	if reviewCount != 0 {
		t.Fatalf("department review must not be created while the only worker is capacity-waiting, got %d review task(s)", reviewCount)
	}

	// RETRY_AT_CAPACITY_DERIVED / CAPACITY_WAIT_DURABLE: available_at must
	// be a real, future, capacity-derived timestamp, persisted on the row
	// itself (not process-local state).
	var availableAt time.Time
	if scanErr := h.store.Pool().QueryRow(h.ctx, `SELECT available_at FROM tasks WHERE id=$1`, worker.Task.ID).Scan(&availableAt); scanErr != nil {
		t.Fatal(scanErr)
	}
	stateA, errA := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateA)
	stateB, errB := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateB)
	if errA != nil || errB != nil {
		t.Fatalf("capacity state read: errA=%v errB=%v", errA, errB)
	}
	if stateA.CooldownUntil == nil || stateB.CooldownUntil == nil {
		t.Fatalf("both candidates must carry a real cooldown_until: A=%+v B=%+v", stateA, stateB)
	}
	earliest := *stateA.CooldownUntil
	if stateB.CooldownUntil.Before(earliest) {
		earliest = *stateB.CooldownUntil
	}
	if availableAt.Sub(earliest).Abs() > 2*time.Second {
		t.Fatalf("persisted available_at=%s must track the earliest candidate cooldown=%s (CASE 1: earliest candidate wins retry_at)", availableAt, earliest)
	}

	// EARLY_RESUME_NOOP / NO_BUSY_POLLING (CASE 2): calling Reconcile+Resume
	// again immediately, before that cooldown has elapsed, must be a
	// complete no-op -- zero additional provider calls, unchanged attempt
	// count, worker still blocked on capacity.
	for i := 0; i < 3; i++ {
		if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
			t.Fatalf("early reconcile: %v", reconcileErr)
		}
		if _, resumeErr := orchestrator.Resume(h.ctx, rootID); resumeErr != nil && !errors.Is(resumeErr, executive.ErrTaskRetryScheduled) {
			t.Fatalf("early resume: %v", resumeErr)
		}
	}
	if got := runtime.adapter.callCount(retryCandidateA); got != 1 {
		t.Fatalf("early Resume/Reconcile before retry_at must not call candidate A again, got %d calls", got)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != 1 {
		t.Fatalf("early Resume/Reconcile before retry_at must not call candidate B again, got %d calls", got)
	}
	worker = workerTask(t, h, rootID)
	if worker.Task.AttemptCount != 2 {
		t.Fatalf("early Resume/Reconcile must not consume another attempt, got attempt_count=%d", worker.Task.AttemptCount)
	}
	if worker.Task.Status != "blocked" {
		t.Fatalf("early Resume/Reconcile must leave the worker still capacity-blocked, got status=%q", worker.Task.Status)
	}

	// Let candidate A recover, then wait past the earliest cooldown and
	// keep driving -- this must wake through the normal Reconcile cycle,
	// re-evaluate routing for real, and produce exactly one real successful
	// Invocation, with no DB recreation and no code path other than the
	// same Reconcile+Resume pair used throughout this test.
	runtime.adapter.setFailCandidateA(false)
	for time.Now().Before(earliest.Add(300 * time.Millisecond)) {
		time.Sleep(200 * time.Millisecond)
	}

	run, err = driveRetryFailoverRunThroughCapacityWait(t, h, runtime, orchestrator, rootID, 40, false)
	if err != nil {
		t.Fatalf("run did not converge after capacity recovery: run=%+v err=%v", run, err)
	}
	if run.State != executive.StateCompleted {
		t.Fatalf("run must complete after capacity recovers, got state=%q", run.State)
	}

	worker = workerTask(t, h, rootID)
	if worker.Task.Status != "completed" {
		t.Fatalf("worker must complete for real after waking from capacity wait, got status=%q", worker.Task.Status)
	}
	// REAL_INVOCATION_AFTER_WAKE / PROVIDER_CALLED_ONCE_AFTER_WAKE: exactly
	// one more real call to A (the recovered candidate) after wake, none to
	// B (still failing, and no longer the earliest/best-ranked candidate
	// once A recovered).
	if got := runtime.adapter.callCount(retryCandidateA); got != 2 {
		t.Fatalf("candidate A must be called exactly once more after waking from the capacity wait, want 2 total calls, got %d", got)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != 1 {
		t.Fatalf("candidate B must not be called again after wake, want 1 total call, got %d", got)
	}
	if worker.Task.AttemptCount != 3 {
		t.Fatalf("the wake must consume exactly one more real attempt (the third and final one), got attempt_count=%d", worker.Task.AttemptCount)
	}

	var reviewCountAfter int
	if scanErr := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM tasks WHERE organization_id=$1 AND correlation_id=$2 AND idempotency_key LIKE '%leader-review:%'`, retryOrganizationID, run.CorrelationID).Scan(&reviewCountAfter); scanErr != nil {
		t.Fatal(scanErr)
	}
	if reviewCountAfter == 0 {
		t.Fatal("department review must be created normally once the capacity-waiting worker completes")
	}
}

// newTestCapacityGate mirrors internal/executive/bootstrap's own
// (unexported) newCapacityGate exactly: a read-only peek that reuses
// modelrouting.LookupSelector(...).Select -- the same function
// RouteResolver itself calls -- to decide whether a pool-routed task's
// claim candidate has any eligible candidate right now. It never creates
// an Invocation, calls a provider, or mutates capacity state, and defers
// (Available:true) for every case it cannot resolve as a clean, timed,
// transient gap.
func newTestCapacityGate(registryRepository *registry.PostgresRepository, store *modelpostgres.Store, organizationID string) tasks.CapacityValidator {
	return func(ctx context.Context, task tasks.Task) (tasks.CapacityCheck, error) {
		role, err := registryRepository.GetRole(ctx, organizationID, task.AssignedRoleID)
		if err != nil || role.ModelPolicy == nil || *role.ModelPolicy == "" {
			return tasks.CapacityCheck{Available: true}, nil
		}
		revision, err := registryRepository.GetCurrentRevision(ctx, organizationID)
		if err != nil || revision == nil {
			return tasks.CapacityCheck{Available: true}, nil
		}
		policy, ok, err := store.GetRoutingPolicy(ctx, organizationID, revision.ID, *role.ModelPolicy)
		if err != nil || !ok || policy.RoutingMode != modelruntime.RoutingModePool {
			return tasks.CapacityCheck{Available: true}, nil
		}
		selector, ok := modelrouting.LookupSelector(policy.SelectorID)
		if !ok {
			return tasks.CapacityCheck{Available: true}, nil
		}
		stored, err := store.ListRoutingCandidates(ctx, organizationID, revision.ID, *role.ModelPolicy)
		if err != nil || len(stored) == 0 {
			return tasks.CapacityCheck{Available: true}, nil
		}
		candidates := make([]modelrouting.Candidate, 0, len(stored))
		state := make(map[string]modelrouting.CandidateState, len(stored))
		for _, c := range stored {
			mc := modelrouting.Candidate{
				ProviderID: c.ProviderID, ProviderModelID: c.ProviderModelID,
				Transport: string(c.Transport), CapacityClass: c.CapacityClass, Priority: c.Priority,
				ProfileID: c.ProfileID, ModelProfileVersionID: c.ModelProfileVersionID,
			}
			candidates = append(candidates, mc)
			cs, err := store.CapacityState(ctx, organizationID, c.ProviderID, c.ProviderModelID)
			if err != nil {
				return tasks.CapacityCheck{Available: true}, nil
			}
			state[c.ProviderID+"|"+c.ProviderModelID] = cs
		}
		now := time.Now()
		if _, err := selector.Select(candidates, state, modelrouting.Requirements{AllowPaid: policy.AllowPaid}, now); err == nil {
			return tasks.CapacityCheck{Available: true}, nil
		} else if !errors.Is(err, modelrouting.ErrNoCapacity) {
			return tasks.CapacityCheck{Available: true}, nil
		}
		var retryAt time.Time
		for _, cs := range state {
			if cs.CooldownUntil == nil {
				continue
			}
			if retryAt.IsZero() || cs.CooldownUntil.Before(retryAt) {
				retryAt = *cs.CooldownUntil
			}
		}
		if retryAt.IsZero() || !retryAt.After(now) {
			return tasks.CapacityCheck{Available: true}, nil
		}
		return tasks.CapacityCheck{Available: false, RetryAt: retryAt, Reason: "transient_no_capacity: every pool candidate is temporarily unavailable"}, nil
	}
}

// ============================================================
// FASE H: restart durability. A capacity wait must be readable and
// re-enterable by a BRAND NEW Orchestrator instance built on the same
// Postgres -- proving re-entry depends on nothing but the durable rows
// (tasks.status/status_reason_code/available_at,
// model_routing_capacity_state), never an in-memory timer, goroutine, map,
// or closure captured by the original Orchestrator.
// ============================================================

func TestExecutiveRetryFailoverPostgreSQL17CapacityWaitSurvivesRestart(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	runtime.adapter.setFailCandidateA(true)
	runtime.adapter.setFailCandidateB(true)

	limits := executive.DefaultLimits()
	orchestrator1 := newRetryFailoverOrchestrator(t, h, runtime, limits)
	rootID := submitRetryFailoverGoal(t, h, orchestrator1, "retry-failover-capacity-restart")

	if _, err := driveRetryFailoverRunThroughCapacityWait(t, h, runtime, orchestrator1, rootID, 40, false); err != nil {
		worker := workerTask(t, h, rootID)
		if worker.Task.Status != "blocked" {
			t.Fatalf("run did not reach a capacity wait before restart: %v (worker status=%q)", err, worker.Task.Status)
		}
	}
	worker := workerTask(t, h, rootID)
	var reasonCode string
	if worker.Task.StatusReasonCode != nil {
		reasonCode = *worker.Task.StatusReasonCode
	}
	if worker.Task.Status != "blocked" || reasonCode != "capacity" {
		t.Fatalf("must be capacity-blocked before simulating restart, got status=%q reason_code=%q", worker.Task.Status, reasonCode)
	}
	attemptsBeforeRestart := worker.Task.AttemptCount
	callsABeforeRestart := runtime.adapter.callCount(retryCandidateA)
	callsBBeforeRestart := runtime.adapter.callCount(retryCandidateB)

	stateA, errA := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateA)
	stateB, errB := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateB)
	if errA != nil || errB != nil || stateA.CooldownUntil == nil || stateB.CooldownUntil == nil {
		t.Fatalf("expected real cooldowns on both candidates: A=%+v/%v B=%+v/%v", stateA, errA, stateB, errB)
	}
	earliest := *stateA.CooldownUntil
	if stateB.CooldownUntil.Before(earliest) {
		earliest = *stateB.CooldownUntil
	}

	// Simulate a process restart: discard orchestrator1 entirely (it is
	// never referenced again) and build a completely new *executive.
	// Orchestrator from scratch, reusing only the same underlying Postgres
	// (runtime/h) -- never any Go value orchestrator1 held.
	orchestrator1 = nil
	_ = orchestrator1
	orchestrator2 := newRetryFailoverOrchestrator(t, h, runtime, limits)

	// Before retry_at: the new instance's own first Resume/Reconcile must
	// still treat this as a no-op capacity wait -- no provider call, no
	// new attempt, capacity_wait preserved -- purely from the durable rows
	// it reads fresh, with zero in-memory state carried over from
	// orchestrator1 (there is none to carry; it is a new instance).
	if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
		t.Fatalf("post-restart reconcile: %v", reconcileErr)
	}
	if _, resumeErr := orchestrator2.Resume(h.ctx, rootID); resumeErr != nil && !errors.Is(resumeErr, executive.ErrTaskRetryScheduled) {
		t.Fatalf("post-restart resume before retry_at: %v", resumeErr)
	}
	worker = workerTask(t, h, rootID)
	if worker.Task.Status != "blocked" || worker.Task.AttemptCount != attemptsBeforeRestart {
		t.Fatalf("new orchestrator instance must not act before retry_at: status=%q attempt_count=%d (want blocked/%d)", worker.Task.Status, worker.Task.AttemptCount, attemptsBeforeRestart)
	}
	if got := runtime.adapter.callCount(retryCandidateA); got != callsABeforeRestart {
		t.Fatalf("new orchestrator instance must not call candidate A before retry_at: got %d calls, want %d", got, callsABeforeRestart)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != callsBBeforeRestart {
		t.Fatalf("new orchestrator instance must not call candidate B before retry_at: got %d calls, want %d", got, callsBBeforeRestart)
	}

	// After retry_at: the new instance must reactivate the task correctly
	// through the same Reconcile+Resume cycle, with no restart-specific
	// code path anywhere.
	runtime.adapter.setFailCandidateA(false)
	for time.Now().Before(earliest.Add(300 * time.Millisecond)) {
		time.Sleep(200 * time.Millisecond)
	}
	run, err := driveRetryFailoverRunThroughCapacityWait(t, h, runtime, orchestrator2, rootID, 40, false)
	if err != nil {
		t.Fatalf("new orchestrator instance did not converge after capacity recovery: run=%+v err=%v", run, err)
	}
	if run.State != executive.StateCompleted {
		t.Fatalf("new orchestrator instance must complete the run after capacity recovers, got state=%q", run.State)
	}
	worker = workerTask(t, h, rootID)
	if worker.Task.Status != "completed" {
		t.Fatalf("worker must complete for real under the new orchestrator instance, got status=%q", worker.Task.Status)
	}
	if worker.Task.AttemptCount != attemptsBeforeRestart+1 {
		t.Fatalf("wake must consume exactly one more real attempt, got attempt_count=%d (was %d before restart)", worker.Task.AttemptCount, attemptsBeforeRestart)
	}
}

// ============================================================
// CASE 7A / CASE 7B: WithNoRetries (MaxAttempts=1) closure.
//
// CASE 7A proves capacity wait does not spend the single allowed attempt
// on the wait itself. CASE 7B proves, separately, that the new capacity
// gate did NOT quietly turn MaxAttempts=1 into a second chance for a real
// provider failure -- capacity wait not consuming attempts must not mean
// provider failures stop consuming them either.
// ============================================================

func TestExecutiveRetryFailoverPostgreSQL17CapacityWaitWithNoRetries(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)

	// Seed both pool candidates into a real, durable cooldown directly on
	// model_routing_capacity_state -- the exact table/columns
	// applyCapacityFeedback (internal/modelruntime/postgres/capacity_state.go)
	// itself writes, read back through the SAME CapacityStateReader this
	// round's gate and RouteResolver both already use unmodified. This is
	// fixture setup (test precondition), not a second implementation of
	// eligibility: the read side (CapacityState/FreeCapacityV1.Select)
	// that actually decides whether a candidate is eligible is completely
	// untouched and is exactly what this test exercises.
	//
	// A prior version of this test drove a throwaway campaign through two
	// real 503s first (mirroring every other test's seeding style) and
	// was flaky by construction: MaxAttempts=1's own campaign still needs
	// a real CEO-plan and leader-plan Invocation before its worker task's
	// first claim, and driving the seed campaign to its own capacity wait
	// already spends most of the real 30s cooldown the 503 path produces
	// -- leaving too little of it for stage two's worker to still find
	// both candidates unavailable at its first claim. Seeding directly
	// removes that race instead of fighting it with a longer cooldown.
	seedCooldown := time.Now().Add(30 * time.Second)
	for _, candidate := range []string{retryCandidateA, retryCandidateB} {
		if _, err := h.store.Pool().Exec(h.ctx, `
			INSERT INTO model_routing_capacity_state(organization_id, provider_id, provider_model_id, cooldown_until)
			VALUES ($1, 'test.fake', $2, $3)
			ON CONFLICT (organization_id, provider_id, provider_model_id) DO UPDATE SET cooldown_until = EXCLUDED.cooldown_until
		`, retryOrganizationID, candidate, seedCooldown); err != nil {
			t.Fatalf("seed capacity cooldown for %s: %v", candidate, err)
		}
	}
	stateA, errA := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateA)
	stateB, errB := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateB)
	if errA != nil || errB != nil || stateA.CooldownUntil == nil || stateB.CooldownUntil == nil {
		t.Fatalf("seeded cooldowns must read back through the real CapacityStateReader: A=%+v/%v B=%+v/%v", stateA, errA, stateB, errB)
	}
	earliest := *stateA.CooldownUntil
	if stateB.CooldownUntil.Before(earliest) {
		earliest = *stateB.CooldownUntil
	}
	callsABaseline := runtime.adapter.callCount(retryCandidateA)
	callsBBaseline := runtime.adapter.callCount(retryCandidateB)

	// The actual CASE 7A campaign, MaxAttempts=1 for every task
	// (executive.WithNoRetries), submitted while both candidates are
	// still cooling down from stage 1 -- so the worker task's very FIRST
	// claim attempt must hit the pre-claim capacity gate before claimOne
	// ever runs, exactly like every other capacity-wait scenario in this
	// file, just now with zero attempts of slack to spend on it.
	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, executive.DefaultLimits(), executive.WithNoRetries())
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-case7a-no-retries")

	if _, err := driveRetryFailoverRunThroughCapacityWait(t, h, runtime, orchestrator, rootID, 40, false); err != nil {
		worker := workerTask(t, h, rootID)
		if worker.Task.Status != "blocked" {
			t.Fatalf("CASE 7A campaign did not reach a capacity wait: %v (worker status=%q)", err, worker.Task.Status)
		}
	}

	worker := workerTask(t, h, rootID)
	var reasonCode string
	if worker.Task.StatusReasonCode != nil {
		reasonCode = *worker.Task.StatusReasonCode
	}
	if worker.Task.Status != "blocked" || reasonCode != "capacity" {
		t.Fatalf("CASE 7A worker must be durably capacity-blocked, got status=%q reason_code=%q", worker.Task.Status, reasonCode)
	}
	if worker.Task.MaxAttempts != 1 {
		t.Fatalf("CASE 7A worker must have MaxAttempts=1 (WithNoRetries), got %d", worker.Task.MaxAttempts)
	}
	// The central CASE 7A assertion: waiting for capacity with
	// MaxAttempts=1 must leave attempt_count at 0, not 1.
	if worker.Task.AttemptCount != 0 {
		t.Fatalf("CASE_7A_CAPACITY_WAIT_WITH_NO_RETRIES: capacity wait must not consume the single allowed attempt, got attempt_count=%d, want 0", worker.Task.AttemptCount)
	}

	var attemptRows, activeLeases, invocationRowsWhileWaiting int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM task_attempts WHERE task_id=$1`, worker.Task.ID).Scan(&attemptRows); err != nil {
		t.Fatal(err)
	}
	if attemptRows != 0 {
		t.Fatalf("no TaskAttempt row may exist for the wait itself, found %d", attemptRows)
	}
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM task_leases WHERE task_id=$1 AND status='active'`, worker.Task.ID).Scan(&activeLeases); err != nil {
		t.Fatal(err)
	}
	if activeLeases != 0 {
		t.Fatalf("no active Lease may exist for the wait itself, found %d", activeLeases)
	}
	// FASE C, direct evidence: model_invocations is exactly the durable
	// table ModelCallBudget.AuthorizeModelCall gates real dispatches
	// against (runtimeadapter.ModelCallBudget reads it through
	// Models.FindTaskAttemptInvocations) -- there is no attempt yet to
	// query it by attempt_id, so this reads it directly by task_id as a
	// plain OBSERVATION, not a second implementation of the gate's own
	// logic.
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM model_invocations WHERE task_id=$1`, worker.Task.ID).Scan(&invocationRowsWhileWaiting); err != nil {
		t.Fatal(err)
	}
	if invocationRowsWhileWaiting != 0 {
		t.Fatalf("NO_MODEL_BUDGET_CHARGE_WHILE_WAITING_DIRECT: no Invocation row may exist for this task while capacity-waiting, found %d", invocationRowsWhileWaiting)
	}
	if got := runtime.adapter.callCount(retryCandidateA); got != callsABaseline {
		t.Fatalf("CASE 7A worker must not call candidate A while waiting, calls=%d want baseline=%d", got, callsABaseline)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != callsBBaseline {
		t.Fatalf("CASE 7A worker must not call candidate B while waiting, calls=%d want baseline=%d", got, callsBBaseline)
	}

	// Multiple early Resume/Reconcile calls before retry_at must be a
	// complete no-op, exactly like CASE 2 for the retries-enabled path --
	// with the added stakes that MaxAttempts=1 means there is zero room
	// for a mistaken extra attempt to hide in.
	for i := 0; i < 3; i++ {
		if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
			t.Fatalf("early reconcile: %v", reconcileErr)
		}
		if _, resumeErr := orchestrator.Resume(h.ctx, rootID); resumeErr != nil && !errors.Is(resumeErr, executive.ErrTaskRetryScheduled) {
			t.Fatalf("early resume: %v", resumeErr)
		}
	}
	worker = workerTask(t, h, rootID)
	if worker.Task.AttemptCount != 0 || worker.Task.Status != "blocked" {
		t.Fatalf("early Resume/Reconcile before retry_at must be a no-op, got attempt_count=%d status=%q", worker.Task.AttemptCount, worker.Task.Status)
	}
	if got := runtime.adapter.callCount(retryCandidateA); got != callsABaseline {
		t.Fatalf("early Resume/Reconcile must not call candidate A, calls=%d want baseline=%d", got, callsABaseline)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != callsBBaseline {
		t.Fatalf("early Resume/Reconcile must not call candidate B, calls=%d want baseline=%d", got, callsBBaseline)
	}

	// Make candidate A recoverable, wait past the earliest cooldown, and
	// drive to completion.
	runtime.adapter.setFailCandidateA(false)
	for time.Now().Before(earliest.Add(300 * time.Millisecond)) {
		time.Sleep(200 * time.Millisecond)
	}
	run, err := driveRetryFailoverRunThroughCapacityWait(t, h, runtime, orchestrator, rootID, 40, false)
	if err != nil {
		t.Fatalf("CASE 7A run did not converge after capacity recovery: run=%+v err=%v", run, err)
	}
	if run.State != executive.StateCompleted {
		t.Fatalf("CASE 7A run must complete after capacity recovers, got state=%q", run.State)
	}

	worker = workerTask(t, h, rootID)
	if worker.Task.Status != "completed" {
		t.Fatalf("CASE 7A worker must complete for real after waking, got status=%q", worker.Task.Status)
	}
	if worker.Task.AttemptCount != 1 {
		t.Fatalf("CASE 7A worker must consume EXACTLY its one real attempt on wake, got attempt_count=%d", worker.Task.AttemptCount)
	}
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM task_attempts WHERE task_id=$1`, worker.Task.ID).Scan(&attemptRows); err != nil {
		t.Fatal(err)
	}
	if attemptRows != 1 {
		t.Fatalf("CASE 7A worker must have exactly 1 TaskAttempt row total, found %d", attemptRows)
	}
	var invocationRowsAfterWake int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM model_invocations WHERE task_id=$1`, worker.Task.ID).Scan(&invocationRowsAfterWake); err != nil {
		t.Fatal(err)
	}
	if invocationRowsAfterWake != 1 {
		t.Fatalf("NO_MODEL_BUDGET_CHARGE_WHILE_WAITING_DIRECT: exactly 1 real Invocation must exist after wake, found %d", invocationRowsAfterWake)
	}
	if got := runtime.adapter.callCount(retryCandidateA); got != callsABaseline+1 {
		t.Fatalf("CASE 7A worker must call candidate A exactly once on wake, calls=%d want %d", got, callsABaseline+1)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != callsBBaseline {
		t.Fatalf("CASE 7A worker must never call candidate B, calls=%d want baseline=%d", got, callsBBaseline)
	}
}

func TestExecutiveRetryFailoverPostgreSQL17ProviderFailureWithNoRetries(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	// Fresh pool state (no prior cooldowns from this test): candidate A is
	// eligible at the worker's first and only claim, and fails for real.
	runtime.adapter.setFailCandidateA(true)

	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, executive.DefaultLimits(), executive.WithNoRetries())
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-case7b-no-retries")

	run, err := driveRetryFailoverRun(t, h, runtime, orchestrator, rootID, 30)
	t.Logf("CASE 7B run=%+v err=%v", run, err)
	if err != nil {
		t.Fatalf("CASE 7B run did not converge: %v run=%+v", err, run)
	}

	worker := workerTask(t, h, rootID)
	var reasonCode string
	if worker.Task.StatusReasonCode != nil {
		reasonCode = *worker.Task.StatusReasonCode
	}
	// The pre-existing MaxAttempts=1 semantics must be exactly what they
	// were before this round: one real, confirmed retryable failure is
	// terminal, never reinterpreted as a capacity wait to sneak the worker
	// a second try.
	if reasonCode == "capacity" {
		t.Fatalf("CASE_7B_PROVIDER_FAILURE_WITH_NO_RETRIES: a real provider failure must never be reinterpreted as a capacity wait, got status=%q reason_code=%q", worker.Task.Status, reasonCode)
	}
	if worker.Task.Status != "failed" && worker.Task.Status != "dead_letter" {
		t.Fatalf("CASE_7B_PROVIDER_FAILURE_WITH_NO_RETRIES: worker must reach a terminal failure, got status=%q reason_code=%q", worker.Task.Status, reasonCode)
	}
	if worker.Task.AttemptCount != 1 {
		t.Fatalf("CASE_7B_PROVIDER_FAILURE_WITH_NO_RETRIES: exactly 1 attempt must be consumed by the real failure, got attempt_count=%d", worker.Task.AttemptCount)
	}
	if worker.Task.MaxAttempts != 1 {
		t.Fatalf("CASE 7B worker must have MaxAttempts=1, got %d", worker.Task.MaxAttempts)
	}
	if got := runtime.adapter.callCount(retryCandidateA); got != 1 {
		t.Fatalf("candidate A must be called exactly once, got %d", got)
	}
	if got := runtime.adapter.callCount(retryCandidateB); got != 0 {
		t.Fatalf("CASE_7B_PROVIDER_FAILURE_WITH_NO_RETRIES: candidate B must never be tried -- the one allowed attempt is exhausted, not converted into failover, got %d calls", got)
	}
}

// ============================================================
// CASE 10: multi-worker capacity wait. One department, two workers: one
// completes normally while the other's own real failures drive both pool
// candidates into cooldown and it durably capacity-waits -- without
// contaminating its sibling, without a premature Department Review, and
// without losing the campaign's ability to finish once the wait resolves.
// ============================================================

var multiWorkerCapacityWaitDepartmentPlan = []byte(`{"schema_version":"department-plan/v2","department_id":"ingenieria_ia","tasks":[` +
	`{"client_key":"inspect-a","assigned_role_id":"ingenieria_ia/qa","task_class":"general.work","title":"Inspect state A","instructions":"Inspect the bounded task context and report findings for area A.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":10},` +
	`{"client_key":"inspect-b","assigned_role_id":"ingenieria_ia/qa","task_class":"general.work","title":"Inspect state B","instructions":"Inspect the bounded task context and report findings for area B.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":1}` +
	`],"review_criteria":["findings verified"],"unresolved":[],"revision_ownership":[]}`)

func TestExecutiveRetryFailoverPostgreSQL17MultiWorkerCapacityWait(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	runtime.adapter.setDepartmentPlanOverride(multiWorkerCapacityWaitDepartmentPlan)

	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, executive.DefaultLimits())
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-case10-multi-worker")

	// Drive until both worker tasks exist, then target -- by its real,
	// database-assigned task ID, discovered by polling, never assumed by
	// position -- whichever one has not yet been attempted. The other is
	// never touched by the adapter and must be free to complete normally.
	var targetID int64
	for i := 0; i < 30 && targetID == 0; i++ {
		if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
			t.Fatalf("reconcile: %v", reconcileErr)
		}
		if _, err := orchestrator.Resume(h.ctx, rootID); err != nil && !errors.Is(err, executive.ErrTaskRetryScheduled) {
			t.Fatalf("resume step %d: %v", i, err)
		}
		workers := workerTasksFor(t, h, rootID)
		if len(workers) == 2 {
			for _, w := range workers {
				if w.Task.AttemptCount == 0 && w.Task.Status != "completed" {
					targetID = w.Task.ID
					break
				}
			}
		}
		if targetID == 0 {
			time.Sleep(1100 * time.Millisecond)
		}
	}
	if targetID == 0 {
		t.Fatal("never observed a not-yet-attempted worker task to target")
	}
	runtime.adapter.setFailTaskIDRetryable(targetID)
	t.Logf("CASE 10: targeting worker task %d for capacity wait", targetID)

	// Drive forward -- never stopping on StateBlocked, since the targeted
	// worker's own capacity wait must not stall the campaign -- until the
	// OTHER worker has completed and the targeted one is durably
	// capacity-blocked.
	var otherWorker, targetWorker tasks.TaskDetail
	var otherCompleted, targetBlocked bool
	for i := 0; i < 40 && !(otherCompleted && targetBlocked); i++ {
		if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
			t.Fatalf("reconcile: %v", reconcileErr)
		}
		if _, err := orchestrator.Resume(h.ctx, rootID); err != nil && !errors.Is(err, executive.ErrTaskRetryScheduled) {
			t.Fatalf("resume step %d: %v", i, err)
		}
		for _, w := range workerTasksFor(t, h, rootID) {
			if w.Task.ID == targetID {
				targetWorker = w
				var rc string
				if w.Task.StatusReasonCode != nil {
					rc = *w.Task.StatusReasonCode
				}
				targetBlocked = w.Task.Status == "blocked" && rc == "capacity"
			} else {
				otherWorker = w
				otherCompleted = w.Task.Status == "completed"
			}
		}
		if !(otherCompleted && targetBlocked) {
			time.Sleep(300 * time.Millisecond)
		}
	}
	if !otherCompleted {
		t.Fatalf("the untargeted worker never completed: status=%q", otherWorker.Task.Status)
	}
	if !targetBlocked {
		t.Fatalf("the targeted worker never reached a durable capacity wait: status=%q", targetWorker.Task.Status)
	}
	otherAttemptsMid := otherWorker.Task.AttemptCount
	otherInvocationsMid := 0
	for _, at := range otherWorker.Attempts {
		invs, err := runtime.models.FindTaskAttemptInvocations(h.ctx, otherWorker.Task.ID, at.ID)
		if err != nil {
			t.Fatal(err)
		}
		otherInvocationsMid += len(invs)
	}
	if targetWorker.Task.AttemptCount == 0 {
		t.Fatalf("CASE 10: targeted worker's own real failures must be reflected in attempt_count, got 0")
	}

	// CRITICAL: Department Review must not exist yet -- the organization
	// cannot review a department while one of its workers is still in a
	// transitory, self-recoverable capacity wait.
	var reviewCountMid int
	if scanErr := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM tasks WHERE organization_id=$1 AND correlation_id=$2 AND idempotency_key LIKE '%leader-review:%'`, retryOrganizationID, rootCorrelationID(t, h, rootID)).Scan(&reviewCountMid); scanErr != nil {
		t.Fatal(scanErr)
	}

	// Recover the target: clear the per-task retryable-failure override
	// (its next real attempt must succeed normally, not through the
	// override) and wait past the earliest cooldown.
	runtime.adapter.setFailTaskIDRetryable(0)
	stateA, errA := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateA)
	stateB, errB := runtime.store.CapacityState(h.ctx, retryOrganizationID, "test.fake", retryCandidateB)
	if errA != nil || errB != nil || stateA.CooldownUntil == nil || stateB.CooldownUntil == nil {
		t.Fatalf("expected real cooldowns on both candidates: A=%+v/%v B=%+v/%v", stateA, errA, stateB, errB)
	}
	earliest := *stateA.CooldownUntil
	if stateB.CooldownUntil.Before(earliest) {
		earliest = *stateB.CooldownUntil
	}
	for time.Now().Before(earliest.Add(300 * time.Millisecond)) {
		time.Sleep(200 * time.Millisecond)
	}

	run, err := driveRetryFailoverRunThroughCapacityWait(t, h, runtime, orchestrator, rootID, 40, false)
	if err != nil {
		t.Fatalf("CASE 10 run did not converge after capacity recovery: run=%+v err=%v", run, err)
	}
	if run.State != executive.StateCompleted {
		t.Fatalf("CASE 10 run must complete after capacity recovers, got state=%q", run.State)
	}

	workers := workerTasksFor(t, h, rootID)
	if len(workers) != 2 {
		t.Fatalf("expected exactly 2 worker tasks, got %d", len(workers))
	}
	for _, w := range workers {
		if w.Task.Status != "completed" {
			t.Fatalf("worker %d must be completed at the end, got status=%q", w.Task.ID, w.Task.Status)
		}
		if w.Task.ID == targetID {
			continue
		}
		// The untargeted worker must be exactly as it was mid-wait: never
		// re-executed, never given an extra Invocation, by its sibling's
		// capacity wait or its recovery.
		if w.Task.AttemptCount != otherAttemptsMid {
			t.Fatalf("untargeted worker must not be re-executed by its sibling's capacity wait, attempt_count changed from %d to %d", otherAttemptsMid, w.Task.AttemptCount)
		}
		invocationsAfter := 0
		for _, at := range w.Attempts {
			invs, err := runtime.models.FindTaskAttemptInvocations(h.ctx, w.Task.ID, at.ID)
			if err != nil {
				t.Fatal(err)
			}
			invocationsAfter += len(invs)
		}
		if invocationsAfter != otherInvocationsMid {
			t.Fatalf("untargeted worker must not receive an extra Invocation, count changed from %d to %d", otherInvocationsMid, invocationsAfter)
		}
	}

	var reviewCountFinal int
	if scanErr := h.store.Pool().QueryRow(h.ctx, `SELECT COUNT(*) FROM tasks WHERE organization_id=$1 AND correlation_id=$2 AND idempotency_key LIKE '%leader-review:%'`, retryOrganizationID, run.CorrelationID).Scan(&reviewCountFinal); scanErr != nil {
		t.Fatal(scanErr)
	}
	if reviewCountMid != 0 {
		t.Fatalf("CASE_10_MULTI_WORKER_CAPACITY_WAIT: department review must not exist while one worker still capacity-waits, found %d", reviewCountMid)
	}
	if reviewCountFinal == 0 {
		t.Fatal("CASE_10_MULTI_WORKER_CAPACITY_WAIT: department review must be created once both workers are terminal")
	}
}

// rootCorrelationID reads the campaign's own correlation ID directly off
// the root task -- needed before the run is complete (the mid-wait
// Department Review check), when driveRetryFailoverRunThroughCapacityWait's
// own Run value is not yet available.
func rootCorrelationID(t *testing.T, h *integrationHarness, rootID int64) string {
	t.Helper()
	detail, err := h.tasks.GetTask(h.ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.CorrelationID == nil {
		t.Fatal("root task has no correlation ID")
	}
	return *detail.Task.CorrelationID
}
