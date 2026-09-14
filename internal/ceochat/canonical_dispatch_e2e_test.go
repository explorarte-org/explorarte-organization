//go:build integration

// GAP 2 of CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_CLOSURE_V1: the
// canonical, literal composition the original blocker
// (CEO_CONVERSATIONAL_REAL_PROVIDER_REHEARSAL_V1) actually failed at, all
// in one test, never split across "ceochat + scripted ModelExecutor" and
// "a different package + real Model Runtime":
//
//	ceochat.Service.Send()
//	    -> ClaimTaskByID -> StartAttempt
//	    -> EnsureAuthorizedAssignmentForRunningAttempt
//	    -> Context Build
//	    -> ExecutionHarness
//	    -> modelruntimeadapter.Adapter
//	    -> InvocationService.Create -> modeldispatch.ResolveActive -> quota consume
//	    -> DispatchService.Dispatch -> deterministic provider adapter (no network, no cost)
//
// This uses the REAL, UNMODIFIED docs/canonical for org/role/capability
// sync -- unlike internal/executive's own retry_failover_integration_test.go,
// which copies and edits model-routing.yaml/model-egress-policy.yaml in a
// temp directory. That path was tried here first and hits a real,
// intentional wall: internal/organization/registry's SynchronizeCanonical
// hardcodes productiveEgressAllowRules (deliberately excluding test.fake --
// "the productive surface contains only HTTP API adapters") and validates
// the WHOLE canonical bundle, including model-egress-policy.yaml, as one
// coherent unit; there is no way to make executive.ceo's canonical policy
// point at test.fake and still pass that sync. retry_failover's own doc
// comment on buildRetryFailoverCanonicalDir already flags this: it builds
// its routing and egress modifications into SEPARATE temp directories
// specifically because combining them fails this exact check, and it
// never routes its OWN "CEO" role through the real canonical empresa/ceo
// binding at all (it overrides GetRole via a fully separate fake
// OrganizationCatalog for Model Runtime).
//
// So instead: sync the REAL canonical documents normally (byte-identical
// to newChatFixture), then repoint EXACTLY empresa/ceo's existing
// role_model_binding at a new model_profile_versions row for provider
// "test.fake" (inserted directly, the same primitive
// internal/modeldispatch/postgres/integration_test.go's own
// insertBindingFixture already uses) and apply an ADDITIONAL model-egress
// policy version allowing test.fake (via modelegress.RegistryPlan, the
// same in-process primitive internal/modelruntime/postgres/integration_test.go's
// own fakeEgressPlan already uses -- never a YAML file). role-catalog.yaml
// itself is never touched: empresa/ceo's model_policy stays the real
// "executive.ceo" string; only what that ONE binding resolves to, in this
// disposable test database, changes.
package ceochat_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	ceochatbootstrap "github.com/Mireuz13/explorarte-organization/internal/ceochat/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egresspostgres "github.com/Mireuz13/explorarte-organization/internal/modelegress/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitybootstrap "github.com/Mireuz13/explorarte-organization/internal/modelidentity/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// writeCEOChatE2EIdentityKeyFile generates a throwaway ed25519 keypair and
// writes the private half to a PEM file inside t.TempDir() -- identical in
// shape to internal/modelruntime/postgres/integration_test.go's own
// writeExecutionIdentityKeyFile, duplicated here (not imported: that
// helper is unexported in a different package) rather than shared.
func writeCEOChatE2EIdentityKeyFile(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ceochat-e2e-execution-identity.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return privateKey, path
}

