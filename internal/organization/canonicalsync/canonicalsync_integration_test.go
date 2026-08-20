//go:build integration

package canonicalsync_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	egressbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelegress/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/canonicalsync"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// The property is that two durable facts in two different tables move
// together. Only a real database can show that, because the failure was
// precisely that one table advanced and the other did not, and every
// individual operation was correct in isolation.
func TestApplyLeavesTheCurrentRevisionBoundPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required for integration tests")
	}
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL,
			"ORG_DATABASE_MAX_CONNS": "8", "ORG_DATABASE_MIN_CONNS": "0",
			"ORG_CANONICAL_DIR": canonicalDir,
		}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "canonicalsync-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repository, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(cfg.Registry.CanonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, repository, cfg.Tasks.OrganizationID, cfg.Registry.SyncTimeout)
	if err != nil {
		t.Fatal(err)
	}
	egressRuntime, err := egressbootstrap.Open(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	applier := canonicalsync.Applier{Registry: registryService, Egress: egressRuntime.Service}

	// Reach a known-applied state on the real canonical documents first.
	if _, err = applier.Apply(ctx, true); err != nil {
		t.Fatalf("reach the applied state: %v", err)
	}
	before := currentRevision(t, ctx, store, cfg.Tasks.OrganizationID)

	// Now reproduce the incident properly: a canonical change that makes the
	// registry create a NEW revision and make it current. That revision is
	// unbound the instant registry.Apply commits -- egress bindings are
	// immutable and per-revision, so nothing carries over -- and it is exactly
	// the state root 124 died in.
	//
	// Faking it by deleting a binding is not possible and should not be: the
	// database refuses with "model egress records are immutable". The only
	// honest way to produce an unbound current revision is to produce a new
	// revision, which is also the only way it happens in practice.
	// The fixture advances the durable canonical state of a shared disposable
	// database, so it puts it back. The revision chain is append-only, so
	// "back" means applying the real canonical documents again rather than
	// undoing anything -- which is also what an operator would do.
	t.Cleanup(func() {
		restore, cancelRestore := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancelRestore()
		if _, restoreErr := applier.Apply(restore, true); restoreErr != nil {
			t.Errorf("restore the real canonical state: %v", restoreErr)
		}
	})
	changedDir := canonicalCopyWithNewHash(t, cfg.Registry.CanonicalDir)
	changed, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL,
			"ORG_DATABASE_MAX_CONNS": "8", "ORG_DATABASE_MIN_CONNS": "0",
			"ORG_CANONICAL_DIR": changedDir,
		}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	changedLoader, err := registry.NewLoader(changed.Registry.CanonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	changedRegistry, err := registry.NewService(changedLoader, repository, changed.Tasks.OrganizationID, changed.Registry.SyncTimeout)
	if err != nil {
		t.Fatal(err)
	}
	changedEgress, err := egressbootstrap.Open(changed, store)
	if err != nil {
		t.Fatal(err)
	}
	changedApplier := canonicalsync.Applier{Registry: changedRegistry, Egress: changedEgress.Service}

	result, err := changedApplier.Apply(ctx, true)
	if err != nil {
		t.Fatalf("apply the changed canonical state: %v", err)
	}
	if !result.Registry.Applied {
		t.Fatalf("the fixture must actually create a new revision, got %+v", result.Registry)
	}
	revisionID := currentRevision(t, ctx, store, changed.Tasks.OrganizationID)
	if revisionID == before {
		t.Fatalf("the current revision did not advance from %d", before)
	}

	// The question that matters	// The question that matters is not "did the registry apply" but "can this
	// organization dispatch". They were the same question until a registry
	// sync started producing revisions nothing was bound to.
	var bound int
	if err = store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM model_egress_revision_bindings WHERE organization_id=$1 AND organization_revision_id=$2`,
		changed.Tasks.OrganizationID, revisionID,
	).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != 1 {
		t.Fatalf("revision %d is current with %d egress bindings: every dispatch under it would fail with \"model egress policy not found\"", revisionID, bound)
	}
	if _, err = changedApplier.Verify(ctx); err != nil {
		t.Fatalf("the newly current revision must verify as executable: %v", err)
	}

	// Re-applying is the operator repeating themselves, and must not move
	// anything or complain.
	if _, err = changedApplier.Apply(ctx, true); err != nil {
		t.Fatalf("re-applying the canonical state must be idempotent: %v", err)
	}
	if after := currentRevision(t, ctx, store, changed.Tasks.OrganizationID); after != revisionID {
		t.Fatalf("a repeated apply moved the current revision from %d to %d", revisionID, after)
	}
	if errors.Is(func() error { _, e := changedApplier.Verify(ctx); return e }(), canonicalsync.ErrRevisionUnbound) {
		t.Fatal("a repeated apply left the revision unbound")
	}
}

func currentRevision(t *testing.T, ctx context.Context, store *platformpostgres.Store, organizationID string) int64 {
	t.Helper()
	var revisionID int64
	if err := store.Pool().QueryRow(ctx,
		`SELECT COALESCE(current_revision_id,0) FROM organizations WHERE id=$1`, organizationID,
	).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	if revisionID == 0 {
		t.Fatal("the organization has no current revision")
	}
	return revisionID
}

// canonicalCopyWithNewHash copies the canonical documents and changes one
// value so the registry genuinely applies a new revision.
//
// It has to be a semantic change, not a comment: the canonical hash is
// computed over parsed content, so perturbing the bytes alone leaves the hash
// identical and the sync a no-op. The organization's display name is the
// smallest thing that is part of the snapshot, alters no structure, and
// validates unchanged.
func canonicalCopyWithNewHash(t *testing.T, source string) string {
	t.Helper()
	destination := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(source, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if entry.Name() == "organization.yaml" {
			replaced := bytes.Replace(body, []byte("display_name: Psi.Explorarte"), []byte("display_name: Psi.Explorarte Canonicalsync Fixture"), 1)
			if bytes.Equal(replaced, body) {
				t.Fatal("the fixture's anchor is gone from organization.yaml; pick another semantic field or this test silently stops creating a new revision")
			}
			body = replaced
		}
		if writeErr := os.WriteFile(filepath.Join(destination, entry.Name()), body, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	return destination
}
