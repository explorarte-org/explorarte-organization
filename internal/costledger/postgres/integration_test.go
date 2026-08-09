//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	ledgerIntegrationOrg  = "explorarte"
	ledgerIntegrationRole = "ingenieria_ia/qa"
	ledgerIntegrationUnit = "ingenieria_ia"
)

type ledgerFixture struct {
	store           *platformpostgres.Store
	revisionID      int64
	profileID       string
	profileVersion  int64
	providerID      string
	providerModelID string
}

func openLedgerFixture(t *testing.T, ctx context.Context) ledgerFixture {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required for integration tests")
	}
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL, "ORG_DATABASE_MAX_CONNS": "8", "ORG_DATABASE_MIN_CONNS": "0", "ORG_CANONICAL_DIR": canonicalDir}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "costledger-integration-test")
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
	if _, err := store.Pool().Exec(ctx, `TRUNCATE organizations, organization_registry_revisions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	// provider_wallets/provider_wallet_events are keyed by provider_id, not
	// by organization — the CASCADE above wipes wallet EVENTS (they FK to
	// model_invocations, which does cascade from organizations) but leaves
	// provider_wallets' denormalized reserved_usd_nanos/balance_usd_nanos
	// stale from whatever earlier test run last touched the same
	// provider_id. Reset both explicitly, then re-seed the real starting
	// balances migration 000021 only inserts once per database, so
	// TestModelPricingSeedWallets still sees them.
	if _, err := store.Pool().Exec(ctx, `TRUNCATE provider_wallets, provider_wallet_events`); err != nil {
		t.Fatalf("reset wallet schema: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, updated_at) VALUES
    ('deepseek', 8660000000, 0, NOW()),
    ('gemini', 10000000000, 0, NOW()),
    ('openai_compatible', 9700000000, 0, NOW())`); err != nil {
		t.Fatalf("reseed wallets: %v", err)
	}

	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, ledgerIntegrationOrg, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	syncResult, err := registryService.SynchronizeCanonical(ctx, true)
	if err != nil || !syncResult.Applied {
		t.Fatalf("sync registry: result=%+v err=%v", syncResult, err)
	}
	revision, err := registryRepo.GetCurrentRevision(ctx, ledgerIntegrationOrg)
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

	var profileID, providerID, providerModelID string
	var profileVersionID int64
	if err := store.Pool().QueryRow(ctx, `
SELECT b.profile_id, b.model_profile_version_id, v.provider_id, v.provider_model_id
FROM role_model_bindings b
JOIN model_profile_versions v
  ON v.id=b.model_profile_version_id
 AND v.organization_id=b.organization_id
 AND v.profile_id=b.profile_id
WHERE b.organization_id=$1 AND b.organization_revision_id=$2 AND b.role_id=$3 AND b.active`,
		ledgerIntegrationOrg, revision.ID, ledgerIntegrationRole,
	).Scan(&profileID, &profileVersionID, &providerID, &providerModelID); err != nil {
		t.Fatalf("load model binding for %s: %v", ledgerIntegrationRole, err)
	}

	return ledgerFixture{store: store, revisionID: revision.ID, profileID: profileID, profileVersion: profileVersionID, providerID: providerID, providerModelID: providerModelID}
}

var ledgerInvocationCounter int64
var ledgerInvocationMu sync.Mutex

