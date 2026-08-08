//go:build integration

package sleep_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/canonical"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisiongraphpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/executive/sleep"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragbootstrap "github.com/Mireuz13/explorarte-organization/internal/rag/bootstrap"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	sleepIntegrationOrg  = "explorarte"
	sleepIntegrationRole = "ingenieria_ia/qa"
	sleepIntegrationUnit = "ingenieria_ia"
)

type modelBinding struct {
	ProfileID        string
	ProfileVersionID int64
	ProviderID       string
	ProviderModelID  string
}

func TestOrganizationalSleepAgainstRealPostgres(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
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
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "sleep-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err = store.Pool().Exec(ctx, `TRUNCATE organizations, organization_registry_revisions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, sleepIntegrationOrg, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	syncResult, err := registryService.SynchronizeCanonical(ctx, true)
	if err != nil || !syncResult.Applied {
		t.Fatalf("sync registry: result=%+v err=%v", syncResult, err)
	}
	revision, err := registryRepo.GetCurrentRevision(ctx, sleepIntegrationOrg)
	if err != nil || revision == nil {
		t.Fatalf("current registry revision=%+v err=%v", revision, err)
	}

	modelRuntime, err := modelbootstrap.OpenRegistry(cfg, store)
	if err != nil {
		t.Fatalf("open model registry: %v", err)
	}
	modelSync, err := modelRuntime.Registry.Sync(ctx, true, cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		t.Fatalf("sync model registry: %v", err)
	}
	if !modelSync.Applied && !modelSync.NoOp {
		t.Fatalf("model registry did not synchronize: %+v", modelSync)
	}
	binding := loadModelBinding(t, ctx, store, revision.ID, sleepIntegrationRole)

	graphStore, err := decisiongraphpostgres.New(store, sleepIntegrationOrg)
	if err != nil {
		t.Fatal(err)
	}
	graphService, err := decisiongraph.NewService(graphStore, decisiongraph.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	canonicalProvider, err := canonical.New(canonicalDir, int64(cfg.Context.MaxTotalBytes))
	if err != nil {
		t.Fatal(err)
	}
	recorder := runtimeadapter.DecisionGraph{Service: graphService, Canonical: canonicalProvider, Limits: executive.DefaultLimits(), Clock: executive.ClockFunc(time.Now)}

	labels := []executive.CompletionVerdict{executive.CompletionPass, executive.CompletionPass, executive.CompletionFail}
	runIDs := make([]int64, 0, len(labels))
	for index, verdict := range labels {
		taskID, attemptID := insertObservedAttempt(t, ctx, store, revision.ID, binding, index+1, verdict)
		if err := recorder.RecordAttemptDecision(ctx, executive.AttemptDecisionRecord{
			TaskID: taskID, AttemptID: attemptID, Verdict: verdict, Detail: fmt.Sprintf("sleep integration verdict %d", index+1),
		}); err != nil {
			t.Fatalf("record decision %d: %v", index+1, err)
		}
		var runID int64
		if err := store.Pool().QueryRow(ctx, `SELECT id FROM decision_graph_runs WHERE organization_id=$1 AND task_id=$2 AND attempt_id=$3`, sleepIntegrationOrg, taskID, attemptID).Scan(&runID); err != nil {
			t.Fatal(err)
		}
		runIDs = append(runIDs, runID)
	}

	// A fail verdict must not leave its decision graph run 'running' forever
	// — the task attempt finished, so the run must reach a real terminal
	// status too, even though nothing was selected.
	for index, verdict := range labels {
		var status string
		var terminalAt *time.Time
		var reasonCode *string
		if err := store.Pool().QueryRow(ctx, `SELECT status, terminal_at, terminal_reason_code FROM decision_graph_runs WHERE id=$1 AND organization_id=$2`, runIDs[index], sleepIntegrationOrg).Scan(&status, &terminalAt, &reasonCode); err != nil {
			t.Fatal(err)
		}
		wantStatus := "succeeded"
		if verdict == executive.CompletionFail {
			wantStatus = "failed"
		}
		if status != wantStatus || terminalAt == nil || reasonCode == nil {
			t.Fatalf("run %d (verdict=%s) status=%s terminal_at=%v reason_code=%v, want status=%s with terminal_at and reason_code set", runIDs[index], verdict, status, terminalAt, reasonCode, wantStatus)
		}
	}

	ragRuntime, err := ragbootstrap.Open(cfg, store)
	if err != nil {
		t.Fatalf("open RAG runtime: %v", err)
	}
	reader, err := sleep.NewPostgresReader(store, ragRuntime.Manager)
	if err != nil {
		t.Fatal(err)
	}
	service, err := sleep.NewService(reader, ragRuntime.Manager, sleep.ClockFunc(time.Now), sleep.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunCycle(ctx, sleepIntegrationOrg, 24*time.Hour)
	if err != nil {
		t.Fatalf("run sleep cycle: %v", err)
	}
	if result.EligibleExperiences != 3 || result.RecurringGroups != 1 || result.MixedContradictionGroups != 1 || result.CandidatesProposed != 1 {
		t.Fatalf("cycle result=%+v", result)
	}
	if len(result.Proposals) != 1 {
		t.Fatalf("proposals=%+v", result.Proposals)
	}
	proposal := result.Proposals[0]
	if proposal.Group.UnitID != sleepIntegrationUnit || proposal.Group.RoleID != sleepIntegrationRole || proposal.Group.ProviderID != binding.ProviderID {
		t.Fatalf("proposal group=%+v", proposal.Group)
	}

	var lifecycle, proposedBy, sourceKind, sourceBoundary string
	var body string
	if err := store.Pool().QueryRow(ctx, `
SELECT lifecycle, proposed_by_role_id, source_kind, source_boundary, body
FROM rag_knowledge_versions
WHERE organization_id=$1 AND version_id=$2`, sleepIntegrationOrg, proposal.VersionID).Scan(&lifecycle, &proposedBy, &sourceKind, &sourceBoundary, &body); err != nil {
		t.Fatal(err)
	}
	if lifecycle != string(rag.LifecycleCandidate) || proposedBy != sleep.ProposerRoleID || sourceKind != string(rag.SourceOperational) || sourceBoundary != sleep.SourceBoundary {
		t.Fatalf("stored lifecycle=%s proposer=%s source=%s boundary=%s", lifecycle, proposedBy, sourceKind, sourceBoundary)
	}
	if body == "" {
		t.Fatal("candidate body is empty")
	}

	rows, err := store.Pool().Query(ctx, `SELECT reference,digest FROM rag_knowledge_evidence_refs WHERE organization_id=$1 AND version_id=$2 ORDER BY reference`, sleepIntegrationOrg, proposal.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seenRuns := map[int64]bool{}
	for rows.Next() {
		var reference, digest string
		if err := rows.Scan(&reference, &digest); err != nil {
			t.Fatal(err)
		}
		if !sleepDigest(digest) {
			t.Fatalf("invalid durable evidence digest %q", digest)
		}
		for _, runID := range runIDs {
			if reference == fmt.Sprintf("decisiongraph:run:%d", runID) {
				seenRuns[runID] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seenRuns) != len(runIDs) {
		t.Fatalf("evidence refs missing runs: got=%v want=%v", seenRuns, runIDs)
	}

	// Candidate knowledge is deliberately not published or indexed by the
	// sleep cycle. A department reader must therefore see no result yet.
	query, err := ragRuntime.Manager.Query(ctx, rag.QueryRequest{
		OrganizationID: sleepIntegrationOrg, ActorRoleID: "ingenieria_ia/orquestador",
		Scope: rag.NamespaceDepartment, QueryText: "completion pattern", Limit: 10,
	})
	if err != nil {
		t.Fatalf("query candidate visibility: %v", err)
	}
	if len(query) != 0 {
		t.Fatalf("candidate became retrievable before review/reindex: %+v", query)
	}

	// Every source run is now durably marked as considered by the immutable
	// RAG evidence relation. A second cycle has nothing left to consume.
	second, err := service.RunCycle(ctx, sleepIntegrationOrg, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if second.EligibleExperiences != 0 || second.CandidatesProposed != 0 || second.CandidatesReused != 0 {
		t.Fatalf("second cycle should be a no-op after durable consumption: %+v", second)
	}
}

func loadModelBinding(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, roleID string) modelBinding {
	t.Helper()
	var binding modelBinding
	if err := store.Pool().QueryRow(ctx, `
SELECT b.profile_id, b.model_profile_version_id, v.provider_id, v.provider_model_id
FROM role_model_bindings b
JOIN model_profile_versions v
  ON v.id=b.model_profile_version_id
 AND v.organization_id=b.organization_id
 AND v.profile_id=b.profile_id
WHERE b.organization_id=$1 AND b.organization_revision_id=$2 AND b.role_id=$3 AND b.active`,
		sleepIntegrationOrg, revisionID, roleID,
	).Scan(&binding.ProfileID, &binding.ProfileVersionID, &binding.ProviderID, &binding.ProviderModelID); err != nil {
		t.Fatalf("load model binding for %s: %v", roleID, err)
	}
	return binding
}

func insertObservedAttempt(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, binding modelBinding, ordinal int, verdict executive.CompletionVerdict) (int64, int64) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	requestHash := digest(fmt.Sprintf("sleep-task-%d", ordinal))
	status := "completed"
	if verdict == executive.CompletionFail {
		status = "failed"
	}
	var taskID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO tasks (
 organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,
 idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,
 max_attempts,attempt_count,version,created_at,updated_at,terminal_at
) VALUES ($1,$2,'empresa/ceo',$3,$4,$5,$6,$7,$8,'[]'::jsonb,$9,0,$10,1,1,1,$10,$10,$10)
RETURNING id`, sleepIntegrationOrg, revisionID, sleepIntegrationRole, sleepIntegrationUnit,
		fmt.Sprintf("sleep-integration-task-%d", ordinal), requestHash, fmt.Sprintf("Sleep fixture %d", ordinal), "durable observed execution", status, now,
	).Scan(&taskID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	var attemptID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO task_attempts (task_id,ordinal,state,worker_id,result_summary,retryable,leased_at,started_at,finished_at,created_at,updated_at)
VALUES ($1,1,'finished','sleep-integration-worker','durable result',FALSE,$2,$2,$2,$2,$2)
RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}

	var snapshotID int64
	contextHash := digest(fmt.Sprintf("context-%d", ordinal))
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO context_snapshots (
 organization_id,organization_revision_id,actor_role_id,purpose,task_ref,idempotency_key,request_hash,
 precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,
 omitted_segment_count,total_bytes,created_at
) VALUES ($1,$2,$3,'sleep integration fixture',$4,$5,$6,$6,$6,$6,'ready',1,0,0,0,0,$7)
RETURNING id`, sleepIntegrationOrg, revisionID, sleepIntegrationRole, fmt.Sprintf("task:%d", taskID),
		fmt.Sprintf("sleep-context-%d", ordinal), contextHash, now.Add(-time.Second),
	).Scan(&snapshotID); err != nil {
		t.Fatalf("insert context snapshot: %v", err)
	}

	if _, err := store.Pool().Exec(ctx, `
INSERT INTO model_invocations (
 organization_id,organization_revision_id,task_id,attempt_id,dispatch_actor_role_id,subject_role_id,
 context_snapshot_id,purpose,model_profile_id,model_profile_version_id,provider_id,provider_model_id,
 required_capabilities,output_mode,max_output_tokens,thinking_mode,idempotency_key,request_hash,status,
 deadline,created_at,updated_at,terminal_at
) VALUES ($1,$2,$3,$4,'ingenieria_ia/code-runner',$5,$6,'sleep integration observed invocation',$7,$8,$9,$10,
 '[]'::jsonb,'json',128,'opaque',$11,$12,'succeeded',$13,$14,$14,$15)`,
		sleepIntegrationOrg, revisionID, taskID, attemptID, sleepIntegrationRole, snapshotID,
		binding.ProfileID, binding.ProfileVersionID, binding.ProviderID, binding.ProviderModelID,
		fmt.Sprintf("sleep-invocation-%d", ordinal), digest(fmt.Sprintf("invocation-%d", ordinal)), now.Add(time.Hour), now.Add(-time.Second), now,
	); err != nil {
		t.Fatalf("insert model invocation: %v", err)
	}
	return taskID, attemptID
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sleepDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
