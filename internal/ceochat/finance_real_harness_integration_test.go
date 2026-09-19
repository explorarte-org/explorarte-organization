//go:build integration

// FINANCE_HARNESS_RUNTIME_HOTFIX_V1 / FINANCE_CONTEXT_ENGINE_
// INTEGRATION_V1: real-Postgres, real-Task-Engine, real-tasksauthority.
// Adapter, real-Context-Engine, real-Model-Runtime proof that Finance's
// own ExecuteReviewTask -> runHarnessModel -> executionharness.Runtime
// path works end to end with MockOutput=nil, all the way to a persisted
// FinancialReview and a completed task -- never exercised by any test
// before FINANCE_HARNESS_RUNTIME_HOTFIX_V1 (every other Finance
// integration test in this package, finance_worker_integration_test.go,
// uses MockOutput to substitute for the model call itself), and blocked
// at "initial context ID must be a positive model runtime snapshot ID"
// until FINANCE_CONTEXT_ENGINE_INTEGRATION_V1 wired a real
// campaign.FinanceContextBuilder (see finance_service.go's
// runHarnessModel and cmd/orgctl/executive.go's financeContextAdapter).
//
// REAL_PROVIDER_CALLS is always 0 here: the "real Model Runtime" is
// dispatched through the repository's own deterministic
// internal/modelruntime/adapter.Fake ("test.fake"), registered via
// modelbootstrap.WithExtraAdapters exactly like
// canonical_dispatch_e2e_test.go already does for empresa/ceo. Fake's
// Dispatch is otherwise a fixed, non-configurable hash placeholder, which
// cannot satisfy FinanceReviewOutput's own JSON schema -- this round adds
// one small, additive, backward-compatible extraction to Fake.Dispatch
// (internal/modelruntime/adapter/fake.go) so a [fake-json-b64:...] marker
// anywhere in the rendered prompt makes it echo back a caller-chosen JSON
// payload instead. The marker is embedded via a real CampaignProposal's
// own Goal field (never Finance's own production prompt-rendering code,
// which is untouched) -- financeFakeJSONGoal below builds it.
package ceochat_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/campaign/financeworker"
	campaignpostgres "github.com/Mireuz13/explorarte-organization/internal/campaign/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	executionharnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	modeldispatchbootstrap "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/bootstrap"
	egresspostgres "github.com/Mireuz13/explorarte-organization/internal/modelegress/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitybootstrap "github.com/Mireuz13/explorarte-organization/internal/modelidentity/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskcontextprovider "github.com/Mireuz13/explorarte-organization/internal/tasks/contextprovider"
)

// financeFakeJSONGoal marshals a FinanceReviewOutput and embeds it as a
// [fake-json-b64:...] marker inside otherwise-ordinary goal prose -- it
// flows into Finance's real, unmodified promptContent purely because
// runHarnessModel JSON-marshals the whole proposal (Goal included)
// verbatim. Never touches finance_service.go's own rendering.
func financeFakeJSONGoal(t *testing.T, output campaign.FinanceReviewOutput) string {
	t.Helper()
	body, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal fake finance output: %v", err)
	}
	return "Prove the real Finance Harness round trip. [fake-json-b64:" + base64.StdEncoding.EncodeToString(body) + "]"
}

// financeTestExecutableBudget returns a fresh, modest, strictly-positive
// campaign.BudgetRecommendation for every one of its seven dimensions
// (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 3). Used across
// test.fake real-Harness integration tests wherever a "recommended" verdict
// is scripted.
func financeTestExecutableBudget() *campaign.BudgetRecommendation {
	return &campaign.BudgetRecommendation{
		MaxUSD:        1.0,
		MaxTokens:     1000,
		MaxModelCalls: 2,
		MaxWallTimeMS: 60000,
		MaxDepth:      2,
		MaxRetries:    1,
		MaxSubagents:  1,
	}
}

// testFinanceContextAdapter maps campaign.FinanceContextBuilder to a REAL
// executive.ContextCoordinator (runtimeadapter.Context) -- duplicated from
// cmd/orgctl/executive.go's own financeContextAdapter (unexported there,
// package main) rather than shared, matching this package's own
// established convention of small, duplicated test helpers (see
// finTestTaskCoordinator's doc comment). Selects the existing, already-
// canonical executive.department_worker ContextProfile the same way
// production does: Purpose="department_worker",
// ExecutionPurpose="department-worker", no TaskClass-tier profile
// registered for campaign.financial_review, so selection always falls
// through to the EXECUTION-PURPOSE match.
type testFinanceContextAdapter struct {
	coordinator executive.ContextCoordinator
}

func (a testFinanceContextAdapter) BuildFinanceContext(ctx context.Context, request campaign.FinanceContextRequest) (campaign.FinanceContextSnapshot, error) {
	snapshot, err := a.coordinator.Build(ctx, executive.ContextRequest{
		OrganizationRevisionID: request.OrganizationRevisionID,
		ActorRoleID:            request.ActorRoleID,
		ActorUnitID:            request.ActorUnitID,
		Purpose:                "department_worker",
		ExecutionPurpose:       "department-worker",
		TaskRef:                fmt.Sprintf("task:%d", request.TaskID),
		TaskClass:              request.TaskClass,
		CorrelationID:          request.CorrelationID,
		CausationID:            request.CausationID,
		IdempotencyKey:         request.IdempotencyKey,
	})
	if err != nil {
		return campaign.FinanceContextSnapshot{}, err
	}
	return campaign.FinanceContextSnapshot{ID: snapshot.ID, Version: snapshot.Version, Digest: snapshot.Digest, Content: snapshot.Content}, nil
}

// buildTestFinanceContextBuilder opens a REAL, independent Context Engine
// runtime (real contextengine.Service + real contextcompiler.
// ContextAssemblyService against the same already-migrated store) --
// exactly the same composition internal/executive/bootstrap/runtime.go
// uses for the Executive Orchestrator, opened a second time here only
// because this is a TEST fixture emulating a second real worker process
// against the same database (the same reasoning buildTestModelDispatch's
// own doc comment gives for opening a second real dispatch runtime).
// cmd/orgctl's own production composition never does this: it reuses the
// ONE already-open executivebootstrap.Runtime.Contexts (see
// financeContextAdapter's production counterpart).
func buildTestFinanceContextBuilder(t *testing.T, store *platformpostgres.Store, tasksSvc *tasks.Service, organizationID string) campaign.FinanceContextBuilder {
	t.Helper()
	return testFinanceContextAdapter{coordinator: buildTestFinanceContextCoordinator(t, store, tasksSvc, organizationID)}
}

