//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	contextpostgres "github.com/Mireuz13/explorarte-organization/internal/contextengine/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const integrationOrganization = "explorarte"

func TestContextEnginePostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	platform := openStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migration 000006: %v", err)
	}
	resetSchema(t, ctx, platform)
	syncCanonical(t, ctx, platform)
	store, err := contextpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("atomic creation, FKs, render reconstruction, audit and outbox", func(t *testing.T) {
		snapshot := validSnapshot(t, ctx, store, "atomic-1")
		result, createErr := store.Create(ctx, contextengine.CreateSnapshotCommand{Snapshot: snapshot, Now: snapshot.CreatedAt})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if result.Reused || result.Snapshot.ID != snapshot.ID {
			t.Fatalf("result=%+v", result)
		}
		loaded, getErr := store.Get(ctx, snapshot.ID, true)
		if getErr != nil {
			t.Fatal(getErr)
		}
		rendered, renderErr := contextengine.NewRenderer().Render(ctx, loaded)
		if renderErr != nil {
			t.Fatal(renderErr)
		}
		if contextengine.DigestCanonicalBytes(rendered) != loaded.RenderedHash {
			t.Fatal("rendered hash mismatch")
		}
		if loaded.SegmentCount != len(loaded.Segments) || loaded.IncludedSegmentCount != 1 {
			t.Fatalf("loaded=%+v", loaded)
		}
		assertCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE subject_type='context_snapshot' AND subject_id=$1 AND event_type='context.snapshot_created'`, fmt.Sprint(snapshot.ID), 1)
		assertCount(t, ctx, platform, `SELECT count(*) FROM outbox_events WHERE aggregate_type='context_snapshot' AND aggregate_id=$1 AND event_type='context.snapshot_created'`, fmt.Sprint(snapshot.ID), 1)
		var payload string
		if err := platform.Pool().QueryRow(ctx, `SELECT payload::text FROM outbox_events WHERE aggregate_type='context_snapshot' AND aggregate_id=$1`, fmt.Sprint(snapshot.ID)).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"content", "profile", "memory", "rag_evidence"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("outbox leaked %q: %s", forbidden, payload)
			}
		}
	})

	t.Run("idempotency conflict and concurrent creation exactly once", func(t *testing.T) {
		base := validSnapshot(t, ctx, store, "concurrent")
		const workers = 12
		var success, reused atomic.Int32
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				candidate := base
				id, idErr := store.AllocateID(ctx)
				if idErr != nil {
					errs <- idErr
					return
				}
				candidate.ID = id
				rerender(t, ctx, &candidate)
				r, e := store.Create(ctx, contextengine.CreateSnapshotCommand{Snapshot: candidate, Now: candidate.CreatedAt})
				if e != nil {
					errs <- e
					return
				}
				success.Add(1)
				if r.Reused {
					reused.Add(1)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Errorf("concurrent create: %v", e)
		}
		if success.Load() != workers || reused.Load() != workers-1 {
			t.Fatalf("success=%d reused=%d", success.Load(), reused.Load())
		}
		assertCount(t, ctx, platform, `SELECT count(*) FROM context_snapshots WHERE organization_id=$1 AND idempotency_key='concurrent'`, integrationOrganization, 1)
		conflict := base
		conflict.ID, _ = store.AllocateID(ctx)
		conflict.RequestHash = contextengine.DigestMarkdown([]byte("different"))
		rerender(t, ctx, &conflict)
		if _, e := store.Create(ctx, contextengine.CreateSnapshotCommand{Snapshot: conflict, Now: conflict.CreatedAt}); !errors.Is(e, contextengine.ErrIdempotencyConflict) {
			t.Fatalf("conflict=%v", e)
		}
	})

	t.Run("invalidated is terminal and exact invalidation is idempotent", func(t *testing.T) {
		snapshot := validSnapshot(t, ctx, store, "invalidate")
		if _, err := store.Create(ctx, contextengine.CreateSnapshotCommand{Snapshot: snapshot, Now: snapshot.CreatedAt}); err != nil {
			t.Fatal(err)
		}
		command := contextengine.InvalidateCommand{SnapshotID: snapshot.ID, ActorRoleID: "ingenieria_ia/qa", Reason: "superseded", CorrelationID: "corr", CausationID: "cause"}
		now := snapshot.CreatedAt.Add(time.Minute)
		value, reused, err := store.Invalidate(ctx, command, now)
		if err != nil || reused || value.Status != contextengine.SnapshotInvalidated {
			t.Fatalf("value=%+v reused=%v err=%v", value, reused, err)
		}
		_, reused, err = store.Invalidate(ctx, command, now)
		if err != nil || !reused {
			t.Fatalf("retry reused=%v err=%v", reused, err)
		}
		command.Reason = "different"
		if _, _, err = store.Invalidate(ctx, command, now); !errors.Is(err, contextengine.ErrSnapshotInvalidated) {
			t.Fatalf("terminal err=%v", err)
		}
		assertCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE subject_type='context_snapshot' AND subject_id=$1 AND event_type='context.snapshot_invalidated'`, fmt.Sprint(snapshot.ID), 1)
	})

	t.Run("database rejects clinical secret invalid capability and segment mutation", func(t *testing.T) {
		snapshot := validSnapshot(t, ctx, store, "constraints")
		if _, err := store.Create(ctx, contextengine.CreateSnapshotCommand{Snapshot: snapshot, Now: snapshot.CreatedAt}); err != nil {
			t.Fatal(err)
		}
		for _, dataClass := range []string{"clinical", "secret"} {
			_, err := platform.Pool().Exec(ctx, segmentInsertSQL(), snapshot.ID, snapshot.OrganizationID, 2, 2, 6, "rag_evidence", "rag_evidence", "bad-"+dataClass, "v1", "data", "untrusted", dataClass, false, true, nil, contextengine.DigestMarkdown([]byte("x")), 1, []byte("x"), snapshot.CreatedAt)
			if err == nil {
				t.Fatalf("%s persisted", dataClass)
			}
		}
		_, err := platform.Pool().Exec(ctx, segmentInsertSQL(), snapshot.ID, snapshot.OrganizationID, 2, 2, 5, "task_context", "task_context", "bad-grant", "v1", "scoped_instruction", "scoped", "organizational", true, true, nil, contextengine.DigestMarkdown([]byte("x")), 1, []byte("x"), snapshot.CreatedAt)
		if err == nil {
			t.Fatal("task may_grant_capabilities persisted")
		}
		if _, err = platform.Pool().Exec(ctx, `UPDATE context_segments SET source_version='v2' WHERE snapshot_id=$1`, snapshot.ID); err == nil {
			t.Fatal("append-only segment was updated")
		}
		if _, err = platform.Pool().Exec(ctx, `DELETE FROM context_segments WHERE snapshot_id=$1`, snapshot.ID); err == nil {
			t.Fatal("append-only segment was deleted")
		}
	})

	t.Run("audit and outbox failures roll back snapshots and segments", func(t *testing.T) {
		for _, target := range []string{"audit", "outbox"} {
			t.Run(target, func(t *testing.T) {
				function := "fail_context_" + target
				table := "audit_events"
				condition := "NEW.subject_type = 'context_snapshot'"
				if target == "outbox" {
					table = "outbox_events"
					condition = "NEW.aggregate_type = 'context_snapshot'"
				}
				_, err := platform.Pool().Exec(ctx, fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %s THEN RAISE EXCEPTION 'forced %s failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s_trigger BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION %s();`, function, condition, target, function, table, function))
				if err != nil {
					t.Fatal(err)
				}
				snapshot := validSnapshot(t, ctx, store, "rollback-"+target)
				_, createErr := store.Create(ctx, contextengine.CreateSnapshotCommand{Snapshot: snapshot, Now: snapshot.CreatedAt})
				if createErr == nil {
					t.Fatal("expected forced failure")
				}
				var rollbackCount int
				if err = platform.Pool().QueryRow(ctx, `SELECT count(*) FROM context_snapshots WHERE organization_id=$1 AND idempotency_key=$2`, integrationOrganization, "rollback-"+target).Scan(&rollbackCount); err != nil || rollbackCount != 0 {
					t.Fatalf("rollback snapshot count=%d err=%v", rollbackCount, err)
				}
				if _, err = platform.Pool().Exec(ctx, fmt.Sprintf(`DROP TRIGGER %s_trigger ON %s; DROP FUNCTION %s();`, function, table, function)); err != nil {
					t.Fatal(err)
				}
			})
		}
	})

	t.Run("service detects revision and skill state drift with real PostgreSQL", func(t *testing.T) {
		root := writeSourceFixture(t)
		loader, err := document.NewLoader(root, 65536)
		if err != nil {
			t.Fatal(err)
		}
		registryRepo, err := registry.NewPostgresRepository(platform)
		if err != nil {
			t.Fatal(err)
		}
		mutableSkills := &skillFixture{record: contextengine.SkillRecord{ID: "test-skill", RoleID: "ingenieria_ia/qa", Department: "ingenieria_ia", MemoryDomain: "ingenieria_ia", Lifecycle: contextengine.SkillActive, Assigned: true, Path: "ingenieria_ia/qa/skills/test-skill/SKILL.md", Version: "v1"}}
		doc, err := loader.Load(ctx, mutableSkills.record.Path, 65536)
		if err != nil {
			t.Fatal(err)
		}
		mutableSkills.record.SourceHash = doc.Hash
		canonical := testCanonical{bundle: contextengine.CanonicalBundle{PrecedenceHash: contextengine.DigestMarkdown([]byte("p")), BundleHash: contextengine.DigestMarkdown([]byte("b")), Sources: []contextengine.CanonicalSource{{LogicalName: "docs/canonical/cell-boundaries.yaml", Version: "v1", Tier: contextengine.TierImmutableSafety, InstructionClass: contextengine.InstructionImmutableConstraint, TrustClass: contextengine.TrustImmutable, DataClass: contextengine.DataOrganizational, Content: []byte("safety"), ContentHash: contextengine.DigestMarkdown([]byte("safety")), SemanticHash: contextengine.DigestMarkdown([]byte("safety"))}}}}
		service, err := contextengine.NewService(contextengine.ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 65536, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 8, MaxRAGSegments: 8}, registryRepo, loader, canonical, contextengine.NoopOwnerConstraintProvider{}, contextengine.UnavailableMemoryProvider{}, mutableSkills, contextengine.UnavailableProjectProvider{}, contextengine.UnavailableTaskProvider{}, contextengine.UnavailableRAGProvider{}, contextengine.NewAssembler(), contextengine.NewRenderer(), store, integrationClock{time.Date(2026, 8, 5, 3, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		revision, err := registryRepo.GetCurrentRevision(ctx, integrationOrganization)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Build(ctx, contextengine.BuildRequest{OrganizationID: integrationOrganization, OrganizationRevisionID: revision.ID, ActorRoleID: "ingenieria_ia/qa", Purpose: "integration drift", RequestedSkillIDs: []string{"test-skill"}, IdempotencyKey: "service-drift"})
		if err != nil {
			t.Fatal(err)
		}
		mutableSkills.mu.Lock()
		mutableSkills.record.Lifecycle = contextengine.SkillSuspended
		mutableSkills.mu.Unlock()
		validation, err := service.Validate(ctx, result.Snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if validation.Valid || !hasDrift(validation, contextengine.ReasonSkillStateDrift) {
			t.Fatalf("skill validation=%+v", validation)
		}
		mutableSkills.mu.Lock()
		mutableSkills.record.Lifecycle = contextengine.SkillActive
		mutableSkills.mu.Unlock()
		newRevisionID := revision.ID + 1000
		_, err = platform.Pool().Exec(ctx, `INSERT INTO organization_registry_revisions(id,canonical_hash,status,schema_versions,document_hashes,counts,diff,applied_at) OVERRIDING SYSTEM VALUE VALUES($1,$2,'applied','{}','{}','{}','{}',NOW())`, newRevisionID, contextengine.DigestMarkdown([]byte("new revision")))
		if err != nil {
			t.Fatal(err)
		}
		_, err = platform.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$1 WHERE id=$2`, newRevisionID, integrationOrganization)
		if err != nil {
			t.Fatal(err)
		}
		validation, err = service.Validate(ctx, result.Snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if validation.Valid || !hasDrift(validation, contextengine.ReasonRevisionMismatch) {
			t.Fatalf("revision validation=%+v", validation)
		}
		_, err = platform.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$1 WHERE id=$2`, revision.ID, integrationOrganization)
		if err != nil {
			t.Fatal(err)
		}
		_, err = platform.Pool().Exec(ctx, `DELETE FROM organization_registry_revisions WHERE id=$1`, newRevisionID)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("down migration and reapply in disposable integration database", func(t *testing.T) {
		loaded, err := platformmigrations.Load(rootmigrations.Files)
		if err != nil {
			t.Fatal(err)
		}
		rolledBack := 0
		for index := len(loaded) - 1; index >= 0; index-- {
			migration := loaded[index]
			if migration.Version < 6 {
				break
			}
			if _, err = platform.Pool().Exec(ctx, migration.DownSQL); err != nil {
				t.Fatalf("down migration %d (%s): %v", migration.Version, migration.Name, err)
			}
			if _, err = platform.Pool().Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, migration.Version); err != nil {
				t.Fatal(err)
			}
			rolledBack++
		}
		reapplied, err := runner.Up(ctx)
		if err != nil {
			t.Fatalf("reapply migration: %v", err)
		}
		if len(reapplied.Applied) != rolledBack || reapplied.Current != loaded[len(loaded)-1].Version {
			t.Fatalf("reapplied=%+v rolled_back=%d", reapplied, rolledBack)
		}
		var exists bool
		if err = platform.Pool().QueryRow(ctx, `SELECT to_regclass('public.context_snapshots') IS NOT NULL AND to_regclass('public.context_segments') IS NOT NULL`).Scan(&exists); err != nil || !exists {
			t.Fatalf("reapply exists=%v err=%v", exists, err)
		}
	})
}

func openStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "context-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func resetSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `TRUNCATE context_segments,context_snapshots,authorization_uses,authorization_decisions,authorization_requests,staging_events,staging_reviews,staging_promotions,staging_checks,staging_workspace_artifacts,staging_artifacts,staging_workspaces,outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,task_requirements,task_dependencies,tasks,organization_reporting_lines,organization_registry_revision_documents,organization_roles,organizational_units,organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}
func syncCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, integrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
}

func validSnapshot(t *testing.T, ctx context.Context, store *contextpostgres.Store, key string) contextengine.Snapshot {
	t.Helper()
	id, err := store.AllocateID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("profile content")
	segment := contextengine.Segment{Ordinal: 1, RenderOrdinal: 1, AuthorityPriority: 4, AuthorityTier: contextengine.TierRoleProfile, SourceKind: contextengine.SourceRoleProfile, SourceReference: "ingenieria_ia/qa/PERFIL.md", SourceVersion: "v1", InstructionClass: contextengine.InstructionRole, TrustClass: contextengine.TrustAuthoritative, DataClass: contextengine.DataOrganizational, Included: true, ContentHash: contextengine.DigestMarkdown(content), ByteCount: len(content), Content: content}
	snapshot := contextengine.Snapshot{ID: id, OrganizationID: integrationOrganization, OrganizationRevisionID: 1, ActorRoleID: "ingenieria_ia/qa", Purpose: "integration", IdempotencyKey: key, Status: contextengine.SnapshotReady, Version: 1, RequestHash: contextengine.DigestMarkdown([]byte("request-" + key)), PrecedenceHash: contextengine.DigestMarkdown([]byte("precedence")), CanonicalBundleHash: contextengine.DigestMarkdown([]byte("bundle")), SegmentCount: 1, IncludedSegmentCount: 1, TotalBytes: int64(len(content)), CorrelationID: "correlation", CausationID: "causation", Segments: []contextengine.Segment{segment}, CreatedAt: time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)}
	rerender(t, ctx, &snapshot)
	return snapshot
}
func rerender(t *testing.T, ctx context.Context, snapshot *contextengine.Snapshot) {
	t.Helper()
	body, err := contextengine.NewRenderer().Render(ctx, *snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.RenderedHash = contextengine.DigestCanonicalBytes(body)
}
func segmentInsertSQL() string {
	return `INSERT INTO context_segments(snapshot_id,organization_id,ordinal,render_ordinal,authority_priority,authority_tier,source_kind,source_reference,source_version,instruction_class,trust_class,data_class,may_grant_capabilities,included,omission_reason,content_hash,byte_count,content,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`
}
func assertCount(t *testing.T, ctx context.Context, store *platformpostgres.Store, query string, arg any, want int) {
	t.Helper()
	var count int
	if err := store.Pool().QueryRow(ctx, query, arg).Scan(&count); err != nil || count != want {
		t.Fatalf("count=%d want=%d err=%v", count, want, err)
	}
}

func writeSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"AGENT.md": "# Organization agent\n", "ingenieria_ia/AGENT.md": "# Department agent\n", "ingenieria_ia/qa/PERFIL.md": "---\ndepartamento: ingenieria_ia\nrol: qa\ndominio_memoria: ingenieria_ia\nagente_base: true\n---\n# QA profile\n", "ingenieria_ia/qa/skills/test-skill/SKILL.md": "---\nname: test-skill\ndescription: Test active skill for PostgreSQL integration validation.\ndepartamento: ingenieria_ia\nrol: qa\ndominio_memoria: ingenieria_ia\nverificador: null\norigen: interno\nprotocolo_base: verificacion_estado\n---\n# Test skill\nPerform deterministic verification.\n"}
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type testCanonical struct{ bundle contextengine.CanonicalBundle }

func (c testCanonical) Load(context.Context) (contextengine.CanonicalBundle, error) {
	return c.bundle, nil
}
func (c testCanonical) Validate(_ context.Context, p, b string) error {
	if p != c.bundle.PrecedenceHash {
		return contextengine.Reject(contextengine.ReasonPrecedenceHashMismatch, "p", "drift")
	}
	if b != c.bundle.BundleHash {
		return contextengine.Reject(contextengine.ReasonCanonicalBundleDrift, "b", "drift")
	}
	return nil
}

type integrationClock struct{ now time.Time }

func (c integrationClock) Now() time.Time { return c.now }

type skillFixture struct {
	mu     sync.Mutex
	record contextengine.SkillRecord
}

func (s *skillFixture) ListActiveForRole(_ context.Context, _, role string) ([]contextengine.SkillRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.RoleID == role && s.record.Lifecycle == contextengine.SkillActive && s.record.Assigned {
		return []contextengine.SkillRecord{s.record}, nil
	}
	return nil, nil
}
func (s *skillFixture) GetActiveForRole(_ context.Context, _, role, id string) (contextengine.SkillRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.ID != id {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotFound, id, "not found")
	}
	if s.record.Lifecycle != contextengine.SkillActive {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotActive, id, "not active")
	}
	if !s.record.Assigned || s.record.RoleID != role {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotAssigned, id, "not assigned")
	}
	return s.record, nil
}
func (s *skillFixture) ValidateVersion(_ context.Context, expected contextengine.SkillRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.Lifecycle != contextengine.SkillActive || !s.record.Assigned {
		return contextengine.Reject(contextengine.ReasonSkillStateDrift, expected.ID, "state changed")
	}
	if s.record.SourceHash != expected.SourceHash {
		return contextengine.Reject(contextengine.ReasonSkillSourceDrift, expected.ID, "source changed")
	}
	return nil
}
func hasDrift(value contextengine.SnapshotValidation, code contextengine.ReasonCode) bool {
	for _, item := range value.Drift {
		if item.ReasonCode == string(code) {
			return true
		}
	}
	return false
}
