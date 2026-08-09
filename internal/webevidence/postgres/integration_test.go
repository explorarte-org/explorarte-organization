//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/webevidence"
	webevidencepostgres "github.com/Mireuz13/explorarte-organization/internal/webevidence/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const webEvidenceIntegrationOrganization = "explorarte"

func TestWebEvidencePostgresStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	platform := openWebEvidenceStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetWebEvidenceSchema(t, ctx, platform)
	t.Cleanup(func() { resetWebEvidenceSchema(t, context.Background(), platform) })
	revision := syncWebEvidenceCanonical(t, ctx, platform)
	taskID := insertWebEvidenceTask(t, ctx, platform, revision.ID)

	store, err := webevidencepostgres.New(platform, webEvidenceIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	evidence := webevidence.Evidence{
		ID: "ev-1", OrganizationID: webEvidenceIntegrationOrganization, TaskID: taskID,
		URL: "https://example.com/page", ContentHash: strings.Repeat("a", 64),
		CapturedAt: now, ExpiresAt: now.Add(time.Hour),
		Chunks:               []webevidence.Chunk{{Ordinal: 0, Text: "reactor core temperature exceeded threshold"}},
		SanitizationFindings: []webevidence.SanitizationFinding{{ChunkOrdinal: 0, Pattern: "ignore_prior_instructions", Excerpt: "ignore all previous instructions"}},
	}
	if err := store.Save(ctx, evidence); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Get(ctx, webEvidenceIntegrationOrganization, "ev-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.URL != evidence.URL || loaded.ContentHash != evidence.ContentHash || len(loaded.Chunks) != 1 || len(loaded.SanitizationFindings) != 1 {
		t.Fatalf("loaded=%+v", loaded)
	}

	listed, err := store.ListForTask(ctx, webEvidenceIntegrationOrganization, taskID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "ev-1" {
		t.Fatalf("listed=%+v", listed)
	}

	// Expired evidence is invisible to Get/ListForTask even before any
	// reaper physically deletes it — "as of" now, not "eventually".
	afterExpiry := evidence.ExpiresAt.Add(time.Second)
	if _, err := store.Get(ctx, webEvidenceIntegrationOrganization, "ev-1", afterExpiry); err != webevidence.ErrNotFound {
		t.Fatalf("err=%v, want ErrNotFound once expired", err)
	}
	listedAfterExpiry, err := store.ListForTask(ctx, webEvidenceIntegrationOrganization, taskID, afterExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedAfterExpiry) != 0 {
		t.Fatalf("listed after expiry=%+v, want empty", listedAfterExpiry)
	}

	// UPDATE is rejected the same way every other evidence table in this
	// system rejects it — delete-and-reinsert is the only path.
	if _, err := platform.Pool().Exec(ctx, `UPDATE web_evidence SET url='https://tampered.example.com' WHERE organization_id=$1 AND id=$2`, webEvidenceIntegrationOrganization, "ev-1"); err == nil {
		t.Fatal("expected web_evidence to reject UPDATE in place")
	}

	// A second, distinct evidence row (already-expired) proves Reap only
	// removes what is actually past expiry, bounded by limit.
	expiredEvidence := webevidence.Evidence{
		ID: "ev-2", OrganizationID: webEvidenceIntegrationOrganization, TaskID: taskID,
		URL: "https://example.com/other", ContentHash: strings.Repeat("b", 64),
		CapturedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
		Chunks: []webevidence.Chunk{{Ordinal: 0, Text: "already expired content"}},
	}
	if err := store.Save(ctx, expiredEvidence); err != nil {
		t.Fatal(err)
	}
	reaped, err := store.Reap(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if reaped != 1 {
		t.Fatalf("reaped=%d want 1 (only ev-2 was past expiry)", reaped)
	}
	// ev-1 (not yet expired as of `now`) must have survived the reap.
	if _, err := store.Get(ctx, webEvidenceIntegrationOrganization, "ev-1", now); err != nil {
		t.Fatalf("ev-1 should have survived reap: %v", err)
	}

	// Cross-organization isolation: a store scoped to another org can
	// never read this org's evidence.
	otherStore, err := webevidencepostgres.New(platform, "other-org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherStore.Get(ctx, "other-org", "ev-1", now); err != webevidence.ErrNotFound {
		t.Fatalf("cross-organization get err=%v, want ErrNotFound", err)
	}
}

func openWebEvidenceStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "webevidence-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetWebEvidenceSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `TRUNCATE organizations, organization_registry_revisions, audit_events RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset webevidence schema: %v", err)
	}
}

func syncWebEvidenceCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, webEvidenceIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !res.Applied {
		t.Fatalf("sync=%+v err=%v", res, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, webEvidenceIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func insertWebEvidenceTask(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64) int64 {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var taskID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO tasks(
    organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,
    idempotency_key,request_hash,title,instructions,acceptance_criteria,status,
    priority,available_at,max_attempts,attempt_count,version,created_at,updated_at
) VALUES($1,$2,'investigacion/research_worker_hourly','investigacion',$3,$4,
         'Web evidence fixture','Exercise ephemeral web evidence storage.','[]','running',
         0,$5,3,1,1,$5,$5)
RETURNING id`, webEvidenceIntegrationOrganization, revisionID, "webevidence-task", webEvidenceDigest("webevidence-task"), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func webEvidenceDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