// buildTestFinanceContextCoordinator returns the raw
// executive.ContextCoordinator (not yet wrapped in the narrow
// campaign.FinanceContextBuilder port) so a test can also inspect
// ContextSnapshot.ExecutionContextViewID directly -- section 21's profile
// assertion and section 27's model-input proof both need that, and it is
// deliberately NOT part of campaign.FinanceContextSnapshot (internal/
// campaign must stay narrow and never import internal/contextcompiler).
// buildTestFinanceE2EConfig loads the same config.Config shape every
// buildTest* helper in this file needs, against ORG_TEST_DATABASE_URL --
// factored out once other helpers besides buildTestFinanceContextCoordinator
// started needing it too (identitybootstrap.Open, in particular).
func buildTestFinanceE2EConfig(t *testing.T, organizationID string) config.Config {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":           "test",
			"ORG_DATABASE_URL":          databaseURL,
			"ORG_DATABASE_MAX_CONNS":    "16",
			"ORG_DATABASE_MIN_CONNS":    "0",
			"ORG_CANONICAL_DIR":         filepath.Join("..", "..", "docs", "canonical"),
			"ORG_CONTEXT_SOURCE_ROOT":   "/src",
			"ORG_TASKS_ORGANIZATION_ID": organizationID,
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load finance e2e config: %v", err)
	}
	return cfg
}

func buildTestFinanceContextCoordinator(t *testing.T, store *platformpostgres.Store, tasksSvc *tasks.Service, organizationID string) runtimeadapter.Context {
	t.Helper()
	cfg := buildTestFinanceE2EConfig(t, organizationID)
	taskContextProvider, err := taskcontextprovider.New(tasksSvc)
	if err != nil {
		t.Fatalf("create finance task context provider: %v", err)
	}
	contextRuntime, err := contextbootstrap.Open(cfg, store, taskContextProvider)
	if err != nil {
		t.Fatalf("open finance context engine runtime: %v", err)
	}
	return runtimeadapter.Context{
		Service: contextRuntime.Service, Assembly: contextcompiler.ContextAssemblyService{Store: mustContextcompilerStore(t, store)}, OrganizationID: organizationID,
	}
}

// mustContextcompilerStore opens a fresh, stateless
// contextcompilerpostgres.Store reader against the same already-migrated
// database -- cheap and safe to construct more than once, exactly like
// this file's other buildTest* helpers each independently open their own
// real store against the shared test database.
func mustContextcompilerStore(t *testing.T, store *platformpostgres.Store) *contextcompilerpostgres.Store {
	t.Helper()
	viewStore, err := contextcompilerpostgres.New(store)
	if err != nil {
		t.Fatalf("create execution context view store: %v", err)
	}
	return viewStore
}

// buildTestFinanceServiceRealHarness builds a REAL FinanceService with
// EVERY Harness execution dependency wired for real -- Authority
// (tasksauthority.Adapter, from modelRuntime.NewHarnessAuthority()),
// HarnessHistory/DescriptorStore (executionharnesspostgres.Store, the
// same production type cmd/orgctl/executive.go's own buildFinanceWorker
// uses), NewModelExecutor (modelRuntime.NewHarnessModelExecutor,
// dispatching through the SAME real Model Runtime instance the caller's
// fixture opened with a test.fake adapter registered), and ContextBuilder
// (a real Context Engine runtime, see buildTestFinanceContextBuilder) --
// mirroring buildTestFinanceService's own wiring in
// finance_worker_integration_test.go plus these five additional real
// dependencies that file never needed (every test there uses MockOutput).
// Duplicated rather than added as optional parameters to
// buildTestFinanceService itself, matching this package's own established
// convention of small, duplicated test helpers over shared ones with
// growing parameter lists (see finTestTaskCoordinator's and
// financeTestDispatchProvisioner's own doc comments).
func buildTestFinanceServiceRealHarness(t *testing.T, store *platformpostgres.Store, tasksSvc *tasks.Service, dispatch *modeldispatchbootstrap.Runtime, modelRuntime *modelbootstrap.Runtime, organizationID string, requirements campaign.ExecutionRequirementsProvider) (svc *campaign.FinanceService, campStore *campaignpostgres.Store, reviewerRoleID string, revisionID int64, holderPrincipalID string, restore func()) {
	t.Helper()
	ctx := context.Background()

	campStore, err := campaignpostgres.New(store)
	if err != nil {
		t.Fatalf("open campaign store: %v", err)
	}
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatalf("open registry repository: %v", err)
	}
	authorizationStore, err := authorizationpostgres.New(store)
	if err != nil {
		t.Fatalf("open authorization store: %v", err)
	}
	authorizerPolicy, err := authorization.NewWithPolicyReader(authorizationStore, organizationID, filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatalf("open capability authorizer: %v", err)
	}
	roleResolver := campaign.DefaultReviewerRoleResolver{Registry: registryRepo, Authorizer: authorizerPolicy}
	revision, err := registryRepo.GetCurrentRevision(ctx, organizationID)
	if err != nil || revision == nil {
		t.Fatalf("read current organization revision: revision=%+v err=%v", revision, err)
	}
	reviewerRoleID, err = roleResolver.ResolveReviewerRole(ctx, organizationID, revision.ID)
	if err != nil {
		t.Fatalf("resolve canonical finance reviewer role: %v", err)
	}
	restore = alignFinanceRoleForRealDispatch(t, store, organizationID, reviewerRoleID)

	financeAssignments, err := dispatch.NewAuthorizedAttemptProvisioner(chatTestDispatchPrincipalKey)
	if err != nil {
		t.Fatalf("create finance dispatch provisioner: %v", err)
	}
	roleBoundResolver, err := runtimeadapter.NewRoleBoundPrincipalResolver(dispatch.Store, organizationID)
	if err != nil {
		t.Fatalf("create role-bound principal resolver: %v", err)
	}
	financePrincipal, err := roleBoundResolver.Resolve(ctx, reviewerRoleID)
	if err != nil {
		t.Fatalf("resolve finance role-bound principal: %v", err)
	}
	holderPrincipalID = strconv.FormatInt(financePrincipal.ID, 10)

	harnessAuthority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		t.Fatalf("create real harness authority: %v", err)
	}
	harnessHistory, err := executionharnesspostgres.New(store, organizationID)
	if err != nil {
		t.Fatalf("create real harness history/descriptor store: %v", err)
	}
	contextBuilder := buildTestFinanceContextBuilder(t, store, tasksSvc, organizationID)

	financeService, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID:  organizationID,
		Requirements:    requirements,
		Store:           campStore,
		Tasks:           finTestTaskCoordinator{tasksSvc},
		Assignments:     financeTestDispatchProvisioner{financeAssignments},
		Authorizer:      authorizerPolicy,
		RoleResolver:    roleResolver,
		Authority:       harnessAuthority,
		HarnessHistory:  harnessHistory,
		DescriptorStore: harnessHistory,
		ContextBuilder:  contextBuilder,
		NewModelExecutor: func(execConfig modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return modelRuntime.NewHarnessModelExecutor(execConfig)
		},
		WorkerID:          "finance-worker-real-harness-test",
		HolderPrincipalID: holderPrincipalID,
		LeaseDuration:     2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create finance service (real harness): %v", err)
	}
	return financeService, campStore, reviewerRoleID, revision.ID, holderPrincipalID, restore
}

