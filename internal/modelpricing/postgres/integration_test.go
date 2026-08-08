//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func openPricingStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required for integration tests")
	}
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL, "ORG_DATABASE_MAX_CONNS": "4", "ORG_DATABASE_MIN_CONNS": "0"}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "modelpricing-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func TestModelPricingSeedIsRealAndResolvable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	platform := openPricingStore(t, ctx)
	store, err := modelpricingpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	service, err := modelpricing.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	tier, err := service.Resolve(ctx, "deepseek", "deepseek-v4-flash", 500, now)
	if err != nil {
		t.Fatal(err)
	}
	cost, err := tier.EstimateCost(0, 1_000_000, 0, 0)
	if err != nil || cost != 2_800_000 {
		t.Fatalf("deepseek-v4-flash cache-hit cost=%d err=%v want=2800000", cost, err)
	}

	short, err := service.Resolve(ctx, "openai_compatible", "gpt-5.6-luna", 1_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if short.ContextTierName != "default" || short.InputPriceNanosPerMillion != 200_000_000 {
		t.Fatalf("gpt-5.6-luna short tier=%+v", short)
	}
	long, err := service.Resolve(ctx, "openai_compatible", "gpt-5.6-luna", 300_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if long.ContextTierName != "long_context" || long.InputPriceNanosPerMillion != 400_000_000 {
		t.Fatalf("gpt-5.6-luna long tier=%+v", long)
	}

	geminiPro, err := service.Resolve(ctx, "gemini", "gemini-2.5-pro", 250_000, now)
	if err != nil {
		t.Fatal(err)
	}
	if geminiPro.ContextTierName != "long_context" || geminiPro.OutputPriceNanosPerMillion != 15_000_000_000 {
		t.Fatalf("gemini-2.5-pro long tier=%+v", geminiPro)
	}

	if _, err := service.Resolve(ctx, "unknown_provider", "no-such-model", 100, now); !errors.Is(err, modelpricing.ErrNoPricingResolved) {
		t.Fatalf("unknown provider err=%v want ErrNoPricingResolved", err)
	}
}

func TestModelPricingUpsertIsImmutableAndVersioned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	platform := openPricingStore(t, ctx)
	store, err := modelpricingpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	service, err := modelpricing.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	newer := now.Add(time.Hour)

	inserted, err := service.Upsert(ctx, modelpricing.PriceTier{
		ProviderID: "test.fake", ProviderModelID: "fixture-model", ContextTierName: "default",
		InputPriceNanosPerMillion: 1_000, OutputPriceNanosPerMillion: 2_000, EffectiveAt: newer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if inserted.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be populated")
	}

	// Before the new row's effective_at, resolution must fail closed — there
	// is no prior fixture-model price to fall back to.
	if _, err := service.Resolve(ctx, "test.fake", "fixture-model", 10, now); !errors.Is(err, modelpricing.ErrNoPricingResolved) {
		t.Fatalf("before effective_at err=%v want ErrNoPricingResolved", err)
	}
	resolved, err := service.Resolve(ctx, "test.fake", "fixture-model", 10, newer.Add(time.Minute))
	if err != nil || resolved.InputPriceNanosPerMillion != 1_000 {
		t.Fatalf("after effective_at resolved=%+v err=%v", resolved, err)
	}

	if _, err := platform.Pool().Exec(ctx, `UPDATE model_pricing SET input_price_nanos_per_million=999 WHERE provider_id='test.fake'`); err == nil {
		t.Fatal("expected UPDATE on model_pricing to be rejected by the immutability trigger")
	}
	if _, err := platform.Pool().Exec(ctx, `DELETE FROM model_pricing WHERE provider_id='test.fake'`); err == nil {
		t.Fatal("expected DELETE on model_pricing to be rejected by the immutability trigger")
	}
}