func ceochatE2EHexFixture(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// repointCEORoleBindingToTestFake mutates, IN PLACE, the one real
// model_profile_versions row empresa/ceo's real role_model_binding
// already points at (model_profile_versions has
// UNIQUE(organization_id, profile_id, organization_revision_id) -- a
// second version for the same profile at the same revision is not a row
// this schema allows.
//
// Mutating the real, current revision's own row in place (an earlier
// version of this fixture did exactly that) turned out to be unsafe in a
// much deeper way than that unique constraint alone: once a real dispatch
// happens, Model Runtime's OWN durable audit trail
// (model_invocations_version_fk, and immutable append-only tables like
// provider_wallet_events that reject DELETE outright by trigger) pins that
// exact version row in place permanently -- there is no way to restore it
// afterward without destroying audit history the rest of this system
// deliberately never allows destroying. Every OTHER ceochat integration
// test in this same package/process/database also reads the real, current
// revision (they all call registry.SynchronizeCanonical /
// modelbootstrap's own registry Sync against it -- there is only ever one
// current revision), so leaving it mutated broke every test that ran
// afterward with "partial model registry materialization detected".
//
// So instead: create a full SIBLING revision (organization_roles and
// model_profiles are both revision-INDEPENDENT tables -- neither carries
// an organization_revision_id column at all, so nothing needs duplicating
// there), with its own role_model_binding for empresa/ceo pointing at a
// brand-new model_profile_versions row for provider "test.fake", and
// temporarily point organizations.current_revision_id at it. Restoring
// afterward is then a single, always-safe UPDATE back to the real
// revision's ID -- organizations.current_revision_id carries no downstream
// FK of its own, so nothing can ever pin it in place. The sibling
// revision, and everything this test durably dispatched under it, is left
// behind once abandoned -- inert, since nothing else's current_revision_id
// ever points at it again, and this is a disposable integration database.
func repointCEORoleBindingToTestFake(t *testing.T, ctx context.Context, store *platformpostgres.Store, organizationID string, revisionID int64) (shadowRevisionID int64, restore func()) {
	t.Helper()
	var profileID string
	if err := store.Pool().QueryRow(ctx, `SELECT v.profile_id FROM model_profile_versions v JOIN role_model_bindings b ON b.model_profile_version_id=v.id WHERE b.organization_id=$1 AND b.organization_revision_id=$2 AND b.role_id='empresa/ceo'`, organizationID, revisionID).
		Scan(&profileID); err != nil {
		t.Fatalf("read empresa/ceo's real role_model_binding: %v", err)
	}
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO organization_registry_revisions(canonical_hash, previous_revision_id, status, schema_versions, document_hashes, counts, diff)
SELECT canonical_hash, id, status, schema_versions, document_hashes, counts, diff FROM organization_registry_revisions WHERE id=$1
RETURNING id`, revisionID).Scan(&shadowRevisionID); err != nil {
		t.Fatalf("create sibling organization_registry_revisions row: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO model_providers(organization_id,id,transport,adapter_status,dispatch_enabled,direct_http_forbidden,canonical_hash,organization_revision_id)
VALUES($1,'test.fake','fake_adapter','available',true,true,$2,$3)`, organizationID, ceochatE2EHexFixture("ceochat-e2e-provider"), shadowRevisionID); err != nil {
		t.Fatalf("insert test.fake model_providers row: %v", err)
	}
	// version_number is unique per (organization_id, profile_id) ACROSS
	// every revision, not just within one -- reusing version_number=1
	// collides with the real revision's own existing version for this
	// same profile_id even though organization_revision_id differs.
	var nextVersion int
	if err := store.Pool().QueryRow(ctx, `SELECT COALESCE(max(version_number),0)+1 FROM model_profile_versions WHERE organization_id=$1 AND profile_id=$2`, organizationID, profileID).Scan(&nextVersion); err != nil {
		t.Fatalf("compute next version_number for profile %q: %v", profileID, err)
	}
	var versionID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO model_profile_versions(organization_id,profile_id,version_number,organization_revision_id,canonical_document_hash,version_hash,provider_id,provider_model_id,transport,adapter_status,dispatch_enabled)
VALUES($1,$2,$3,$4,$5,$6,'test.fake','ceochat-e2e-fake','fake_adapter','available',true) RETURNING id`,
		organizationID, profileID, nextVersion, shadowRevisionID, ceochatE2EHexFixture("ceochat-e2e-doc"), ceochatE2EHexFixture("ceochat-e2e-version")).Scan(&versionID); err != nil {
		t.Fatalf("insert test.fake model_profile_versions row: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `INSERT INTO model_capability_snapshots(organization_id,model_profile_version_id,capabilities,capability_hash) VALUES($1,$2,'[]',$3)`,
		organizationID, versionID, ceochatE2EHexFixture("ceochat-e2e-caps")); err != nil {
		t.Fatalf("insert test.fake model_capability_snapshots row: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `INSERT INTO role_model_bindings(organization_id,organization_revision_id,role_id,policy_id,profile_id,model_profile_version_id,binding_hash,active) VALUES($1,$2,'empresa/ceo','executive.ceo',$3,$4,$5,true)`,
		organizationID, shadowRevisionID, profileID, versionID, ceochatE2EHexFixture("ceochat-e2e-binding")); err != nil {
		t.Fatalf("insert empresa/ceo role_model_binding for the sibling revision: %v", err)
	}
	if tag, err := store.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$1, updated_at=clock_timestamp() WHERE id=$2`, shadowRevisionID, organizationID); err != nil {
		t.Fatalf("point current_revision_id at the sibling revision: %v", err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("point current_revision_id at the sibling revision: affected %d rows, want 1", tag.RowsAffected())
	}
	// organization_roles.source_revision_id (a plain FK, no downstream
	// constraint keys off it) is checked for drift against the CURRENT
	// revision by role routing authority resolution -- empresa/ceo's row
	// still names the real revision, which the sibling revision above is
	// not. A single reversible UPDATE, restored alongside current_revision_id.
	var originalRoleSourceRevisionID int64
	if err := store.Pool().QueryRow(ctx, `SELECT source_revision_id FROM organization_roles WHERE organization_id=$1 AND id='empresa/ceo'`, organizationID).Scan(&originalRoleSourceRevisionID); err != nil {
		t.Fatalf("read empresa/ceo's real source_revision_id: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE organization_roles SET source_revision_id=$1 WHERE organization_id=$2 AND id='empresa/ceo'`, shadowRevisionID, organizationID); err != nil {
		t.Fatalf("point empresa/ceo's source_revision_id at the sibling revision: %v", err)
	}
	return shadowRevisionID, func() {
		if _, err := store.Pool().Exec(ctx, `UPDATE organization_roles SET source_revision_id=$1 WHERE organization_id=$2 AND id='empresa/ceo'`, originalRoleSourceRevisionID, organizationID); err != nil {
			t.Errorf("restore empresa/ceo's real source_revision_id: %v", err)
		}
		if tag, err := store.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$1, updated_at=clock_timestamp() WHERE id=$2`, revisionID, organizationID); err != nil {
			t.Errorf("restore current_revision_id to the real revision: %v", err)
		} else if tag.RowsAffected() != 1 {
			t.Errorf("restore current_revision_id to the real revision: affected %d rows, want 1", tag.RowsAffected())
		}
	}
}

// allowTestFakeEgress applies an ADDITIONAL model-egress policy version
// (modelegress.RegistryPlan, the same in-process primitive
// internal/modelruntime/postgres/integration_test.go's own fakeEgressPlan
// uses) permitting provider "test.fake" at every classification this
// fixture's context ever renders -- never a YAML edit, never touching
// internal/organization/registry's own sync/validation path at all.
func allowTestFakeEgress(t *testing.T, ctx context.Context, store *platformpostgres.Store, egressStore *egresspostgres.Store, organizationID string, revisionID int64, canonicalHash string) {
	t.Helper()
	// policy_version is unique per (organization_id, policy_id) across
	// every revision, exactly like model_profile_versions.version_number
	// above -- and in "all" mode, many other suites' own fixtures have
	// already advanced it well past any hardcoded small number by the
	// time this one runs.
	var nextPolicyVersion int
	if err := store.Pool().QueryRow(ctx, `SELECT COALESCE(max(policy_version),0)+1 FROM model_egress_policy_versions WHERE organization_id=$1 AND policy_id='model-egress'`, organizationID).Scan(&nextPolicyVersion); err != nil {
		t.Fatalf("compute next model-egress policy_version: %v", err)
	}
	plan := modelegress.RegistryPlan{
		OrganizationID: organizationID, OrganizationRevisionID: revisionID, CanonicalHash: canonicalHash,
		Policy: modelegress.CanonicalPolicy{
			SchemaVersion: "0.1.0", DocumentStatus: "test_fixture", PolicyID: "model-egress", PolicyVersion: nextPolicyVersion,
			DefaultAction: modelegress.EffectDeny, CanonicalHash: canonicalHash,
			HardDenies: []modelegress.HardDeny{
				{DataClassification: modelegress.ClassificationSecret, ReasonCode: "secret_egress_forbidden"},
				{DataClassification: modelegress.ClassificationClinical, ReasonCode: "clinical_egress_forbidden"},
			},
			Rules: []modelegress.Rule{
				{ProviderID: "test.fake", DataClassification: modelegress.ClassificationPublic, Effect: modelegress.EffectAllow, ReasonCode: "ceochat_e2e_public_allow"},
				{ProviderID: "test.fake", DataClassification: modelegress.ClassificationSanitized, Effect: modelegress.EffectAllow, ReasonCode: "ceochat_e2e_sanitized_allow"},
				{ProviderID: "test.fake", DataClassification: modelegress.ClassificationOrganizational, Effect: modelegress.EffectAllow, ReasonCode: "ceochat_e2e_organizational_allow"},
			},
		},
	}
	if _, err := egressStore.Apply(ctx, plan); err != nil {
		t.Fatalf("apply test.fake egress allow plan: %v", err)
	}
}

// ceochatE2EAdapter is the deterministic, in-process provider double this
// test's whole point rests on. It is turn-aware (unlike
// internal/modelruntime/adapter.Fake's content-marker branching, which is
// unconditional across every invocation of a run by design, for whatever
// existing test relies on that): it answers with a real tool request only
// on the first invocation of a run (no tool result anywhere in
// VisibleHistory yet), and answers with a final answer ONLY once it has
// genuinely observed that tool's own result in VisibleHistory -- proving
// the second invocation is conditioned on the first's real, executed
// outcome, not merely on invocation count.
type ceochatE2EAdapter struct {
	dispatchCalls int32
}

func (a *ceochatE2EAdapter) ProviderID() string { return "test.fake" }

func (a *ceochatE2EAdapter) Descriptor() modelruntime.AdapterDescriptor {
	return modelruntime.AdapterDescriptor{
		ProviderID: "test.fake", AdapterID: "ceochat-e2e-fake", AdapterVersion: 1,
		Transport: modelruntime.TransportFake, RequestSchemaVersion: "test.fake.request.v1",
		ResponseSchemaVersion: "test.fake.response.v1",
		EndpointFingerprint:   modelruntime.SHA256Bytes([]byte("ceochat-e2e-fake:endpoint")),
		CredentialRefHash:     modelruntime.SHA256Bytes([]byte("ceochat-e2e-fake:credential")),
	}
}

func (a *ceochatE2EAdapter) Preflight(ctx context.Context, request modelruntime.ProviderPreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ProviderID != "test.fake" || request.ProviderModelID == "" || request.Deadline.IsZero() {
		return modelruntime.ErrInvalidRequest
	}
	return nil
}

func (a *ceochatE2EAdapter) Dispatch(ctx context.Context, req modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	atomic.AddInt32(&a.dispatchCalls, 1)

	sawToolResult := false
	for _, message := range req.ModelInput.Envelope.VisibleHistory {
		if message.Role == modelruntime.ModelInputRoleTool && message.ToolName == ceochat.ToolTasksList && strings.TrimSpace(message.Content) != "" {
			sawToolResult = true
		}
	}

	response := modelruntime.RawResponse{
		ProviderRequestID: "ceochat-e2e-" + strconv.Itoa(int(atomic.LoadInt32(&a.dispatchCalls))),
		InputTokens:       int64(len(req.RenderedContext)/4 + 1), OutputTokens: 12, ProviderReported: false,
	}
	if !sawToolResult {
		response.ToolIntents = []modelruntime.RawToolIntent{{ID: "ceochat-e2e-call-1", Name: ceochat.ToolTasksList, Arguments: json.RawMessage(`{}`)}}
	} else {
		response.Content = []byte("ceochat-e2e final answer: tool result observed")
	}
	response.ProviderOutcome = modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		ProviderRequestID:     response.ProviderRequestID, HTTPStatus: 200,
		ResponseHash:          modelruntime.SHA256Bytes(response.Content),
		ResponseSchemaVersion: "test.fake.response.v1",
	}
	return response, nil
}

var _ modelruntime.ProviderAdapter = (*ceochatE2EAdapter)(nil)

// newCEOChatCanonicalE2EFixture mirrors newChatFixture's own migrate ->
// sync canonical registry -> register test dispatch principal -> sync
// model registry -> ceochatbootstrap.Open sequence exactly, against the
// REAL, unmodified docs/canonical -- then repoints empresa/ceo's one real
// role_model_binding at test.fake and allows test.fake in the egress
// evaluator (both in-process, both documented on their own functions
// above), and finally opens ceochatbootstrap with
// modelbootstrap.WithExtraAdapters(adapter) so that repointed policy
// actually resolves to something dispatchable.
func newCEOChatCanonicalE2EFixture(t *testing.T, adapter *ceochatE2EAdapter) (*ceochat.Service, *platformpostgres.Store, func()) {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	t.Setenv("ORG_MODEL_EXECUTION_PRINCIPAL_KEY", chatTestDispatchPrincipalKey)
	t.Setenv("ORG_MODEL_RUNTIME_ENABLED", "true")
	// DispatchService.Dispatch fails closed ("ORG_MODEL_EXECUTION_IDENTITY_ENABLED
	// is false") once an invocation has an identity policy pinned unless
	// this is explicitly enabled with a real key on file -- production
	// always has this configured; no prior ceochat test needed it because
	// none reached real DispatchService.Dispatch before this round.
	identityPrivateKey, identityKeyFile := writeCEOChatE2EIdentityKeyFile(t)
	t.Setenv("ORG_MODEL_EXECUTION_IDENTITY_ENABLED", "true")
	t.Setenv("ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE", identityKeyFile)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":           "test",
			"ORG_DATABASE_URL":          databaseURL,
			"ORG_DATABASE_MAX_CONNS":    "16",
			"ORG_DATABASE_MIN_CONNS":    "0",
			"ORG_CANONICAL_DIR":         "/src/docs/canonical",
			"ORG_CONTEXT_SOURCE_ROOT":   "/src",
			"ORG_TASKS_ORGANIZATION_ID": chatTestOrganization,
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "ceochat-canonical-e2e")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	// restoreCEORoleBinding is nil until repointCEORoleBindingToTestFake
	// succeeds; fail (below) must still call it if it is set by then, or a
	// setup failure AFTER that repoint (e.g. identity key registration)
	// would leave the shared organization revision corrupted for every
	// later ceochat test in this same process.
	var restoreCEORoleBinding func()
	fail := func(format string, args ...any) {
		if restoreCEORoleBinding != nil {
			restoreCEORoleBinding()
		}
		store.Close()
		cancel()
		t.Fatalf(format, args...)
	}
	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		fail("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		fail("migration runner: %v", err)
	}
	if _, err = runner.Up(ctx); err != nil {
		fail("migrate up: %v", err)
	}
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		fail("registry repository: %v", err)
	}
	loader, err := registry.NewLoader(cfg.Registry.CanonicalDir)
	if err != nil {
		fail("registry loader: %v", err)
	}
	registryService, err := registry.NewService(loader, registryRepo, chatTestOrganization, 30*time.Second)
	if err != nil {
		fail("registry service: %v", err)
	}
	if result, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || (!result.Applied && !result.NoOp) {
		fail("sync canonical registry: result=%+v err=%v", result, syncErr)
	}
	registerChatTestDispatchPrincipal(t, ctx, store, fail)
	modelRegistryRuntime, err := modelbootstrap.OpenRegistry(cfg, store)
	if err != nil {
		fail("open model registry for canonical e2e fixture: %v", err)
	}
	if sync, syncErr := modelRegistryRuntime.Registry.Sync(ctx, true, cfg.Tasks.OutboxMaxAttempts); syncErr != nil || (!sync.Applied && !sync.NoOp) {
		fail("sync model registry for canonical e2e fixture: result=%+v err=%v", sync, syncErr)
	}

	revision, err := registryRepo.GetCurrentRevision(ctx, chatTestOrganization)
	if err != nil || revision == nil {
		fail("read current organization revision: revision=%+v err=%v", revision, err)
	}
	var shadowRevisionID int64
	shadowRevisionID, restoreCEORoleBinding = repointCEORoleBindingToTestFake(t, ctx, store, chatTestOrganization, revision.ID)
	egressStore, err := egresspostgres.New(store)
	if err != nil {
		fail("open egress store for canonical e2e fixture: %v", err)
	}
	// model_egress_policy_versions has UNIQUE(organization_id, policy_id,
	// canonical_hash): the real document_hashes["model-egress-policy.yaml"]
	// value (copied verbatim into the sibling revision above) is a REAL
	// content hash some earlier suite's own sync has very likely already
	// registered a policy version under, with the REAL rules -- reusing it
	// here for fabricated test.fake-only rules collides ("policy hash
	// already exists as version N"). A fresh, disposable hash, written
	// into the sibling revision's own document_hashes so
	// egressStore.Apply's staleness check (which compares against
	// organizations.current_revision_id's document_hashes, already
	// pointed at the sibling revision) still matches, avoids that
	// collision entirely.
	fixtureEgressHash := ceochatE2EHexFixture("ceochat-e2e-egress-policy")
	if _, err = store.Pool().Exec(ctx, `UPDATE organization_registry_revisions SET document_hashes = jsonb_set(document_hashes, '{model-egress-policy.yaml}', to_jsonb($1::text)) WHERE id=$2`,
		fixtureEgressHash, shadowRevisionID); err != nil {
		fail("set sibling revision's egress document hash: %v", err)
	}
	allowTestFakeEgress(t, ctx, store, egressStore, chatTestOrganization, shadowRevisionID, fixtureEgressHash)
	// identitybootstrap.Open (called internally by ceochatbootstrap.Open ->
	// modelbootstrap.Open) only CONSTRUCTS the execution identity policy
	// service; it never syncs the canonical policy into the DB by itself
	// (internal/modelruntime/postgres/integration_test.go's own fixture
	// does this exact explicit step too). Without it, DispatchService's
	// "model execution identity policy not found" fires on the very first
	// real dispatch, before test.fake is ever reached.
	identityRuntime, err := identitybootstrap.Open(cfg, store)
	if err != nil {
		fail("open model identity runtime for canonical e2e fixture: %v", err)
	}
	if sync, syncErr := identityRuntime.Policy.Sync(ctx, true); syncErr != nil || (!sync.Applied && !sync.NoOp) {
		fail("sync model identity policy for canonical e2e fixture: result=%+v err=%v", sync, syncErr)
	}
	var dispatchPrincipalID int64
	if err = store.Pool().QueryRow(ctx, `SELECT id FROM model_execution_principals WHERE organization_id=$1 AND principal_key=$2`, chatTestOrganization, chatTestDispatchPrincipalKey).Scan(&dispatchPrincipalID); err != nil {
		fail("read registered dispatch principal ID: %v", err)
	}
	identityPublicKey := identityPrivateKey.Public().(ed25519.PublicKey)
	preparedIdentityKey := modelidentity.PreparedKey{
		OrganizationID: chatTestOrganization, ExecutionPrincipalID: dispatchPrincipalID,
		PublicKey: identityPublicKey, PublicKeyFingerprint: modelidentity.PublicKeyFingerprint(identityPublicKey),
		SecretRef: "file://ceochat-e2e/execution-identity-key-1", IdempotencyKey: "ceochat-e2e-identity-key",
		CreatedByRoleID: "empresa/human",
	}
	if preparedIdentityKey.RequestHash, err = modelidentity.KeyRequestHash(preparedIdentityKey); err != nil {
		fail("compute execution identity key request hash: %v", err)
	}
	if _, err = identityRuntime.Store.RegisterKey(ctx, preparedIdentityKey); err != nil {
		fail("register execution identity key: %v", err)
	}

	// CostBudgetGate (wired automatically inside modelbootstrap.Open) needs
	// a priced tier and a funded wallet for "test.fake" before Dispatch
	// will reserve cost and proceed -- the same primitive
	// internal/modelruntime/postgres/integration_test.go's own "cost and
	// budget reservation gates dispatch" test uses.
	pricingStore, err := modelpricingpostgres.New(store)
	if err != nil {
		fail("open pricing store for canonical e2e fixture: %v", err)
	}
	pricingService, err := modelpricing.NewService(pricingStore)
	if err != nil {
		fail("open pricing service for canonical e2e fixture: %v", err)
	}
	if _, err = pricingService.Upsert(ctx, modelpricing.PriceTier{
		ProviderID: "test.fake", ProviderModelID: "ceochat-e2e-fake", ContextTierName: "default",
		InputPriceNanosPerMillion: 1_000_000_000, OutputPriceNanosPerMillion: 2_000_000_000,
		BillingMode: modelpricing.BillingOnline, EffectiveAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		fail("seed test.fake price tier: %v", err)
	}
	walletStore, err := costledgerpostgres.New(store)
	if err != nil {
		fail("open wallet store for canonical e2e fixture: %v", err)
	}
	if _, err = walletStore.SetBalance(ctx, "test.fake", modelpricing.USDFromDollars(10), time.Now().UTC()); err != nil {
		fail("fund test.fake wallet: %v", err)
	}

	runtime, err := ceochatbootstrap.Open(cfg, store, modelbootstrap.WithExtraAdapters(adapter))
	if err != nil {
		fail("open ceochat runtime: %v", err)
	}
	return runtime.Service, store, func() { restoreCEORoleBinding(); store.Close(); cancel() }
}

// TestCEOChatCanonicalMultiInvocationDispatchComposition is
// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_CLOSURE_V1's GAP 2/3
// mandatory trajectory test.
func TestCEOChatCanonicalMultiInvocationDispatchComposition(t *testing.T) {
	adapter := &ceochatE2EAdapter{}
	service, store, cleanup := newCEOChatCanonicalE2EFixture(t, adapter)
	defer cleanup()
	ctx := context.Background()

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{
		ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "canonical-e2e-turn-1", Content: "¿Qué tareas están listas?",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	// The ORIGINAL blocker (CEO_CONVERSATIONAL_REAL_PROVIDER_REHEARSAL_V1):
	// InvocationService.Create -> modeldispatch.ResolveActive ->
	// modeldispatch.ErrNotFound, before any provider call, on every single
	// real invocation. A non-nil err or a non-Completed outcome here IS
	// that failure recurring.
	if result.Outcome != ceochat.RunOutcomeCompleted {
		var diagAttemptID int64
		_ = store.Pool().QueryRow(ctx, `SELECT id FROM task_attempts WHERE task_id=$1 ORDER BY ordinal LIMIT 1`, result.OwnerMessage.TaskID).Scan(&diagAttemptID)
		diagRows, diagErr := store.Pool().Query(ctx, `SELECT event_type, terminal_status, payload FROM execution_run_events WHERE task_id=$1 AND attempt_id=$2 ORDER BY sequence`, result.OwnerMessage.TaskID, diagAttemptID)
		if diagErr == nil {
			defer diagRows.Close()
			for diagRows.Next() {
				var eventType, terminalStatus string
				var payload []byte
				if scanErr := diagRows.Scan(&eventType, &terminalStatus, &payload); scanErr == nil {
					t.Logf("DIAGNOSTIC event=%s terminal=%s payload=%s", eventType, terminalStatus, payload)
				}
			}
		}
		t.Fatalf("outcome=%v, want Completed (a non-Completed outcome here is the original ErrNotFound-shaped blocker recurring)", result.Outcome)
	}
	if result.AssistantMessage == nil || !strings.Contains(result.AssistantMessage.Content, "final answer") {
		t.Fatalf("assistant message=%+v, want the deterministic final answer", result.AssistantMessage)
	}
	if result.TurnsUsed != 2 {
		t.Fatalf("turns_used=%d, want exactly 2 (tool request, then final answer)", result.TurnsUsed)
	}
	if result.ToolCallsUsed != 1 {
		t.Fatalf("tool_calls_used=%d, want exactly 1", result.ToolCallsUsed)
	}
	if calls := atomic.LoadInt32(&adapter.dispatchCalls); calls != 2 {
		t.Fatalf("provider adapter Dispatch calls=%d, want exactly 2 (proves the deterministic adapter -- not a scripted ModelExecutor -- actually ran the trajectory)", calls)
	}

	taskID := result.OwnerMessage.TaskID
	var attemptID int64
	if err = store.Pool().QueryRow(ctx, `SELECT id FROM task_attempts WHERE task_id=$1 ORDER BY ordinal LIMIT 1`, taskID).Scan(&attemptID); err != nil {
		t.Fatalf("read attempt for task %d: %v", taskID, err)
	}

	// Durable proof the REAL consumer (InvocationService.Create) resolved
	// and used the assignment -- not merely that a row exists.
	var invocationCount int
	if err = store.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM model_invocations WHERE task_id=$1 AND attempt_id=$2`, taskID, attemptID).Scan(&invocationCount); err != nil {
		t.Fatalf("count model_invocations: %v", err)
	}
	if invocationCount != 2 {
		t.Fatalf("model_invocations for task=%d attempt=%d: got %d, want exactly 2", taskID, attemptID, invocationCount)
	}

	var assignmentCount, maxInvocations, usedInvocations int
	if err = store.Pool().QueryRow(ctx, `SELECT COUNT(*), max(max_invocations), max(used_invocations) FROM model_dispatcher_assignments WHERE task_id=$1 AND attempt_id=$2`, taskID, attemptID).Scan(&assignmentCount, &maxInvocations, &usedInvocations); err != nil {
		t.Fatalf("read assignment for task %d attempt %d: %v", taskID, attemptID, err)
	}
	if assignmentCount != 1 {
		t.Fatalf("assignment rows for task=%d attempt=%d: got %d, want exactly 1 (same assignment consumed by both invocations)", taskID, attemptID, assignmentCount)
	}
	if maxInvocations != ceochat.MaxTurns {
		t.Fatalf("assignment.max_invocations=%d, want ceochat.MaxTurns=%d (never hardcoded)", maxInvocations, ceochat.MaxTurns)
	}
	if usedInvocations != 2 {
		t.Fatalf("assignment.used_invocations=%d, want exactly 2", usedInvocations)
	}

	// Confirm every model_invocations row for this task/attempt is pinned
	// to that SAME assignment (both invocations share it, not two
	// separate assignments coincidentally both counted above).
	var distinctAssignments int
	if err = store.Pool().QueryRow(ctx, `SELECT COUNT(DISTINCT dispatcher_assignment_id) FROM model_invocations WHERE task_id=$1 AND attempt_id=$2`, taskID, attemptID).Scan(&distinctAssignments); err != nil {
		t.Fatalf("count distinct assignments across invocations: %v", err)
	}
	if distinctAssignments != 1 {
		t.Fatalf("distinct dispatcher_assignment_id across the 2 invocations=%d, want exactly 1", distinctAssignments)
	}
}