// insertFixtureInvocation creates the minimum durable task/attempt/context
// snapshot/model invocation chain needed to satisfy provider_wallet_events'
// FK to model_invocations, always using the fixture's real role binding.
// The wallet under test is addressed independently via costledger's own
// providerID argument — the ledger does not require it to match the
// invocation's real provider, so tests can use isolated wallet namespaces
// without needing a real binding for every provider name under test.
func (f ledgerFixture) insertInvocation(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	ledgerInvocationMu.Lock()
	ledgerInvocationCounter++
	ordinal := ledgerInvocationCounter
	ledgerInvocationMu.Unlock()

	now := time.Now().UTC().Truncate(time.Microsecond)
	var taskID int64
	if err := f.store.Pool().QueryRow(ctx, `
INSERT INTO tasks (
 organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,
 idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,
 max_attempts,attempt_count,version,created_at,updated_at
) VALUES ($1,$2,'empresa/ceo',$3,$4,$5,$6,$7,$8,'[]'::jsonb,'running',0,$9,1,1,1,$9,$9)
RETURNING id`, ledgerIntegrationOrg, f.revisionID, ledgerIntegrationRole, ledgerIntegrationUnit,
		fmt.Sprintf("costledger-fixture-task-%d", ordinal), digest(fmt.Sprintf("costledger-task-%d", ordinal)), fmt.Sprintf("Ledger fixture %d", ordinal), "durable ledger fixture", now,
	).Scan(&taskID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	var attemptID int64
	if err := f.store.Pool().QueryRow(ctx, `
INSERT INTO task_attempts (task_id,ordinal,state,worker_id,leased_at,created_at,updated_at)
VALUES ($1,1,'leased','costledger-integration-worker',$2,$2,$2)
RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	var snapshotID int64
	contextHash := digest(fmt.Sprintf("costledger-context-%d", ordinal))
	if err := f.store.Pool().QueryRow(ctx, `
INSERT INTO context_snapshots (
 organization_id,organization_revision_id,actor_role_id,purpose,task_ref,idempotency_key,request_hash,
 precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,
 omitted_segment_count,total_bytes,created_at
) VALUES ($1,$2,$3,'costledger integration fixture',$4,$5,$6,$6,$6,$6,'ready',1,0,0,0,0,$7)
RETURNING id`, ledgerIntegrationOrg, f.revisionID, ledgerIntegrationRole, fmt.Sprintf("task:%d", taskID),
		fmt.Sprintf("costledger-context-%d", ordinal), contextHash, now.Add(-time.Second),
	).Scan(&snapshotID); err != nil {
		t.Fatalf("insert context snapshot: %v", err)
	}
	var invocationID int64
	if err := f.store.Pool().QueryRow(ctx, `
INSERT INTO model_invocations (
 organization_id,organization_revision_id,task_id,attempt_id,dispatch_actor_role_id,subject_role_id,
 context_snapshot_id,purpose,model_profile_id,model_profile_version_id,provider_id,provider_model_id,
 required_capabilities,output_mode,max_output_tokens,thinking_mode,idempotency_key,request_hash,status,
 deadline,created_at,updated_at
) VALUES ($1,$2,$3,$4,'ingenieria_ia/code-runner',$5,$6,'costledger integration fixture',$7,$8,$9,$10,
 '[]'::jsonb,'json',128,'opaque',$11,$12,'requested',$13,$14,$14)
RETURNING id`,
		ledgerIntegrationOrg, f.revisionID, taskID, attemptID, ledgerIntegrationRole, snapshotID,
		f.profileID, f.profileVersion, f.providerID, f.providerModelID,
		fmt.Sprintf("costledger-invocation-%d", ordinal), digest(fmt.Sprintf("costledger-invocation-%d", ordinal)), now.Add(time.Hour), now,
	).Scan(&invocationID); err != nil {
		t.Fatalf("insert model invocation: %v", err)
	}
	return invocationID
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestModelPricingSeedWallets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	for providerID, want := range map[string]modelpricing.USDNanos{
		"deepseek":          8_660_000_000,
		"gemini":            10_000_000_000,
		"openai_compatible": 9_700_000_000,
	} {
		wallet, err := ledger.GetWallet(ctx, providerID)
		if err != nil {
			t.Fatalf("%s: %v", providerID, err)
		}
		if wallet.BalanceUSD != want {
			t.Fatalf("%s balance=%d want=%d", providerID, wallet.BalanceUSD, want)
		}
	}
	if _, err := ledger.GetWallet(ctx, "alibaba_token_plan_via_claude_code"); !errors.Is(err, costledger.ErrWalletNotFound) {
		t.Fatalf("alibaba token-plan provider must not have a wallet: err=%v want ErrWalletNotFound", err)
	}
}

func TestReserveReconcileRoundTripAdjustsWalletCorrectly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := ledger.SetBalance(ctx, "test.roundtrip", modelpricing.USDFromDollars(10), now); err != nil {
		t.Fatal(err)
	}
	invocationID := fixture.insertInvocation(t, ctx)

	if err := ledger.Reserve(ctx, "test.roundtrip", invocationID, modelpricing.USDFromDollars(5), now); err != nil {
		t.Fatal(err)
	}
	wallet, err := ledger.GetWallet(ctx, "test.roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	if wallet.ReservedUSD != modelpricing.USDFromDollars(5) {
		t.Fatalf("after reserve wallet=%+v", wallet)
	}

	// Reserving again for the same invocation must be a no-op, not a
	// second reservation.
	if err := ledger.Reserve(ctx, "test.roundtrip", invocationID, modelpricing.USDFromDollars(5), now); err != nil {
		t.Fatal(err)
	}
	wallet, err = ledger.GetWallet(ctx, "test.roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	if wallet.ReservedUSD != modelpricing.USDFromDollars(5) {
		t.Fatalf("duplicate reserve changed reserved amount: %+v", wallet)
	}

	balanceBefore := wallet.BalanceUSD
	if err := ledger.Reconcile(ctx, "test.roundtrip", invocationID, modelpricing.USDFromDollars(3.2), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	wallet, err = ledger.GetWallet(ctx, "test.roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	if wallet.ReservedUSD != 0 {
		t.Fatalf("reconcile did not release the reservation: %+v", wallet)
	}
	if wallet.BalanceUSD != balanceBefore-modelpricing.USDFromDollars(3.2) {
		t.Fatalf("reconcile debit wrong: before=%d after=%d", balanceBefore, wallet.BalanceUSD)
	}

	// Reconciling twice must not double-debit.
	if err := ledger.Reconcile(ctx, "test.roundtrip", invocationID, modelpricing.USDFromDollars(3.2), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	again, err := ledger.GetWallet(ctx, "test.roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	if again.BalanceUSD != wallet.BalanceUSD {
		t.Fatalf("duplicate reconcile changed balance: %d -> %d", wallet.BalanceUSD, again.BalanceUSD)
	}
}

func TestListOrphanedReservationsFindsOnlyReservationsWithoutATerminalEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.SetBalance(ctx, "test.orphan", modelpricing.USDFromDollars(10), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour)

	orphanID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.orphan", orphanID, modelpricing.USDFromDollars(1), old); err != nil {
		t.Fatal(err)
	}

	reconciledID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.orphan", reconciledID, modelpricing.USDFromDollars(1), old); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reconcile(ctx, "test.orphan", reconciledID, modelpricing.USDFromDollars(1), old.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	releasedID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.orphan", releasedID, modelpricing.USDFromDollars(1), old); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Release(ctx, "test.orphan", releasedID, old.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	tooRecentID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.orphan", tooRecentID, modelpricing.USDFromDollars(1), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	orphans, err := ledger.ListOrphanedReservations(ctx, time.Now().UTC().Add(-30*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	var found []int64
	for _, event := range orphans {
		if event.ProviderID != "test.orphan" {
			continue
		}
		found = append(found, event.InvocationID)
		if event.Kind != costledger.EventReserved {
			t.Fatalf("orphan event kind=%q want reserved", event.Kind)
		}
	}
	if len(found) != 1 || found[0] != orphanID {
		t.Fatalf("orphans=%v want=[%d]", found, orphanID)
	}
}

func TestReserveFailsClosedOnInsufficientBalanceAndUnknownWallet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	unknownInvocation := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.unconfigured", unknownInvocation, 1, now); !errors.Is(err, costledger.ErrWalletNotFound) {
		t.Fatalf("reserve against a provider with no wallet: err=%v want ErrWalletNotFound", err)
	}

	if _, err := ledger.SetBalance(ctx, "test.insufficient", modelpricing.USDFromDollars(1), now); err != nil {
		t.Fatal(err)
	}
	overInvocation := fixture.insertInvocation(t, ctx)
	before, err := ledger.GetWallet(ctx, "test.insufficient")
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(ctx, "test.insufficient", overInvocation, before.BalanceUSD+1, now); !errors.Is(err, costledger.ErrInsufficientBalance) {
		t.Fatalf("reserve above balance: err=%v want ErrInsufficientBalance", err)
	}
	after, err := ledger.GetWallet(ctx, "test.insufficient")
	if err != nil {
		t.Fatal(err)
	}
	if after.ReservedUSD != before.ReservedUSD {
		t.Fatalf("rejected reservation must not change reserved amount: before=%d after=%d", before.ReservedUSD, after.ReservedUSD)
	}
}

func TestReconcileWithoutReservationFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := ledger.SetBalance(ctx, "test.reconcile-missing", modelpricing.USDFromDollars(5), now); err != nil {
		t.Fatal(err)
	}
	invocationID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reconcile(ctx, "test.reconcile-missing", invocationID, 1, now); !errors.Is(err, costledger.ErrReservationNotFound) {
		t.Fatalf("reconcile without reservation: err=%v want ErrReservationNotFound", err)
	}
}

func TestReleaseFreesReservationWithoutDebitingBalance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := ledger.SetBalance(ctx, "test.release", modelpricing.USDFromDollars(5), now); err != nil {
		t.Fatal(err)
	}
	invocationID := fixture.insertInvocation(t, ctx)
	before, err := ledger.GetWallet(ctx, "test.release")
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(ctx, "test.release", invocationID, modelpricing.USDFromDollars(1), now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Release(ctx, "test.release", invocationID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := ledger.GetWallet(ctx, "test.release")
	if err != nil {
		t.Fatal(err)
	}
	if after.ReservedUSD != 0 || after.BalanceUSD != before.BalanceUSD {
		t.Fatalf("release should free the reservation without touching balance: before=%+v after=%+v", before, after)
	}
}

func TestReconcileThenReleaseIsRejectedNotDoubleApplied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := ledger.SetBalance(ctx, "test.oneterminal", modelpricing.USDFromDollars(5), now); err != nil {
		t.Fatal(err)
	}
	invocationID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.oneterminal", invocationID, modelpricing.USDFromDollars(2), now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reconcile(ctx, "test.oneterminal", invocationID, modelpricing.USDFromDollars(1.5), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	afterReconcile, err := ledger.GetWallet(ctx, "test.oneterminal")
	if err != nil {
		t.Fatal(err)
	}
	if afterReconcile.ReservedUSD != 0 {
		t.Fatalf("reconcile did not release the reservation: %+v", afterReconcile)
	}

	// A Release for the same invocation after it was already reconciled
	// must be rejected, not silently double-decrement reserved_usd_nanos.
	if err := ledger.Release(ctx, "test.oneterminal", invocationID, now.Add(2*time.Second)); !errors.Is(err, costledger.ErrAlreadyTerminal) {
		t.Fatalf("release after reconcile: err=%v want ErrAlreadyTerminal", err)
	}
	afterRelease, err := ledger.GetWallet(ctx, "test.oneterminal")
	if err != nil {
		t.Fatal(err)
	}
	if afterRelease.ReservedUSD != afterReconcile.ReservedUSD || afterRelease.BalanceUSD != afterReconcile.BalanceUSD {
		t.Fatalf("rejected release must not change wallet state: before=%+v after=%+v", afterReconcile, afterRelease)
	}
}

func TestReleaseThenReconcileIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := ledger.SetBalance(ctx, "test.oneterminal2", modelpricing.USDFromDollars(5), now); err != nil {
		t.Fatal(err)
	}
	invocationID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.oneterminal2", invocationID, modelpricing.USDFromDollars(2), now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Release(ctx, "test.oneterminal2", invocationID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reconcile(ctx, "test.oneterminal2", invocationID, modelpricing.USDFromDollars(1), now.Add(2*time.Second)); !errors.Is(err, costledger.ErrAlreadyTerminal) {
		t.Fatalf("reconcile after release: err=%v want ErrAlreadyTerminal", err)
	}
	wallet, err := ledger.GetWallet(ctx, "test.oneterminal2")
	if err != nil {
		t.Fatal(err)
	}
	if wallet.BalanceUSD != modelpricing.USDFromDollars(5) {
		t.Fatalf("rejected reconcile must not debit balance: %+v", wallet)
	}
}

func TestReserveRetryWithDifferentAmountFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := ledger.SetBalance(ctx, "test.amountmismatch", modelpricing.USDFromDollars(5), now); err != nil {
		t.Fatal(err)
	}
	invocationID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.amountmismatch", invocationID, modelpricing.USDFromDollars(1), now); err != nil {
		t.Fatal(err)
	}
	// Same amount again: idempotent no-op.
	if err := ledger.Reserve(ctx, "test.amountmismatch", invocationID, modelpricing.USDFromDollars(1), now); err != nil {
		t.Fatalf("same-amount retry must be idempotent: %v", err)
	}
	// Different amount: must fail, not silently keep the stale reservation.
	if err := ledger.Reserve(ctx, "test.amountmismatch", invocationID, modelpricing.USDFromDollars(2), now); !errors.Is(err, costledger.ErrAmountMismatch) {
		t.Fatalf("different-amount retry: err=%v want ErrAmountMismatch", err)
	}
	wallet, err := ledger.GetWallet(ctx, "test.amountmismatch")
	if err != nil {
		t.Fatal(err)
	}
	if wallet.ReservedUSD != modelpricing.USDFromDollars(1) {
		t.Fatalf("mismatched retry must not change the reserved amount: %+v", wallet)
	}
}

func TestSetBalanceRejectsGoingBelowAlreadyReserved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := ledger.SetBalance(ctx, "test.setbalance", modelpricing.USDFromDollars(5), now); err != nil {
		t.Fatal(err)
	}
	invocationID := fixture.insertInvocation(t, ctx)
	if err := ledger.Reserve(ctx, "test.setbalance", invocationID, modelpricing.USDFromDollars(3), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.SetBalance(ctx, "test.setbalance", modelpricing.USDFromDollars(2), now.Add(time.Second)); !errors.Is(err, costledger.ErrInvalidRequest) {
		t.Fatalf("set-balance below reserved: err=%v want ErrInvalidRequest", err)
	}
	updated, err := ledger.SetBalance(ctx, "test.setbalance", modelpricing.USDFromDollars(10), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if updated.BalanceUSD != modelpricing.USDFromDollars(10) || updated.ReservedUSD != modelpricing.USDFromDollars(3) {
		t.Fatalf("top-up above reserved should succeed: %+v", updated)
	}
}

func TestConcurrentReservationsNeverOverspendTheWallet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := ledger.SetBalance(ctx, "test.concurrent", modelpricing.USDFromDollars(1), now); err != nil {
		t.Fatal(err)
	}

	const attempts = 10
	perAttempt := modelpricing.USDFromDollars(0.2) // 5 of these would exactly exhaust $1
	invocationIDs := make([]int64, attempts)
	for i := range invocationIDs {
		invocationIDs[i] = fixture.insertInvocation(t, ctx)
	}

	var wg sync.WaitGroup
	successes := make([]bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := ledger.Reserve(ctx, "test.concurrent", invocationIDs[i], perAttempt, now)
			successes[i] = err == nil
			if err != nil && !errors.Is(err, costledger.ErrInsufficientBalance) {
				t.Errorf("unexpected reserve error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	if successCount != 5 {
		t.Fatalf("expected exactly 5 successful reservations to exhaust $1.00 at $0.20 each, got %d", successCount)
	}
	wallet, err := ledger.GetWallet(ctx, "test.concurrent")
	if err != nil {
		t.Fatal(err)
	}
	if wallet.ReservedUSD != modelpricing.USDFromDollars(1) {
		t.Fatalf("reserved=%d want exactly the full balance reserved, no overspend", wallet.ReservedUSD)
	}
}
