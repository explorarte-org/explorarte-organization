//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextpostgres "github.com/Mireuz13/explorarte-organization/internal/contextengine/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const integrationOrganization = "explorarte"

// TestExecutionContextViewPostgreSQL17 proves the durable
// ExecutionContextView store's real-database behavior: restart/reload
// (F), immutability enforced by the database itself, not only by the Go
// integrity check (part of E), and cross-organization/wrong-snapshot binding
// fails closed at the FK level (J). Idempotency/drift against real
// PostgreSQL (C/D) are also exercised here since MemoryStore's equivalent
// unit tests (internal/contextcompiler) cannot prove the real UNIQUE
// constraint/ON CONFLICT path.
func TestExecutionContextViewPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	platformStore := openStore(t, ctx)
	defer platformStore.Close()
	runner, err := platformmigrations.New(platformStore.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	resetSchema(t, ctx, platformStore)
	syncCanonical(t, ctx, platformStore)

	contextStore, err := contextpostgres.New(platformStore)
	if err != nil {
		t.Fatal(err)
	}
	viewStore, err := contextcompilerpostgres.New(platformStore)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := createSnapshot(t, ctx, contextStore, "empresa/ceo", "ecv-generic")
	assembly := contextcompiler.ContextAssemblyService{Store: viewStore}

	t.Run("idempotent persist against real PostgreSQL", func(t *testing.T) {
		first, err := assembly.ResolveAndPersist(ctx, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		second, err := assembly.ResolveAndPersist(ctx, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if first.ID != second.ID {
			t.Fatalf("resolving twice created two durable rows: %d != %d", first.ID, second.ID)
		}
	})

	t.Run("drift fails closed against real PostgreSQL", func(t *testing.T) {
		other := createSnapshot(t, ctx, contextStore, "empresa/ceo", "ecv-drift")
		base := contextcompiler.ExecutionContextView{
			OrganizationID: integrationOrganization, ContextSnapshotID: other.ID,
			FellBackToCanonical: true, FallbackReason: "task_class_not_projected",
			AuthorityOrderHash: sha256Hex(t, "order"), CompiledContentHash: sha256Hex(t, "content"),
			ProviderVisibleBytes: []byte("original bytes"), ProviderVisibleDigest: sha256HexBytes([]byte("original bytes")), ProviderVisibleByteCount: len("original bytes"),
		}
		if _, err := viewStore.Persist(ctx, base); err != nil {
			t.Fatal(err)
		}
		drifted := base
		drifted.ProviderVisibleBytes = []byte("different bytes entirely")
		drifted.ProviderVisibleDigest = sha256HexBytes(drifted.ProviderVisibleBytes)
		drifted.ProviderVisibleByteCount = len(drifted.ProviderVisibleBytes)
		if _, err := viewStore.Persist(ctx, drifted); err == nil {
			t.Fatal("expected drift to be rejected")
		} else if !isDrift(err) {
			t.Fatalf("expected ErrExecutionContextViewDrift, got %v", err)
		}
		existing, err := viewStore.GetByContextSnapshot(ctx, integrationOrganization, other.ID)
		if err != nil {
			t.Fatal(err)
		}
		if string(existing.ProviderVisibleBytes) != "original bytes" {
			t.Fatalf("drift attempt corrupted the existing row: %s", existing.ProviderVisibleBytes)
		}
	})

	t.Run("metadata-only drift fails closed against real PostgreSQL", func(t *testing.T) {
		baseFor := func(snapshotID int64) contextcompiler.ExecutionContextView {
			sameBytes := []byte("identical provider-visible bytes")
			return contextcompiler.ExecutionContextView{
				OrganizationID: integrationOrganization, ContextSnapshotID: snapshotID,
				ContextProfileID: "research.corpus_curate", ContextProfileVersion: "v1",
				FellBackToCanonical: false, ProviderRenderVersion: "research-corpus-curate-render/v2",
				StablePrefixHash: "s1", StablePrefixBytes: 10, DynamicSuffixHash: "d1", DynamicSuffixBytes: 20,
				AuthorityOrderHash: sha256Hex(t, "order-metadata"), CompiledContentHash: sha256Hex(t, "content-metadata"),
				SegmentDiffs:         []contextcompiler.SegmentDiff{{SourceReference: "docs/canonical/role-catalog.yaml", Projected: true, Reason: "projected_subset:role_catalog_self_entry"}},
				ProviderVisibleBytes: sameBytes, ProviderVisibleDigest: sha256HexBytes(sameBytes), ProviderVisibleByteCount: len(sameBytes),
			}
		}
		// Bytes/digest are held constant throughout every case below --
		// this is exactly the gap the independent review found: a
		// bytes/digest-only comparison would silently accept every one of
		// these as idempotent.
		mutations := map[string]func(v *contextcompiler.ExecutionContextView){
			"context_profile_id":      func(v *contextcompiler.ExecutionContextView) { v.ContextProfileID = "some.other.profile" },
			"context_profile_version": func(v *contextcompiler.ExecutionContextView) { v.ContextProfileVersion = "v2" },
			"compiled_content_hash": func(v *contextcompiler.ExecutionContextView) {
				v.CompiledContentHash = sha256Hex(t, "different-content-hash")
			},
			"authority_order_hash_and_segment_diffs": func(v *contextcompiler.ExecutionContextView) {
				v.AuthorityOrderHash = sha256Hex(t, "a-completely-different-order-hash")
				v.SegmentDiffs = []contextcompiler.SegmentDiff{{SourceReference: "different-reference", Projected: false}}
			},
			"fallback_reason":         func(v *contextcompiler.ExecutionContextView) { v.FallbackReason = "changed" },
			"provider_render_version": func(v *contextcompiler.ExecutionContextView) { v.ProviderRenderVersion = "changed/v3" },
		}

		for name, mutate := range mutations {
			t.Run(name, func(t *testing.T) {
				extraSnap := createSnapshot(t, ctx, contextStore, "empresa/ceo", "ecv-metadata-drift-"+name)
				base := baseFor(extraSnap.ID)
				if _, err := viewStore.Persist(ctx, base); err != nil {
					t.Fatal(err)
				}
				drifted := base
				mutate(&drifted)
				if _, err := viewStore.Persist(ctx, drifted); err == nil {
					t.Fatalf("expected metadata-only drift (%s) to be rejected", name)
				} else if !isDrift(err) {
					t.Fatalf("expected ErrExecutionContextViewDrift for %s, got %v", name, err)
				}
				existing, err := viewStore.GetByContextSnapshot(ctx, integrationOrganization, extraSnap.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !contextcompiler.SameLogicalView(existing, base) {
					t.Fatalf("metadata-only drift attempt (%s) corrupted the existing row", name)
				}

				// A truly identical second Persist (fresh slice backing
				// arrays, same content) must still be idempotent.
				identicalCopy := base
				identicalCopy.SegmentDiffs = append([]contextcompiler.SegmentDiff(nil), base.SegmentDiffs...)
				identicalCopy.ProviderVisibleBytes = append([]byte(nil), base.ProviderVisibleBytes...)
				reidempotent, err := viewStore.Persist(ctx, identicalCopy)
				if err != nil {
					t.Fatalf("truly identical persist must remain idempotent (%s), got: %v", name, err)
				}
				if reidempotent.ID != existing.ID {
					t.Fatalf("truly identical persist returned a different ID for %s: %d != %d", name, reidempotent.ID, existing.ID)
				}
			})
		}
	})

	t.Run("invalid byte count rejected by Go before touching PostgreSQL", func(t *testing.T) {
		snap := createSnapshot(t, ctx, contextStore, "empresa/ceo", "ecv-invalid-byte-count")
		invalid := contextcompiler.ExecutionContextView{
			OrganizationID: integrationOrganization, ContextSnapshotID: snap.ID,
			FellBackToCanonical: true, FallbackReason: "task_class_not_projected",
			AuthorityOrderHash: sha256Hex(t, "order-bytecount"), CompiledContentHash: sha256Hex(t, "content-bytecount"),
			ProviderVisibleBytes: []byte("hello"), ProviderVisibleDigest: sha256HexBytes([]byte("hello")),
			ProviderVisibleByteCount: 999, // wrong on purpose: len("hello") == 5
		}
		_, err := viewStore.Persist(ctx, invalid)
		if !isIntegrity(err) {
			t.Fatalf("want ErrExecutionContextViewIntegrity rejected in Go, got %v", err)
		}
		if _, getErr := viewStore.GetByContextSnapshot(ctx, integrationOrganization, snap.ID); !errors.Is(getErr, contextcompiler.ErrExecutionContextViewNotFound) {
			t.Fatalf("a Go-rejected persist must never have reached PostgreSQL, got %v", getErr)
		}

		valid := invalid
		valid.ProviderVisibleByteCount = len(valid.ProviderVisibleBytes)
		if _, err := viewStore.Persist(ctx, valid); err != nil {
			t.Fatalf("a view with a correct byte count must persist normally: %v", err)
		}
	})

	t.Run("restart/reload reproduces identity, bytes, digest, and metadata", func(t *testing.T) {
		researchSnapshot := createSnapshot(t, ctx, contextStore, "investigacion/research_worker_hourly", "ecv-research")
		persisted, err := assembly.ResolveAndPersist(ctx, researchSnapshot)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.FellBackToCanonical {
			t.Fatal("expected the research profile to project, not fall back")
		}

		// Simulate a process restart: a brand-new Store bound to the same
		// PostgreSQL connection, no shared in-memory state whatsoever.
		freshStore, err := contextcompilerpostgres.New(platformStore)
		if err != nil {
			t.Fatal(err)
		}
		reloadedByID, err := freshStore.Get(ctx, persisted.ID)
		if err != nil {
			t.Fatal(err)
		}
		reloadedBySnapshot, err := freshStore.GetByContextSnapshot(ctx, integrationOrganization, researchSnapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		for name, reloaded := range map[string]contextcompiler.ExecutionContextView{"by_id": reloadedByID, "by_snapshot": reloadedBySnapshot} {
			if reloaded.ID != persisted.ID {
				t.Fatalf("%s: view identity did not survive reload: %d != %d", name, reloaded.ID, persisted.ID)
			}
			if reloaded.ContextSnapshotID != persisted.ContextSnapshotID {
				t.Fatalf("%s: canonical snapshot reference did not survive reload", name)
			}
			if reloaded.ContextProfileID != persisted.ContextProfileID || reloaded.ContextProfileVersion != persisted.ContextProfileVersion {
				t.Fatalf("%s: profile identity did not survive reload", name)
			}
			if string(reloaded.ProviderVisibleBytes) != string(persisted.ProviderVisibleBytes) {
				t.Fatalf("%s: bytes did not survive reload", name)
			}
			if reloaded.ProviderVisibleDigest != persisted.ProviderVisibleDigest {
				t.Fatalf("%s: digest did not survive reload", name)
			}
			if len(reloaded.SegmentDiffs) != len(persisted.SegmentDiffs) {
				t.Fatalf("%s: segment diffs did not survive reload", name)
			}
			if reloaded.StablePrefixHash != persisted.StablePrefixHash || reloaded.DynamicSuffixHash != persisted.DynamicSuffixHash {
				t.Fatalf("%s: render metadata did not survive reload", name)
			}
		}
	})

	t.Run("database rejects direct tampering: immutability trigger", func(t *testing.T) {
		snap := createSnapshot(t, ctx, contextStore, "empresa/ceo", "ecv-immutable")
		view, err := assembly.ResolveAndPersist(ctx, snap)
		if err != nil {
			t.Fatal(err)
		}
		_, updateErr := platformStore.Pool().Exec(ctx, `UPDATE execution_context_views SET provider_visible_bytes = $1 WHERE id = $2`, []byte("tampered"), view.ID)
		if updateErr == nil {
			t.Fatal("expected the immutability trigger to reject a direct UPDATE")
		}
		_, deleteErr := platformStore.Pool().Exec(ctx, `DELETE FROM execution_context_views WHERE id = $1`, view.ID)
		if deleteErr == nil {
			t.Fatal("expected the immutability trigger to reject a direct DELETE")
		}
		// The row must still read back exactly as originally persisted.
		reread, err := viewStore.Get(ctx, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if string(reread.ProviderVisibleBytes) != string(view.ProviderVisibleBytes) {
			t.Fatal("row content changed despite the rejected UPDATE")
		}
	})

	t.Run("cross-organization binding fails closed", func(t *testing.T) {
		snap := createSnapshot(t, ctx, contextStore, "empresa/ceo", "ecv-crossorg")
		wrongOrg := contextcompiler.ExecutionContextView{
			OrganizationID: "some-other-organization", ContextSnapshotID: snap.ID,
			FellBackToCanonical: true, FallbackReason: "task_class_not_projected",
			AuthorityOrderHash: sha256Hex(t, "order2"), CompiledContentHash: sha256Hex(t, "content2"),
			ProviderVisibleBytes: []byte("bytes"), ProviderVisibleDigest: sha256HexBytes([]byte("bytes")), ProviderVisibleByteCount: len("bytes"),
		}
		if _, err := viewStore.Persist(ctx, wrongOrg); err == nil {
			t.Fatal("expected a foreign-key rejection for a snapshot/organization mismatch")
		}
	})
}

func openStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 20, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "execution-context-view-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	_, err := store.Pool().Exec(ctx, `TRUNCATE execution_context_views,context_segments,context_snapshots,organization_reporting_lines,organization_registry_revision_documents,organization_roles,organizational_units,organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE`)
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

// createSnapshot inserts a minimal, valid canonical context_snapshots row
// directly (mirroring internal/contextengine/postgres's own
// validSnapshot/Create integration-test pattern) rather than exercising the
// full Context Engine Build pipeline, which is already covered by
// contextengine's own integration suite. What this test needs is a real,
// FK-satisfying canonical snapshot to bind ExecutionContextView rows to.
func createSnapshot(t *testing.T, ctx context.Context, store *contextpostgres.Store, actorRoleID, idempotencyKey string) contextengine.Snapshot {
	t.Helper()
	id, err := store.AllocateID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	roleCatalog := []byte(`schema_version: 0.1.0
document_status: branch_0_candidate
roles:
- id: empresa/ceo
  department: empresa
  summary: CEO summary text irrelevant to corpus curation.
- id: investigacion/research_worker_hourly
  department: investigacion
  summary: Research worker contract.
`)
	segments := []contextengine.Segment{
		{Ordinal: 1, RenderOrdinal: 1, AuthorityPriority: 0, AuthorityTier: contextengine.TierImmutableSafety, SourceKind: contextengine.SourceCanonicalDocument, SourceReference: "docs/canonical/cell-boundaries.yaml", SourceVersion: "v1", InstructionClass: contextengine.InstructionImmutableConstraint, TrustClass: contextengine.TrustImmutable, DataClass: contextengine.DataOrganizational, Included: true, Content: []byte("safety"), ByteCount: 6, ContentHash: contextengine.DigestMarkdown([]byte("safety"))},
		{Ordinal: 2, RenderOrdinal: 2, AuthorityPriority: 1, AuthorityTier: contextengine.TierOwnerDecisions, SourceKind: contextengine.SourceOwnerConstraint, SourceReference: "docs/canonical/decisions-required.yaml", SourceVersion: "v1", InstructionClass: contextengine.InstructionOwnerConstraint, TrustClass: contextengine.TrustAuthoritative, DataClass: contextengine.DataOrganizational, Included: true, Content: []byte("owner"), ByteCount: 5, ContentHash: contextengine.DigestMarkdown([]byte("owner"))},
		{Ordinal: 3, RenderOrdinal: 3, AuthorityPriority: 2, AuthorityTier: contextengine.TierCanonicalPolicies, SourceKind: contextengine.SourceCanonicalDocument, SourceReference: "docs/canonical/role-catalog.yaml", SourceVersion: "v1", InstructionClass: contextengine.InstructionCanonicalPolicy, TrustClass: contextengine.TrustAuthoritative, DataClass: contextengine.DataOrganizational, Included: true, Content: roleCatalog, ByteCount: len(roleCatalog), ContentHash: contextengine.DigestMarkdown(roleCatalog)},
		{Ordinal: 4, RenderOrdinal: 4, AuthorityPriority: 3, AuthorityTier: contextengine.TierOrganizationAgent, SourceKind: contextengine.SourceOrganizationAgent, SourceReference: "AGENT.md", SourceVersion: "v1", InstructionClass: contextengine.InstructionOrganizational, TrustClass: contextengine.TrustAuthoritative, DataClass: contextengine.DataOrganizational, Included: true, Content: []byte("org"), ByteCount: 3, ContentHash: contextengine.DigestMarkdown([]byte("org"))},
		{Ordinal: 5, RenderOrdinal: 5, AuthorityPriority: 3, AuthorityTier: contextengine.TierDepartmentAgent, SourceKind: contextengine.SourceDepartmentAgent, SourceReference: "investigacion/AGENT.md", SourceVersion: "v1", InstructionClass: contextengine.InstructionOrganizational, TrustClass: contextengine.TrustAuthoritative, DataClass: contextengine.DataOrganizational, Included: true, Content: []byte("dept"), ByteCount: 4, ContentHash: contextengine.DigestMarkdown([]byte("dept"))},
		{Ordinal: 6, RenderOrdinal: 6, AuthorityPriority: 4, AuthorityTier: contextengine.TierRoleProfile, SourceKind: contextengine.SourceRoleProfile, SourceReference: actorRoleID + "/PERFIL.md", SourceVersion: "v1", InstructionClass: contextengine.InstructionRole, TrustClass: contextengine.TrustAuthoritative, DataClass: contextengine.DataOrganizational, Included: true, Content: []byte("perfil"), ByteCount: 6, ContentHash: contextengine.DigestMarkdown([]byte("perfil"))},
		{Ordinal: 7, RenderOrdinal: 7, AuthorityPriority: 5, AuthorityTier: contextengine.TierTask, SourceKind: contextengine.SourceTaskContext, SourceReference: "task:1", SourceVersion: "v1", InstructionClass: contextengine.InstructionData, TrustClass: contextengine.TrustScoped, DataClass: contextengine.DataOrganizational, Included: true, Content: []byte("task payload"), ByteCount: 12, ContentHash: contextengine.DigestMarkdown([]byte("task payload"))},
	}
	total := int64(0)
	for _, s := range segments {
		total += int64(s.ByteCount)
	}
	snapshot := contextengine.Snapshot{
		ID: id, OrganizationID: integrationOrganization, OrganizationRevisionID: 1, ActorRoleID: actorRoleID,
		Purpose: "integration", IdempotencyKey: idempotencyKey, Status: contextengine.SnapshotReady, Version: 1,
		RequestHash: sha256Hex(t, "request-"+idempotencyKey), PrecedenceHash: sha256Hex(t, "precedence-"+idempotencyKey), CanonicalBundleHash: sha256Hex(t, "bundle-"+idempotencyKey),
		SegmentCount: len(segments), IncludedSegmentCount: len(segments), TotalBytes: total,
		CorrelationID: "correlation", CausationID: "causation", Segments: segments, CreatedAt: time.Now().UTC(),
	}
	rendered, err := contextengine.NewRenderer().Render(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.RenderedHash = contextengine.DigestCanonicalBytes(rendered)
	result, err := store.Create(ctx, contextengine.CreateSnapshotCommand{Snapshot: snapshot, Now: snapshot.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	return result.Snapshot
}

func sha256Hex(t *testing.T, seed string) string {
	t.Helper()
	return sha256HexBytes([]byte(seed))
}

func sha256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func isDrift(err error) bool {
	return errors.Is(err, contextcompiler.ErrExecutionContextViewDrift)
}

func isIntegrity(err error) bool {
	return errors.Is(err, contextcompiler.ErrExecutionContextViewIntegrity)
}
