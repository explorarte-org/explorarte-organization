package mistral

import (
	"fmt"
	"math"
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
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD is not a number: %w", err)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD must be finite")
	}
	if value <= 0 {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD must be positive")
	}
	if value > 10000 {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD exceeds the 10000 USD sanity bound")
	}
	nanos = int64(math.Round(value * 1e9))
	if nanos <= 0 {
		return 0, true, fmt.Errorf("MISTRAL_CREDIT_CEILING_USD rounds to zero nanos")
	}
	return nanos, true, nil
}