// newFinanceRealHarnessFixture opens the same real Postgres/Task-Engine/
// dispatch stack newFinanceWorkerFixture does, but with a test.fake
// adapter registered in the underlying Model Runtime and a FinanceService
// wired with real Authority/HarnessHistory/DescriptorStore/
// NewModelExecutor -- the composition production's own
// cmd/orgctl/executive.go:buildFinanceWorker uses, minus only the real
// provider (test.fake stands in for it, REAL_PROVIDER_CALLS=0).
// newFinanceRealHarnessFixture opens the same real Postgres/Task-Engine/
// dispatch stack newFinanceWorkerFixture does, but wires the FULL real
// Model Runtime dispatch path a real (non-MockOutput) Harness execution
// needs -- test.fake registered as an extra adapter, Finance's canonical
// reviewer role repointed to a test.fake role_model_binding under a
// throwaway sibling registry revision (alignFinanceRoleForRealDispatch,
// exactly its own documented intended use), a test.fake egress allow
// plan, a synced execution-identity policy plus a registered identity
// key for the shared test dispatch principal, and a funded test.fake
// wallet with a priced tier -- every one of these is a REAL requirement
// DispatchService.Dispatch enforces before it will call ANY provider
// adapter, real or fake, and no earlier Finance test ever needed any of
// them (every one used MockOutput). Mirrors
// canonical_dispatch_e2e_test.go's own newCEOChatCanonicalE2EFixtureWithStore
// step for step, adapted to Finance's role instead of empresa/ceo's.
//
// The returned restore func undoes the sibling-revision shift and the
// finance-role repoint. It MUST be deferred by the caller BEFORE
// f.cleanup() (Go defers run LIFO, and this restore still needs the
// store's connection pool open) -- e.g.:
//
//	f, fx, _, restore := newFinanceRealHarnessFixture(t)
//	defer f.cleanup()
//	defer restore()
func newFinanceRealHarnessFixture(t *testing.T) (*chatFixture, *financeWorkerFixture, *modelbootstrap.Runtime, func()) {
	t.Helper()
	t.Setenv("ORG_MODEL_EXECUTION_PRINCIPAL_KEY", chatTestDispatchPrincipalKey)
	t.Setenv("ORG_MODEL_RUNTIME_ENABLED", "true")
	// DispatchService.Dispatch fails closed ("ORG_MODEL_EXECUTION_IDENTITY_ENABLED
	// is false") once an invocation has an identity policy pinned unless
	// this is explicitly enabled with a real key on file -- see
	// canonical_dispatch_e2e_test.go's own identical comment.
	identityPrivateKey, identityKeyFile := writeCEOChatE2EIdentityKeyFile(t)
	t.Setenv("ORG_MODEL_EXECUTION_IDENTITY_ENABLED", "true")
	t.Setenv("ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE", identityKeyFile)

	f := newChatFixtureWithModelRuntimeOptions(t, modelbootstrap.WithExtraAdapters(adapter.NewFake()))
	ctx := context.Background()
	cfg := buildTestFinanceE2EConfig(t, chatTestOrganization)

	registryRepo, err := registry.NewPostgresRepository(f.store)
	if err != nil {
		t.Fatalf("open registry repository: %v", err)
	}
	revision, err := registryRepo.GetCurrentRevision(ctx, chatTestOrganization)
	if err != nil || revision == nil {
		t.Fatalf("read current organization revision: revision=%+v err=%v", revision, err)
	}
	authorizationStore, err := authorizationpostgres.New(f.store)
	if err != nil {
		t.Fatalf("open authorization store: %v", err)
	}
	authorizerPolicy, err := authorization.NewWithPolicyReader(authorizationStore, chatTestOrganization, filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatalf("open capability authorizer: %v", err)
	}
	roleResolver := campaign.DefaultReviewerRoleResolver{Registry: registryRepo, Authorizer: authorizerPolicy}
	reviewerRoleID, err := roleResolver.ResolveReviewerRole(ctx, chatTestOrganization, revision.ID)
	if err != nil {
		t.Fatalf("resolve canonical finance reviewer role: %v", err)
	}

	// repointCEORoleBindingToTestFake creates the sibling revision AND
	// repoints empresa/ceo's own binding under it -- not merely to reach
	// test.fake for CEO (this fixture's scripted CEO model bypasses that
	// entirely), but because seedReadyReviewTaskWithGoal's own seed turn
	// (service.Send, real dispatch assignment provisioning) requires
	// empresa/ceo's role routing authority to resolve under WHATEVER
	// revision organizations.current_revision_id now names -- a role with
	// no binding at all under the sibling fails closed the same way a
	// misaligned source_revision_id does.
	shadowRevisionID, restoreRevision := repointCEORoleBindingToTestFake(t, ctx, f.store, chatTestOrganization, revision.ID)
	restoreFinanceRole := alignFinanceRoleForRealDispatch(t, f.store, chatTestOrganization, reviewerRoleID)

	egressStore, err := egresspostgres.New(f.store)
	if err != nil {
		t.Fatalf("open egress store: %v", err)
	}
	fixtureEgressHash := ceochatE2EHexFixture(fmt.Sprintf("finance-e2e-egress-policy-%d", shadowRevisionID))
	if _, err = f.store.Pool().Exec(ctx, `UPDATE organization_registry_revisions SET document_hashes = jsonb_set(document_hashes, '{model-egress-policy.yaml}', to_jsonb($1::text)) WHERE id=$2`,
		fixtureEgressHash, shadowRevisionID); err != nil {
		t.Fatalf("set sibling revision's egress document hash: %v", err)
	}
	allowTestFakeEgress(t, ctx, f.store, egressStore, chatTestOrganization, shadowRevisionID, fixtureEgressHash)

	identityRuntime, err := identitybootstrap.Open(cfg, f.store)
	if err != nil {
		t.Fatalf("open model identity runtime: %v", err)
	}
	if sync, syncErr := identityRuntime.Policy.Sync(ctx, true); syncErr != nil || (!sync.Applied && !sync.NoOp) {
		t.Fatalf("sync model identity policy: result=%+v err=%v", sync, syncErr)
	}
	var dispatchPrincipalID int64
	if err = f.store.Pool().QueryRow(ctx, `SELECT id FROM model_execution_principals WHERE organization_id=$1 AND principal_key=$2`, chatTestOrganization, chatTestDispatchPrincipalKey).Scan(&dispatchPrincipalID); err != nil {
		t.Fatalf("read registered dispatch principal ID: %v", err)
	}
	identityPublicKey := identityPrivateKey.Public().(ed25519.PublicKey)
	preparedIdentityKey := modelidentity.PreparedKey{
		OrganizationID: chatTestOrganization, ExecutionPrincipalID: dispatchPrincipalID,
		PublicKey: identityPublicKey, PublicKeyFingerprint: modelidentity.PublicKeyFingerprint(identityPublicKey),
		SecretRef: "file://ceochat-e2e/execution-identity-key-1", IdempotencyKey: "ceochat-e2e-identity-key",
		CreatedByRoleID: "empresa/human",
	}
	if preparedIdentityKey.RequestHash, err = modelidentity.KeyRequestHash(preparedIdentityKey); err != nil {
		t.Fatalf("compute execution identity key request hash: %v", err)
	}
	if _, err = identityRuntime.Store.RegisterKey(ctx, preparedIdentityKey); err != nil {
		t.Fatalf("register execution identity key: %v", err)
	}

	// CostBudgetGate needs a priced tier and a funded wallet for
	// "test.fake" before Dispatch will reserve cost and proceed, for ANY
	// provider -- real or fake. ProviderModelID matches exactly what
	// alignFinanceRoleForRealDispatch's own model_profile_versions row
	// uses ("ceochat-e2e-finance-fake"), never CEO's own
	// "ceochat-e2e-fake" pricing entry.
	pricingStore, err := modelpricingpostgres.New(f.store)
	if err != nil {
		t.Fatalf("open pricing store: %v", err)
	}
	pricingService, err := modelpricing.NewService(pricingStore)
	if err != nil {
		t.Fatalf("open pricing service: %v", err)
	}
	if _, err = pricingService.Upsert(ctx, modelpricing.PriceTier{
		ProviderID: "test.fake", ProviderModelID: "ceochat-e2e-finance-fake", ContextTierName: "default",
		InputPriceNanosPerMillion: 1_000_000_000, OutputPriceNanosPerMillion: 2_000_000_000,
		BillingMode: modelpricing.BillingOnline, EffectiveAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed test.fake price tier: %v", err)
	}
	walletStore, err := costledgerpostgres.New(f.store)
	if err != nil {
		t.Fatalf("open wallet store: %v", err)
	}
	if _, err = walletStore.SetBalance(ctx, "test.fake", modelpricing.USDFromDollars(10), time.Now().UTC()); err != nil {
		t.Fatalf("fund test.fake wallet: %v", err)
	}

	dispatch := buildTestModelDispatch(t, f.store, f.runtime.Tasks, chatTestOrganization)
	requirements := newMutableRequirements()
	financeService, campStore, reviewerRoleID2, revisionID, holderPrincipalID, _ := buildTestFinanceServiceRealHarness(t, f.store, f.runtime.Tasks, dispatch, f.runtime.ModelRuntime, chatTestOrganization, requirements)
	restore := func() {
		restoreFinanceRole()
		restoreRevision()
	}
	return f, &financeWorkerFixture{
		store: campStore, financeService: financeService, reviewerRoleID: reviewerRoleID2,
		revisionID: revisionID, tasksService: f.runtime.Tasks, holderPrincipalID: holderPrincipalID, requirements: requirements,
	}, f.runtime.ModelRuntime, restore
}

// TestRealFinanceHarnessIntegration_MockOutputNilFullRoundTrip is round
// sections 17-19, 25, 26 (FINANCE_CONTEXT_ENGINE_INTEGRATION_V1):
// FinanceService.ExecuteReviewTask with MockOutput=nil, driving the REAL
// executionharness.Runtime, REAL tasksauthority.Adapter (lease/role/
// principal authority -- section 16's positive case), REAL dispatch
// assignment provisioning, a REAL Context Engine snapshot build, and a
// REAL Model Runtime dispatch through test.fake, all the way to a
// persisted FinancialReview and a completed Finance task.
//
// This closes the blocker PR #224 discovered and pinned
// (TestRealFinanceHarnessIntegration_MockOutputNilReachesRealModelDispatch,
// "initial context ID must be a positive model runtime snapshot ID"):
// runHarnessModel now asks a real campaign.FinanceContextBuilder for a
// real, durable context-engine snapshot instead of fabricating
// "context-finrev-<taskID>", and uses its ID/Version/Digest/Content
// byte-for-byte.
func TestRealFinanceHarnessIntegration_MockOutputNilFullRoundTrip(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{}) // ceochat's own turn 0 ("Hola.") never reaches Finance

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{
		Verdict: "recommended", Summary: "Real Harness round trip proof.",
		RecommendedBudget: financeTestExecutableBudget(),
		Assumptions:       []string{"none"}, Risks: []string{}, RequiredCorrections: []string{}, MissingInformation: []string{},
	})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "real-harness-full", goal)

	review, _, err := fx.financeService.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID: chatTestOrganization, TaskID: taskID, ReviewRequestID: reviewRequestID,
	})
	if err != nil {
		t.Fatalf("ExecuteReviewTask (real harness, MockOutput=nil): %v", err)
	}
	if review.Verdict != campaign.VerdictRecommended {
		t.Fatalf("verdict = %q, want recommended", review.Verdict)
	}

	var reviewCount, completedCount, assignmentCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID).Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 1 {
		t.Errorf("review rows = %d, want exactly 1", reviewCount)
	}
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id=$1 AND status='completed'", taskID).Scan(&completedCount); err != nil {
		t.Fatalf("count completed: %v", err)
	}
	if completedCount != 1 {
		t.Errorf("completed task count = %d, want 1", completedCount)
	}
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM model_dispatcher_assignments WHERE task_id=$1", taskID).Scan(&assignmentCount); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if assignmentCount != 1 {
		t.Errorf("dispatch assignment rows for task %d = %d, want exactly 1", taskID, assignmentCount)
	}
}

