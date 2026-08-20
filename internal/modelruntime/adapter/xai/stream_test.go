package xai

import (
	"encoding/json"
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

func TestStreamRespectsTheByteLimit(t *testing.T) {
	huge := `data: {"id":"resp-3","choices":[{"delta":{"content":"` + strings.Repeat("a", 4096) + `"}}]}`
	if _, err := reassembleStream(strings.NewReader(huge), 512); err == nil {
		t.Fatal("the accumulated stream must stay bounded")
	}
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
