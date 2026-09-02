package costgate

import (
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// TestTranslateReserveErrDistinguishesWalletProvisioning proves G2-001's
// fix at its narrowest point: a provider with NO provider_wallets row at
// all becomes modelruntime.ErrProviderWalletNotProvisioned (detectable via
// errors.Is by DispatchService, which never imports internal/costledger
// directly), while every other reservation failure -- including a real,
// funded wallet that ran out of balance -- passes through completely
// unchanged, since those are genuine budget exhaustion, not a forgotten
// provisioning step.
func TestTranslateReserveErrDistinguishesWalletProvisioning(t *testing.T) {
	t.Run("missing wallet row becomes ErrProviderWalletNotProvisioned", func(t *testing.T) {
		got := translateReserveErr(costledger.ErrWalletNotFound)
		if !errors.Is(got, modelruntime.ErrProviderWalletNotProvisioned) {
			t.Fatalf("expected errors.Is match against ErrProviderWalletNotProvisioned, got %v", got)
		}
		if !errors.Is(got, costledger.ErrWalletNotFound) {
			t.Fatalf("expected the original costledger.ErrWalletNotFound to remain inspectable via errors.Is, got %v", got)
		}
	})

	t.Run("real budget exhaustion is left untouched", func(t *testing.T) {
		got := translateReserveErr(costledger.ErrInsufficientBalance)
		if !errors.Is(got, costledger.ErrInsufficientBalance) {
			t.Fatalf("expected errors.Is match against ErrInsufficientBalance, got %v", got)
		}
		if errors.Is(got, modelruntime.ErrProviderWalletNotProvisioned) {
			t.Fatal("a real, funded wallet running out of balance must never be classified as unprovisioned")
		}
	})

	t.Run("an unrelated error passes through unchanged", func(t *testing.T) {
		unrelated := errors.New("some other reservation failure")
		got := translateReserveErr(unrelated)
		if got != unrelated {
			t.Fatalf("expected the unrelated error to pass through unchanged, got %v", got)
		}
	})
}
