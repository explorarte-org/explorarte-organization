//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	harnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	memorybootstrap "github.com/Mireuz13/explorarte-organization/internal/memory/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/consolidation"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	memoryospostgres "github.com/Mireuz13/explorarte-organization/internal/memoryos/postgres"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	ragbootstrap "github.com/Mireuz13/explorarte-organization/internal/rag/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	testOrganization = "explorarte"
	testRole         = "ingenieria_ia/qa"
)

type modelBinding struct {
	ProfileID        string
	ProfileVersionID int64
	ProviderID       string
	ProviderModelID  string
}

type integrationFixture struct {
	ctx           context.Context
	cfg           config.Config
	platformStore *platformpostgres.Store
	memoryStore   *memoryospostgres.Store
	harnessStore  *harnesspostgres.Store
	revisionID    int64
	binding       modelBinding
	cleanup       func()
}

func loadModelBinding(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, roleID string) modelBinding {
	t.Helper()
	var binding modelBinding
	if err := store.Pool().QueryRow(ctx, `
		SELECT b.profile_id, b.model_profile_version_id, v.provider_id, v.provider_model_id
		FROM role_model_bindings b
		JOIN model_profile_versions v
		  ON v.id = b.model_profile_version_id
		 AND v.organization_id = b.organization_id
		 AND v.profile_id = b.profile_id
		WHERE b.organization_id = $1 AND b.organization_revision_id = $2 AND b.role_id = $3 AND b.active
	`, testOrganization, revisionID, roleID).Scan(&binding.ProfileID, &binding.ProfileVersionID, &binding.ProviderID, &binding.ProviderModelID); err != nil {
		t.Fatalf("load model binding for %s: %v", roleID, err)
	}
	return binding
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":              "test",
			"ORG_DATABASE_URL":             databaseURL,
			"ORG_DATABASE_MAX_CONNS":       "16",
			"ORG_DATABASE_MIN_CONNS":       "0",
			"ORG_TASKS_ORGANIZATION_ID":    testOrganization,
			"ORG_CANONICAL_DIR":            canonicalDir,
			"ORG_EMBEDDING_ACTIVE_PROFILE": "gemini_text_embedding_004_768",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "memoryos-integration-test")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	fail := func(format string, args ...any) {
		platformStore.Close()
		cancel()
		t.Fatalf(format, args...)
	}

	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, platformStore.Pool()); err != nil {
		fail("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(platformStore.Pool(), rootmigrations.Files)
	if err != nil {
		fail("migration runner: %v", err)
	}
	if _, err = runner.Up(ctx); err != nil {
		fail("migrate up: %v", err)
	}
	if err = testdbguard.RequireDestructive(ctx, databaseURL, platformStore.Pool()); err != nil {
		fail("refusing destructive truncate: %v", err)
	}

	if _, err = platformStore.Pool().Exec(ctx, `
		TRUNCATE memoryos_clusters, memoryos_episodes, memoryos_completion_observations,
		         execution_run_descriptors, execution_run_events,
		         provider_wallet_events, model_invocation_usage, model_invocations,
		         decision_graph_runs, execution_context_views, context_segments, context_snapshots,
		         role_model_bindings, model_capability_snapshots, model_profile_versions, model_profiles, model_providers,
		         outbox_events, task_dead_letters, task_events, task_leases, task_attempts,
		         task_evidence, task_requirements, task_dependencies, tasks, organization_reporting_lines,
		         organization_registry_revision_documents, organization_roles, organizational_units,
		         organizations, organization_registry_revisions, audit_events RESTART IDENTITY CASCADE
	`); err != nil {
		fail("reset schema: %v", err)
	}

	registryRepo, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		fail("registry repo: %v", err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		fail("registry loader: %v", err)
	}
	registryService, err := registry.NewService(loader, registryRepo, testOrganization, 30*time.Second)
	if err != nil {
		fail("registry service: %v", err)
	}
	if result, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || !result.Applied {
		fail("sync canonical: result=%+v err=%v", result, syncErr)
	}

	var revisionID int64
	if err = platformStore.Pool().QueryRow(ctx, `SELECT id FROM organization_registry_revisions ORDER BY id DESC LIMIT 1`).Scan(&revisionID); err != nil {
		fail("read revision: %v", err)
	}

	modelRuntime, err := modelbootstrap.OpenRegistry(cfg, platformStore)
	if err != nil {
		fail("open model registry: %v", err)
	}
	modelSync, err := modelRuntime.Registry.Sync(ctx, true, cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		fail("sync model registry: %v", err)
	}
	if !modelSync.Applied && !modelSync.NoOp {
		fail("model registry did not synchronize: %+v", modelSync)
	}
	binding := loadModelBinding(t, ctx, platformStore, revisionID, testRole)

	memStore, err := memoryospostgres.New(platformStore, testOrganization)
	if err != nil {
		fail("create memoryos store: %v", err)
	}
	harnessStore, err := harnesspostgres.New(platformStore, testOrganization)
	if err != nil {
		fail("create harness store: %v", err)
	}

	return &integrationFixture{
		ctx:           ctx,
		cfg:           cfg,
		platformStore: platformStore,
		memoryStore:   memStore,
		harnessStore:  harnessStore,
		revisionID:    revisionID,
		binding:       binding,
		cleanup: func() {
			platformStore.Close()
			cancel()
		},
	}
}

func (f *integrationFixture) createTaskAndAttempt(t *testing.T, taskClass, title string) (int64, int64) {
	t.Helper()
	var taskID, attemptID int64
	reqHash := sha256.Sum256([]byte(title + time.Now().String()))
	reqHashHex := hex.EncodeToString(reqHash[:])
	err := f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO tasks(organization_id, organization_revision_id, assigned_role_id, assigned_unit_id,
		                  task_class, idempotency_key, request_hash, title, instructions,
		                  acceptance_criteria, status, priority, available_at, max_attempts, attempt_count, version)
		VALUES($1, $2, $3, 'ingenieria_ia', $4, $5, $6, $7, 'test instructions', '[]', 'running', 0, NOW(), 5, 1, 1)
		RETURNING id
	`, testOrganization, f.revisionID, testRole, taskClass, "idem-"+reqHashHex[:16], reqHashHex, title).Scan(&taskID)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	err = f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO task_attempts(task_id, ordinal, state, worker_id, leased_at, started_at, created_at, updated_at)
		VALUES($1, 1, 'running', 'worker-1', NOW(), NOW(), NOW(), NOW())
		RETURNING id
	`, taskID).Scan(&attemptID)
	if err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	return taskID, attemptID
}

