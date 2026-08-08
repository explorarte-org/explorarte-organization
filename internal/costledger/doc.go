// Package costledger tracks a real-money running balance per model
// provider and reserves/reconciles it around each call. A call reserves
// its worst-case estimated cost against the provider's wallet before the
// provider is ever contacted; after the call completes, the reservation is
// reconciled down to the real cost computed from reported token usage.
// There is no auto-top-up and no provider is ever spendable without an
// explicitly configured wallet — the same fail-closed posture as
// internal/modelpricing.
package costledger
