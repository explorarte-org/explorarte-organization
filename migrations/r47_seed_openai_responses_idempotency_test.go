//go:build integration

package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	"github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// CUTOVER-REHEARSAL-001: migration 000047 seeds an openai_responses wallet
// and pricing tiers, but production already carries both -- seeded
// manually on 2026-08-10, predating this migration (see
// HANDOFF-2026-08-10-noche.md). A dress-rehearsal migration run against a
// byte-proven copy of production data failed here with a duplicate-key
// error on provider_wallets_pkey. These four tests pin down migration
// 000047's behavior across every combination of pre-existing state it must
// now tolerate, so a future edit to this migration can't silently
// reintroduce the same failure mode against real production data.
func TestMigration047_NoPreexistingRows(t *testing.T) {
	store, cleanup := openMigrationTestStore(t)
	defer cleanup()
	ctx := context.Background()

	applyThroughVersion(t, ctx, store, 46)

	runFullUp(t, ctx, store)

	tiers := readOpenAIResponsesTiers(t, ctx, store)
	if len(tiers) != 2 {
		t.Fatalf("tier count=%d want 2: %+v", len(tiers), tiers)
	}
	wallet := readOpenAIResponsesWallet(t, ctx, store)
	if wallet.balance != 9700000000 || wallet.reserved != 0 {
		t.Fatalf("wallet=%+v want balance=9700000000 reserved=0 (fresh placeholder seed)", wallet)
	}
	requireSchemaTip(t, ctx, store, 49)
}

// The production-like case: exercises every invariant the operator
// explicitly required before authorizing a real cutover.
func TestMigration047_WalletAndBothTiersPreexisting_ProductionLike(t *testing.T) {
	store, cleanup := openMigrationTestStore(t)
	defer cleanup()
	ctx := context.Background()

	applyThroughVersion(t, ctx, store, 46)

	const preexistingBalance = 9680000000
	const preexistingReserved = 42424242 // distinctive non-zero, non-default value
	preexistingEffectiveAt := seedWallet(t, ctx, store, preexistingBalance, preexistingReserved)
	defaultEffectiveAt := seedTier(t, ctx, store, "default", 0, 200000000, 20000000, 250000000, 1200000000)
	longContextEffectiveAt := seedTier(t, ctx, store, "long_context", 272000, 400000000, 40000000, 500000000, 1800000000)

	runFullUp(t, ctx, store)

	wallet := readOpenAIResponsesWallet(t, ctx, store)
	if wallet.balance != preexistingBalance {
		t.Fatalf("wallet.balance=%d want %d (must not be overwritten to the 9700000000 placeholder)", wallet.balance, preexistingBalance)
	}
	if wallet.reserved != preexistingReserved {
		t.Fatalf("wallet.reserved=%d want %d (must not change)", wallet.reserved, preexistingReserved)
	}
	if !wallet.updatedAt.Equal(preexistingEffectiveAt) {
		t.Fatalf("wallet.updated_at=%v want unchanged %v (ON CONFLICT DO NOTHING must not touch the row at all)", wallet.updatedAt, preexistingEffectiveAt)
	}

	tiers := readOpenAIResponsesTiers(t, ctx, store)
	if len(tiers) != 2 {
		t.Fatalf("tier count=%d want exactly 2 (no duplicates): %+v", len(tiers), tiers)
	}
	byTier := map[string]pricingRow{}
	for _, r := range tiers {
		byTier[r.contextTierName] = r
	}
	if got := byTier["default"].effectiveAt; !got.Equal(defaultEffectiveAt) {
		t.Fatalf("default tier effective_at=%v want unchanged %v (a newer duplicate must not have been inserted)", got, defaultEffectiveAt)
	}
	if got := byTier["long_context"].effectiveAt; !got.Equal(longContextEffectiveAt) {
		t.Fatalf("long_context tier effective_at=%v want unchanged %v (a newer duplicate must not have been inserted)", got, longContextEffectiveAt)
	}

	requireSchemaTip(t, ctx, store, 49)
}

