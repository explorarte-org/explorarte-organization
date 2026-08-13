//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/mimo"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
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
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
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

	tier, err := service.Resolve(ctx, "deepseek", "deepseek-v4-flash", 500, modelpricing.BillingOnline, now)
	if err != nil {
		t.Fatal(err)
	}
	cost, err := tier.EstimateCost(0, 1_000_000, 0, 0)
	if err != nil || cost != 2_800_000 {
		t.Fatalf("deepseek-v4-flash cache-hit cost=%d err=%v want=2800000", cost, err)
	}

	short, err := service.Resolve(ctx, "openai_compatible", "gpt-5.6-luna", 1_000, modelpricing.BillingOnline, now)
	if err != nil {
		t.Fatal(err)
	}
	if short.ContextTierName != "default" || short.InputPriceNanosPerMillion != 200_000_000 {
		t.Fatalf("gpt-5.6-luna short tier=%+v", short)
	}
	long, err := service.Resolve(ctx, "openai_compatible", "gpt-5.6-luna", 300_000, modelpricing.BillingOnline, now)
	if err != nil {
		t.Fatal(err)
	}
	if long.ContextTierName != "long_context" || long.InputPriceNanosPerMillion != 400_000_000 {
		t.Fatalf("gpt-5.6-luna long tier=%+v", long)
	}

	geminiPro, err := service.Resolve(ctx, "gemini", "gemini-2.5-pro", 250_000, modelpricing.BillingOnline, now)
	if err != nil {
		t.Fatal(err)
	}
	if geminiPro.ContextTierName != "long_context" || geminiPro.OutputPriceNanosPerMillion != 15_000_000_000 {
		t.Fatalf("gemini-2.5-pro long tier=%+v", geminiPro)
	}

	// ORG-AUDIT-003: executive.ceo routes to openai_responses/gpt-5.6-luna
	// in docs/canonical/model-routing.yaml; this must resolve exactly like
	// openai_compatible's identical rate card, or the CEO's cost gate fails
	// closed on every model.invoke.
	ceoShort, err := service.Resolve(ctx, "openai_responses", "gpt-5.6-luna", 1_000, modelpricing.BillingOnline, now)
	if err != nil {
		t.Fatalf("openai_responses/gpt-5.6-luna must be resolvable: %v", err)
	}
	if ceoShort.ContextTierName != "default" || ceoShort.InputPriceNanosPerMillion != 200_000_000 {
		t.Fatalf("openai_responses gpt-5.6-luna short tier=%+v", ceoShort)
	}

	// R30 retired gemini-2.5-flash from model-routing.yaml (research.worker
	// now routes to deepseek/deepseek-v4-flash; Gemini is generation-only
	// history from here on, never a live routing target) — this row is kept
	// seeded and resolvable on purpose, since historical prices are never
	// deleted, but nothing productive depends on it resolving anymore.
	geminiFlash, err := service.Resolve(ctx, "gemini", "gemini-2.5-flash", 1_000, modelpricing.BillingOnline, now)
	if err != nil {
		t.Fatal(err)
	}
	if geminiFlash.InputPriceNanosPerMillion != 300_000_000 || geminiFlash.OutputPriceNanosPerMillion != 2_500_000_000 {
		t.Fatalf("gemini-2.5-flash tier=%+v", geminiFlash)
	}

	if _, err := service.Resolve(ctx, "unknown_provider", "no-such-model", 100, modelpricing.BillingOnline, now); !errors.Is(err, modelpricing.ErrNoPricingResolved) {
		t.Fatalf("unknown provider err=%v want ErrNoPricingResolved", err)
	}
}

