package executive

import "testing"

func TestDefaultLimitsGiveStructuredExecutivePlansOutputHeadroom(t *testing.T) {
	got := DefaultLimits()

	if got.MaxOutputTokens != 32768 {
		t.Fatalf(
			"MaxOutputTokens=%d, want 32768: the previous 8192 ceiling was exhausted by a real CEO structured planning call before the JSON contract completed",
			got.MaxOutputTokens,
		)
	}
}
