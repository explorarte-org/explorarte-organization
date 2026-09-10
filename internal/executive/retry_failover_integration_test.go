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
	calls          []retryFailoverCall
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
	a.calls = append(a.calls, retryFailoverCall{ProviderModelID: req.ProviderModelID, IdempotencyKey: req.ProviderIdempotencyKey, InvocationID: req.InvocationID})
	a.mu.Unlock()

	if req.ProviderModelID == retryCandidateA && failA {
		return modelruntime.RawResponse{}, &modelruntime.AdapterError{
			Phase: modelruntime.AdapterFailureResponseReceived,
			Outcome: modelruntime.ProviderOutcome{
				OutcomeClassification: modelruntime.ProviderOutcomeRejected,
				HTTPStatus:            503, ErrorClass: "transport", ErrorCode: "http_error",
				Retryable:             true,
				ResponseHash:          modelruntime.SHA256Bytes([]byte("retry-failover-503-" + strconv.FormatInt(req.InvocationID, 10))),
				ResponseSchemaVersion: "test.fake.response.v1",
			},
			Cause: errors.New("retry failover test: candidate A confirmed 503"),
		}
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
	case strings.Contains(s, "assigned_role_id"):
		return []byte(`{"schema_version":"department-plan/v2","department_id":"ingenieria_ia","tasks":[{"client_key":"inspect","assigned_role_id":"ingenieria_ia/qa","task_class":"general.work","title":"Inspect state","instructions":"Inspect the bounded task context and report findings.","acceptance_criteria":["return findings"],"dependencies":[],"requirements":[],"priority":5}],"review_criteria":["findings verified"],"unresolved":[],"revision_ownership":[]}`)
	case strings.Contains(s, "unsatisfied_criteria"):
		return []byte(`{"schema_version":"department-review/v2","verdict":"accept","findings":["criteria satisfied"],"unsatisfied_criteria":[],"evidence_refs":[],"proposed_followup_tasks":[],"revision_outcomes":[],"followup_ownership":[]}`)
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

	egressPlan := modelegress.RegistryPlan{
		OrganizationID: retryOrganizationID, OrganizationRevisionID: revisionID,
		CanonicalHash: egressPolicy.CanonicalHash, Policy: egressPolicy,
	}
	if _, err = egressStore.Apply(ctx, egressPlan); err != nil {
		t.Fatalf("apply fixture egress policy: %v", err)
	}
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
func driveRetryFailoverRun(t *testing.T, h *integrationHarness, orchestrator *executive.Orchestrator, rootID int64, max int) (executive.Run, error) {
	t.Helper()
	var run executive.Run
	var err error
	for i := 0; i < max; i++ {
		if _, reconcileErr := h.tasks.Reconcile(h.ctx, 50); reconcileErr != nil {
			t.Fatalf("reconcile: %v", reconcileErr)
		}
		run, err = orchestrator.Resume(h.ctx, rootID)
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
// KNOWN RESULT (documented finding, not a test bug): this test is expected
// to FAIL at driveRetryFailoverRun, and that failure IS the finding.
//
// The task-engine-level retry mechanism works correctly in isolation:
// RecordAttemptFailed(..., retryable=true) really does persist retryable=true
// and the task transitions to retry_wait with a correct backoff (verified
// directly against Postgres while debugging this test). But Executive's
// campaign orchestration never gets to observe that retry happening: a
// worker's model-invocation failure goes through failAttempt (orchestrator.go,
// the ProcessTypedTask path around the ErrCompletionFailed/"model_invocation_failed"
// sentinel), which correctly records retryable into the task engine but then
// unconditionally returns an ErrCompletionFailed-wrapped error regardless of
// the retryable value. That error reaches handlePhaseError, whose
// isNonBlockingPhaseError allowlist covers only infrastructure-level
// transience (lost lease, unavailable authority, ambiguous outcome, ...) --
// it has no knowledge of the retryable bool and ErrCompletionFailed is not on
// it -- so the very first retryable provider failure calls o.blockRoot(...)
// and marks the ROOT task itself status=blocked. handlePhaseError's own
// comment states ResumeDurable deliberately refuses to auto-reopen a blocked
// root, so nothing ever calls Resume() again in a way that would observe the
// task engine's own fresh attempt and let the campaign continue onto
// candidate B. The retry engine and the failover routing are each
// individually correct; they just never get exercised together through the
// Executive campaign layer. This is a Section 21 STOP condition (changing
// handlePhaseError/isNonBlockingPhaseError touches Executive orchestration
// semantics) -- reported, not patched, per Section 23.
// ============================================================

func TestExecutiveRetryFailoverPostgreSQL17CandidateAToCandidateB(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	runtime := newRetryFailoverRuntime(t, h)
	runtime.adapter.setFailCandidateA(true)

	orchestrator := newRetryFailoverOrchestrator(t, h, runtime, executive.DefaultLimits())
	rootID := submitRetryFailoverGoal(t, h, orchestrator, "retry-failover-a-to-b")

	run, err := driveRetryFailoverRun(t, h, orchestrator, rootID, 30)
	if err != nil {
		t.Fatalf("run did not converge -- EXPECTED per this test's doc comment above: "+
			"Executive blocks the root campaign on the worker's first retryable "+
			"model-invocation failure instead of letting the task engine's own "+
			"retry (already confirmed durable and correctly retryable=true) carry "+
			"the campaign to candidate B. %v run=%+v", err, run)
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
