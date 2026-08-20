package xai

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// The reassembled stream must be the SAME document the non-streaming path
// returns, because everything downstream -- hashing, provenance, choice
// validation, content decoding, tool intents, finish reason, usage -- reads
// that one shape. A second shape here would be a second representation of the
// provider's answer, and the two would drift.

func TestReassembledStreamIsTheNonStreamingDocument(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"resp-1","choices":[{"delta":{"content":"{\"verdict\":"},"finish_reason":null}]}`,
		`data: {"id":"resp-1","choices":[{"delta":{"content":"\"reject\"}"},"finish_reason":null}]}`,
		`data: {"id":"resp-1","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7}}`,
		"data: [DONE]",
		"",
	}, "\n")

	body, err := reassembleStream(strings.NewReader(stream), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chatResponse
	if err = json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the reassembly must parse as the non-streaming response type: %v", err)
	}
	if decoded.ID != "resp-1" {
		t.Fatalf("provenance lost: id=%q", decoded.ID)
	}
	if len(decoded.Choices) != 1 {
		t.Fatalf("want exactly one choice, got %d", len(decoded.Choices))
	}
	content, err := decodeContent(decoded.Choices[0].Message.Content)
	if err != nil {
		t.Fatal(err)
	}
	// Fragments concatenate in order. Reordering or dropping one would
	// produce invalid JSON from a perfectly good review.
	if string(content) != `{"verdict":"reject"}` {
		t.Fatalf("content=%q", content)
	}
	if decoded.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason=%q", decoded.Choices[0].FinishReason)
	}
	// Usage arrives on its own terminal chunk; losing it would make the cost
	// ledger settle a real call against nothing.
	if decoded.Usage.PromptTokens != 12 || decoded.Usage.CompletionTokens != 7 {
		t.Fatalf("usage lost: %+v", decoded.Usage)
	}
}

func TestReassembledStreamRebuildsFragmentedToolCalls(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"resp-2","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`data: {"id":"resp-2","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`data: {"id":"resp-2","choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"data: [DONE]",
		"",
	}, "\n")

	body, err := reassembleStream(strings.NewReader(stream), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chatResponse
	if err = json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	calls := decoded.Choices[0].Message.ToolCalls
	if len(calls) != 1 {
		t.Fatalf("want one tool call, got %d", len(calls))
	}
	// Identity arrives once, on the first fragment only; arguments arrive
	// split. Treating either the way the other works loses the call.
	if calls[0].ID != "call-1" || calls[0].Function.Name != "lookup" {
		t.Fatalf("tool call identity lost: %+v", calls[0])
	}
	if string(calls[0].Function.Arguments) != `{"q":"x"}` {
		t.Fatalf("arguments=%s", calls[0].Function.Arguments)
	}
}

// A stream that carried nothing is a failed call, not an empty completion.
// Returning a well-formed empty document would pass every downstream check
// and be delivered as a legitimately empty adversarial review.
func TestAnEmptyStreamIsAFailureNotAnEmptyReview(t *testing.T) {
	for _, tc := range []struct{ name, stream string }{
		{"only done", "data: [DONE]\n"},
		{"nothing at all", ""},
		{"only comments", ": keep-alive\n: keep-alive\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := reassembleStream(strings.NewReader(tc.stream), 1<<20); err == nil {
				t.Fatal("an empty stream must fail rather than produce an empty completion")
			}
		})
	}
}

// The stream gets its own budget, derived from the document limit rather than
// equal to it. Sizing a stream like a document is what put a real review at
// 84% of the ceiling: 876,829 stream bytes carried a 14,901 byte document,
// because every chunk repeats the envelope around a few tokens of delta.
func TestStreamBudgetIsAMultipleOfTheDocumentLimit(t *testing.T) {
	const documentLimit = 512

	// Comfortably over the document limit, comfortably under the stream
	// budget: this must succeed, and under the old sizing it did not.
	moderate := `data: {"id":"r","choices":[{"delta":{"content":"` + strings.Repeat("a", documentLimit*4) + `"},"finish_reason":"stop"}]}`
	if _, err := reassembleStream(strings.NewReader(moderate), documentLimit); err != nil {
		t.Fatalf("a stream larger than the document it produces must still be read: %v", err)
	}

	// Past the stream budget it must still stop, and say why.
	huge := `data: {"id":"r","choices":[{"delta":{"content":"` + strings.Repeat("a", documentLimit*streamBudgetMultiplier*2) + `"}}]}`
	err := reassembleStream2(strings.NewReader(huge), documentLimit)
	if err == nil {
		t.Fatal("the accumulated stream must stay bounded")
	}
	if code := StreamErrorCode(err, "generic"); code != "stream_exceeds_budget" {
		t.Fatalf("the durable record must name the reason, got %q", code)
	}
}

// Every structural failure names itself. Reporting a single generic code is
// what turned the first streaming failure in production into archaeology that
// could not be completed from the durable record alone.
func TestStreamFailuresNameTheirCause(t *testing.T) {
	for _, tc := range []struct{ name, stream, want string }{
		{"empty", "data: [DONE]\n", "stream_empty"},
		{"invalid event", "data: {not json}\n", "stream_event_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := reassembleStream2(strings.NewReader(tc.stream), 1<<20)
			if err == nil {
				t.Fatal("expected a failure")
			}
			if code := StreamErrorCode(err, "generic"); code != tc.want {
				t.Fatalf("code=%q want %q", code, tc.want)
			}
			// The payload must never travel in an error string: it is
			// logged, wrapped and read by humans, and for the adversarial
			// reviewer it is exactly what the context boundary controls.
			if strings.Contains(err.Error(), "not json") {
				t.Fatalf("the failure quoted provider output: %v", err)
			}
		})
	}
}

func reassembleStream2(r io.Reader, limit int) error {
	_, err := reassembleStream(r, limit)
	return err
}

// Keep-alive comments and event names are not data and must not be parsed as
// chunks; a provider is free to send them.
func TestNonDataLinesAreSkipped(t *testing.T) {
	stream := strings.Join([]string{
		": keep-alive",
		"event: message",
		`data: {"id":"resp-4","choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
	}, "\n")
	body, err := reassembleStream(strings.NewReader(stream), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chatResponse
	if err = json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	content, _ := decodeContent(decoded.Choices[0].Message.Content)
	if string(content) != "ok" {
		t.Fatalf("content=%q", content)
	}
}

