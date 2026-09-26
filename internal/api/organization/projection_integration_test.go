//go:build integration

// Audit 2026-09-26, findings 3 and 7, against real PostgreSQL: the snapshot projects the owner.goal
// roots only, each with its own status, reason and children, with counters over every root; and the
// report counts each task once and shows the result the executive accepted, not a rejected one.
// Every figure is compared with a direct SQL count over the same rows.
package organization_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	orgapi "github.com/Mireuz13/explorarte-organization/internal/api/organization"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const projectionOrg = "explorarte"

type projectionFixture struct {
	t               *testing.T
	ctx             context.Context
	store           *platformpostgres.Store
	service         *orgapi.Service
	revisionID      int64
	profileID       string
	profileVersion  int64
	providerID      string
	providerModelID string
	ordinal         int
}

func openProjectionFixture(t *testing.T) *projectionFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required for integration tests")
	}
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL, "ORG_DATABASE_MAX_CONNS": "8",
			"ORG_DATABASE_MIN_CONNS": "0", "ORG_CANONICAL_DIR": canonicalDir}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tasks.OrganizationID = projectionOrg
	store, err := platformpostgres.Open(ctx, cfg.Database, "api-organization-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := testdbguard.RequireDestructive(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `TRUNCATE organizations, organization_registry_revisions RESTART IDENTITY CASCADE`); err != nil {
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
	registryService, err := registry.NewService(loader, registryRepo, projectionOrg, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := registryService.SynchronizeCanonical(ctx, true); err != nil || !result.Applied {
		t.Fatalf("sync registry: result=%+v err=%v", result, err)
	}
	revision, err := registryRepo.GetCurrentRevision(ctx, projectionOrg)
	if err != nil || revision == nil {
		t.Fatalf("current registry revision=%+v err=%v", revision, err)
	}
	modelRuntime, err := modelbootstrap.OpenRegistry(cfg, store)
	if err != nil {
		t.Fatalf("open model registry: %v", err)
	}
	if modelSync, err := modelRuntime.Registry.Sync(ctx, true, cfg.Tasks.OutboxMaxAttempts); err != nil || (!modelSync.Applied && !modelSync.NoOp) {
		t.Fatalf("sync model registry: %+v %v", modelSync, err)
	}
	f := &projectionFixture{t: t, ctx: ctx, store: store, revisionID: revision.ID}
	if err := store.Pool().QueryRow(ctx, `
SELECT b.profile_id, b.model_profile_version_id, v.provider_id, v.provider_model_id
FROM role_model_bindings b
JOIN model_profile_versions v ON v.id = b.model_profile_version_id AND v.organization_id = b.organization_id AND v.profile_id = b.profile_id
WHERE b.organization_id = $1 AND b.organization_revision_id = $2 AND b.role_id = 'ingenieria_ia/qa' AND b.active`,
		projectionOrg, revision.ID).Scan(&f.profileID, &f.profileVersion, &f.providerID, &f.providerModelID); err != nil {
		t.Fatalf("load model binding: %v", err)
	}
	f.service, err = orgapi.NewService(store.Pool(), nil, nil, nil, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func projectionDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

var terminalStatuses = map[string]bool{"completed": true, "no_action": true, "failed": true, "dead_letter": true, "rejected": true, "cancelled": true}

func (f *projectionFixture) task(class, requestedBy, role, unit, correlation, status, reasonCode string) int64 {
	f.t.Helper()
	f.ordinal++
	now := time.Now().UTC()
	var terminal *time.Time
	if terminalStatuses[status] {
		terminal = &now
	}
	var reason *string
	var code *string
	if reasonCode != "" {
		code = &reasonCode
		text := "the root stopped: " + reasonCode
		reason = &text
	}
	var id int64
	if err := f.store.Pool().QueryRow(f.ctx, `
INSERT INTO tasks (organization_id, organization_revision_id, requested_by_role_id, assigned_role_id, assigned_unit_id,
 idempotency_key, request_hash, title, instructions, acceptance_criteria, status, priority, available_at,
 max_attempts, attempt_count, version, correlation_id, causation_id, task_class, status_reason_code, status_reason,
 created_at, updated_at, terminal_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '[]'::jsonb, $10, 0, $11, 3, 0, 1, $12, $13, $14, $15, $16, $11, $11, $17)
RETURNING id`, projectionOrg, f.revisionID, requestedBy, role, unit,
		fmt.Sprintf("projection-%d", f.ordinal), projectionDigest(fmt.Sprintf("projection-%d", f.ordinal)),
		fmt.Sprintf("%s %d", class, f.ordinal), "projection fixture instructions", status, now,
		correlation, "owner:projection", class, code, reason, terminal).Scan(&id); err != nil {
		f.t.Fatalf("insert task: %v", err)
	}
	return id
}

// attempt records one attempt of taskID and, when summary is not empty, a succeeded provider
// invocation that answered it -- whether or not the host then accepted the attempt.
func (f *projectionFixture) attempt(taskID int64, ordinal int, state, summary string) {
	f.t.Helper()
	now := time.Now().UTC()
	var finished *time.Time
	if state == "finished" || state == "failed" {
		finished = &now
	}
	var attemptID int64
	if err := f.store.Pool().QueryRow(f.ctx, `
INSERT INTO task_attempts (task_id, ordinal, state, worker_id, leased_at, started_at, finished_at, created_at, updated_at)
VALUES ($1, $2, $3, 'projection-worker', $4, $4, $5, $4, $4) RETURNING id`, taskID, ordinal, state, now, finished).Scan(&attemptID); err != nil {
		f.t.Fatalf("insert attempt: %v", err)
	}
	if summary == "" {
		return
	}
	f.ordinal++
	key := fmt.Sprintf("projection-invocation-%d", f.ordinal)
	var snapshotID, invocationID, dispatchID int64
	if err := f.store.Pool().QueryRow(f.ctx, `
INSERT INTO context_snapshots (organization_id, organization_revision_id, actor_role_id, purpose, task_ref, idempotency_key, request_hash,
 precedence_hash, canonical_bundle_hash, rendered_hash, status, version, segment_count, included_segment_count, omitted_segment_count, total_bytes, created_at)
VALUES ($1, $2, 'ingenieria_ia/qa', 'projection fixture', $3, $4, $5, $5, $5, $5, 'ready', 1, 0, 0, 0, 0, $6) RETURNING id`,
		projectionOrg, f.revisionID, fmt.Sprintf("task:%d", taskID), key, projectionDigest(key), now.Add(-time.Second)).Scan(&snapshotID); err != nil {
		f.t.Fatalf("insert context snapshot: %v", err)
	}
	if err := f.store.Pool().QueryRow(f.ctx, `
INSERT INTO model_invocations (organization_id, organization_revision_id, task_id, attempt_id, dispatch_actor_role_id, subject_role_id,
 context_snapshot_id, purpose, model_profile_id, model_profile_version_id, provider_id, provider_model_id, required_capabilities,
 output_mode, max_output_tokens, thinking_mode, idempotency_key, request_hash, status, deadline, created_at, updated_at, terminal_at)
VALUES ($1, $2, $3, $4, 'ingenieria_ia/qa', 'ingenieria_ia/qa', $5, 'projection fixture', $6, $7, $8, $9, '[]'::jsonb,
 'json', 128, 'opaque', $10, $11, 'succeeded', $12, $13, $13, $13) RETURNING id`,
		projectionOrg, f.revisionID, taskID, attemptID, snapshotID, f.profileID, f.profileVersion, f.providerID, f.providerModelID,
		key, projectionDigest(key), now.Add(time.Hour), now).Scan(&invocationID); err != nil {
		f.t.Fatalf("insert invocation: %v", err)
	}
	if err := f.store.Pool().QueryRow(f.ctx, `
INSERT INTO model_dispatch_attempts (invocation_id, attempt_number, status, claim_token_hash, claimed_by, claimed_at, claim_expires_at, retry_safety)
VALUES ($1, 1, 'claimed', $2, 'projection-worker', $3, $4, 'safe_before_send') RETURNING id`,
		invocationID, projectionDigest(key+"-claim"), now, now.Add(time.Minute)).Scan(&dispatchID); err != nil {
		f.t.Fatalf("insert dispatch attempt: %v", err)
	}
	body := `{"summary":` + mustJSON(summary) + `}`
	if _, err := f.store.Pool().Exec(f.ctx, `
INSERT INTO model_invocation_results (invocation_id, dispatch_attempt_id, output_mode, json_output, response_hash, response_bytes)
VALUES ($1, $2, 'json', $3::jsonb, $4, $5)`, invocationID, dispatchID, body, projectionDigest(body), len(body)); err != nil {
		f.t.Fatalf("insert result: %v", err)
	}
}

func mustJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (f *projectionFixture) sqlCount(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.store.Pool().QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count: %v", err)
	}
	return n
}

func TestTheSnapshotAndTheReportProjectTheCanonicalRoots(t *testing.T) {
	f := openProjectionFixture(t)

	// A blocked root with three departments' work: a worker whose first answer was rejected and whose
	// second was accepted, a blocked worker, and a completed one.
	blocked := f.task("owner.goal", "empresa/human", "empresa/ceo", "empresa", "executive:blocked", "blocked", "design_rounds_exhausted")
	retried := f.task("engineering.design", "empresa/ceo", "ingenieria_ia/qa", "ingenieria_ia", "executive:blocked", "completed", "")
	f.attempt(retried, 1, "failed", "REJECTED ANSWER")
	f.attempt(retried, 2, "finished", "ACCEPTED ANSWER")
	f.task("coordination.department_plan", "empresa/ceo", "negocio/administrador_financiero", "negocio", "executive:blocked", "blocked", "department_replans_exhausted")
	done := f.task("research.review", "empresa/ceo", "investigacion/revisor_adversarial", "investigacion", "executive:blocked", "completed", "")
	f.attempt(done, 1, "finished", "REVIEW DONE")
	// A completed root, and a task the owner requested that is not a root (a chat turn).
	completed := f.task("owner.goal", "empresa/human", "empresa/ceo", "empresa", "executive:completed", "completed", "")
	chat := f.task("ceo.chat_turn", "empresa/human", "empresa/ceo", "empresa", "ceochat:1", "completed", "")

	snapshot, err := f.service.GetSnapshot(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Capabilities.Chat || snapshot.Capabilities.CreateMission {
		t.Fatalf("a read-only API advertises owner capabilities: %+v", snapshot.Capabilities)
	}
	if len(snapshot.Partial) != 0 {
		t.Fatalf("sections could not be read: %v", snapshot.Partial)
	}
	if len(snapshot.Missions) != 2 || snapshot.Missions[0].RootTaskID != completed || snapshot.Missions[1].RootTaskID != blocked {
		t.Fatalf("missions are not exactly the two roots, newest first: %+v", snapshot.Missions)
	}
	for _, m := range snapshot.Missions {
		if m.RootTaskID == chat {
			t.Fatal("a chat turn is shown as a mission")
		}
	}
	root := snapshot.Missions[1]
	if root.Status != "blocked" || root.TaskStatus != "blocked" || root.StatusReasonCode != "design_rounds_exhausted" || root.StatusReason == "" {
		t.Fatalf("the blocked root lost its state: %+v", root)
	}
	// Each figure against SQL over the same rows.
	wantTotal := f.sqlCount(`SELECT count(*) FROM tasks WHERE correlation_id = 'executive:blocked' AND id <> $1`, blocked)
	wantCompleted := f.sqlCount(`SELECT count(*) FROM tasks WHERE correlation_id = 'executive:blocked' AND id <> $1 AND status = 'completed'`, blocked)
	wantBlocked := f.sqlCount(`SELECT count(*) FROM tasks WHERE correlation_id = 'executive:blocked' AND id <> $1 AND status = 'blocked'`, blocked)
	if root.TotalTasks != wantTotal || root.CompletedTasks != wantCompleted || root.BlockedTasks != wantBlocked || wantTotal != 3 {
		t.Fatalf("root counts %d/%d/%d, SQL %d/%d/%d", root.TotalTasks, root.CompletedTasks, root.BlockedTasks, wantTotal, wantCompleted, wantBlocked)
	}
	if root.Progress <= 66 || root.Progress >= 67 {
		t.Fatalf("progress %v, want 2 of 3 children", root.Progress)
	}
	if root.BudgetMicrousd != 0 {
		t.Fatalf("a root without a budget shows %d, not zero", root.BudgetMicrousd)
	}
	counts := snapshot.Metrics.Missions
	for name, pair := range map[string][2]int64{
		"total":     {counts.Total, f.sqlCount(`SELECT count(*) FROM tasks WHERE task_class = 'owner.goal'`)},
		"blocked":   {counts.Blocked, f.sqlCount(`SELECT count(*) FROM tasks WHERE task_class = 'owner.goal' AND status = 'blocked'`)},
		"completed": {counts.Completed, f.sqlCount(`SELECT count(*) FROM tasks WHERE task_class = 'owner.goal' AND status = 'completed'`)},
		"active":    {counts.Active, 0},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s missions: snapshot %d, SQL %d", name, pair[0], pair[1])
		}
	}
	var holder *orgapi.Role
	for _, d := range snapshot.Departments {
		for i := range d.Roles {
			if d.Roles[i].ID == "negocio/administrador_financiero" {
				holder = &d.Roles[i]
			}
		}
	}
	if holder == nil || holder.Status != "blocked" || holder.MissionID != fmt.Sprintf("MS-%d", blocked) {
		t.Fatalf("the role holding a blocked task is not shown blocked on its mission: %+v", holder)
	}

	report, err := f.service.GetMissionReport(f.ctx, fmt.Sprintf("MS-%d", blocked))
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalTasks != wantTotal || report.CompletedTasks != wantCompleted {
		t.Fatalf("report counts %d/%d, SQL %d/%d", report.TotalTasks, report.CompletedTasks, wantTotal, wantCompleted)
	}
	seen := 0
	for _, audit := range report.SpecialistAudits {
		if audit.TaskID == retried {
			seen++
			if audit.Summary != "ACCEPTED ANSWER" {
				t.Fatalf("the report shows %q, not the accepted answer", audit.Summary)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("the retried task appears %d times in the report", seen)
	}

	// Only a root has a report, and a missing one is a 404, not a 500.
	if _, err := f.service.GetMissionReport(f.ctx, fmt.Sprintf("MS-%d", retried)); err == nil {
		t.Fatal("a child task was reported as a mission")
	}
	mux := http.NewServeMux()
	f.service.RegisterRoutes(mux)
	for path, want := range map[string]int{
		"/api/organization/snapshot":                                    http.StatusOK,
		fmt.Sprintf("/api/organization/missions/MS-%d/report", blocked): http.StatusOK,
		fmt.Sprintf("/api/organization/missions/MS-%d/report", retried): http.StatusNotFound,
		"/api/organization/missions/MS-999999999/report":                http.StatusNotFound,
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Errorf("GET %s = %d, want %d: %s", path, w.Code, want, w.Body.String())
		}
	}
}