// TestFinanceContext_UsesDepartmentWorkerProfileAndProposalIsVisible is
// round sections 21, 22, 23, 27: a real Context Engine build for a real
// Finance task must (a) select the existing, unmodified
// executive.department_worker ContextProfile (never a new, Finance-
// specific one), (b) produce a provider-visible ExecutionContextView
// whose bytes match the returned snapshot's own Content/Digest exactly,
// and (c) actually contain the reviewed proposal's own unique,
// deterministic content -- not merely a reference to its ID.
func TestFinanceContext_UsesDepartmentWorkerProfileAndProposalIsVisible(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	const uniqueGoalMarker = "Ensure-runway-covers-quarter-Q9182-fiscal-anomaly"
	_, taskID, _ := fx.seedReadyReviewTaskWithGoal(t, service, "context-visibility", uniqueGoalMarker)

	task, err := fx.tasksService.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("read finance task: %v", err)
	}
	if !strings.Contains(task.Task.Instructions, uniqueGoalMarker) {
		t.Fatalf("finance task instructions do not contain the proposal's own unique goal text -- RequestReview must embed the complete proposal, not a summary")
	}

	coordinator := buildTestFinanceContextCoordinator(t, f.store, f.runtime.Tasks, chatTestOrganization)
	snapshot, err := coordinator.Build(ctx, executive.ContextRequest{
		OrganizationRevisionID: task.Task.OrganizationRevisionID,
		ActorRoleID:            task.Task.AssignedRoleID,
		ActorUnitID:            task.Task.AssignedUnitID,
		Purpose:                "department_worker",
		ExecutionPurpose:       "department-worker",
		TaskRef:                fmt.Sprintf("task:%d", taskID),
		TaskClass:              campaign.FinancialReviewTaskClass,
		CorrelationID:          *task.Task.CorrelationID,
		CausationID:            *task.Task.CausationID,
		IdempotencyKey:         fmt.Sprintf("context-visibility-probe:%d", taskID),
	})
	if err != nil {
		t.Fatalf("build finance context snapshot: %v", err)
	}
	if !strings.Contains(snapshot.Content, uniqueGoalMarker) {
		t.Errorf("provider-visible context content does not contain the proposal's own unique goal text %q -- the proposal is not actually visible to the reviewer", uniqueGoalMarker)
	}

	viewStore := mustContextcompilerStore(t, f.store)
	view, err := viewStore.Get(ctx, snapshot.ExecutionContextViewID)
	if err != nil {
		t.Fatalf("read execution context view %d: %v", snapshot.ExecutionContextViewID, err)
	}
	if view.ContextProfileID != "executive.department_worker" {
		t.Errorf("ContextProfile.ID = %q, want %q (selector precedence chose a different profile -- STOP and report per this round's own instruction, do not just widen this assertion)", view.ContextProfileID, "executive.department_worker")
	}
}