func (f *integrationFixture) createContextSnapshot(t *testing.T, taskID, attemptID int64) (int64, string, int64) {
	t.Helper()
	var snapshotID, viewID int64
	content := fmt.Sprintf("context content %d-%d-%d", taskID, attemptID, time.Now().UnixNano())
	digestBytes := sha256.Sum256([]byte(content))
	digestHex := hex.EncodeToString(digestBytes[:])
	now := time.Now().UTC()

	err := f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO context_snapshots (
			organization_id, organization_revision_id, actor_role_id, purpose, task_ref,
			idempotency_key, request_hash, precedence_hash, canonical_bundle_hash, rendered_hash,
			status, version, segment_count, included_segment_count, omitted_segment_count, total_bytes, created_at
		) VALUES ($1, $2, $3, 'test purpose', $4, $5, $6, $6, $6, $6, 'ready', 1, 1, 1, 0, 100, $7)
		RETURNING id
	`, testOrganization, f.revisionID, testRole, fmt.Sprintf("task:%d", taskID), fmt.Sprintf("snap-idem-%s", digestHex[:16]), digestHex, now).Scan(&snapshotID)
	if err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}

	// Insert approved skill segment
	skillHashBytes := sha256.Sum256([]byte("skill content"))
	skillHashHex := hex.EncodeToString(skillHashBytes[:])
	_, err = f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO context_segments(
			snapshot_id, organization_id, ordinal, render_ordinal, authority_priority,
			authority_tier, source_kind, source_reference, source_version, instruction_class,
			trust_class, data_class, may_grant_capabilities, included, content_hash, byte_count, content, created_at
		) VALUES (
			$1, $2, 1, 1, 4,
			'matched_approved_skills', 'approved_skill', 'skill-qa', 'v1.0.0', 'approved_procedure',
			'approved', 'organizational', false, true, $3, 13, 'skill content'::bytea, NOW()
		)
	`, snapshotID, testOrganization, skillHashHex)
	if err != nil {
		t.Fatalf("insert context segment: %v", err)
	}

	// Insert execution context view
	err = f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO execution_context_views(
			organization_id, context_snapshot_id, context_profile_id, context_profile_version,
			fell_back_to_canonical, fallback_reason, selection_kind, selector_algorithm_version,
			provider_render_version, stable_prefix_hash, stable_prefix_bytes, dynamic_suffix_hash, dynamic_suffix_bytes,
			authority_order_hash, compiled_content_hash, segment_diffs,
			provider_visible_bytes, provider_visible_digest, provider_visible_byte_count, created_at
		) VALUES (
			$1, $2, 'profile-qa', 'v1',
			false, '', 'task_class', 'v1',
			'v1', '', 10, '', 8,
			$3, $3, '[]'::jsonb,
			'visible view bytes'::bytea, $3, 18, NOW()
		)
		RETURNING id
	`, testOrganization, snapshotID, digestHex).Scan(&viewID)
	if err != nil {
		t.Fatalf("insert context view: %v", err)
	}

	return snapshotID, content, viewID
}

func (f *integrationFixture) createDescriptor(t *testing.T, runID string, taskID, attemptID, snapshotID int64, contextContent, profileID string) episode.RunDescriptor {
	t.Helper()
	digestBytes := sha256.Sum256([]byte(contextContent))
	snapshotDigest := hex.EncodeToString(digestBytes[:])
	spec := executionharness.RunSpec{
		Identity: executionharness.RunIdentity{
			RunID: runID, OrganizationID: testOrganization, TaskID: taskID,
			AttemptID: attemptID, RoleID: testRole, ExecutionPrincipalID: "principal-1",
			CorrelationID: runID + ":corr", CausationID: runID + ":cause",
		},
		LeaseToken: "test-lease-token",
		Context: executionharness.InitialContext{
			ID: fmt.Sprintf("%d", snapshotID), Version: "v1", Digest: snapshotDigest, Content: contextContent,
		},
		Tools: []executionharness.ToolDefinition{
			{Name: "search", Description: "search tool", InputSchema: []byte(`{"type":"object"}`)},
		},
		Policy: executionharness.RunPolicy{
			MaxTurns: 3, MaxToolCalls: 1, ExecutionProfileID: profileID, ModelPolicyRef: "policy/qa", BuildRef: "build/v1",
		},
	}
	desc, err := executionharness.BuildRunDescriptor(spec)
	if err != nil {
		t.Fatalf("build descriptor: %v", err)
	}
	if err = f.harnessStore.EnsureRunDescriptor(f.ctx, desc); err != nil {
		t.Fatalf("ensure descriptor: %v", err)
	}

	var d episode.RunDescriptor
	d.RunID = desc.RunID
	d.OrganizationID = desc.OrganizationID
	d.TaskID = desc.TaskID
	d.AttemptID = desc.AttemptID
	d.RoleID = desc.RoleID
	d.ExecutionPrincipalID = desc.ExecutionPrincipalID
	d.ContextID = desc.ContextID
	d.ContextVersion = desc.ContextVersion
	d.ContextDigest = desc.ContextDigest
	d.ExecutionProfileID = desc.ExecutionProfileID
	d.ModelPolicyRef = desc.ModelPolicyRef
	d.BuildRef = desc.BuildRef
	d.MaxTurns = desc.MaxTurns
	d.MaxToolCalls = desc.MaxToolCalls
	d.IdentityDigest = desc.IdentityDigest
	for _, tool := range desc.FrozenTools {
		d.FrozenTools = append(d.FrozenTools, episode.FrozenToolRef{Name: tool.Name, DefinitionDigest: tool.DefinitionDigest})
	}
	return d
}

func (f *integrationFixture) createHarnessEvents(t *testing.T, runID string, taskID, attemptID int64, termStatus string) {
	t.Helper()
	now := time.Now().UTC()
	events := []struct {
		seq        int
		evType     string
		termStatus string
		payload    string
	}{
		{1, "run_started", "", `{"run_id":"` + runID + `"}`},
		{2, "model_request_prepared", "", `{"invocation_ref":"inv-1"}`},
		{3, "model_response_received", "", `{"invocation_ref":"inv-1"}`},
		{4, "run_completed", termStatus, `{"terminal_status":"` + termStatus + `"}`},
	}

	for _, ev := range events {
		_, err := f.platformStore.Pool().Exec(f.ctx, `
			INSERT INTO execution_run_events(organization_id, run_id, sequence, task_id, attempt_id, event_type, correlation_id, causation_id, terminal_status, payload, recorded_at)
			VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)
		`, testOrganization, runID, ev.seq, taskID, attemptID, ev.evType, runID+":corr", runID+":cause", ev.termStatus, ev.payload, now.Add(time.Duration(ev.seq)*10*time.Millisecond))
		if err != nil {
			t.Fatalf("insert event %d: %v", ev.seq, err)
		}
	}
}

func (f *integrationFixture) createModelInvocation(t *testing.T, taskID, attemptID, snapshotID int64, binding modelBinding) int64 {
	t.Helper()
	var invID, dispatchID int64
	now := time.Now().UTC()
	reqHashBytes := sha256.Sum256([]byte(fmt.Sprintf("inv-%d-%d-%d-%s", taskID, attemptID, time.Now().UnixNano(), binding.ProviderID)))
	reqHashHex := hex.EncodeToString(reqHashBytes[:])
	idemKey := fmt.Sprintf("idem-inv-%s", reqHashHex[:16])

	err := f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO model_invocations(
			organization_id, organization_revision_id, task_id, attempt_id, dispatch_actor_role_id, subject_role_id,
			context_snapshot_id, purpose, model_profile_id, model_profile_version_id, provider_id, provider_model_id,
			required_capabilities, output_mode, max_output_tokens, thinking_mode, idempotency_key, request_hash, status,
			deadline, created_at, updated_at, terminal_at
		) VALUES (
			$1, $2, $3, $4, 'ingenieria_ia/qa', $5,
			$6, 'memoryos integration invocation', $7, $8, $9, $10,
			'[]'::jsonb, 'json', 1000, 'opaque', $11, $12, 'succeeded',
			NOW() + INTERVAL '1 hour', $13, $13, $13
		)
		RETURNING id
	`, testOrganization, f.revisionID, taskID, attemptID, testRole, snapshotID, binding.ProfileID, binding.ProfileVersionID, binding.ProviderID, binding.ProviderModelID, idemKey, reqHashHex, now).Scan(&invID)
	if err != nil {
		t.Fatalf("insert invocation: %v", err)
	}

	claimHash := sha256.Sum256([]byte("claim-token-" + reqHashHex))
	claimHex := hex.EncodeToString(claimHash[:])
	idemHash := sha256.Sum256([]byte("idem-hash-" + reqHashHex))
	idemHashHex := hex.EncodeToString(idemHash[:])

	// Dispatch attempt for usage FK
	err = f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO model_dispatch_attempts(
			invocation_id, attempt_number, status, claim_token_hash, claimed_by,
			claimed_at, claim_expires_at, send_started_at, response_received_at,
			provider_idempotency_key_hash, retry_safety, finished_at
		) VALUES ($1, 1, 'completed', $2, 'integration-worker', $3, $4, $3, $3, $5, 'not_retryable', $3)
		RETURNING id
	`, invID, claimHex, now, now.Add(20*time.Minute), idemHashHex).Scan(&dispatchID)
	if err != nil {
		t.Fatalf("insert dispatch attempt: %v", err)
	}

	// Model usage
	_, err = f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO model_invocation_usage(
			invocation_id, dispatch_attempt_id, input_tokens, output_tokens, total_tokens,
			provider_reported, created_at
		) VALUES (
			$1, $2, 100, 50, 150, true, NOW()
		)
	`, invID, dispatchID)
	if err != nil {
		t.Fatalf("insert usage: %v", err)
	}

	// Ensure provider wallet exists
	_, err = f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO provider_wallets(provider_id, balance_usd_nanos, updated_at)
		VALUES($1, 1000000000, NOW())
		ON CONFLICT (provider_id) DO NOTHING
	`, binding.ProviderID)
	if err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}

	// Wallet event
	_, err = f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO provider_wallet_events(
			provider_id, invocation_id, kind, amount_usd_nanos, cost_provenance, financial_outcome, created_at
		) VALUES (
			$1, $2, 'committed', 2500000, 'actual_provider_reported', 'actual', NOW()
		)
	`, binding.ProviderID, invID)
	if err != nil {
		t.Fatalf("insert wallet event: %v", err)
	}

	return invID
}

