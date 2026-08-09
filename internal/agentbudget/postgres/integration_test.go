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

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	budgetIntegrationOrg  = "explorarte"
	budgetIntegrationRole = "ingenieria_ia/qa"
	budgetIntegrationUnit = "ingenieria_ia"
)

type budgetFixture struct {
	store      *platformpostgres.Store
	revisionID int64
}

func openBudgetFixture(t *testing.T, ctx context.Context) budgetFixture {
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
	store, err := platformpostgres.Open(ctx, cfg.Database, "agentbudget-integration-test")
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

	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, budgetIntegrationOrg, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	syncResult, err := registryService.SynchronizeCanonical(ctx, true)
	if err != nil || !syncResult.Applied {
		t.Fatalf("sync registry: result=%+v err=%v", syncResult, err)
	}
	revision, err := registryRepo.GetCurrentRevision(ctx, budgetIntegrationOrg)
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

	return budgetFixture{store: store, revisionID: revision.ID}
}

var budgetTaskCounter int64
var budgetTaskMu sync.Mutex

func (f budgetFixture) insertTask(t *testing.T, ctx context.Context, roleID string) int64 {
	t.Helper()
	budgetTaskMu.Lock()
	budgetTaskCounter++
	ordinal := budgetTaskCounter
	budgetTaskMu.Unlock()

	now := time.Now().UTC().Truncate(time.Microsecond)
	var taskID int64
	if err := f.store.Pool().QueryRow(ctx, `
INSERT INTO tasks (
 organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,
 idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,
 max_attempts,attempt_count,version,created_at,updated_at
) VALUES ($1,$2,'empresa/ceo',$3,$4,$5,$6,$7,$8,'[]'::jsonb,'running',0,$9,1,1,1,$9,$9)
RETURNING id`, budgetIntegrationOrg, f.revisionID, roleID, budgetIntegrationUnit,
		fmt.Sprintf("agentbudget-fixture-task-%d", ordinal), digest(fmt.Sprintf("agentbudget-task-%d", ordinal)), fmt.Sprintf("Budget fixture %d", ordinal), "durable budget fixture", now,
	).Scan(&taskID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	return taskID
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func testLimits() agentbudget.Limits {
	return agentbudget.Limits{
		MaxUSD: modelpricing.USDFromDollars(10), MaxTokens: 100_000, MaxModelCalls: 20,
		MaxWallTimeMS: 3_600_000, MaxDepth: 3, MaxRetries: 5, MaxSubagents: 10,
	}
}

func TestCreateRootBudgetIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, budgetIntegrationRole)

	first, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, budgetIntegrationRole, testLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	if first.TaskID != rootTaskID || first.RootTaskID != rootTaskID || first.ParentBudgetID != nil || first.Usage.Depth != 1 {
		t.Fatalf("root budget=%+v", first)
	}
	second, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, budgetIntegrationRole, testLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Version != first.Version {
		t.Fatalf("re-creating the same root task must be idempotent: first=%+v second=%+v", first, second)
	}
}