func TestMigration047_WalletPreexisting_PricingAbsent(t *testing.T) {
	store, cleanup := openMigrationTestStore(t)
	defer cleanup()
	ctx := context.Background()

	applyThroughVersion(t, ctx, store, 46)

	const preexistingBalance = 9680000000
	const preexistingReserved = 17
	preexistingEffectiveAt := seedWallet(t, ctx, store, preexistingBalance, preexistingReserved)

	runFullUp(t, ctx, store)

	wallet := readOpenAIResponsesWallet(t, ctx, store)
	if wallet.balance != preexistingBalance || wallet.reserved != preexistingReserved {
		t.Fatalf("wallet=%+v want balance=%d reserved=%d unchanged", wallet, preexistingBalance, preexistingReserved)
	}
	if !wallet.updatedAt.Equal(preexistingEffectiveAt) {
		t.Fatalf("wallet.updated_at=%v want unchanged %v", wallet.updatedAt, preexistingEffectiveAt)
	}

	tiers := readOpenAIResponsesTiers(t, ctx, store)
	if len(tiers) != 2 {
		t.Fatalf("tier count=%d want 2 (both freshly seeded, since neither pre-existed): %+v", len(tiers), tiers)
	}
	requireSchemaTip(t, ctx, store, 49)
}

func TestMigration047_OneTierPreexisting_OtherAbsent(t *testing.T) {
	store, cleanup := openMigrationTestStore(t)
	defer cleanup()
	ctx := context.Background()

	applyThroughVersion(t, ctx, store, 46)

	defaultEffectiveAt := seedTier(t, ctx, store, "default", 0, 200000000, 20000000, 250000000, 1200000000)

	runFullUp(t, ctx, store)

	tiers := readOpenAIResponsesTiers(t, ctx, store)
	if len(tiers) != 2 {
		t.Fatalf("tier count=%d want exactly 2 (pre-existing default + freshly seeded long_context): %+v", len(tiers), tiers)
	}
	byTier := map[string]pricingRow{}
	for _, r := range tiers {
		byTier[r.contextTierName] = r
	}
	if _, ok := byTier["long_context"]; !ok {
		t.Fatalf("long_context tier missing -- must have been seeded fresh since it did not pre-exist")
	}
	if got := byTier["default"].effectiveAt; !got.Equal(defaultEffectiveAt) {
		t.Fatalf("default tier effective_at=%v want unchanged %v (must not have been duplicated)", got, defaultEffectiveAt)
	}

	wallet := readOpenAIResponsesWallet(t, ctx, store)
	if wallet.balance != 9700000000 || wallet.reserved != 0 {
		t.Fatalf("wallet=%+v want the fresh placeholder seed since no wallet pre-existed", wallet)
	}
	requireSchemaTip(t, ctx, store, 49)
}

// ---- shared test plumbing ----

func openMigrationTestStore(t *testing.T) (*postgres.Store, func()) {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL, "ORG_DATABASE_MAX_CONNS": "4", "ORG_DATABASE_MIN_CONNS": "0"}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, cfg.Database, "explorarte-integration-test-r47")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	if err := testdbguard.RequireDestructive(ctx, databaseURL, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing destructive schema reset: %v", err)
	}
	resetSchema(t, context.Background(), store)

	cleanup := func() {
		// Leave a fully-migrated, pristine schema behind for whichever
		// integration package shares this database and runs next -- same
		// discipline as internal/platform/postgres/integration_test.go.
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancelCleanup()
		resetSchema(t, cleanupCtx, store)
		runFullUp(t, cleanupCtx, store)
		store.Close()
	}
	return store, cleanup
}

func resetSchema(t *testing.T, ctx context.Context, store *postgres.Store) {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector;`); err != nil {
		t.Fatalf("recreate pgvector extension after schema reset: %v", err)
	}
}

// applyThroughVersion hand-applies migrations 1..maxVersion using the same
// UpSQL/Checksum the real Runner would compute, so a later full runFullUp
// call recognizes them (via matching checksums in schema_migrations) and
// applies only the migrations strictly after maxVersion.
func applyThroughVersion(t *testing.T, ctx context.Context, store *postgres.Store, maxVersion int64) {
	t.Helper()
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL CHECK (length(checksum) = 64),
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			execution_time_ms BIGINT NOT NULL CHECK (execution_time_ms >= 0)
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, m := range loaded {
		if m.Version > maxVersion {
			continue
		}
		tx, err := store.Pool().Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx for migration %d: %v", m.Version, err)
		}
		if _, err := tx.Exec(ctx, m.UpSQL); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("apply migration %06d_%s: %v", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum, execution_time_ms) VALUES ($1,$2,$3,$4)`, m.Version, m.Name, m.Checksum, 0); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("record migration %06d_%s: %v", m.Version, m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit migration %d: %v", m.Version, err)
		}
	}
}

