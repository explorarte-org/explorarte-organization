package modelruntime

import (
	"bytes"
	"strings"
	"testing"
)

const reasoningMarker = "this is how the role decided"

// Reasoning reaches the one place that is allowed to hold it.
func TestReasoningReachesTheNormalizedResponse(t *testing.T) {
	normalized, _ := normalizeWithReasoning(t, []byte(reasoningMarker))
	if !bytes.Equal(normalized.RoleReasoning, []byte(reasoningMarker)) {
		t.Fatalf("reasoning did not reach the normalized response: %q", normalized.RoleReasoning)
	}
	// A copy, not an alias: a caller that mutated the raw slice afterwards
	// must not be able to rewrite what was recorded as evidence.
	source := []byte(reasoningMarker)
	kept, _ := normalizeWithReasoning(t, source)
	source[0] = 'X'
	if bytes.Equal(kept.RoleReasoning, source) {
		t.Fatal("the normalized response aliases the caller's buffer, so recorded reasoning can be changed after the fact")
	}
}

// The rule that existed before this feature still holds, and this is the test
// that says so. Reasoning explains a result; it is not part of one, so it can
// reach neither the hash that identifies the result nor anything derived from
// it.
func TestReasoningIsAbsentFromTheResultItExplains(t *testing.T) {
	withReasoning, _ := normalizeWithReasoning(t, []byte(reasoningMarker))
	without, _ := normalizeWithReasoning(t, nil)

	// The strongest form of "not part of the result": the result is
	// byte-identical whether or not reasoning was produced.
	if withReasoning.Result.ResponseHash != without.Result.ResponseHash {
		t.Fatal("reasoning changed the response hash, so it is participating in result identity")
	}
	if withReasoning.Result.ResponseBytes != without.Result.ResponseBytes {
		t.Fatal("reasoning changed the recorded response size")
	}
	if strings.Contains(string(withReasoning.Result.TextOutput), reasoningMarker) ||
		bytes.Contains(withReasoning.Result.JSONOutput, []byte(reasoningMarker)) {
		t.Fatal("reasoning leaked into the result payload, which is hashed, projected into context, and acted on")
	}
}

// Reasoning is bounded by the same budget as the rest of the response, and
// truncated rather than rejected: losing an invocation because its
// explanation ran long would trade the thing that matters for the thing that
// describes it.
func TestOversizedReasoningIsTruncatedNotFatal(t *testing.T) {
	normalized, _ := normalizeWithReasoning(t, bytes.Repeat([]byte("r"), 4096))
	if len(normalized.RoleReasoning) == 0 {
		t.Fatal("an oversized explanation must still be kept, bounded")
	}
	if len(normalized.RoleReasoning) > 1024 {
		t.Fatalf("reasoning=%d bytes exceeds the response budget", len(normalized.RoleReasoning))
	}
}

func TestAbsentReasoningStaysAbsent(t *testing.T) {
	normalized, _ := normalizeWithReasoning(t, nil)
	if normalized.RoleReasoning != nil {
		t.Fatal("a provider that reports no reasoning must not produce an empty row")
	}
}

func normalizeWithReasoning(t *testing.T, reasoning []byte) (NormalizedResponse, RawResponse) {
	t.Helper()
	raw := RawResponse{
		Content:          []byte(`{"ok":true}`),
		HiddenReasoning:  reasoning,
		InputTokens:      10,
		OutputTokens:     20,
		ProviderReported: true,
	}
	normalized, err := Normalizer{MaxResponseBytes: 1024, MaxToolIntents: 0}.Normalize(
		Invocation{ID: 1, OutputMode: OutputJSON}, 2, raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return normalized, raw
}
