package xai

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// A non-streaming completion sends no bytes until the model has finished
// generating. That is fine for a fast model and fatal for a reasoning one:
// the transport's ResponseHeaderTimeout measures the wait for the FIRST
// response byte, so it races the model's entire thinking time and wins.
//
// This is not hypothetical. AUTONOMY-SMOKE-001's adversarial review was sent,
// produced no header for 90 seconds, and was cut by the client with
// transport_error while xAI was still working -- nothing was billed, and the
// campaign blocked on an ambiguous outcome nobody could resolve. A direct
// reproduction of the same request shape returned successfully in 80.6s, which
// is how close to the edge the previous configuration ran.
//
// Streaming removes the race rather than moving it. The provider emits the
// first event almost immediately, so ResponseHeaderTimeout stops competing
// with generation time and the configured request timeout becomes the single
// authority on how long a call may take.
//
// The stream is reassembled into the EXACT document the non-streaming path
// returns. That is deliberate: the response representation stays one thing, so
// hashing, provenance, choice validation, content decoding, tool intents,
// finish-reason handling and usage accounting downstream are untouched and
// cannot drift from a second shape.

// A stream is not a document, and sizing it like one was wrong.
//
// Every SSE chunk repeats the envelope -- id, object, created, model,
// system_fingerprint, service_tier -- around a few tokens of delta. Measured
// on a real reasoning review: 876,829 stream bytes carrying a 14,901 byte
// document, a 14.5x ratio. Applying MaxResponseBytes (1 MiB by default, sized
// for a response document) to the raw stream therefore put a moderate review
// at 84% of the ceiling and made a harder one fail for its length rather than
// its content.
//
// The multiplier gives the stream its own budget derived from the configured
// one, so a deployment that raises the document limit raises both together and
// the two cannot be tuned into disagreement. The DOCUMENT this produces is
// still bounded by the original limit -- that is the value the rest of the
// pipeline was sized for.
const streamBudgetMultiplier = 16

// streamError names which structural condition failed, so the durable record
// says what went wrong instead of only that something did.
//
// The codes are content-free by construction. A failure code that quoted the
// payload would put provider output into an error string that gets logged,
// wrapped, and read by humans -- and for the adversarial reviewer that payload
// is the one thing the whole context boundary exists to control.
type streamError struct {
	code string
	err  error
	// retryable is set only for failures the provider itself described as
	// transient. Everything else stays false, because guessing that a
	// failure is safe to repeat is how one bad request becomes several.
	retryable bool
}

func (e *streamError) Error() string { return e.err.Error() }
func (e *streamError) Unwrap() error { return e.err }

func streamFailure(code string, err error) error { return &streamError{code: code, err: err} }

// StreamErrorRetryable reports whether a stream failure is one the provider
// said would pass. It is false for anything else, including a nil error.
func StreamErrorRetryable(err error) bool {
	var streamErr *streamError
	return errors.As(err, &streamErr) && streamErr.retryable
}

// StreamErrorCode returns the specific failure code for a stream error, or the
// generic fallback for anything else.
func StreamErrorCode(err error, fallback string) string {
	var streamErr *streamError
	if errors.As(err, &streamErr) {
		return streamErr.code
	}
	return fallback
}

// streamContentType is what the provider must answer with when Stream is set.
// A JSON body here means the provider ignored the flag, and reassembly would
// silently produce an empty document from a perfectly good response.
const streamContentType = "text/event-stream"

