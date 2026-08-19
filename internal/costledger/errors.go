package costledger

import "errors"

var (
	ErrWalletNotFound      = errors.New("provider wallet not configured")
	ErrInsufficientBalance = errors.New("insufficient provider wallet balance")
	ErrReservationNotFound = errors.New("cost reservation not found")
	ErrInvalidRequest      = errors.New("invalid cost ledger request")
	// ErrAlreadyTerminal means this reservation already has the OTHER
	// terminal outcome (Reconcile called after Release already applied, or
	// vice versa) — enforced by a database constraint, not just the
	// (provider_id, invocation_id, kind) idempotency key, since kind
	// differs between 'committed' and 'released'.
	ErrAlreadyTerminal = errors.New("cost reservation already has a different terminal outcome")
	// ErrAmountMismatch means a retried Reserve call for an invocation
	// that was already reserved asked for a different amount than what is
	// actually on the ledger.
	ErrAmountMismatch        = errors.New("cost reservation retried with a different amount")
	ErrProgramBudgetExceeded = errors.New("program model budget exceeded")
)
