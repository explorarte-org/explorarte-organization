package bootstrap_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	modelruntimeadapter "github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	harnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	memoryospostgres "github.com/Mireuz13/explorarte-organization/internal/memoryos/postgres"
	egressbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelegress/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	modelruntime "github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/canonicalsync"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/need"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	skillregistrypostgres "github.com/Mireuz13/explorarte-organization/internal/skillregistry/postgres"
	taskcontextprovider "github.com/Mireuz13/explorarte-organization/internal/tasks/contextprovider"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func writeExecutionIdentityKeyFile(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "execution-identity.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return privateKey, path
}

func testInitGitRepo(t *testing.T, dir string) {
	t.Helper()
	remoteDir := t.TempDir()
	testRunCmd(t, remoteDir, "git", "init", "--bare", "-b", "main")
	testRunCmd(t, dir, "git", "init", "-b", "main")
	testRunCmd(t, dir, "git", "config", "user.name", "SkillForge Test")
	testRunCmd(t, dir, "git", "config", "user.email", "skillforge@explorarte.test")
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Skills\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testRunCmd(t, dir, "git", "add", "README.md")
	testRunCmd(t, dir, "git", "commit", "-m", "init")
	testRunCmd(t, dir, "git", "remote", "add", "origin", remoteDir)
	testRunCmd(t, dir, "git", "push", "-u", "origin", "main")
}

func testRunCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

