package mistral

import (
	"fmt"
	"strconv"
	"strings"
)

// =============================================================================
// Local credit ceiling — barrier 2 (Explorarte side)
//
// Barrier 1 is upstream (organization PAYG setting, human-confirmed).
// Barrier 2 is local and fail-closed: the deployment seeds the mistral
// provider wallet with an EXPLICIT ceiling. There is no default value —
// a missing/invalid ceiling means the Mistral candidate stays ineligible.
//
// Naming honesty: this is a LOCAL ceiling on local accounting
// (LocalCeiling/LocalUsed/LocalRemaining). It is never claimed to be the
// provider's real remaining credit unless that comes from Mistral itself.
// =============================================================================

// ParseCreditCeilingUSD converts MISTRAL_CREDIT_CEILING_USD into USDNanos.
//
// Rules (fail-closed):
//   - absent/empty  -> (0, false, nil): not configured, candidate ineligible
//   - non-numeric   -> error
//   - <= 0          -> error
//   - > 10,000 USD  -> error (bounds against fat-finger configuration)
//
// Example: "10" -> 10,000,000,000 nanos. Sub-cent values are representable
// ("0.05" -> 50,000,000 nanos).
func ParseCreditCeilingUSD(raw string) (nanos int64, configured bool, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false, nil
	}
	// Exact decimal parsing — NEVER float64: the modelpricing domain keeps
	// money in integer nanos, and a monetary invariant must not pass
	// through binary floating point.
	parts := strings.SplitN(trimmed, ".", 2)
	intPart := strings.TrimSpace(parts[0])
	fracPart := ""
	if len(parts) == 2 {
		fracPart = strings.TrimSpace(parts[1])
	}
	if intPart == "" && fracPart == "" {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD is empty")
	}
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD has a non-digit integer part")
		}
	}
	for _, r := range fracPart {
		if r < '0' || r > '9' {
			return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD has a non-digit fractional part")
		}
	}
	if len(fracPart) > 9 {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD supports at most 9 decimals (1 nano resolution)")
	}
	if intPart == "" {
		intPart = "0"
	}
	for len(fracPart) < 9 {
		fracPart += "0"
	}
	digits := intPart + fracPart
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD overflows USDNanos: %w", err)
	}
	if n <= 0 {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD must be positive")
	}
	if n > 10_000_000_000_000 { // 10,000 USD sanity bound
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD exceeds the 10000 USD sanity bound")
	}
	return n, true, nil
}

// =============================================================================
// Local credit ceiling — barrier 2 (Explorarte side)
//
// Barrier 1 is upstream (organization PAYG setting, human-confirmed).
// Barrier 2 is local and fail-closed: the deployment seeds the mistral
// provider wallet with an EXPLICIT ceiling. There is no default value —
// a missing/invalid ceiling means the Mistral candidate stays ineligible.
//
// Naming honesty: this is a LOCAL ceiling on local accounting
// (LocalCeiling/LocalUsed/LocalRemaining). It is never claimed to be the
// provider's real remaining credit unless that comes from Mistral itself.
// =============================================================================

// ParseCreditCeilingUSD converts MISTRAL_CREDIT_CEILING_USD into USDNanos.
//
// Rules (fail-closed):
//   - absent/empty  -> (0, false, nil): not configured, candidate ineligible
//   - non-numeric   -> error
//   - <= 0          -> error
//   - > 10,000 USD  -> error (bounds against fat-finger configuration)
//
// Example: "10" -> 10,000,000,000 nanos. Sub-cent values are representable
// ("0.05" -> 50,000,000 nanos).
