package costledger

import (
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

type ProviderWallet struct {
	ProviderID  string
	BalanceUSD  modelpricing.USDNanos
	ReservedUSD modelpricing.USDNanos
	UpdatedAt   time.Time
	Version     int64
}

// Available is the balance not already committed to an in-flight
// reservation — the amount a new call may still reserve against.
func (w ProviderWallet) Available() modelpricing.USDNanos { return w.BalanceUSD - w.ReservedUSD }

type EventKind string

const (
	EventReserved  EventKind = "reserved"
	EventCommitted EventKind = "committed"
	EventReleased  EventKind = "released"
)

func (k EventKind) Valid() bool {
	switch k {
	case EventReserved, EventCommitted, EventReleased:
		return true
	default:
		return false
	}
}

type WalletEvent struct {
	ID           int64
	ProviderID   string
	InvocationID int64
	Kind         EventKind
	AmountUSD    modelpricing.USDNanos
	CreatedAt    time.Time
}