func TestInheritForChildSharedModeUpdatesDepthAndSubagentsOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", testLimits(), now)
	if err != nil {
		t.Fatal(err)
	}

	leaderTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador")
	afterLeader, err := ledger.InheritForChild(ctx, root.ID, leaderTaskID, "ingenieria_ia/orquestador", 2, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if afterLeader.ID != root.ID {
		t.Fatalf("shared mode must return the same budget id: got=%d want=%d", afterLeader.ID, root.ID)
	}
	if afterLeader.Usage.Depth != 2 || afterLeader.Usage.UsedSubagents != 1 {
		t.Fatalf("after leader inherit: %+v", afterLeader.Usage)
	}

	// Two workers at the same depth under the leader must not each push
	// depth deeper — only the first inherit at a new depth should move it.
	workerAID := fixture.insertTask(t, ctx, budgetIntegrationRole)
	afterWorkerA, err := ledger.InheritForChild(ctx, root.ID, workerAID, budgetIntegrationRole, 3, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if afterWorkerA.Usage.Depth != 3 || afterWorkerA.Usage.UsedSubagents != 2 {
		t.Fatalf("after worker A: %+v", afterWorkerA.Usage)
	}
	workerBID := fixture.insertTask(t, ctx, budgetIntegrationRole)
	afterWorkerB, err := ledger.InheritForChild(ctx, root.ID, workerBID, budgetIntegrationRole, 3, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if afterWorkerB.Usage.Depth != 3 || afterWorkerB.Usage.UsedSubagents != 3 {
		t.Fatalf("after worker B (sibling, same depth): %+v", afterWorkerB.Usage)
	}

	// Re-attaching the same child again must be a no-op.
	again, err := ledger.InheritForChild(ctx, root.ID, workerBID, budgetIntegrationRole, 3, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if again.Usage.UsedSubagents != 3 {
		t.Fatalf("duplicate inherit changed subagent count: %+v", again.Usage)
	}
}

func TestInheritForChildRejectsDepthBeyondLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	limits := testLimits()
	limits.MaxDepth = 2
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", limits, now)
	if err != nil {
		t.Fatal(err)
	}
	childTaskID := fixture.insertTask(t, ctx, budgetIntegrationRole)
	if _, err := ledger.InheritForChild(ctx, root.ID, childTaskID, budgetIntegrationRole, 3, nil, now); !errors.Is(err, agentbudget.ErrBudgetExceeded) {
		t.Fatalf("depth beyond max: err=%v want ErrBudgetExceeded", err)
	}
}

func TestInheritForChildWithAllocationCarvesOutFromParent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", testLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	allocation := agentbudget.Limits{MaxUSD: modelpricing.USDFromDollars(2), MaxTokens: 10_000, MaxModelCalls: 5, MaxWallTimeMS: 60_000, MaxDepth: 3, MaxRetries: 2, MaxSubagents: 1}
	childTaskID := fixture.insertTask(t, ctx, budgetIntegrationRole)
	child, err := ledger.InheritForChild(ctx, root.ID, childTaskID, budgetIntegrationRole, 2, &allocation, now)
	if err != nil {
		t.Fatal(err)
	}
	if child.ID == root.ID {
		t.Fatal("an explicit allocation must create a new budget row, not share the parent's")
	}
	if child.ParentBudgetID == nil || *child.ParentBudgetID != root.ID {
		t.Fatalf("child parent=%v want=%d", child.ParentBudgetID, root.ID)
	}
	if child.Limits != allocation {
		t.Fatalf("child limits=%+v want=%+v", child.Limits, allocation)
	}

	parentAfter, err := ledger.GetBudget(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parentAfter.Usage.UsedUSD != allocation.MaxUSD || parentAfter.Usage.UsedTokens != allocation.MaxTokens || parentAfter.Usage.UsedSubagents != 1 {
		t.Fatalf("parent after carve-out: %+v", parentAfter.Usage)
	}

	// Re-attaching must be idempotent: same child budget id, parent not
	// debited a second time.
	again, err := ledger.InheritForChild(ctx, root.ID, childTaskID, budgetIntegrationRole, 2, &allocation, now)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != child.ID {
		t.Fatalf("duplicate allocation inherit created a different budget: %d vs %d", again.ID, child.ID)
	}
	parentAgain, err := ledger.GetBudget(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parentAgain.Usage.UsedUSD != parentAfter.Usage.UsedUSD {
		t.Fatalf("duplicate carve-out double-debited the parent: %d -> %d", parentAfter.Usage.UsedUSD, parentAgain.Usage.UsedUSD)
	}
}

func TestInheritForChildRejectsAllocationExceedingParentRemainder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", testLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	tooMuch := agentbudget.Limits{MaxUSD: modelpricing.USDFromDollars(1000), MaxTokens: 1, MaxModelCalls: 1, MaxWallTimeMS: 1, MaxDepth: 3, MaxRetries: 1, MaxSubagents: 1}
	childTaskID := fixture.insertTask(t, ctx, budgetIntegrationRole)
	if _, err := ledger.InheritForChild(ctx, root.ID, childTaskID, budgetIntegrationRole, 2, &tooMuch, now); !errors.Is(err, agentbudget.ErrParentExhausted) {
		t.Fatalf("err=%v want ErrParentExhausted", err)
	}
}

func TestConsumeModelCallEnforcesLimitsAndIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	limits := agentbudget.Limits{MaxUSD: modelpricing.USDFromDollars(1), MaxTokens: 1_000, MaxModelCalls: 2, MaxWallTimeMS: 60_000, MaxDepth: 3, MaxRetries: 2, MaxSubagents: 1}
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", limits, now)
	if err != nil {
		t.Fatal(err)
	}

	delta := agentbudget.Usage{UsedUSD: modelpricing.USDFromDollars(0.5), UsedTokens: 400, UsedModelCalls: 1, UsedWallTimeMS: 1000}
	if err := ledger.ConsumeModelCall(ctx, root.ID, 111, delta, now); err != nil {
		t.Fatal(err)
	}
	// Same invocation id again must be a no-op, not a second debit.
	if err := ledger.ConsumeModelCall(ctx, root.ID, 111, delta, now); err != nil {
		t.Fatal(err)
	}
	afterDup, err := ledger.GetBudget(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterDup.Usage.UsedUSD != delta.UsedUSD || afterDup.Usage.UsedModelCalls != 1 {
		t.Fatalf("duplicate consume changed usage: %+v", afterDup.Usage)
	}

	// A second real call that would exceed max_model_calls (2) combined
	// with the token limit must fail closed and not partially apply.
	over := agentbudget.Usage{UsedUSD: modelpricing.USDFromDollars(0.1), UsedTokens: 700, UsedModelCalls: 1}
	if err := ledger.ConsumeModelCall(ctx, root.ID, 222, over, now); !errors.Is(err, agentbudget.ErrBudgetExceeded) {
		t.Fatalf("err=%v want ErrBudgetExceeded", err)
	}
	afterRejected, err := ledger.GetBudget(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRejected.Usage.UsedTokens != afterDup.Usage.UsedTokens {
		t.Fatalf("rejected consume must not partially apply: before=%d after=%d", afterDup.Usage.UsedTokens, afterRejected.Usage.UsedTokens)
	}
}

func TestConcurrentConsumeNeverExceedsModelCallLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openBudgetFixture(t, ctx)
	ledger, err := agentbudgetpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTaskID := fixture.insertTask(t, ctx, "empresa/ceo")
	limits := agentbudget.Limits{MaxUSD: modelpricing.USDFromDollars(1000), MaxTokens: 1_000_000, MaxModelCalls: 5, MaxWallTimeMS: 3_600_000, MaxDepth: 3, MaxRetries: 5, MaxSubagents: 5}
	root, err := ledger.CreateRootBudget(ctx, budgetIntegrationOrg, rootTaskID, "empresa/ceo", limits, now)
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 12
	var wg sync.WaitGroup
	successes := make([]bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := ledger.ConsumeModelCall(ctx, root.ID, int64(1000+i), agentbudget.Usage{UsedModelCalls: 1}, now)
			successes[i] = err == nil
			if err != nil && !errors.Is(err, agentbudget.ErrBudgetExceeded) {
				t.Errorf("unexpected consume error: %v", err)
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
		t.Fatalf("expected exactly 5 successful calls against a limit of 5, got %d", successCount)
	}
	final, err := ledger.GetBudget(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Usage.UsedModelCalls != 5 {
		t.Fatalf("used_model_calls=%d want=5, no overspend", final.Usage.UsedModelCalls)
	}
}