func (f *integrationFixture) createDecisionGraphRun(t *testing.T, taskID, attemptID int64, status string) int64 {
	t.Helper()
	var runID int64
	hashBytes := sha256.Sum256([]byte("policy" + time.Now().String()))
	hashHex := hex.EncodeToString(hashBytes[:])
	idem := fmt.Sprintf("idem-dg-%d-%s", taskID, hashHex[:12])

	err := f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO decision_graph_runs(
			organization_id, task_id, attempt_id, status, reasoning_policy_schema_version,
			reasoning_policy_hash, idempotency_key, max_nodes, max_depth, max_parallel_nodes,
			max_model_calls, max_input_tokens, max_output_tokens, max_replans, max_verifications,
			max_wall_time_ms, deadline, created_by, created_at, updated_at, terminal_at
		) VALUES (
			$1, $2, $3, $4, 'v1',
			$5, $6, 10, 5, 2,
			10, 100000, 10000, 2, 2,
			60000, NOW() + INTERVAL '1 hour', $7, NOW(), NOW(), NOW()
		)
		RETURNING id
	`, testOrganization, taskID, attemptID, status, hashHex, idem, testRole).Scan(&runID)
	if err != nil {
		t.Fatalf("insert decision graph run: %v", err)
	}
	return runID
}

func TestEpisodeProjectionE2E(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", "E2E Projection Task")
	snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)
	runID := "harness-run-e2e-1"
	f.createDescriptor(t, runID, taskID, attemptID, snapshotID, contextContent, "profile/e2e")
	f.createHarnessEvents(t, runID, taskID, attemptID, "completed")
	f.createModelInvocation(t, taskID, attemptID, snapshotID, f.binding)
	decisionRunID := f.createDecisionGraphRun(t, taskID, attemptID, "succeeded")

	// Host completion observation
	now := time.Now().UTC()
	obs, err := episode.NewCompletionObservation(testOrganization, taskID, attemptID, "pass", now, []episode.ObligationObservation{
		{
			Key:             "qa-check-e2e",
			Kind:            "acceptance_criteria",
			Label:           "verified",
			VerifierRef:     "verifier/qa",
			VerifierVersion: "v1.0.0",
		},
	})
	if err != nil {
		t.Fatalf("new completion observation: %v", err)
	}
	if err = f.memoryStore.RecordCompletionObservation(f.ctx, obs); err != nil {
		t.Fatalf("record completion observation: %v", err)
	}

	// Execute ProjectHarnessRun
	ep, err := f.memoryStore.ProjectHarnessRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("ProjectHarnessRun: %v", err)
	}

	// Assertions on projected Episode
	if ep.TaskClass != "qa.verification" {
		t.Errorf("Expected TaskClass=qa.verification, got %s", ep.TaskClass)
	}
	if ep.HarnessRunID != runID {
		t.Errorf("Expected HarnessRunID=%s, got %s", runID, ep.HarnessRunID)
	}
	if ep.DecisionRunID == nil || *ep.DecisionRunID != decisionRunID {
		t.Errorf("Expected DecisionRunID=%d, got %v", decisionRunID, ep.DecisionRunID)
	}
	if ep.ExecutionProfileID != "profile/e2e" {
		t.Errorf("Expected ExecutionProfileID=profile/e2e, got %s", ep.ExecutionProfileID)
	}
	if len(ep.Skills) != 1 || ep.Skills[0].SkillID != "skill-qa" || !ep.Skills[0].Included {
		t.Errorf("Expected 1 included skill 'skill-qa', got %+v", ep.Skills)
	}
	if len(ep.Invocations) != 1 || ep.Invocations[0].ProviderID != f.binding.ProviderID || ep.Invocations[0].ProviderModelID != f.binding.ProviderModelID {
		t.Errorf("Expected %s %s invocation, got %+v", f.binding.ProviderID, f.binding.ProviderModelID, ep.Invocations)
	}
	if ep.ActualCostUSDNanos == nil || *ep.ActualCostUSDNanos != 2500000 {
		t.Errorf("Expected actual cost 2500000 nanos, got %v", ep.ActualCostUSDNanos)
	}
	if ep.Verification == nil || len(ep.Verification.Obligations) != 1 || ep.Verification.Obligations[0].Key != "qa-check-e2e" {
		t.Errorf("Expected obligation 'qa-check-e2e', got %+v", ep.Verification)
	}
	if ep.TerminalStatus != "completed" {
		t.Errorf("Expected terminal status completed, got %s", ep.TerminalStatus)
	}

	// Verify persistence in PostgreSQL
	loaded, ok, err := f.memoryStore.GetEpisode(f.ctx, testOrganization, ep.ID)
	if err != nil || !ok {
		t.Fatalf("GetEpisode from DB: ok=%v err=%v", ok, err)
	}
	if loaded.CanonicalDigest != ep.CanonicalDigest {
		t.Errorf("Canonical digest mismatch: loaded=%s, projected=%s", loaded.CanonicalDigest, ep.CanonicalDigest)
	}
}

func TestMultipleRunsPerAttemptRemainSeparateEpisodes(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", "Multi-Run Attempt Task")
	snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)

	runPlan := "harness-run-plan-1"
	runExec := "harness-run-exec-1"

	f.createDescriptor(t, runPlan, taskID, attemptID, snapshotID, contextContent, "profile/planner")
	f.createDescriptor(t, runExec, taskID, attemptID, snapshotID, contextContent, "profile/executor")

	f.createHarnessEvents(t, runPlan, taskID, attemptID, "completed")
	f.createHarnessEvents(t, runExec, taskID, attemptID, "completed")

	epPlan, err := f.memoryStore.ProjectHarnessRun(f.ctx, runPlan)
	if err != nil {
		t.Fatalf("Project plan run: %v", err)
	}
	epExec, err := f.memoryStore.ProjectHarnessRun(f.ctx, runExec)
	if err != nil {
		t.Fatalf("Project exec run: %v", err)
	}

	if epPlan.ID == epExec.ID {
		t.Fatalf("Episodes must have distinct IDs")
	}
	if epPlan.TaskID != epExec.TaskID || epPlan.AttemptID != epExec.AttemptID {
		t.Fatalf("Episodes must belong to the same TaskID and AttemptID")
	}
	if epPlan.HarnessRunID != runPlan || epExec.HarnessRunID != runExec {
		t.Fatalf("Episodes must retain their distinct HarnessRunIDs")
	}
	if epPlan.ExecutionProfileID != "profile/planner" || epExec.ExecutionProfileID != "profile/executor" {
		t.Fatalf("Episodes must retain their distinct execution profiles")
	}
}

func TestMixedModelBindingFailsClosedForAttribution(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", "Mixed Model Task")
	snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)
	runID := "harness-run-mixed-1"

	f.createDescriptor(t, runID, taskID, attemptID, snapshotID, contextContent, "profile/standard")
	f.createHarnessEvents(t, runID, taskID, attemptID, "completed")

	// Two invocations: first with default binding
	f.createModelInvocation(t, taskID, attemptID, snapshotID, f.binding)

	// Create a second distinct provider and profile version for mixed binding
	var binding2 modelBinding
	binding2.ProviderID = "test.second_provider"
	binding2.ProviderModelID = "model-second-v1"
	binding2.ProfileID = "profile-second"

	_, err := f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO model_providers(organization_id, id, organization_revision_id, transport, adapter_status, dispatch_enabled, direct_http_forbidden, canonical_hash, created_at)
		VALUES($1, $2, $3, 'fake_adapter', 'available', true, true, repeat('b', 64), NOW())
		ON CONFLICT (organization_id, id, organization_revision_id) DO NOTHING
	`, testOrganization, binding2.ProviderID, f.revisionID)
	if err != nil {
		t.Fatalf("insert second provider: %v", err)
	}

	_, err = f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO model_profiles(organization_id, id, policy_id, created_at)
		VALUES($1, $2, 'policy-second', NOW())
		ON CONFLICT (organization_id, id) DO NOTHING
	`, testOrganization, binding2.ProfileID)
	if err != nil {
		t.Fatalf("insert second profile: %v", err)
	}

	err = f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO model_profile_versions(
			organization_id, profile_id, version_number, organization_revision_id,
			canonical_document_hash, version_hash, provider_id, provider_model_id,
			transport, adapter_status, dispatch_enabled, created_at
		) VALUES (
			$1, $2, 1, $3,
			repeat('c', 64), repeat('d', 64), $4, $5,
			'fake_adapter', 'available', true, NOW()
		)
		RETURNING id
	`, testOrganization, binding2.ProfileID, f.revisionID, binding2.ProviderID, binding2.ProviderModelID).Scan(&binding2.ProfileVersionID)
	if err != nil {
		t.Fatalf("insert second profile version: %v", err)
	}

	f.createModelInvocation(t, taskID, attemptID, snapshotID, binding2)

	ep, err := f.memoryStore.ProjectHarnessRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("Project mixed run: %v", err)
	}

	if ep.BindingMode != episode.BindingModeMixed {
		t.Fatalf("Expected BindingModeMixed, got %s", ep.BindingMode)
	}
	if len(ep.Invocations) != 2 {
		t.Fatalf("Expected 2 invocations, got %d", len(ep.Invocations))
	}
}

