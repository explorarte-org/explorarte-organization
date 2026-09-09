package mistral

import "testing"

func TestParseCreditCeilingUSD(t *testing.T) {
	t.Run("absent means not configured, candidate ineligible", func(t *testing.T) {
		nanos, configured, err := ParseCreditCeilingUSD("")
		if err != nil || configured || nanos != 0 {
			t.Fatalf("absent ceiling must be (0,false,nil), got %d %v %v", nanos, configured, err)
		}
	})
	t.Run("ten dollars converts exactly to 1e10 nanos", func(t *testing.T) {
		nanos, configured, err := ParseCreditCeilingUSD("10")
		if err != nil || !configured || nanos != 10_000_000_000 {
			t.Fatalf("10 USD must be 1e10 nanos, got %d %v %v", nanos, configured, err)
		}
	})
	t.Run("sub-cent values are representable", func(t *testing.T) {
		nanos, _, err := ParseCreditCeilingUSD("0.05")
		if err != nil || nanos != 50_000_000 {
			t.Fatalf("0.05 USD must be 5e7 nanos, got %d %v", nanos, err)
		}
	})
	t.Run("nano resolution is exact without float", func(t *testing.T) {
		nanos, _, err := ParseCreditCeilingUSD("0.000000001")
		if err != nil || nanos != 1 {
			t.Fatalf("0.000000001 USD must be exactly 1 nano, got %d %v", nanos, err)
		}
	})
	for name, raw := range map[string]string{
		"negative":               "-5",
		"zero":                   "0",
		"non-numeric":            "ten-dollars",
		"over-bound":             "10001",
		"beyond-nano-resolution": "0.0000000001",
	} {
		if _, _, err := ParseCreditCeilingUSD(raw); err == nil {
			t.Fatalf("%s: %q must be rejected", name, raw)
		}
	}
}