// reassembleStream turns an SSE chat-completion stream into the single
// non-streaming response document it describes.
//
// The byte limit applies to the accumulated stream, not to one event, because
// the thing that must stay bounded is what the provider can make this process
// hold in memory.
func reassembleStream(reader io.Reader, documentLimit int) ([]byte, error) {
	limit := documentLimit * streamBudgetMultiplier
	var (
		id           string
		content      strings.Builder
		finishReason string
		usage        *json.RawMessage
		calls        = map[int]*streamToolCall{}
		order        []int
		consumed     int
		events       int
	)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), limit+1)
	for scanner.Scan() {
		line := scanner.Text()
		consumed += len(line) + 1
		if consumed > limit {
			return nil, streamFailure("stream_exceeds_budget", fmt.Errorf("provider stream exceeds %d bytes", limit))
		}
		payload, ok := eventPayload(line)
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		events++
		// xAI reports mid-stream failures as an event carrying an error
		// object, with HTTP 200 and an event-stream content type. Treating
		// that as "no completion events" is what turned a transient
		// "model is currently at capacity" -- which says to retry in a few
		// minutes -- into an opaque, NOT-RETRYABLE read failure that ended
		// AUTONOMY-SMOKE-001's campaign permanently in one second.
		//
		// The provider's own words are the diagnosis. They are surfaced with
		// its code, and its type decides retryability rather than a guess
		// made here.
		if providerErr := decodeStreamError(payload); providerErr != nil {
			return nil, providerErr
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// The offending bytes are deliberately not quoted: this string
			// is logged and read by humans, and provider output must not
			// reach it.
			return nil, streamFailure("stream_event_invalid", fmt.Errorf("stream event %d is not valid JSON: %w", events, err))
		}
		if id == "" {
			id = strings.TrimSpace(chunk.ID)
		}
		// Usage arrives on its own terminal chunk and must not be lost: the
		// cost ledger settles the real spend against it.
		if len(chunk.Usage) > 0 && !isJSONNull(chunk.Usage) {
			raw := append(json.RawMessage(nil), chunk.Usage...)
			usage = &raw
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if reason := strings.TrimSpace(choice.FinishReason); reason != "" {
			finishReason = reason
		}
		if text := choice.Delta.Content; text != "" {
			content.WriteString(text)
		}
		for _, fragment := range choice.Delta.ToolCalls {
			call, seen := calls[fragment.Index]
			if !seen {
				call = &streamToolCall{}
				calls[fragment.Index] = call
				order = append(order, fragment.Index)
			}
			// Every field arrives fragmented and only sometimes. Later
			// non-empty values win for identity; arguments concatenate,
			// because that is the one field the provider splits across
			// events rather than repeating.
			if v := strings.TrimSpace(fragment.ID); v != "" {
				call.ID = v
			}
			if v := strings.TrimSpace(fragment.Type); v != "" {
				call.Type = v
			}
			if v := strings.TrimSpace(fragment.Function.Name); v != "" {
				call.Name = v
			}
			call.Arguments.WriteString(fragment.Function.Arguments)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, streamFailure("stream_read_failed", fmt.Errorf("read provider stream after %d events: %w", events, err))
	}
	// A stream that produced no event at all is not an empty completion, it
	// is a failed one. Emitting a well-formed document with no id would let
	// it pass every downstream check as a legitimately empty review.
	if id == "" && content.Len() == 0 && len(order) == 0 && finishReason == "" {
		return nil, streamFailure("stream_empty", fmt.Errorf("provider stream carried no completion events (%d data events seen)", events))
	}
	return encodeReassembled(id, content.String(), finishReason, usage, calls, order)
}

func encodeReassembled(id, content, finishReason string, usage *json.RawMessage, calls map[int]*streamToolCall, order []int) ([]byte, error) {
	message := map[string]any{"content": content}
	if len(order) > 0 {
		tools := make([]map[string]any, 0, len(order))
		for _, index := range order {
			call := calls[index]
			arguments := call.Arguments.String()
			if strings.TrimSpace(arguments) == "" {
				// The downstream contract is json.RawMessage; an empty
				// string is not valid JSON and would fail to unmarshal far
				// from the cause.
				arguments = "{}"
			}
			tools = append(tools, map[string]any{
				"id": call.ID, "type": call.Type,
				"function": map[string]any{"name": call.Name, "arguments": json.RawMessage(arguments)},
			})
		}
		message["tool_calls"] = tools
	}
	document := map[string]any{
		"id":      id,
		"choices": []map[string]any{{"message": message, "finish_reason": finishReason}},
	}
	if usage != nil {
		document["usage"] = *usage
	}
	return json.Marshal(document)
}

// eventPayload extracts the payload of one `data:` line. Comments, event
// names and blank separators are not data and are skipped rather than
// rejected, because a provider is free to send them.
func eventPayload(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return "", false
	}
	if !strings.HasPrefix(trimmed, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")), true
}

// decodeStreamError recognises a provider error event and returns it as a
// failure that names itself, or nil when the payload is an ordinary chunk.
//
// The provider's message is deliberately NOT copied into the error. It is
// free-form text from an external service that would then be logged, wrapped
// and read by humans; the stable `code` is what an operator or a retry policy
// can act on, and it is a closed value rather than prose.
func decodeStreamError(payload string) error {
	var envelope struct {
		Error *struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil || envelope.Error == nil {
		return nil
	}
	code := strings.TrimSpace(envelope.Error.Code)
	if code == "" {
		code = "unspecified"
	}
	// A capacity or server error is the provider saying "later"; anything
	// else -- a rejected request, an authorization problem -- would only be
	// rejected again.
	retryable := envelope.Error.Type == "server_error" || code == "resource-exhausted"
	return &streamError{
		code:      providerErrorCode(code),
		err:       fmt.Errorf("provider reported %q (%s) in the response stream", code, envelope.Error.Type),
		retryable: retryable,
	}
}

const streamProviderErrorPrefix = "stream_provider_error"

// providerErrorCode turns a provider's own error code into a durable one.
//
// The provider's code is EXTERNAL INPUT and it lands in a field this system
// validates: an outcome whose ErrorCode is not a normalized token is rejected.
// That rejection happens while RECORDING a failure, so the failure cannot be
// recorded and the attempt it described is left in send_started forever.
//
// That is not hypothetical. This code was previously concatenated in verbatim
// with a colon, which no normalized token may contain, so xAI's first capacity
// error became a stranded invocation (62) and a campaign that could not
// explain itself. An upstream service was effectively choosing the shape of a
// field this system enforces.
//
// The normalization is this package's existing normalizeProviderToken -- the
// same rule already applied to HTTP error bodies -- rather than a second
// sanitizer written beside it. A provider code that does not survive becomes
// "unspecified": losing which capacity error it was costs an operator a
// detail, while an unstorable code costs an invocation.
//
// The 90-byte bound leaves room for the prefix and separator inside the
// durable column's 120, so an over-long provider code is replaced rather than
// cut into something that reads like a different code.
func providerErrorCode(providerCode string) string {
	return streamProviderErrorPrefix + "." + normalizeProviderToken(providerCode, "unspecified", 90)
}

func isJSONNull(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == "null" }

type streamChunk struct {
	ID      string          `json:"id"`
	Usage   json.RawMessage `json:"usage"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

type streamToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}
