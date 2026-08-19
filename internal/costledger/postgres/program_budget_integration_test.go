//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

func bindCorrelation(t *testing.T, f ledgerFixture, ctx context.Context, invocationID int64, correlation string) {
	t.Helper()
	if _, err := f.store.Pool().Exec(ctx, `UPDATE tasks SET correlation_id=$1 WHERE id=(SELECT task_id FROM model_invocations WHERE id=$2)`, correlation, invocationID); err != nil {
		t.Fatalf("bind correlation: %v", err)
	}
}

func programReservation(provider string, models []string, invocation int64, correlation string, max, estimate int64) costledger.ProgramReservation {
	return costledger.ProgramReservation{ProviderID: provider, FamilyModelIDs: models, InvocationID: invocation, CorrelationID: correlation, MaxUSD: modelpricing.USDNanos(max * 1_000_000_000), EstimatedUSD: modelpricing.USDNanos(estimate * 1_000_000_000)}
}

func TestProgramFamilyCeilingAndConcurrency(t *testing.T) {
	ctx := context.Background()
	f := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(f.store)
	if err != nil {
		t.Fatal(err)
	}
	family := []string{f.providerModelID, "deepseek-v4-flash"}
	a := f.insertInvocation(t, ctx)
	b := f.insertInvocation(t, ctx)
	bindCorrelation(t, f, ctx, a, "program-family-a")
	bindCorrelation(t, f, ctx, b, "program-family-a")
	now := time.Now().UTC()
	if err := ledger.ReserveWithinProgramCeiling(ctx, programReservation(f.providerID, family, a, "program-family-a", 7, 4), now); err != nil {
		t.Fatalf("pro reserve: %v", err)
	}
	if err := ledger.ReserveWithinProgramCeiling(ctx, programReservation(f.providerID, family, b, "program-family-a", 7, 3), now); err != nil {
		t.Fatalf("flash exact boundary: %v", err)
	}
	if err := ledger.Release(ctx, f.providerID, a, now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Release(ctx, f.providerID, b, now); err != nil {
		t.Fatal(err)
	}
	c := f.insertInvocation(t, ctx)
	d := f.insertInvocation(t, ctx)
	bindCorrelation(t, f, ctx, c, "program-family-b")
	bindCorrelation(t, f, ctx, d, "program-family-b")
	if err := ledger.ReserveWithinProgramCeiling(ctx, programReservation(f.providerID, family, c, "program-family-b", 7, 4), now); err != nil {
		t.Fatal(err)
	}
	err = ledger.ReserveWithinProgramCeiling(ctx, programReservation(f.providerID, family, d, "program-family-b", 7, 4), now)
	if !errors.Is(err, costledger.ErrProgramBudgetExceeded) {
		t.Fatalf("expected combined ceiling denial, got %v", err)
	}
	if err := ledger.Release(ctx, f.providerID, c, now); err != nil {
		t.Fatal(err)
	}
	e := f.insertInvocation(t, ctx)
	g := f.insertInvocation(t, ctx)
	bindCorrelation(t, f, ctx, e, "program-family-c")
	bindCorrelation(t, f, ctx, g, "program-family-c")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []int64{e, g} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			results <- ledger.ReserveWithinProgramCeiling(ctx, programReservation(f.providerID, family, id, "program-family-c", 7, 4), time.Now().UTC())
		}(id)
	}
	wg.Wait()
	close(results)
	ok, denied := 0, 0
	for err := range results {
		if err == nil {
			ok++
		}
		if errors.Is(err, costledger.ErrProgramBudgetExceeded) {
			denied++
		}
	}
	if ok != 1 || denied != 1 {
		t.Fatalf("concurrent ceiling result success=%d denied=%d", ok, denied)
	}
}