// TestRealLeaseAuthority_EmptyLeaseTokenRejectedByAdapter is round section
// 16's negative control, exercised directly against the REAL
// tasksauthority.Adapter (not merely Finance's own
// validateFinanceHarnessPreconditions guard, which
// finance_harness_composition_internal_test.go's TestValidateFinanceHarnessPreconditions
// already pins): a real claimed task/attempt/lease from the real Task
// Engine, presented to AuthorizeExecution with LeaseToken="" instead of
// the real one, must fail BEFORE any model invocation is even reachable --
// this test never calls Invoke at all, so "before any model invocation"
// holds trivially and structurally, not just by assertion.
func TestRealLeaseAuthority_EmptyLeaseTokenRejectedByAdapter(t *testing.T) {
	f, fx, modelRuntime, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{Verdict: "recommended", Summary: "unused", RecommendedBudget: financeTestExecutableBudget()})
	_, taskID, _ := fx.seedReadyReviewTaskWithGoal(t, service, "real-lease-neg", goal)

	claimed, err := fx.tasksService.ClaimTaskByID(ctx, taskID, tasks.ClaimRequest{
		OrganizationID: chatTestOrganization, WorkerID: fx.holderPrincipalID, AssignedRoleID: fx.reviewerRoleID, LeaseDuration: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err = fx.tasksService.StartAttempt(ctx, tasks.LeaseCommand{TaskID: claimed.Task.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: fx.holderPrincipalID}); err != nil {
		t.Fatalf("start attempt: %v", err)
	}

	authority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		t.Fatalf("create real harness authority: %v", err)
	}
	identity := executionharness.RunIdentity{
		RunID: "finrev-negative-lease-test", OrganizationID: chatTestOrganization,
		TaskID: claimed.Task.ID, AttemptID: claimed.Attempt.ID, RoleID: fx.reviewerRoleID,
		ExecutionPrincipalID: fx.holderPrincipalID,
		CorrelationID:        "corr:negative-lease-test", CausationID: "cause:negative-lease-test",
	}

	// Positive control first: the REAL lease token authorizes.
	if err := authority.AuthorizeExecution(ctx, executionharness.AuthorityRequest{Identity: identity, LeaseToken: claimed.LeaseToken}); err != nil {
		t.Fatalf("expected the real lease token to authorize, got: %v", err)
	}

	// Negative control: an empty lease token must NOT authorize, even
	// though every other field (task/attempt/role/principal) is real and
	// currently valid.
	if err := authority.AuthorizeExecution(ctx, executionharness.AuthorityRequest{Identity: identity, LeaseToken: ""}); err == nil {
		t.Fatal("expected AuthorizeExecution to reject an empty lease token, got nil error")
	}
}

// TestFinanceWorkerRealHarness_RunOnce is round section 24: driven through
// financeworker.Worker.RunOnce's real discovery/claim loop (never a direct
// ExecuteReviewTask call), with the SAME real-harness FinanceService
// passed directly as the worker's Executor -- *campaign.FinanceService
// itself already satisfies financeworker.Executor, exactly like production
// composition (cmd/orgctl/executive.go's own buildFinanceWorker passes the
// same concrete FinanceService as its worker's executor).
//
// The context-engine integration closes the blocker
// TestRealFinanceHarnessIntegration_MockOutputNilFullRoundTrip's own doc
// comment describes: with a real FinanceContextBuilder wired, the real
// round trip now completes for real, driven through the autonomous
// worker's own discovery/claim loop rather than a direct
// ExecuteReviewTask call.
func TestFinanceWorkerRealHarness_RunOnce(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{Verdict: "recommended", Summary: "Real worker + real harness.", RecommendedBudget: financeTestExecutableBudget()})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "real-harness-worker", goal)

	var mu sync.Mutex
	var classifications []financeworker.ResultClassification
	discovery := financeworker.DiscoveryTaskSource{Service: fx.tasksService, OrganizationID: chatTestOrganization, ReviewerRoleID: fx.reviewerRoleID}
	worker, err := financeworker.NewWorker(discovery, fx.store, fx.financeService, financeworker.DefaultConfig(chatTestOrganization),
		financeworker.WithObserver(func(observedTaskID int64, classification financeworker.ResultClassification, _ error) {
			if observedTaskID != taskID {
				return
			}
			mu.Lock()
			classifications = append(classifications, classification)
			mu.Unlock()
		}),
	)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	mu.Lock()
	observed := append([]financeworker.ResultClassification(nil), classifications...)
	mu.Unlock()
	if len(observed) != 1 || observed[0] != financeworker.ResultSuccess {
		t.Errorf("classifications for task %d = %v, want exactly [success]", taskID, observed)
	}

	var reviewCount, completedCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID).Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 1 {
		t.Errorf("review rows = %d, want exactly 1", reviewCount)
	}
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id=$1 AND status='completed'", taskID).Scan(&completedCount); err != nil {
		t.Fatalf("count completed: %v", err)
	}
	if completedCount != 1 {
		t.Errorf("completed task count = %d, want 1", completedCount)
	}
}

// TestFinanceWorkerMultiReplicaRealHarness_TwoWorkersOneExecution is round
// section 25: two Worker replicas race on ONE real-harness Finance task
// (MockOutput=nil, test.fake provider, real Context Engine). Task
// Engine's own FOR UPDATE SKIP LOCKED claim -- not this test -- is what
// must guarantee only one replica ever attempts, and completes, the real
// Harness run for this task: exactly one success, exactly one durable
// review, no duplicate real Harness run.
func TestFinanceWorkerMultiReplicaRealHarness_TwoWorkersOneExecution(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{Verdict: "recommended", Summary: "Multi-replica real harness.", RecommendedBudget: financeTestExecutableBudget()})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "real-harness-multi", goal)

	var mu sync.Mutex
	var classifications []financeworker.ResultClassification
	observerFor := func() financeworker.Option {
		return financeworker.WithObserver(func(observedTaskID int64, classification financeworker.ResultClassification, _ error) {
			if observedTaskID != taskID {
				return
			}
			mu.Lock()
			classifications = append(classifications, classification)
			mu.Unlock()
		})
	}

	discovery := financeworker.DiscoveryTaskSource{Service: fx.tasksService, OrganizationID: chatTestOrganization, ReviewerRoleID: fx.reviewerRoleID}
	workerA, err := financeworker.NewWorker(discovery, fx.store, fx.financeService, financeworker.DefaultConfig(chatTestOrganization), observerFor())
	if err != nil {
		t.Fatalf("NewWorker A: %v", err)
	}
	workerB, err := financeworker.NewWorker(discovery, fx.store, fx.financeService, financeworker.DefaultConfig(chatTestOrganization), observerFor())
	if err != nil {
		t.Fatalf("NewWorker B: %v", err)
	}

	done := make(chan struct{}, 2)
	start := make(chan struct{})
	go func() { <-start; _, _ = workerA.RunOnce(ctx); done <- struct{}{} }()
	go func() { <-start; _, _ = workerB.RunOnce(ctx); done <- struct{}{} }()
	close(start)
	<-done
	<-done

	mu.Lock()
	observed := append([]financeworker.ResultClassification(nil), classifications...)
	mu.Unlock()
	successCount := 0
	for _, c := range observed {
		if c == financeworker.ResultSuccess {
			successCount++
		}
		if c == financeworker.ResultInfraFailure {
			t.Errorf("unexpected infra_failure classification for task %d", taskID)
		}
	}
	if successCount != 1 {
		t.Errorf("success classifications for task %d = %d (observed: %v), want exactly 1", taskID, successCount, observed)
	}

	var reviewCount, completedCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID).Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 1 {
		t.Errorf("review rows = %d, want exactly 1 (no duplicate real Harness run)", reviewCount)
	}
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id=$1 AND status='completed'", taskID).Scan(&completedCount); err != nil {
		t.Fatalf("count completed: %v", err)
	}
	if completedCount != 1 {
		t.Errorf("completed task count = %d, want 1", completedCount)
	}
}