func TestRecurrentCorrectionConsolidationIntegration(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	// 3 separate tasks and attempts, each failing on the same obligation
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", fmt.Sprintf("Correction Recurrence Task %d", i+1))
		snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)
		runID := fmt.Sprintf("harness-run-corr-%d", i+1)

		f.createDescriptor(t, runID, taskID, attemptID, snapshotID, contextContent, "profile/standard")
		f.createHarnessEvents(t, runID, taskID, attemptID, "completed")
		f.createModelInvocation(t, taskID, attemptID, snapshotID, f.binding)
		decisionID := f.createDecisionGraphRun(t, taskID, attemptID, "failed")

		obs, err := episode.NewCompletionObservation(testOrganization, taskID, attemptID, "fail", now.Add(time.Duration(i)*time.Hour), []episode.ObligationObservation{
			{
				Key:             "req-security-token",
				Kind:            "task_requirement",
				Label:           consolidation.VerificationContradicted,
				VerifierRef:     "verifier/security",
				VerifierVersion: "v1.0.0",
				EvidenceDigest:  strings.Repeat("a", 64),
				EvidenceRefs:    []string{fmt.Sprintf("decisiongraph:run:%d", decisionID)},
			},
		})
		if err != nil {
			t.Fatalf("create observation %d: %v", i+1, err)
		}
		if err = f.memoryStore.RecordCompletionObservation(f.ctx, obs); err != nil {
			t.Fatalf("record observation %d: %v", i+1, err)
		}

		if _, err = f.memoryStore.ProjectHarnessRun(f.ctx, runID); err != nil {
			t.Fatalf("project run %d: %v", i+1, err)
		}
	}

	// Open real memory runtime to verify candidate proposal through memory.Manager
	memRuntime, err := memorybootstrap.Open(f.cfg, f.platformStore)
	if err != nil {
		t.Fatalf("open memory runtime: %v", err)
	}
	ragRuntime, err := ragbootstrap.Open(f.cfg, f.platformStore)
	if err != nil {
		t.Fatalf("open rag runtime: %v", err)
	}

	consolidationCfg := consolidation.DefaultConfig()
	consolidationCfg.MinCorrectiveRecurrence = 3

	service, err := consolidation.NewService(
		f.memoryStore, f.memoryStore,
		ragRuntime.Manager, memRuntime.Manager,
		consolidationCfg,
	)
	if err != nil {
		t.Fatalf("new consolidation service: %v", err)
	}

	from := now.Add(-2 * time.Hour)
	to := now.Add(24 * time.Hour)
	result, err := service.Consolidate(f.ctx, testOrganization, from, to)
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	if result.CorrectiveClusters != 1 {
		t.Fatalf("Expected 1 corrective cluster, got %d (failures=%+v)", result.CorrectiveClusters, result.Failures)
	}
	if result.CorrectiveCandidates != 1 {
		t.Fatalf("Expected 1 corrective candidate, got %d", result.CorrectiveCandidates)
	}

	// Verify cluster is stored durably in PostgreSQL
	clusters, err := f.memoryStore.ListClusters(f.ctx, testOrganization, "corrective", 10)
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("Expected 1 durable cluster in DB, got %d", len(clusters))
	}
	cluster := clusters[0]
	if cluster.ObligationKey != "req-security-token" || cluster.ObligationKind != "task_requirement" {
		t.Errorf("Cluster obligation mismatch: key=%s kind=%s", cluster.ObligationKey, cluster.ObligationKind)
	}
	if cluster.FailCount != 3 {
		t.Errorf("Expected fail_count=3, got %d", cluster.FailCount)
	}

	// Verify idempotency on second consolidation pass
	resultRerun, err := service.Consolidate(f.ctx, testOrganization, from, to)
	if err != nil {
		t.Fatalf("Consolidate rerun: %v", err)
	}
	if resultRerun.CorrectiveCandidates != 0 {
		t.Fatalf("Expected 0 new candidates on rerun, got %d", resultRerun.CorrectiveCandidates)
	}
	if resultRerun.CorrectiveReused != 1 {
		t.Fatalf("Expected 1 candidate reused on rerun, got %d", resultRerun.CorrectiveReused)
	}
}

