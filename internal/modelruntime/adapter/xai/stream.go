package xai

import (
	"bufio"
	"encoding/json"
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
func reassembleStream(reader io.Reader, limit int) ([]byte, error) {
	var (
		id           string
		content      strings.Builder
		finishReason string
		usage        *json.RawMessage
		calls        = map[int]*streamToolCall{}
		order        []int
		consumed     int
	)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), limit+1)
	for scanner.Scan() {
		line := scanner.Text()
		consumed += len(line) + 1
		if consumed > limit {
			return nil, fmt.Errorf("provider stream exceeds %d bytes", limit)
		}
		payload, ok := eventPayload(line)
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return nil, fmt.Errorf("stream event is not valid JSON: %w", err)
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
		return nil, fmt.Errorf("read provider stream: %w", err)
	}
	// A stream that produced no event at all is not an empty completion, it
	// is a failed one. Emitting a well-formed document with no id would let
	// it pass every downstream check as a legitimately empty review.
	if id == "" && content.Len() == 0 && len(order) == 0 && finishReason == "" {
		return nil, fmt.Errorf("provider stream carried no completion events")
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
