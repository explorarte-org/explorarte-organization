package costledger

import "errors"

var (
	ErrWalletNotFound      = errors.New("provider wallet not configured")
	ErrInsufficientBalance = errors.New("insufficient provider wallet balance")
	ErrReservationNotFound = errors.New("cost reservation not found")
	ErrInvalidRequest      = errors.New("invalid cost ledger request")
)
