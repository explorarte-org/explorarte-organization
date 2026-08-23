package modelruntime

import "testing"

// The pre-call reservation is what admits a call against a ceiling, so the
// estimate must never come in under what the provider will report. Three bytes
// per token did, on every call of AUTONOMY-SMOKE-017-R4.
//
// These are that campaign's measured pairs: bytes actually sent, and the
// prompt_tokens DeepSeek reported for them.
func TestTheInputEstimateIsNeverUnderTheTruth(t *testing.T) {
	measured := []struct {
		name     string
		sent     int
		reported int64
	}{
		{"department plan", 168761, 106074},
		{"worker 1", 184010, 114907},
		{"worker 2", 184099, 114959},
		{"worker 3", 184911, 115514},
		{"worker 4", 182931, 114192},
		{"department review", 171932, 108663},
		{"second round plan", 185049, 115987},
	}
	for _, call := range measured {
		estimate := estimateTokenCount(make([]byte, call.sent))
		if estimate < call.reported {
			t.Errorf("%s: estimated %d for %d bytes, provider reported %d -- the reservation admits a call it cannot pay for",
				call.name, estimate, call.sent, call.reported)
		}
	}
}

// Over-estimating is safe only because the call is settled afterwards; it must
// still stay within reach of reality, or a single call would reserve a whole
// campaign.
func TestTheInputEstimateStaysWithinReachOfTheTruth(t *testing.T) {
	const sent, reported = 184010, 114907
	estimate := estimateTokenCount(make([]byte, sent))
	if estimate > reported*2 {
		t.Fatalf("estimated %d for a call that reported %d: a reservation that large blocks calls it should admit",
			estimate, reported)
	}
}