// The failure that ended AUTONOMY-SMOKE-001's campaign. xAI reports mid-stream
// failures as an event carrying an error object, with HTTP 200 and an
// event-stream content type. Reading that as "no completion events" threw away
// the provider's own diagnosis and, worse, marked a transient capacity error
// NOT retryable -- so a condition that says "try again in a few minutes" ended
// the run permanently, in one second.
func TestProviderErrorsInsideTheStreamAreSurfaced(t *testing.T) {
	const capacity = `data: {"error":{"message":"The model is currently at capacity due to high demand. Please try again in a few minutes","type":"server_error","code":"resource-exhausted"}}` + "\n"

	err := reassembleStream2(strings.NewReader(capacity), 1<<20)
	if err == nil {
		t.Fatal("a provider error event must fail the read, not produce an empty completion")
	}
	if code := StreamErrorCode(err, "generic"); code != "stream_provider_error:resource-exhausted" {
		t.Fatalf("the durable record must carry the provider's own code, got %q", code)
	}
	if !StreamErrorRetryable(err) {
		t.Fatal("a capacity error is the provider saying \"later\"; refusing to retry ends a campaign for a condition that resolves itself")
	}
	// The provider's free-form message must not travel in the error: it is
	// external text that gets logged, wrapped and read by humans. The stable
	// code is what an operator and a retry policy act on.
	if strings.Contains(err.Error(), "high demand") {
		t.Fatalf("the failure quoted the provider's prose: %v", err)
	}
}

// Not every provider error is transient. Guessing that one is safe to repeat
// is how a single rejected request becomes several.
func TestNonTransientProviderErrorsAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		wantRetry    bool
	}{
		{"capacity", `data: {"error":{"type":"server_error","code":"resource-exhausted"}}`, true},
		{"bad request", `data: {"error":{"type":"invalid_request_error","code":"invalid_schema"}}`, false},
		{"auth", `data: {"error":{"type":"authentication_error","code":"invalid_api_key"}}`, false},
		{"no code", `data: {"error":{"type":"invalid_request_error"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := reassembleStream2(strings.NewReader(tc.stream+"\n"), 1<<20)
			if err == nil {
				t.Fatal("expected a failure")
			}
			if got := StreamErrorRetryable(err); got != tc.wantRetry {
				t.Fatalf("retryable=%v want %v (code %q)", got, tc.wantRetry, StreamErrorCode(err, "-"))
			}
		})
	}
}

// Usage arrives only when it is asked for, and the cost ledger settles real
// spend against it. A document reporting zero tokens for a call that consumed
// them is not a missing detail, it is wrong accounting.
func TestUsageSurvivesWhenTheProviderSendsIt(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"r","choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`data: {"id":"r","choices":[],"usage":{"prompt_tokens":4321,"completion_tokens":765}}`,
		"data: [DONE]",
		"",
	}, "\n")
	body, err := reassembleStream(strings.NewReader(stream), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chatResponse
	if err = json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Usage.PromptTokens != 4321 || decoded.Usage.CompletionTokens != 765 {
		t.Fatalf("usage lost: %+v", decoded.Usage)
	}
}

// Reasoning tokens are generated, billed, and reported OUTSIDE
// completion_tokens. Reading completion_tokens alone recorded 10 output
// tokens for a call that produced 1046, and the agent budget's ceiling and
// the provider wallet both settle against that number.
func TestReasoningTokensAreCountedAsBilledOutput(t *testing.T) {
	// The exact figures xAI returned for a trivial call: the parts add up to
	// its own total, which is what proves reasoning is not already inside
	// completion_tokens.
	usage := chatUsage{
		PromptTokens:           208,
		CompletionTokens:       10,
		CompletionTokensDetail: &completionTokensDetails{ReasoningTokens: 1036},
	}
	if got := usage.billedOutputTokens(); got != 1046 {
		t.Fatalf("billed output=%d, want 1046: reasoning is generated and billed like any other output token", got)
	}
	if usage.PromptTokens+usage.billedOutputTokens() != 1254 {
		t.Fatal("the parts must reconstruct the provider's own total, or one of them is being double counted or dropped")
	}

	// A provider or model that reports no reasoning detail must be unchanged,
	// not treated as zero output.
	plain := chatUsage{PromptTokens: 100, CompletionTokens: 40}
	if got := plain.billedOutputTokens(); got != 40 {
		t.Fatalf("without a reasoning detail the completion count stands, got %d", got)
	}
}