func runFullUp(t *testing.T, ctx context.Context, store *postgres.Store) {
	t.Helper()
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
}

func requireSchemaTip(t *testing.T, ctx context.Context, store *postgres.Store, want int64) {
	t.Helper()
	var got int64
	if err := store.Pool().QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&got); err != nil {
		t.Fatalf("read schema tip: %v", err)
	}
	if got != want {
		t.Fatalf("schema tip=%d want %d", got, want)
	}
}

type walletRow struct {
	balance   int64
	reserved  int64
	updatedAt time.Time
}

func readOpenAIResponsesWallet(t *testing.T, ctx context.Context, store *postgres.Store) walletRow {
	t.Helper()
	var w walletRow
	err := store.Pool().QueryRow(ctx, `SELECT balance_usd_nanos, reserved_usd_nanos, updated_at FROM provider_wallets WHERE provider_id = 'openai_responses'`).Scan(&w.balance, &w.reserved, &w.updatedAt)
	if err != nil {
		t.Fatalf("read openai_responses wallet: %v", err)
	}
	return w
}

type pricingRow struct {
	contextTierName string
	effectiveAt     time.Time
}

func readOpenAIResponsesTiers(t *testing.T, ctx context.Context, store *postgres.Store) []pricingRow {
	t.Helper()
	rows, err := store.Pool().Query(ctx, `SELECT context_tier_name, effective_at FROM model_pricing WHERE provider_id = 'openai_responses' AND provider_model_id = 'gpt-5.6-luna' AND billing_mode = 'online' ORDER BY context_tier_name`)
	if err != nil {
		t.Fatalf("read openai_responses tiers: %v", err)
	}
	defer rows.Close()
	var out []pricingRow
	for rows.Next() {
		var r pricingRow
		if err := rows.Scan(&r.contextTierName, &r.effectiveAt); err != nil {
			t.Fatalf("scan tier row: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// seedWallet inserts a pre-existing openai_responses wallet row (simulating
// production's manual 2026-08-10 seed) and returns its updated_at, so the
// test can later assert the row was never touched.
func seedWallet(t *testing.T, ctx context.Context, store *postgres.Store, balance, reserved int64) time.Time {
	t.Helper()
	var updatedAt time.Time
	err := store.Pool().QueryRow(ctx, `
		INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, updated_at)
		VALUES ('openai_responses', $1, $2, NOW() - interval '3 days')
		RETURNING updated_at
	`, balance, reserved).Scan(&updatedAt)
	if err != nil {
		t.Fatalf("seed preexisting wallet: %v", err)
	}
	return updatedAt
}

// seedTier inserts a pre-existing openai_responses/gpt-5.6-luna pricing row
// for the given tier and returns its effective_at, so the test can later
// assert it was never shadowed by a newer duplicate.
func seedTier(t *testing.T, ctx context.Context, store *postgres.Store, tier string, minInputTokens, inputPrice, cachedInputPrice, cacheWritePrice, outputPrice int64) time.Time {
	t.Helper()
	var effectiveAt time.Time
	err := store.Pool().QueryRow(ctx, `
		INSERT INTO model_pricing (provider_id, provider_model_id, context_tier_name, min_input_tokens, input_price_nanos_per_million, cached_input_price_nanos_per_million, cache_write_price_nanos_per_million, output_price_nanos_per_million, billing_mode, effective_at)
		VALUES ('openai_responses', 'gpt-5.6-luna', $1, $2, $3, $4, $5, $6, 'online', NOW() - interval '3 days')
		RETURNING effective_at
	`, tier, minInputTokens, inputPrice, cachedInputPrice, cacheWritePrice, outputPrice).Scan(&effectiveAt)
	if err != nil {
		t.Fatalf("seed preexisting %s tier: %v", tier, err)
	}
	return effectiveAt
}