func TestCleanPassProducesNoCorrectiveCandidate(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", fmt.Sprintf("Clean Pass Task %d", i+1))
		snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)
		runID := fmt.Sprintf("harness-run-clean-%d", i+1)

		f.createDescriptor(t, runID, taskID, attemptID, snapshotID, contextContent, "profile/standard")
		f.createHarnessEvents(t, runID, taskID, attemptID, "completed")
		f.createModelInvocation(t, taskID, attemptID, snapshotID, f.binding)
		f.createDecisionGraphRun(t, taskID, attemptID, "succeeded")

		obs, err := episode.NewCompletionObservation(testOrganization, taskID, attemptID, "pass", now.Add(time.Duration(i)*time.Hour), []episode.ObligationObservation{
			{
				Key:             "req-healthy",
				Kind:            "task_requirement",
				Label:           "verified",
				VerifierRef:     "verifier/qa",
				VerifierVersion: "v1.0.0",
			},
		})
		if err != nil {
			t.Fatalf("create observation: %v", err)
		}
		if err = f.memoryStore.RecordCompletionObservation(f.ctx, obs); err != nil {
			t.Fatalf("record observation: %v", err)
		}
		if _, err = f.memoryStore.ProjectHarnessRun(f.ctx, runID); err != nil {
			t.Fatalf("project clean run: %v", err)
		}
	}

	memRuntime, err := memorybootstrap.Open(f.cfg, f.platformStore)
	if err != nil {
		t.Fatalf("open memory runtime: %v", err)
	}

	consolidationCfg := consolidation.DefaultConfig()
	service, err := consolidation.NewService(
		f.memoryStore, f.memoryStore,
		nil, memRuntime.Manager,
		consolidationCfg,
	)
	if err != nil {
		t.Fatalf("new consolidation service: %v", err)
	}

	result, err := service.Consolidate(f.ctx, testOrganization, now.Add(-2*time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate clean: %v", err)
	}
	if result.CorrectiveCandidates != 0 {
		t.Fatalf("Expected 0 corrective candidates for clean pass runs, got %d", result.CorrectiveCandidates)
	}
}