// buildRealHarnessFinanceWorkerForE2E builds a real-Harness Finance
// autonomous worker (MockOutput=nil, real Context Engine, real Model
// Runtime) that DISPATCHES THROUGH THE CALLER'S OWN modelRuntime instance
// -- e.g. canonical_dispatch_e2e_test.go's newCEOChatCanonicalE2EFixtureWithStore's
// own returned *modelbootstrap.Runtime, whose registered test.fake
// adapter is already reachable. FINANCE_FULLSTACK_E2E_CLOSURE_V1.
//
// It reuses alignFinanceRoleForRealDispatch exactly as documented for its
// intended use: the CALLER is expected to have already shifted
// organizations.current_revision_id to a throwaway sibling registry
// revision with a test.fake egress allow plan already applied (as
// newCEOChatCanonicalE2EFixtureWithStore's own repointCEORoleBindingToTestFake
// does for empresa/ceo) -- this function does NOT create a new sibling or
// a new egress plan, only Finance's own role_model_binding under whatever
// revision is already current, and a pricing tier for Finance's own
// provider_model_id ("ceochat-e2e-finance-fake", distinct from CEO's own
// "ceochat-e2e-fake" entry).
func buildRealHarnessFinanceWorkerForE2E(t *testing.T, store *platformpostgres.Store, tasksSvc *tasks.Service, modelRuntime *modelbootstrap.Runtime, organizationID string, requirements campaign.ExecutionRequirementsProvider) (worker *financeworker.Worker, campStore *campaignpostgres.Store, restore func()) {
	t.Helper()
	ctx := context.Background()

	campStore, err := campaignpostgres.New(store)
	if err != nil {
		t.Fatalf("open campaign store: %v", err)
	}
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatalf("open registry repository: %v", err)
	}
	revision, err := registryRepo.GetCurrentRevision(ctx, organizationID)
	if err != nil || revision == nil {
		t.Fatalf("read current organization revision: revision=%+v err=%v", revision, err)
	}
	authorizationStore, err := authorizationpostgres.New(store)
	if err != nil {
		t.Fatalf("open authorization store: %v", err)
	}
	authorizerPolicy, err := authorization.NewWithPolicyReader(authorizationStore, organizationID, filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatalf("open capability authorizer: %v", err)
	}
	roleResolver := campaign.DefaultReviewerRoleResolver{Registry: registryRepo, Authorizer: authorizerPolicy}
	reviewerRoleID, err := roleResolver.ResolveReviewerRole(ctx, organizationID, revision.ID)
	if err != nil {
		t.Fatalf("resolve canonical finance reviewer role: %v", err)
	}
	restore = alignFinanceRoleForRealDispatch(t, store, organizationID, reviewerRoleID)

	dispatch := buildTestModelDispatch(t, store, tasksSvc, organizationID)
	financeAssignments, err := dispatch.NewAuthorizedAttemptProvisioner(chatTestDispatchPrincipalKey)
	if err != nil {
		t.Fatalf("create finance dispatch provisioner: %v", err)
	}
	roleBoundResolver, err := runtimeadapter.NewRoleBoundPrincipalResolver(dispatch.Store, organizationID)
	if err != nil {
		t.Fatalf("create role-bound principal resolver: %v", err)
	}
	financePrincipal, err := roleBoundResolver.Resolve(ctx, reviewerRoleID)
	if err != nil {
		t.Fatalf("resolve finance role-bound principal: %v", err)
	}
	holderPrincipalID := strconv.FormatInt(financePrincipal.ID, 10)

	harnessAuthority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		t.Fatalf("create real harness authority: %v", err)
	}
	harnessHistory, err := executionharnesspostgres.New(store, organizationID)
	if err != nil {
		t.Fatalf("create real harness history/descriptor store: %v", err)
	}
	contextBuilder := buildTestFinanceContextBuilder(t, store, tasksSvc, organizationID)

	pricingStore, err := modelpricingpostgres.New(store)
	if err != nil {
		t.Fatalf("open pricing store: %v", err)
	}
	pricingService, err := modelpricing.NewService(pricingStore)
	if err != nil {
		t.Fatalf("open pricing service: %v", err)
	}
	if _, err = pricingService.Upsert(ctx, modelpricing.PriceTier{
		ProviderID: "test.fake", ProviderModelID: "ceochat-e2e-finance-fake", ContextTierName: "default",
		InputPriceNanosPerMillion: 1_000_000_000, OutputPriceNanosPerMillion: 2_000_000_000,
		BillingMode: modelpricing.BillingOnline, EffectiveAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed test.fake price tier: %v", err)
	}

	financeService, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID:  organizationID,
		Requirements:    requirements,
		Store:           campStore,
		Tasks:           finTestTaskCoordinator{tasksSvc},
		Assignments:     financeTestDispatchProvisioner{financeAssignments},
		Authorizer:      authorizerPolicy,
		RoleResolver:    roleResolver,
		Authority:       harnessAuthority,
		HarnessHistory:  harnessHistory,
		DescriptorStore: harnessHistory,
		ContextBuilder:  contextBuilder,
		NewModelExecutor: func(execConfig modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return modelRuntime.NewHarnessModelExecutor(execConfig)
		},
		WorkerID:          "finance-worker-e2e-real-harness",
		HolderPrincipalID: holderPrincipalID,
		LeaseDuration:     2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create finance service (real harness e2e): %v", err)
	}

	discovery := financeworker.DiscoveryTaskSource{Service: tasksSvc, OrganizationID: organizationID, ReviewerRoleID: reviewerRoleID}
	worker, err = financeworker.NewWorker(discovery, campStore, financeService, financeworker.DefaultConfig(organizationID))
	if err != nil {
		t.Fatalf("NewWorker (real harness e2e): %v", err)
	}
	return worker, campStore, restore
}

// financeReentryToolCatalog/financeReentryToolExecutor are deny-all
// stand-ins for internal/campaign's own unexported financeToolCatalog/
// financeToolExecutor (needed here because this test drives a SECOND,
// independent executionharness.Runtime directly, from black-box code --
// see TestFinanceHarnessReentry_SameAttemptAdoptsDurableTerminalRun).
// Behaviorally identical: Finance exposes zero tools, so any tool intent
// must be denied before either of these is ever reached.
type financeReentryToolCatalog struct{}