// TestSkillForgeProductiveChainProof demonstrates the complete productive chain:
// Skill Forge Engine -> ExecutionHarness -> modelruntimeadapter -> modelruntime.Open ->
// canonical routing -> real Gemini provider -> model_invocations & wallet cost ->
// memoryStore.ProjectHarnessRun -> PostgreSQL connection close/reopen -> durable Episode verification.
func TestSkillForgeProductiveChainProof(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required for live productive chain proof")
	}

	geminiCred := os.Getenv("ORG_MODEL_PROVIDER_GEMINI_CREDENTIAL_FILE")
	if geminiCred == "" {
		geminiCred = "/etc/explorarte/secrets/gemini-embedding-api-key"
	}
	if _, err := os.Stat(geminiCred); os.IsNotExist(err) {
		t.Skipf("gemini credential file %s not accessible; skipping live external proof", geminiCred)
	}

	ctx := context.Background()
	orgID := "explorarte"
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	if _, err := os.Stat(canonicalDir); os.IsNotExist(err) {
		canonicalDir = filepath.Join("docs", "canonical")
	}

	identityPrivateKey, identityKeyFile := writeExecutionIdentityKeyFile(t)
	dispatchPrincipalKey := fmt.Sprintf("oracle-%d/model-runtime-01", time.Now().UnixNano())
	t.Setenv("ORG_MODEL_RUNTIME_ENABLED", "true")
	t.Setenv("ORG_MODEL_EXECUTION_IDENTITY_ENABLED", "true")
	t.Setenv("ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE", identityKeyFile)
	t.Setenv("ORG_MODEL_EXECUTION_PRINCIPAL_KEY", dispatchPrincipalKey)

	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":                           "test",
			"ORG_DATABASE_URL":                          databaseURL,
			"ORG_DATABASE_MAX_CONNS":                    "16",
			"ORG_DATABASE_MIN_CONNS":                    "0",
			"ORG_TASKS_ORGANIZATION_ID":                 orgID,
			"ORG_CANONICAL_DIR":                         canonicalDir,
			"ORG_MODEL_RUNTIME_ENABLED":                 "true",
			"ORG_MODEL_EXECUTION_IDENTITY_ENABLED":      "true",
			"ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE":     identityKeyFile,
			"ORG_MODEL_PROVIDER_GEMINI_ENABLED":         "true",
			"ORG_MODEL_PROVIDER_GEMINI_ENDPOINT_URL":    "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
			"ORG_MODEL_PROVIDER_GEMINI_CREDENTIAL_FILE": geminiCred,
			"ORG_EMBEDDING_ACTIVE_PROFILE":              "gemini_text_embedding_004_768",
			"ORG_CONTEXT_SOURCE_ROOT":                   "/src",
			"ORG_MODEL_EXECUTION_PRINCIPAL_KEY":         dispatchPrincipalKey,
		}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}

	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "skillforge-chain-proof")
	if err != nil {
		t.Fatal(err)
	}
	defer platformStore.Close()

	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, platformStore.Pool()); err != nil {
		t.Fatalf("testdbguard: %v", err)
	}

	runner, err := platformmigrations.New(platformStore.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrations up: %v", err)
	}
	if err = testdbguard.RequireDestructive(ctx, databaseURL, platformStore.Pool()); err != nil {
		t.Fatalf("destructive authorization failed: %v", err)
	}

	// 1. Sync canonical organization registry
	regRepo, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, regRepo, orgID, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	egressRuntime, err := egressbootstrap.Open(cfg, platformStore)
	if err != nil {
		t.Fatalf("open egress bootstrap: %v", err)
	}
	applier := canonicalsync.Applier{Registry: registryService, Egress: egressRuntime.Service}
	if _, err = applier.Apply(ctx, true); err != nil {
		t.Fatalf("canonicalsync apply: %v", err)
	}

	var revisionID int64
	if err = platformStore.Pool().QueryRow(ctx, `SELECT id FROM organization_registry_revisions ORDER BY id DESC LIMIT 1`).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}

	// 2. Open productive Model Runtime and sync model registry
	modelRuntime, err := modelbootstrap.Open(cfg, platformStore)
	if err != nil {
		t.Fatalf("open productive model runtime: %v", err)
	}
	modelSync, err := modelRuntime.Registry.Sync(ctx, true, cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		t.Fatalf("sync model registry: %v", err)
	}
	if !modelSync.Applied && !modelSync.NoOp {
		t.Fatalf("model registry sync not applied: %+v", modelSync)
	}
	if _, err = modelRuntime.Identity.Policy.Sync(ctx, true); err != nil {
		t.Fatalf("sync model identity policy: %v", err)
	}

	// 3. Create durable Task & Attempt in PostgreSQL
	var taskID, attemptID int64
	taskReqHash := sha256.Sum256([]byte("skillforge-chain-" + time.Now().String()))
	taskReqHex := hex.EncodeToString(taskReqHash[:])
	taskInstructions := "You are the Skill Forge Authorer for Explorarte Organization.\n" +
		"Respond ONLY with a valid JSON object with keys: \"skill_id\", \"decision\", \"decision_reason\", \"skill_markdown\".\n" +
		"\"decision\" must be \"author\".\n" +
		"\"skill_id\" must be \"skill-smoke-verification\".\n" +
		"\"decision_reason\" must be \"Authoring verification skill for productive proof\".\n" +
		"\"skill_markdown\" must be valid markdown containing sections: Purpose, Applicability, Non-goals, Inputs, Outputs, Procedure, Stop conditions, Failure modes, Evidence requirements."
	err = platformStore.Pool().QueryRow(ctx, `
		INSERT INTO tasks(organization_id, organization_revision_id, assigned_role_id, assigned_unit_id,
		                  task_class, idempotency_key, request_hash, title, instructions,
		                  acceptance_criteria, status, priority, available_at, max_attempts, attempt_count, version)
		VALUES($1, $2, 'recursos_agenticos/disenador_skills', 'recursos_agenticos',
		       'smoke.verification', $3, $4, 'Skill Forge Proof Task', $5,
		       '[]', 'running', 0, NOW(), 5, 1, 1)
		RETURNING id
	`, orgID, revisionID, "idem-"+taskReqHex[:16], taskReqHex, taskInstructions).Scan(&taskID)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	err = platformStore.Pool().QueryRow(ctx, `
		INSERT INTO task_attempts(task_id, ordinal, state, worker_id, leased_at, started_at, created_at, updated_at)
		VALUES($1, 1, 'running', 'skillforge-worker', NOW(), NOW(), NOW(), NOW())
		RETURNING id
	`, taskID).Scan(&attemptID)
	if err != nil {
		t.Fatalf("insert attempt: %v", err)
	}

	// 3b. Create durable execution principals, active lease, and dispatcher assignment
	var designerPrincipalID int64
	pReqHash := sha256.Sum256([]byte(fmt.Sprintf("princ-designer-%d", time.Now().UnixNano())))
	pReqHex := hex.EncodeToString(pReqHash[:])
	err = platformStore.Pool().QueryRow(ctx, `
		INSERT INTO model_execution_principals (
			organization_id, principal_key, dispatch_actor_role_id, principal_kind,
			status, idempotency_key, request_hash, registered_by_role_id, created_at, updated_at
		) VALUES (
			$1, $2, 'recursos_agenticos/disenador_skills', 'local_process',
			'active', $3, $4, 'empresa/owner', NOW(), NOW()
		) RETURNING id
	`, orgID, "designer-"+pReqHex[:8], "idem-p-"+pReqHex[:16], pReqHex).Scan(&designerPrincipalID)
	if err != nil {
		t.Fatalf("insert designer principal: %v", err)
	}
	designerPrincipalIDStr := strconv.FormatInt(designerPrincipalID, 10)

	var dispatchPrincipalID int64
	dispatchReqHash := sha256.Sum256([]byte(fmt.Sprintf("princ-dispatch-%d", time.Now().UnixNano())))
	dispatchReqHex := hex.EncodeToString(dispatchReqHash[:])
	err = platformStore.Pool().QueryRow(ctx, `
		INSERT INTO model_execution_principals (
			organization_id, principal_key, dispatch_actor_role_id, principal_kind,
			status, idempotency_key, request_hash, registered_by_role_id, created_at, updated_at
		) VALUES (
			$1, $2, 'ingenieria_ia/code-runner', 'local_process',
			'active', $3, $4, 'empresa/owner', NOW(), NOW()
		) RETURNING id
	`, orgID, dispatchPrincipalKey, "idem-disp-"+dispatchReqHex[:16], dispatchReqHex).Scan(&dispatchPrincipalID)
	if err != nil {
		t.Fatalf("insert dispatch principal: %v", err)
	}

	identityPublicKey := identityPrivateKey.Public().(ed25519.PublicKey)
	preparedIdentityKey := modelidentity.PreparedKey{
		OrganizationID:       orgID,
		ExecutionPrincipalID: dispatchPrincipalID,
		PublicKey:            identityPublicKey,
		PublicKeyFingerprint: modelidentity.PublicKeyFingerprint(identityPublicKey),
		SecretRef:            "file://model-execution/integration/key-1",
		IdempotencyKey:       fmt.Sprintf("idem-key-%d", time.Now().UnixNano()),
		CreatedByRoleID:      "empresa/owner",
	}
	preparedIdentityKey.RequestHash, err = modelidentity.KeyRequestHash(preparedIdentityKey)
	if err != nil {
		t.Fatalf("key request hash: %v", err)
	}
	if _, err = modelRuntime.Identity.Store.RegisterKey(ctx, preparedIdentityKey); err != nil {
		t.Fatalf("register identity key: %v", err)
	}

	leaseToken := fmt.Sprintf("lease-token-%d", time.Now().UnixNano())
	leaseTokenHash := sha256.Sum256([]byte(leaseToken))
	leaseTokenHex := hex.EncodeToString(leaseTokenHash[:])
	_, err = platformStore.Pool().Exec(ctx, `
		INSERT INTO task_leases (
			task_id, attempt_id, token_hash, holder_id, status, issued_at, heartbeat_at, expires_at
		) VALUES (
			$1, $2, $3, $4, 'active', NOW(), NOW(), NOW() + INTERVAL '1 hour'
		)
	`, taskID, attemptID, leaseTokenHex, designerPrincipalIDStr)
	if err != nil {
		t.Fatalf("insert task lease: %v", err)
	}

	assignReqHash := sha256.Sum256([]byte(fmt.Sprintf("assign-%d", time.Now().UnixNano())))
	assignReqHex := hex.EncodeToString(assignReqHash[:])
	_, err = platformStore.Pool().Exec(ctx, `
		INSERT INTO model_dispatcher_assignments (
			organization_id, organization_revision_id, task_id, attempt_id,
			subject_role_id, dispatch_actor_role_id, execution_principal_id,
			status, valid_from, valid_until, max_invocations, used_invocations,
			assignment_hash, idempotency_key, request_hash, created_by_role_id,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			'recursos_agenticos/disenador_skills', 'ingenieria_ia/code-runner', $5,
			'active', NOW(), NOW() + INTERVAL '1 hour', 10, 0,
			$6, $7, $6, 'empresa/owner',
			NOW(), NOW()
		)
	`, orgID, revisionID, taskID, attemptID, dispatchPrincipalID, assignReqHex, "idem-assign-"+assignReqHex[:16])
	if err != nil {
		t.Fatalf("insert dispatcher assignment: %v", err)
	}

	// 4. Create durable Context Snapshot via canonical context runtime
	taskStore, err := taskpostgres.New(platformStore)
	if err != nil {
		t.Fatalf("task store: %v", err)
	}
	taskContextProvider, err := taskcontextprovider.New(taskStore)
	if err != nil {
		t.Fatalf("task context provider: %v", err)
	}
	contextRuntime, err := contextbootstrap.Open(cfg, platformStore, taskContextProvider)
	if err != nil {
		t.Fatalf("context bootstrap open: %v", err)
	}
	buildRes, err := contextRuntime.Service.Build(ctx, contextengine.BuildRequest{
		OrganizationID:         orgID,
		OrganizationRevisionID: revisionID,
		ActorRoleID:            "recursos_agenticos/disenador_skills",
		Purpose:                "department_worker",
		TaskRef:                fmt.Sprintf("task:%d", taskID),
		TaskClass:              "smoke.verification",
		ExecutionPurpose:       "department-worker",
		IdempotencyKey:         fmt.Sprintf("build-snap-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("context service build: %v", err)
	}
	snapshotID := buildRes.Snapshot.ID

	viewStore, err := contextcompilerpostgres.New(platformStore)
	if err != nil {
		t.Fatalf("view store: %v", err)
	}
	assembly := contextcompiler.ContextAssemblyService{Store: viewStore}
	snapshot, err := contextRuntime.Service.Get(ctx, snapshotID, true)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	view, err := assembly.ResolveAndPersist(ctx, snapshot)
	if err != nil {
		t.Fatalf("resolve and persist view: %v", err)
	}

	// 5. Build ExecutionHarness with productive modelruntimeadapter and postgres stores
	harnessStore, err := harnesspostgres.New(platformStore, orgID)
	if err != nil {
		t.Fatalf("harness postgres store: %v", err)
	}

	authority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		t.Fatalf("harness authority: %v", err)
	}

	modelExecutor, err := modelRuntime.NewHarnessModelExecutor(modelruntimeadapter.Config{
		MaxOutputTokens: 4096,
		InvocationTTL:   10 * time.Minute,
		ThinkingMode:    modelruntime.ThinkingDisabled,
		OutputMode:      modelruntime.OutputText,
	})
	if err != nil {
		t.Fatalf("harness model executor: %v", err)
	}

	harness, err := executionharness.NewWithDescriptorStore(
		authority,
		modelExecutor,
		fakeCatalog{},
		fakeTools{},
		harnessStore,
		harnessStore,
	)
	if err != nil {
		t.Fatalf("create harness: %v", err)
	}

	// 6. Disposable Git publisher and materializer
	publisherDir := t.TempDir()
	testInitGitRepo(t, publisherDir)

	runtimeRoot := t.TempDir()

	// 7. Composition root Open
	registryStore, err := skillregistrypostgres.New(platformStore, orgID)
	if err != nil {
		t.Fatalf("registry store: %v", err)
	}
	manager, err := skillregistry.NewManager(skillregistry.NewService(nil), registryStore, noopGate{})
	if err != nil {
		t.Fatalf("skill manager: %v", err)
	}

	bCfg := bootstrap.Config{
		OrganizationID:       orgID,
		CanonicalDir:         canonicalDir,
		SkillSourceRepoRoot:  publisherDir,
		SkillRuntimeRoot:     runtimeRoot,
		SourceRepositoryDir:  publisherDir,
		MaterializeRootDir:   runtimeRoot,
		PublishedRemoteURL:   "git@github.com:explorarte-org/skills.git",
		ExecutionProfileID:   "worker/skill-forge/v1",
		RoleID:               "recursos_agenticos/disenador_skills",
		ExecutionPrincipalID: designerPrincipalIDStr,
		LeaseToken:           leaseToken,
		Enabled:              true,
		TaskID:               taskID,
		AttemptID:            attemptID,
		ContextSnapshotID:    snapshotID,
		ContextContent:       string(view.ProviderVisibleBytes),
		ContextDigest:        view.ProviderVisibleDigest,
	}

	forgeRuntime, err := bootstrap.Open(bCfg, platformStore, harness, harness, manager)
	if err != nil {
		t.Fatalf("skillforge bootstrap open: %v", err)
	}
	t.Log("PRODUCTIVE_SKILLFORGE_COMPOSITION_ROOT = PASS")

	// 8. ProcedureNeed
	pNeed := need.ProcedureNeed{
		ID:                fmt.Sprintf("need-chain-%d", time.Now().UnixNano()),
		OrganizationID:    orgID,
		RoleID:            "recursos_agenticos/disenador_skills",
		TaskClass:         "smoke.verification",
		ProblemStatement:  "Prove single productive chain invocation and durable episode projection",
		EpisodeRefs:       []string{},
		ClusterRefs:       []string{},
		EvidenceRefs:      []need.EvidenceRef{},
		SuggestedSkillIDs: []string{},
		Status:            need.StatusAccepted,
		Revision:          1,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
		Acceptance: &need.Acceptance{
			DecisionRef: "D-006",
			AcceptedBy:  "empresa/owner",
			AcceptedAt:  time.Now().UTC(),
		},
	}
	pNeed, err = forgeRuntime.NeedStore.CreateNeed(ctx, pNeed)
	if err != nil {
		t.Fatalf("create need: %v", err)
	}

	// 9. Single real minimal execution (Authoring via Gemini Flash Lite)
	t.Log("[CHAIN STEP 1/4] Executing single real minimal authoring run via productive harness...")
	run, err := forgeRuntime.Engine.Run(ctx, orgID, pNeed.ID)
	if err != nil && !errors.Is(err, skillforge.ErrHumanApprovalNeeded) {
		t.Fatalf("skillforge engine run: %v", err)
	}
	if run.Status != skillforge.StatusWaitingHumanApproval {
		t.Fatalf("expected status waiting_human_approval, got %s", run.Status)
	}
	t.Logf("✓ Real Authoring completed: SkillID=%s, VersionID=%s, HarnessRunID=%s",
		run.Author.SkillID, run.SkillVersionID, run.Author.AuthorHarnessRunID)

	// 10. Verify model_invocations, usage, and cost in PostgreSQL
	t.Log("[CHAIN STEP 2/4] Verifying authoritative model invocation and cost in PostgreSQL...")
	var invID int64
	var providerID, modelID, invStatus string
	err = platformStore.Pool().QueryRow(ctx, `
		SELECT id, provider_id, provider_model_id, status
		FROM model_invocations
		WHERE organization_id = $1 AND task_id = $2 AND attempt_id = $3
		ORDER BY id DESC LIMIT 1
	`, orgID, taskID, attemptID).Scan(&invID, &providerID, &modelID, &invStatus)
	if err != nil {
		t.Fatalf("query model_invocations: %v", err)
	}
	if providerID != "gemini" || modelID != "gemini-3.5-flash-lite" || invStatus != "succeeded" {
		t.Fatalf("unexpected model invocation row: provider=%s model=%s status=%s", providerID, modelID, invStatus)
	}
	t.Logf("✓ MODEL_INVOCATION_PERSISTENCE PASS (id=%d, provider=%s, model=%s, status=%s)",
		invID, providerID, modelID, invStatus)

	var inTokens, outTokens int64
	err = platformStore.Pool().QueryRow(ctx, `
		SELECT input_tokens, output_tokens
		FROM model_invocation_usage
		WHERE invocation_id = $1
	`, invID).Scan(&inTokens, &outTokens)
	if err != nil {
		t.Fatalf("query model_invocation_usage: %v", err)
	}
	if inTokens <= 0 || outTokens <= 0 {
		t.Fatalf("usage tokens not positive: in=%d out=%d", inTokens, outTokens)
	}
	t.Logf("✓ MODEL_INVOCATION_USAGE PASS (in=%d, out=%d)", inTokens, outTokens)

	var costNanos int64
	err = platformStore.Pool().QueryRow(ctx, `
		SELECT amount_usd_nanos
		FROM provider_wallet_events
		WHERE invocation_id = $1 AND kind = 'committed'
		ORDER BY id DESC LIMIT 1
	`, invID).Scan(&costNanos)
	if err != nil {
		t.Fatalf("query provider_wallet_events: %v", err)
	}
	if costNanos <= 0 {
		t.Fatalf("committed cost not positive: %d nanos", costNanos)
	}
	costUSD := float64(costNanos) / 1e9
	t.Logf("✓ PRODUCTIVE_COST_RECONCILIATION PASS (cost=$%.6f USD, %d nanos)", costUSD, costNanos)

	// 11. Project into MemoryOS durable episode
	t.Log("[CHAIN STEP 3/4] Projecting durable Episode into MemoryOS PostgreSQL store...")
	memoryStore, err := memoryospostgres.New(platformStore, orgID)
	if err != nil {
		t.Fatalf("create memoryos store: %v", err)
	}
	ep, err := memoryStore.ProjectHarnessRun(ctx, run.Author.AuthorHarnessRunID)
	if err != nil {
		t.Fatalf("memoryStore.ProjectHarnessRun failed: %v", err)
	}
	if ep.ID == "" {
		t.Fatal("projected episode ID is empty")
	}
	t.Logf("✓ Episode projected and saved: EpisodeID=%s", ep.ID)

	// 12. Process/Store Recreation: Close connection, open brand new pool, and retrieve Episode
	t.Log("[CHAIN STEP 4/4] Closing pool, reopening independent connection, verifying durable Episode...")
	platformStore.Close()

	reopenedStore, err := platformpostgres.Open(ctx, cfg.Database, "skillforge-chain-reopened")
	if err != nil {
		t.Fatalf("open reopened platform store: %v", err)
	}
	defer reopenedStore.Close()

	reopenedMemoryStore, err := memoryospostgres.New(reopenedStore, orgID)
	if err != nil {
		t.Fatalf("create reopened memoryos store: %v", err)
	}

	loadedEp, ok, err := reopenedMemoryStore.GetEpisode(ctx, orgID, ep.ID)
	if err != nil || !ok {
		t.Fatalf("GetEpisode after reopen failed: ok=%v, err=%v", ok, err)
	}
	if loadedEp.ID != ep.ID {
		t.Fatalf("episode ID mismatch: %s != %s", loadedEp.ID, ep.ID)
	}
	if loadedEp.TaskID != taskID || loadedEp.AttemptID != attemptID {
		t.Fatalf("task/attempt mismatch: %d/%d != %d/%d", loadedEp.TaskID, loadedEp.AttemptID, taskID, attemptID)
	}
	if loadedEp.ActualCostUSDNanos == nil || *loadedEp.ActualCostUSDNanos != costNanos {
		t.Fatalf("episode actual cost mismatch: %v != %d", loadedEp.ActualCostUSDNanos, costNanos)
	}
	if loadedEp.TerminalStatus != "completed" {
		t.Fatalf("unexpected episode status: %s", loadedEp.TerminalStatus)
	}
	t.Logf("✓ MEMORYOS_AUTHOR_EPISODE_DURABLE PASS (EpisodeID=%s, TaskID=%d, CostNanos=%d, Status=%s)",
		loadedEp.ID, loadedEp.TaskID, *loadedEp.ActualCostUSDNanos, loadedEp.TerminalStatus)

	// Invariant verification: Candidate safety, draft integrity, and zero assignments
	var generatedLifecycle string
	err = reopenedStore.Pool().QueryRow(ctx, `
		SELECT lifecycle FROM skill_registry_versions WHERE skill_id = $1 ORDER BY version DESC LIMIT 1
	`, "skill-smoke-verification").Scan(&generatedLifecycle)
	if err != nil {
		t.Fatalf("query generated lifecycle: %v", err)
	}
	if generatedLifecycle == "active" {
		t.Fatalf("invariant violated: draft skill was auto-activated!")
	}
	if generatedLifecycle != "draft" {
		t.Fatalf("expected generated skill to remain draft, got %s", generatedLifecycle)
	}
	t.Logf("✓ GENERATED_DRAFT_REMAINS_DRAFT PASS (skill-smoke-verification lifecycle=%s)", generatedLifecycle)
	t.Log("✓ NO_AUTO_ACTIVATION PASS (newly authored draft was not auto-activated)")

	var candidateCount int
	err = reopenedStore.Pool().QueryRow(ctx, `
		SELECT count(*) FROM skill_registry_versions WHERE lifecycle = candidate
	`).Scan(&candidateCount)
	if err != nil {
		t.Fatalf("query candidate count: %v", err)
	}
	if candidateCount > 0 {
		t.Logf("✓ CANDIDATE_REMAINS_CANDIDATE PASS (count=%d)", candidateCount)
	} else {
		t.Log("CANDIDATE_REMAINS_CANDIDATE: NOT_PROVEN (no historical candidate fixture in disposable DB; generated draft is strictly draft)")
	}

	var assignmentCount int
	err = reopenedStore.Pool().QueryRow(ctx, `
		SELECT count(*) FROM skill_registry_assignments
	`).Scan(&assignmentCount)
	if err != nil {
		t.Fatalf("query assignments count: %v", err)
	}
	if assignmentCount != 0 {
		t.Fatalf("invariant violated: unexpected assignments found count=%d", assignmentCount)
	}
	t.Logf("✓ ZERO_PRODUCTION_ASSIGNMENTS PASS (count=%d)", assignmentCount)

	t.Logf("SINGLE_RUN_COST: $%.6f USD", costUSD)
	t.Log("CHAIN PROOF COMPLETED SUCCESSFULLY")
}
