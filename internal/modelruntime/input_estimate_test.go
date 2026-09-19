package modelruntime

import (
	"strings"
	"testing"
)

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

// The dispatcher and every host preflight must share ONE input-token rule.
func TestEstimateInputTokensIsTheDispatchRule(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 49901, 184010, 524288} {
		if got, want := EstimateInputTokens(n), estimateTokenCount(make([]byte, n)); got != want {
			t.Fatalf("EstimateInputTokens(%d) = %d, dispatch rule = %d", n, got, want)
		}
	}
	// The measured production CEO-plan input: 49,901 canonical bytes -> 33,268 tokens.
	if got := EstimateInputTokens(49901); got != 33268 {
		t.Fatalf("EstimateInputTokens(49901) = %d, want 33268", got)
	}
}

// The ceiling must dominate a real, measured envelope: it can only ever be
// used to refuse a budget that is too small, never to admit one that is too
// small for the input the real PrepareModelInput would have built.
func TestSingleShotModelInputCeilingDominatesTheRealEnvelope(t *testing.T) {
	const contextBytes, contractBytes = 8192, 1500
	rendered := []byte(strings.Repeat("x", contextBytes))
	snapshot := ContextSnapshotRef{ID: 987654321, RenderedHash: SHA256Bytes(rendered)}
	real, err := PrepareModelInput(&ModelInputEnvelope{
		SchemaVersion: ModelInputEnvelopeSchemaV1, ContextSnapshotID: snapshot.ID,
		CanonicalProjectionDigest: SHA256Bytes(rendered),
		StablePrefix: []ModelInputMessage{
			{Role: ModelInputRoleUser, Content: string(rendered)},
			{Role: ModelInputRoleUser, Content: strings.Repeat("y", contractBytes)},
		},
	}, snapshot, rendered)
	if err != nil {
		t.Fatal(err)
	}
	ceiling, err := SingleShotModelInputCeilingBytes(contextBytes, contractBytes)
	if err != nil {
		t.Fatal(err)
	}
	if ceiling < len(real.CanonicalBytes) {
		t.Fatalf("ceiling %d is below the real canonical input %d", ceiling, len(real.CanonicalBytes))
	}
	if ceiling-len(real.CanonicalBytes) > 512 {
		t.Fatalf("ceiling %d is implausibly far above the real input %d (framing only should differ)", ceiling, len(real.CanonicalBytes))
	}
	if _, err := SingleShotModelInputCeilingBytes(0, 1); err == nil {
		t.Fatal("a zero rendered-context bound must be refused")
	}
}