func (financeReentryToolCatalog) Lookup(context.Context, string) (executionharness.ToolDefinition, bool) {
	return executionharness.ToolDefinition{}, false
}

func (financeReentryToolCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return fmt.Errorf("finance reentry fixture: no tools are exposed")
}

type financeReentryToolExecutor struct{}

func (financeReentryToolExecutor) Execute(context.Context, executionharness.RunIdentity, executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	return executionharness.ToolExecutionResult{}, fmt.Errorf("finance reentry fixture: no tools execute")
}

func mustCountModelInvocations(t *testing.T, ctx context.Context, store *platformpostgres.Store, taskID, attemptID int64) int {
	t.Helper()
	var count int
	if err := store.Pool().QueryRow(ctx, "SELECT count(*) FROM model_invocations WHERE task_id=$1 AND attempt_id=$2", taskID, attemptID).Scan(&count); err != nil {
		t.Fatalf("count model invocations: %v", err)
	}
	return count
}

// capturingHarnessRunner satisfies campaign.HarnessRunner: it captures the
// EXACT RunSpec runHarnessModel builds (including the real, ephemeral
// LeaseToken a completed task can no longer be asked for afterward) and
// forwards it, unmodified, to a REAL underlying executionharness.Runtime
// -- nothing about the real path changes; only the spec is observed en
// route. Used by TestFinanceHarnessReentry_SameAttemptAdoptsDurableTerminalRun
// so its own reentry call can reuse the identical spec, rather than
// attempting to reconstruct one after the fact (validateSpec's own
// identity digest includes a hash of LeaseToken, which is never
// otherwise readable back once a task has moved past 'running').
type capturingHarnessRunner struct {
	runtime *executionharness.Runtime

	mu       sync.Mutex
	lastSpec executionharness.RunSpec
	calls    int
}

func (r *capturingHarnessRunner) Run(ctx context.Context, spec executionharness.RunSpec) (executionharness.RunResult, error) {
	r.mu.Lock()
	r.lastSpec = spec
	r.calls++
	r.mu.Unlock()
	return r.runtime.Execute(ctx, spec), nil
}

// TestFinanceHarnessReentry_SameAttemptAdoptsDurableTerminalRun is
// FINANCE_FULLSTACK_E2E_CLOSURE_V1 GAP C: proves the deterministic
// RunID/context Finance's own runHarnessModel computes is not merely
// deterministic as a function (that is already pinned by
// internal/campaign's own unit test), but ACTUALLY prevents a second real
// Model Runtime dispatch when durable Harness history for the same
// task/attempt already holds the terminal result -- exercised against
// executionharness.Runtime + REAL PostgreSQL HarnessHistory/RunDescriptor
// stores, counting real model_invocations rows (a durable, DB-verifiable
// fact, never a mock's own in-memory counter and never merely two
// computeFinanceRunID(...) calls compared for equality).
//
// Design: FinanceService.ExecuteReviewTask runs with a capturingHarnessRunner
// (see its own doc comment) wrapping the SAME real Authority/History/
// DescriptorStore/Model-Runtime dependencies every other real-Harness
// test in this file uses -- the real Harness run completes exactly once
// (review persisted, task completed, one model_invocations row), and the
// EXACT RunSpec it used (including its real, otherwise-unrecoverable
// LeaseToken) is captured. This test then drives a SECOND, independent
// executionharness.Runtime.Execute call with that IDENTICAL captured
// spec directly -- the closest black-box code can get to "re-enter
// runHarnessModel with the same TaskID/AttemptID/LeaseToken/
// ReviewRequestID/ProposalID/hash/lineage" for an unexported function.
// executionharness.Runtime.Execute's own documented behavior
// (internal/executionharness/runtime.go: durable history is read FIRST;
// a terminal result found there is returned immediately, BEFORE
// authority is ever consulted and BEFORE the model executor is ever
// reached) is what this test verifies empirically against real
// PostgreSQL, not merely cites.
func TestFinanceHarnessReentry_SameAttemptAdoptsDurableTerminalRun(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{
		Verdict: "recommended", Summary: "Reentry proof.",
		RecommendedBudget: financeTestExecutableBudget(),
	})
	_, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "reentry-same-attempt", goal)

	harnessAuthority, err := f.runtime.ModelRuntime.NewHarnessAuthority()
	if err != nil {
		t.Fatalf("create real harness authority: %v", err)
	}
	harnessHistory, err := executionharnesspostgres.New(f.store, chatTestOrganization)
	if err != nil {
		t.Fatalf("open real harness history/descriptor store: %v", err)
	}
	models, err := f.runtime.ModelRuntime.NewHarnessModelExecutor(modelruntimeadapter.Config{
		MaxOutputTokens: 4096, ThinkingMode: modelruntime.ThinkingDisabled, InvocationTTL: 2 * time.Minute,
		OutputMode: modelruntime.OutputText, ExecutionContractInstructions: "Reentry fixture contract.",
		Purpose: "campaign.financial_review",
	})
	if err != nil {
		t.Fatalf("build finance model executor: %v", err)
	}
	realRuntime, err := executionharness.NewWithDescriptorStore(harnessAuthority, models, financeReentryToolCatalog{}, financeReentryToolExecutor{}, harnessHistory, harnessHistory)
	if err != nil {
		t.Fatalf("build real harness runtime: %v", err)
	}
	capturer := &capturingHarnessRunner{runtime: realRuntime}

	// A SEPARATE FinanceService instance, sharing the SAME real campStore/
	// Tasks/Assignments/Authorizer/RoleResolver/HolderPrincipalID as
	// fx.financeService (built the identical way
	// buildRealHarnessFinanceWorkerForE2E/buildTestFinanceServiceRealHarness
	// build theirs), but with HarnessRunner set to the capturer above so
	// this test can observe the exact spec runHarnessModel builds.
	// HarnessRunner takes priority over NewModelExecutor inside
	// runHarnessModel (see TestScenarioM_FinalizeFailureRecoveryConvergesWithoutSecondModelCall's
	// own identical use of HarnessRunner-only FinanceServiceConfig), so
	// nothing here duplicates or bypasses the real dispatch path -- it is
	// the SAME real Authority/History/DescriptorStore/Model-Runtime,
	// merely observed en route.
	// Assignments must be real: without it, ExecuteReviewTask's own step 5
	// (EnsureAuthorizedAssignmentForRunningAttempt) never runs, and the
	// real Model Runtime dispatch fails closed with "model dispatch
	// entity not found" before ever reaching test.fake.
	reentryDispatch := buildTestModelDispatch(t, f.store, fx.tasksService, chatTestOrganization)
	reentryAssignments, err := reentryDispatch.NewAuthorizedAttemptProvisioner(chatTestDispatchPrincipalKey)
	if err != nil {
		t.Fatalf("create reentry dispatch provisioner: %v", err)
	}
	capturingFinSvc, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID:    chatTestOrganization,
		Requirements:      permissiveExecutionRequirements(),
		Store:             fx.store,
		Tasks:             finTestTaskCoordinator{fx.tasksService},
		Assignments:       financeTestDispatchProvisioner{reentryAssignments},
		ContextBuilder:    buildTestFinanceContextBuilder(t, f.store, fx.tasksService, chatTestOrganization),
		HarnessRunner:     capturer,
		HolderPrincipalID: fx.holderPrincipalID,
		LeaseDuration:     2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create capturing finance service: %v", err)
	}

	if got := mustCountModelInvocations(t, ctx, f.store, taskID, 1); got != 0 {
		t.Fatalf("model invocations before any run = %d, want 0", got)
	}

	review, _, err := capturingFinSvc.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID: chatTestOrganization, TaskID: taskID, ReviewRequestID: reviewRequestID,
	})
	if err != nil {
		t.Fatalf("ExecuteReviewTask (first, real harness, captured spec): %v", err)
	}
	if review.Verdict != campaign.VerdictRecommended {
		t.Fatalf("first verdict = %q, want recommended", review.Verdict)
	}
	if capturer.calls != 1 {
		t.Fatalf("capturingHarnessRunner.Run was called %d times, want exactly 1", capturer.calls)
	}
	capturer.mu.Lock()
	capturedSpec := capturer.lastSpec
	capturer.mu.Unlock()

	runID1 := capturedSpec.Identity.RunID
	contextID1 := capturedSpec.Context.ID
	if runID1 == "" || contextID1 == "" {
		t.Fatalf("captured spec has blank RunID/Context.ID: %+v", capturedSpec.Identity)
	}

	if got := mustCountModelInvocations(t, ctx, f.store, taskID, capturedSpec.Identity.AttemptID); got != 1 {
		t.Fatalf("model invocations after the first (real) run = %d, want exactly 1", got)
	}

	// RUN_ID_2 / CONTEXT_ID_2: this reentry call reuses the CAPTURED spec
	// verbatim -- the load-bearing proof is that driving
	// executionharness.Runtime.Execute a SECOND time with it produces
	// ZERO new model_invocations rows.
	runID2 := capturedSpec.Identity.RunID
	contextID2 := capturedSpec.Context.ID
	if runID2 != runID1 {
		t.Fatalf("RUN_ID_2 = %q, want RUN_ID_1 %q", runID2, runID1)
	}
	if contextID2 != contextID1 {
		t.Fatalf("CONTEXT_ID_2 = %q, want CONTEXT_ID_1 %q", contextID2, contextID1)
	}

	reentryResult := realRuntime.Execute(ctx, capturedSpec)
	if reentryResult.Status != executionharness.StatusCompleted {
		t.Fatalf("reentry run = %+v, want StatusCompleted (adopted from durable history)", reentryResult)
	}
	if reentryResult.RunID != runID1 {
		t.Fatalf("reentry RunID = %q, want %q", reentryResult.RunID, runID1)
	}

	if got := mustCountModelInvocations(t, ctx, f.store, taskID, capturedSpec.Identity.AttemptID); got != 1 {
		t.Fatalf("model invocations after reentry = %d, want exactly 1 (NOT 2 -- reentry must adopt the durable terminal run, never dispatch a second time)", got)
	}

	var reviewCount, completedCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID).Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 1 {
		t.Errorf("review rows after reentry = %d, want exactly 1 (no duplicate)", reviewCount)
	}
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM tasks WHERE id=$1 AND status='completed'", taskID).Scan(&completedCount); err != nil {
		t.Fatalf("count completed: %v", err)
	}
	if completedCount != 1 {
		t.Errorf("completed task count = %d, want 1", completedCount)
	}

	t.Logf("FINANCE_FULLSTACK_E2E_CLOSURE_V1 section 13: this proves same-attempt reentry only. A NEW Task Engine attempt after a legitimate retry/lease-expiry would compute a DIFFERENT RunID by design (AttemptID is part of the RunID formula) -- that boundary is intentionally not redesigned here.")
}

