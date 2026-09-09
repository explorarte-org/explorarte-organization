package postgres_test

// Postgres concurrency proofs for the Mistral local credit gate:
//   1. ProvisionWalletIfAbsent 8 concurrent -> exactly 1 creator
//   2. never auto-raise (higher/lower ceiling after provisioning)
//   3. Reserve 8 concurrent with balance for exactly one call ->
//      1 success, 7 ErrInsufficientBalance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

// TestProvisionWalletIfAbsentEightWay proves exactly one concurrent
// provisioner wins and the wallet ends up with the configured ceiling.
func TestProvisionWalletIfAbsentEightWay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	provider := fmt.Sprintf("test.provision8x-%d", time.Now().UnixNano())
	ceiling := modelpricing.USDFromDollars(10)

	const workers = 8
	created := make(chan bool, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := ledger.ProvisionWalletIfAbsent(ctx, provider, ceiling, time.Now().UTC())
			created <- ok
			errs <- err
		}()
	}
	wg.Wait()
	close(created)
	close(errs)
	winners := 0
	for err := range errs {
		if err != nil {
			t.Fatalf("provision error: %v", err)
		}
	}
	for ok := range created {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("exactly one creator expected, got %d", winners)
	}
	wallet, err := ledger.GetWallet(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.BalanceUSD != ceiling {
		t.Fatalf("balance = %d, want %d", wallet.BalanceUSD, ceiling)
	}
	if wallet.ReservedUSD != 0 {
		t.Fatalf("reserved = %d, want 0", wallet.ReservedUSD)
	}

	// Never auto-raise: higher ceiling must NOT change the balance.
	higher := ceiling * 2
	ok, err := ledger.ProvisionWalletIfAbsent(ctx, provider, higher, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("provision on existing wallet must return created=false")
	}
	after, err := ledger.GetWallet(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	if after.BalanceUSD != ceiling {
		t.Fatalf("balance changed after higher provision: %d -> %d", ceiling, after.BalanceUSD)
	}

	// Never auto-raise: lower ceiling must NOT change the balance either.
	lower := ceiling / 2
	ok, err = ledger.ProvisionWalletIfAbsent(ctx, provider, lower, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("lower provision on existing wallet must return created=false")
	}
	after, err = ledger.GetWallet(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	if after.BalanceUSD != ceiling {
		t.Fatalf("balance changed after lower provision: %d -> %d", ceiling, after.BalanceUSD)
	}
}

// TestReserveEightWaySingleWinner proves a wallet funded for exactly one
// reservation yields 1 success and 7 ErrInsufficientBalance across 8
// concurrent Reserve calls, and that the reserved amount matches exactly one
// estimated cost at the canonical Mistral Standard pricing.
func TestReserveEightWaySingleWinner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fixture := openLedgerFixture(t, ctx)
	ledger, err := costledgerpostgres.New(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	provider := fmt.Sprintf("test.reserve8x-%d", time.Now().UnixNano())

	// Exactly one call at the verified Mistral Standard pricing:
	// 28 input + 10 output tokens at 150000000 nanos/1M each = 5700 nanos.
	const tokensIn, tokensOut = int64(28), int64(10)
	perCall := (tokensIn + tokensOut) * 150000000 / 1_000_000 // 5700 nanos
	if perCall != 5700 {
		t.Fatalf("perCall = %d, want 5700", perCall)
	}

	if _, err := ledger.ProvisionWalletIfAbsent(ctx, provider, modelpricing.USDNanos(perCall), now); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	invocationIDs := make([]int64, workers)
	for i := 0; i < workers; i++ {
		invocationIDs[i] = fixture.insertInvocation(t, ctx)
	}
	successes := make(chan bool, workers)
	deniedCount := make(chan int, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := ledger.Reserve(ctx, provider, invocationIDs[idx], modelpricing.USDNanos(perCall), time.Now().UTC())
			if err == nil {
				successes <- true
				deniedCount <- 0
			} else if errors.Is(err, costledger.ErrInsufficientBalance) {
				successes <- false
				deniedCount <- 1
			} else {
				successes <- false
				deniedCount <- 0
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(successes)
	close(deniedCount)
	close(errs)
	wins := 0
	for ok := range successes {
		if ok {
			wins++
		}
	}
	denied := 0
	for d := range deniedCount {
		denied += d
	}
	for err := range errs {
		t.Fatalf("unexpected reserve error: %v", err)
	}
	if wins != 1 {
		t.Fatalf("exactly 1 reservation must succeed, got %d", wins)
	}
	if denied != 7 {
		t.Fatalf("exactly 7 must be denied with ErrInsufficientBalance, got %d", denied)
	}
	wallet, err := ledger.GetWallet(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.ReservedUSD != modelpricing.USDNanos(perCall) {
		t.Fatalf("reserved = %d, want exactly one call cost %d", wallet.ReservedUSD, perCall)
	}
}