func TestFailureHandling(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	// 1. Non-existent run descriptor -> fails with error
	_, err := f.memoryStore.ProjectHarnessRun(f.ctx, "non-existent-run")
	if err == nil {
		t.Fatalf("Expected error for non-existent run descriptor, got nil")
	}

	// 2. Events incomplete (no terminal event) -> projected as incomplete
	taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", "Incomplete Run Task")
	snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)
	runID := "harness-run-incomplete-1"

	f.createDescriptor(t, runID, taskID, attemptID, snapshotID, contextContent, "profile/standard")

	// Insert only run_started event, missing run_completed
	_, err = f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO execution_run_events(organization_id, run_id, sequence, task_id, attempt_id, event_type, correlation_id, causation_id, terminal_status, payload, recorded_at)
		VALUES($1, $2, 1, $3, $4, 'run_started', 'corr-1', 'cause-1', '', '{"run_id":"`+runID+`"}'::jsonb, NOW())
	`, testOrganization, runID, taskID, attemptID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	epIncomp, err := f.memoryStore.ProjectHarnessRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("Project incomplete run: %v", err)
	}
	if epIncomp.Status != episode.EpisodeStatusIncomplete {
		t.Fatalf("Expected status incomplete, got %s", epIncomp.Status)
	}
	if !epIncomp.Observability.Incomplete {
		t.Fatalf("Expected Observability.Incomplete = true")
	}

	// 3. Duplicate projection -> idempotent reuse
	epReused, err := f.memoryStore.ProjectHarnessRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("Project duplicate run: %v", err)
	}
	if epReused.CanonicalDigest != epIncomp.CanonicalDigest {
		t.Fatalf("Expected same canonical digest on duplicate projection")
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestEpisodeImmutabilityAndReprojection(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", "Immutability Task")
	snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)
	runID := "harness-run-immutability-1"

	f.createDescriptor(t, runID, taskID, attemptID, snapshotID, contextContent, "profile/standard")
	f.createHarnessEvents(t, runID, taskID, attemptID, "completed")

	// 1. Initial project -> revision 1, reused = false
	ep1, err := f.memoryStore.ProjectHarnessRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("ProjectHarnessRun 1: %v", err)
	}

	// 2. Same durable facts -> project -> same Episode -> reuse
	ep2, err := f.memoryStore.ProjectHarnessRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("ProjectHarnessRun 2: %v", err)
	}
	if ep1.CanonicalDigest != ep2.CanonicalDigest {
		t.Fatalf("Expected identical CanonicalDigest on rerun")
	}

	// 3. Direct SQL UPDATE on memoryos_episodes MUST FAIL closed
	_, err = f.platformStore.Pool().Exec(f.ctx, `UPDATE memoryos_episodes SET turns_used = 999 WHERE id = $1`, ep1.ID)
	if err == nil {
		t.Fatalf("Expected UPDATE on memoryos_episodes to fail via immutable trigger, got nil")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("Expected append-only error, got: %v", err)
	}

	// 4. Direct SQL UPDATE on memoryos_episode_skills MUST FAIL closed
	_, err = f.platformStore.Pool().Exec(f.ctx, `UPDATE memoryos_episode_skills SET included = false WHERE episode_id = $1`, ep1.ID)
	if err == nil {
		t.Fatalf("Expected UPDATE on memoryos_episode_skills to fail via immutable trigger, got nil")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("Expected append-only error, got: %v", err)
	}

	// 5. Direct SQL DELETE MUST FAIL closed
	_, err = f.platformStore.Pool().Exec(f.ctx, `DELETE FROM memoryos_episodes WHERE id = $1`, ep1.ID)
	if err == nil {
		t.Fatalf("Expected DELETE on memoryos_episodes to fail via immutable trigger, got nil")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("Expected append-only error, got: %v", err)
	}
}

func TestRawContentRetentionZeroSentinels(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	const (
		secretSentinel      = "SECRET_SENTINEL_8fa1b2c3d4e5f6071829"
		clinicalSentinel    = "CLINICAL_SENTINEL_7a0b1c2d3e4f5a6b7c8d"
		toolArgSentinel     = "TOOL_ARG_SENTINEL_2b3c4d5e6f7a8b9c0d1e"
		modelOutputSentinel = "MODEL_OUTPUT_SENTINEL_1928374650abcdef1234"
	)

	taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", "Privacy Sentinel Task")

	// Create context snapshot with clinicalSentinel
	snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)
	contextContentWithSentinel := contextContent + "\n" + clinicalSentinel

	runID := "harness-run-privacy-sentinel-1"

	// Create descriptor
	f.createDescriptor(t, runID, taskID, attemptID, snapshotID, contextContentWithSentinel, "profile/standard")

	// Insert events with tool argument and model output sentinels in raw payload
	toolReqPayload := fmt.Sprintf(`{"run_id":"%s","tool_request":{"tool_call_id":"call-1","tool_name":"exec_tool","arguments":{"param":"%s"}}}`, runID, toolArgSentinel)
	toolResPayload := fmt.Sprintf(`{"run_id":"%s","tool_request":{"tool_call_id":"call-1","tool_name":"exec_tool"},"tool_result":{"result":"%s"}}`, runID, toolArgSentinel)
	modelPayload := fmt.Sprintf(`{"run_id":"%s","model_result":{"finish_reason":"final","final_output":"%s"}}`, runID, modelOutputSentinel)

	_, err := f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO execution_run_events(organization_id, run_id, sequence, task_id, attempt_id, event_type, correlation_id, causation_id, terminal_status, payload, recorded_at)
		VALUES
		($1, $2, 1, $3, $4, 'run_started', 'corr-1', 'cause-1', '', jsonb_build_object('run_id', $2::text), NOW()),
		($1, $2, 2, $3, $4, 'tool_call_requested', 'corr-1', 'cause-1', '', $5::jsonb, NOW()),
		($1, $2, 3, $3, $4, 'tool_result_recorded', 'corr-1', 'cause-1', '', $6::jsonb, NOW()),
		($1, $2, 4, $3, $4, 'model_response_recorded', 'corr-1', 'cause-1', '', $7::jsonb, NOW()),
		($1, $2, 5, $3, $4, 'run_completed', 'corr-1', 'cause-1', 'completed', jsonb_build_object('run_id', $2::text), NOW())
	`, testOrganization, runID, taskID, attemptID, toolReqPayload, toolResPayload, modelPayload)
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}

	// Project and persist into MemoryOS tables
	_, err = f.memoryStore.ProjectHarnessRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("ProjectHarnessRun: %v", err)
	}

	// Verify all columns of memoryos_% and execution_run_descriptors for sentinels
	tables := []string{
		"execution_run_descriptors",
		"memoryos_episodes",
		"memoryos_episode_skills",
		"memoryos_episode_tools",
		"memoryos_episode_invocations",
		"memoryos_episode_obligations",
		"memoryos_completion_observations",
		"memoryos_clusters",
	}

	sentinels := []string{secretSentinel, clinicalSentinel, toolArgSentinel, modelOutputSentinel}

	for _, table := range tables {
		rows, err := f.platformStore.Pool().Query(f.ctx, `
			SELECT column_name
			FROM information_schema.columns
			WHERE table_name = $1 AND table_schema = 'public'
			  AND data_type IN ('text', 'character varying', 'json', 'jsonb')
		`, table)
		if err != nil {
			t.Fatalf("query columns for %s: %v", table, err)
		}
		var columns []string
		for rows.Next() {
			var col string
			if scanErr := rows.Scan(&col); scanErr == nil {
				columns = append(columns, col)
			}
		}
		rows.Close()

		for _, col := range columns {
			for _, sentinel := range sentinels {
				query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s::text LIKE '%%%s%%'`, table, col, sentinel)
				var count int
				if scanErr := f.platformStore.Pool().QueryRow(f.ctx, query).Scan(&count); scanErr != nil {
					t.Fatalf("scan sentinel count in %s.%s: %v", table, col, scanErr)
				}
				if count > 0 {
					t.Fatalf("RAW CONTENT RETENTION VIOLATION: table %s column %s contains sentinel %q (%d occurrences)", table, col, sentinel, count)
				}
			}
		}
	}
}

func TestObligationAuthorityIgnoresNonCompletionVerifier(t *testing.T) {
	f := newIntegrationFixture(t)
	defer f.cleanup()

	taskID, attemptID := f.createTaskAndAttempt(t, "qa.verification", "Authority Filter Task")
	snapshotID, contextContent, _ := f.createContextSnapshot(t, taskID, attemptID)
	runID := "harness-run-auth-filter-1"

	f.createDescriptor(t, runID, taskID, attemptID, snapshotID, contextContent, "profile/standard")
	f.createHarnessEvents(t, runID, taskID, attemptID, "completed")

	// Create a decision graph run using fixture helper
	decisionRunID := f.createDecisionGraphRun(t, taskID, attemptID, "succeeded")

	evHash := sha256Hex("evidence-proof")

	// Create decision graph version
	var versionID int64
	err := f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO decision_graph_versions(organization_id, run_id, version_number, snapshot_hash, node_count, max_depth, created_by)
		VALUES($1, $2, 1, $3, 1, 0, $4)
		RETURNING id
	`, testOrganization, decisionRunID, evHash, testRole).Scan(&versionID)
	if err != nil {
		t.Fatalf("insert version: %v", err)
	}

	// Create a decision node
	var nodeID int64
	err = f.platformStore.Pool().QueryRow(f.ctx, `
		INSERT INTO decision_graph_nodes(
			organization_id, run_id, graph_version_id, logical_node_id,
			node_type, branch_state, execution_state, payload_schema_version,
			payload_hash, depth, created_by
		) VALUES (
			$1, $2, $3, 1,
			'verification', 'active', 'ready', 'v1',
			$4, 0, $5
		)
		RETURNING id
	`, testOrganization, decisionRunID, versionID, evHash, testRole).Scan(&nodeID)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	// Verification A: Non-completion verifier contradicts
	_, err = f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO decision_verifications(organization_id, run_id, node_id, label, verifier_ref, verifier_version, evidence_set_hash, created_at)
		VALUES($1, $2, $3, 'contradicted', 'internal/intermediate_step_check', 'v1', $4, NOW())
	`, testOrganization, decisionRunID, nodeID, evHash)
	if err != nil {
		t.Fatalf("insert non-completion verification: %v", err)
	}

	// Verification B: Completion verifier passes
	_, err = f.platformStore.Pool().Exec(f.ctx, `
		INSERT INTO decision_verifications(organization_id, run_id, node_id, label, verifier_ref, verifier_version, evidence_set_hash, created_at)
		VALUES($1, $2, $3, 'verified', 'internal/completion', 'phase2', $4, NOW() - INTERVAL '1 minute')
	`, testOrganization, decisionRunID, nodeID, evHash)
	if err != nil {
		t.Fatalf("insert completion verification: %v", err)
	}

	// Project without pre-existing memoryos_completion_observations (forcing fallback to decision_verifications)
	ep, err := f.memoryStore.ProjectHarnessRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("ProjectHarnessRun: %v", err)
	}

	if ep.Verification == nil {
		t.Fatalf("expected verification to be projected")
	}

	// Must be "pass" because non-completion contradicted verifier was ignored
	if ep.Verification.Verdict != "pass" {
		t.Fatalf("expected verdict 'pass', got '%s' (non-completion verifier was not filtered out!)", ep.Verification.Verdict)
	}
	if len(ep.Verification.Obligations) != 1 {
		t.Fatalf("expected exactly 1 obligation (from completion verifier), got %d", len(ep.Verification.Obligations))
	}
	if ep.Verification.Obligations[0].VerifierRef != "internal/completion" {
		t.Fatalf("expected verifier_ref 'internal/completion', got %s", ep.Verification.Obligations[0].VerifierRef)
	}
}