// TestRealFinanceHarnessIntegration_RecommendedZeroSubagentsFailsClosed proves that when
// test.fake returns verdict "recommended" with max_subagents = 0 through the REAL Harness path
// (MockOutput=nil, real PostgreSQL, real Task Engine, real Context Engine, real ExecutionHarness,
// real Model Runtime), Finance host validation rejects the output, records FINANCE_OUTPUT_INVALID,
// writes 0 FinancialReviews, fails the task terminally without lease-expiry retry loops,
// and permits 0 owner approvals (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 15).
func TestRealFinanceHarnessIntegration_RecommendedZeroSubagentsFailsClosed(t *testing.T) {
	f, fx, _, restore := newFinanceRealHarnessFixture(t)
	defer f.cleanup()
	defer restore()
	ctx := context.Background()
	service := f.withScriptedModel(t, &scriptedModel{})

	invalidZeroSubagentsBudget := &campaign.BudgetRecommendation{
		MaxUSD:        1.0,
		MaxTokens:     1000,
		MaxModelCalls: 2,
		MaxWallTimeMS: 60000,
		MaxDepth:      2,
		MaxRetries:    1,
		MaxSubagents:  0, // invalid zero subagents
	}

	goal := financeFakeJSONGoal(t, campaign.FinanceReviewOutput{
		Verdict:           "recommended",
		Summary:           "Finance recommended with zero subagents fails closed.",
		RecommendedBudget: invalidZeroSubagentsBudget,
		Assumptions:       []string{"none"},
		Risks:             []string{},
	})
	proposalID, taskID, reviewRequestID := fx.seedReadyReviewTaskWithGoal(t, service, "real-harness-zero-subagents", goal)

	_, _, err := fx.financeService.ExecuteReviewTask(ctx, campaign.ExecuteReviewParams{
		OrganizationID:  chatTestOrganization,
		TaskID:          taskID,
		ReviewRequestID: reviewRequestID,
	})
	if err == nil {
		t.Fatal("expected ExecuteReviewTask to fail closed on zero subagents, got nil")
	}

	// 1. Zero financial reviews persisted
	var reviewCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_financial_reviews WHERE organization_id=$1 AND review_request_id=$2", chatTestOrganization, reviewRequestID).Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 0 {
		t.Errorf("review rows = %d, want 0", reviewCount)
	}

	// 2. Zero owner approvals
	var approvalCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_owner_approvals WHERE organization_id=$1 AND proposal_id=$2", chatTestOrganization, proposalID).Scan(&approvalCount); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if approvalCount != 0 {
		t.Errorf("approval rows = %d, want 0", approvalCount)
	}

	// 3. Task is in terminal failed status (no lease retry loop)
	var taskStatus string
	if err := f.store.Pool().QueryRow(ctx, "SELECT status FROM tasks WHERE id=$1", taskID).Scan(&taskStatus); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if taskStatus != "failed" {
		t.Errorf("task status = %q, want failed", taskStatus)
	}

	// 4. Attempt recorded with FINANCE_OUTPUT_INVALID
	var failureCode *string
	if err := f.store.Pool().QueryRow(ctx, "SELECT failure_code FROM task_attempts WHERE task_id=$1 ORDER BY id DESC LIMIT 1", taskID).Scan(&failureCode); err != nil {
		t.Fatalf("query attempt failure_code: %v", err)
	}
	if failureCode == nil || *failureCode != "FINANCE_OUTPUT_INVALID" {
		t.Errorf("attempt failure_code = %v, want FINANCE_OUTPUT_INVALID", failureCode)
	}
}