// TestModelPricingBillingModeNeverConflatesOnlineAndBatch verifies against
// real PostgreSQL — not just the pure Resolve() unit tests — that R29's
// seeded gemini-embedding-2 rows (000027) resolve to the row matching the
// requested billing mode, and that Store.ListTiers' SQL-level billing_mode
// filter and Resolve's own defensive re-check agree.
// ORG-AUDIT-003 regression: closes the catalog gap directly, rather than
// only proving the one provider (openai_responses) this pass happened to
// fix. Every provider docs/canonical/model-routing.yaml names for a
// non-subscription policy must have a resolvable model_pricing tier and a
// provider_wallets row, or costgate.Reserve fails closed for that role's
// every model.invoke -- exactly the gap that let openai_responses go
// unnoticed until an audit found it.
func TestEveryRoutedNonSubscriptionProviderHasPricingAndAWallet(t *testing.T) {
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

	routing, err := modelruntime.LoadCanonicalRouting(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	subscriptionProviders := map[string]bool{mimo.ProviderID: true}

	type routedModel struct{ provider, model string }
	seen := map[routedModel]bool{}
	for policyID, policy := range routing.Policies {
		if subscriptionProviders[policy.Provider] {
			continue
		}
		key := routedModel{policy.Provider, policy.Model}
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := service.Resolve(ctx, policy.Provider, policy.Model, 1_000, modelpricing.BillingOnline, now); err != nil {
			t.Errorf("policy %q routes to %s/%s, which has no resolvable model_pricing tier: %v", policyID, policy.Provider, policy.Model, err)
		}
	}

	providers := map[string]bool{}
	for _, policy := range routing.Policies {
		if !subscriptionProviders[policy.Provider] {
			providers[policy.Provider] = true
		}
	}
	for providerID := range providers {
		var count int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM provider_wallets WHERE provider_id=$1`, providerID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Errorf("provider %q is routed to by canonical policy but has no provider_wallets row", providerID)
		}
	}
}

func TestModelPricingBillingModeNeverConflatesOnlineAndBatch(t *testing.T) {
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

	online, err := service.Resolve(ctx, "gemini", "gemini-embedding-2", 1_000, modelpricing.BillingOnline, now)
	if err != nil {
		t.Fatal(err)
	}
	if online.BillingMode != modelpricing.BillingOnline || online.InputPriceNanosPerMillion != 200_000_000 {
		t.Fatalf("online tier=%+v", online)
	}

	batch, err := service.Resolve(ctx, "gemini", "gemini-embedding-2", 1_000, modelpricing.BillingBatch, now)
	if err != nil {
		t.Fatal(err)
	}
	if batch.BillingMode != modelpricing.BillingBatch || batch.InputPriceNanosPerMillion != 100_000_000 {
		t.Fatalf("batch tier=%+v", batch)
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
		InputPriceNanosPerMillion: 1_000, OutputPriceNanosPerMillion: 2_000, BillingMode: modelpricing.BillingOnline, EffectiveAt: newer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if inserted.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be populated")
	}

	// Before the new row's effective_at, resolution must fail closed — there
	// is no prior fixture-model price to fall back to.
	if _, err := service.Resolve(ctx, "test.fake", "fixture-model", 10, modelpricing.BillingOnline, now); !errors.Is(err, modelpricing.ErrNoPricingResolved) {
		t.Fatalf("before effective_at err=%v want ErrNoPricingResolved", err)
	}
	resolved, err := service.Resolve(ctx, "test.fake", "fixture-model", 10, modelpricing.BillingOnline, newer.Add(time.Minute))
	if err != nil || resolved.InputPriceNanosPerMillion != 1_000 {
		t.Fatalf("after effective_at resolved=%+v err=%v", resolved, err)
	}

	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), platform.Pool()); err != nil {
		t.Fatalf("refusing destructive operation: %v", err)
	}
	if _, err := platform.Pool().Exec(ctx, `UPDATE model_pricing SET input_price_nanos_per_million=999 WHERE provider_id='test.fake'`); err == nil {
		t.Fatal("expected UPDATE on model_pricing to be rejected by the immutability trigger")
	}
	if _, err := platform.Pool().Exec(ctx, `DELETE FROM model_pricing WHERE provider_id='test.fake'`); err == nil {
		t.Fatal("expected DELETE on model_pricing to be rejected by the immutability trigger")
	}
}
