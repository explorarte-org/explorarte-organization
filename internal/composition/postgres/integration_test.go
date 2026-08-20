//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/composition"
	compositionpostgres "github.com/Mireuz13/explorarte-organization/internal/composition/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func TestCompositionStorePostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	platform := openCompositionStore(t, ctx)
	t.Cleanup(platform.Close)

	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	// TRUNCATE is data loss, so the DSN naming the disposable database is
	// not sufficient authorization on its own: the sentinel proves somebody
	// separately decided this run may destroy data.
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), platform.Pool()); err != nil {
		t.Skipf("refusing to truncate without explicit authorization: %v", err)
	}
	if _, err := platform.Pool().Exec(ctx, `TRUNCATE composition_bindings, composition_episodes`); err != nil {
		t.Fatal(err)
	}

	store, err := compositionpostgres.New(platform.Pool())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease := now.Add(time.Minute)

	for _, ep := range []struct {
		id, component string
	}{{"orgd-1", "runtime-orgd"}, {"ctl-1", "composition-controller"}} {
		if err := store.Start(ctx, composition.EpisodeID(ep.id), ep.component, lease); err != nil {
			t.Fatalf("start %s: %v", ep.id, err)
		}
	}

	activate := func(id composition.EpisodeID) {
		t.Helper()
		step := composition.Step{Kind: composition.StepActivate, Episode: id}
		if err := store.ApplyStep(ctx, step, composition.Reloading, now); err != nil {
			t.Fatalf("activate %s: %v", id, err)
		}
	}
	activate("orgd-1")
	activate("ctl-1")

	// The database enforces one active episode per component too, so no
	// path outside the domain can produce a state the domain refuses.
	if err := store.Start(ctx, "orgd-2", "runtime-orgd", lease); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyStep(ctx, composition.Step{Kind: composition.StepActivate, Episode: "orgd-2"}, composition.Reloading, now); err == nil {
		t.Fatal("the unique index must refuse a second active episode of one component")
	}

	binding := composition.CommittedBinding{Key: composition.KeyRuntimeObservedSHA, Consumer: "ctl-1", Provider: "orgd-1"}
	if err := store.Bind(ctx, binding); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Leave, then try to finish the teardown while the holder is alive.
	if err := store.ApplyStep(ctx, composition.Step{Kind: composition.StepLeave, Episode: "orgd-1"}, composition.Active, now); err != nil {
		t.Fatalf("leave: %v", err)
	}
	// An unloading provider must not take a new consumer, in SQL as well.
	if err := store.Bind(ctx, composition.CommittedBinding{Key: composition.KeyRuntimeSchemaCompatibility, Consumer: "orgd-2", Provider: "orgd-1"}); err == nil {
		t.Fatal("a provider on its way out must not acquire a new consumer")
	}
	if err := store.ApplyStep(ctx, composition.Step{Kind: composition.StepUnload, Episode: "orgd-1"}, composition.Unloading, now); err == nil {
		t.Fatal("the teardown gate must hold in SQL, not only in Go")
	}

	// The holder stops answering. The teardown becomes available -- and it
	// becomes available because of liveness, without the binding being
	// deleted to achieve it.
	dead := now.Add(2 * time.Minute)
	if err := store.Heartbeat(ctx, "ctl-1", dead.Add(time.Minute), dead); err == nil {
		t.Fatal("a lapsed lease must not be renewable")
	}
	if err := store.ApplyStep(ctx, composition.Step{Kind: composition.StepUnload, Episode: "orgd-1"}, composition.Unloading, dead); err != nil {
		t.Fatalf("unload after the holder lapsed: %v", err)
	}

	// A step computed against a state the world has left must fail rather
	// than overwrite whatever is there now.
	if err := store.ApplyStep(ctx, composition.Step{Kind: composition.StepUnload, Episode: "orgd-1"}, composition.Unloading, dead); err == nil {
		t.Fatal("a stale step must not be applied twice")
	}

	world, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if e, ok := world.Episode("orgd-1"); !ok || e.State != composition.Inactive {
		t.Fatalf("orgd-1 should be inactive, got %+v", e)
	}
	if got := world.Bindings(); len(got) != 1 || got[0] != binding {
		t.Fatalf("the binding must survive as evidence: %v", got)
	}
}

func openCompositionStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{
		URL: url, SSLMode: "disable", MaxConns: 10, MinConns: 0,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
		HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second,
		PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second,
		LockTimeout: 5 * time.Second, AutoMigrate: true,
		MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second,
	}
	store, err := platformpostgres.Open(ctx, cfg, "composition-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}
