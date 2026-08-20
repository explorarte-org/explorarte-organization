package modelruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

type Normalizer struct {
	MaxResponseBytes int
	MaxToolIntents   int
}

func (n Normalizer) Normalize(invocation Invocation, dispatchAttemptID int64, raw RawResponse) (NormalizedResponse, error) {
	if n.MaxResponseBytes < 1 || n.MaxToolIntents < 0 {
		return NormalizedResponse{}, fmt.Errorf("%w: invalid response limits", ErrResponseRejected)
	}
	if len(raw.ToolIntents) > n.MaxToolIntents {
		return NormalizedResponse{}, fmt.Errorf("%w: tool intent limit exceeded", ErrResponseRejected)
	}
	if len(raw.ProviderRequestID) > 400 {
		return NormalizedResponse{}, fmt.Errorf("%w: provider request ID exceeds limit", ErrResponseRejected)
	}
	if raw.InputTokens < 0 || raw.OutputTokens < 0 || raw.InputTokens > math.MaxInt64-raw.OutputTokens {
		return NormalizedResponse{}, fmt.Errorf("%w: invalid token usage", ErrResponseRejected)
	}

	// Reasoning is lifted out of the working copy before the result is
	// assembled, exactly as it was when it was discarded here.
	//
	// The rule that clearing enforced still holds: nothing built below this
	// line can reach the reasoning, so it cannot participate in result
	// hashing, logs, audit events or the outbox. What changes is only its
	// destination -- NormalizedResponse now carries it to a table of its own,
	// so a role's decision can be explained later without that explanation
	// entering any path built for material that carries no secrets.
	//
	// raw is a value, so this clears THIS function's copy, not the caller's.
	// That is enough for the invariant, because every durable structure is
	// built from here down; the caller reads only token counts from its own
	// copy (see recoveredUsage). Saying it plainly matters: a comment
	// claiming the caller was cleared would be false, and someone would
	// eventually rely on it.
	roleReasoning := boundedReasoning(raw.HiddenReasoning, n.MaxResponseBytes)
	raw.HiddenReasoning = nil

	result := InvocationResult{
		InvocationID:      invocation.ID,
		DispatchAttemptID: dispatchAttemptID,
		OutputMode:        invocation.OutputMode,
		ToolIntents:       make([]ToolIntent, 0, len(raw.ToolIntents)),
	}
	seenToolCallIDs := make(map[string]struct{}, len(raw.ToolIntents))
	totalBytes := len(raw.Content)
	for _, rawIntent := range raw.ToolIntents {
		callID := strings.TrimSpace(rawIntent.ID)
		if callID != "" {
			if !modelInputCallIDPattern.MatchString(callID) {
				return NormalizedResponse{}, fmt.Errorf("%w: invalid tool call ID", ErrResponseRejected)
			}
			if _, duplicate := seenToolCallIDs[callID]; duplicate {
				return NormalizedResponse{}, fmt.Errorf("%w: duplicate tool call ID", ErrResponseRejected)
			}
			seenToolCallIDs[callID] = struct{}{}
		}
		name := strings.TrimSpace(rawIntent.Name)
		if !toolNamePattern.MatchString(name) {
			return NormalizedResponse{}, fmt.Errorf("%w: invalid tool intent name", ErrResponseRejected)
		}
		arguments, err := CanonicalizeRawJSON(rawIntent.Arguments)
		if err != nil || len(arguments) == 0 {
			return NormalizedResponse{}, fmt.Errorf("%w: invalid tool intent arguments", ErrResponseRejected)
		}
		totalBytes += len(name) + len(arguments)
		if totalBytes > n.MaxResponseBytes {
			return NormalizedResponse{}, fmt.Errorf("%w: response exceeds byte limit", ErrResponseRejected)
		}
		result.ToolIntents = append(result.ToolIntents, ToolIntent{ID: callID, Name: name, Arguments: arguments})
	}
	if totalBytes > n.MaxResponseBytes {
		return NormalizedResponse{}, fmt.Errorf("%w: response exceeds byte limit", ErrResponseRejected)
	}

	var normalizedContent []byte
	switch invocation.OutputMode {
	case OutputText:
		if !utf8.Valid(raw.Content) {
			return NormalizedResponse{}, fmt.Errorf("%w: text output is not UTF-8", ErrResponseRejected)
		}
		result.TextOutput = string(raw.Content)
		normalizedContent = append([]byte(nil), raw.Content...)
	case OutputJSON:
		canonical, err := CanonicalizeRawJSON(raw.Content)
		if err != nil || len(canonical) == 0 {
			return NormalizedResponse{}, fmt.Errorf("%w: invalid JSON response", ErrResponseRejected)
		}
		if len(invocation.OutputSchema) > 0 {
			value, err := decodeJSONWithNumbers(canonical)
			if err != nil {
				return NormalizedResponse{}, fmt.Errorf("%w: decode JSON response", ErrResponseRejected)
			}
			schemaValue, err := decodeJSONWithNumbers(invocation.OutputSchema)
			if err != nil {
				return NormalizedResponse{}, fmt.Errorf("%w: invalid stored schema", ErrResponseRejected)
			}
			schema, ok := schemaValue.(map[string]any)
			if !ok {
				return NormalizedResponse{}, fmt.Errorf("%w: stored schema is not an object", ErrResponseRejected)
			}
			if err = validateAgainstSchema(value, schema, "$"); err != nil {
				return NormalizedResponse{}, fmt.Errorf("%w: schema mismatch: %v", ErrResponseRejected, err)
			}
		}
		result.JSONOutput = canonical
		normalizedContent = canonical
		totalBytes = len(canonical)
		for _, intent := range result.ToolIntents {
			totalBytes += len(intent.Name) + len(intent.Arguments)
		}
		if totalBytes > n.MaxResponseBytes {
			return NormalizedResponse{}, fmt.Errorf("%w: normalized response exceeds byte limit", ErrResponseRejected)
		}
	default:
		return NormalizedResponse{}, fmt.Errorf("%w: unsupported output mode", ErrResponseRejected)
	}

	hashBody, err := CanonicalJSON(map[string]any{
		"output_mode":  result.OutputMode,
		"content":      json.RawMessage(nil),
		"text":         result.TextOutput,
		"tool_intents": result.ToolIntents,
	})
	if err != nil {
		return NormalizedResponse{}, fmt.Errorf("%w: hash normalized response", ErrResponseRejected)
	}
	if result.OutputMode == OutputJSON {
		hashBody, err = CanonicalJSON(map[string]any{
			"output_mode":  result.OutputMode,
			"json":         json.RawMessage(normalizedContent),
			"tool_intents": result.ToolIntents,
		})
		if err != nil {
			return NormalizedResponse{}, fmt.Errorf("%w: hash normalized JSON response", ErrResponseRejected)
		}
	}
	result.ResponseHash = SHA256Bytes(hashBody)
	result.ResponseBytes = totalBytes
	usage := Usage{
		InvocationID:      invocation.ID,
		DispatchAttemptID: dispatchAttemptID,
		InputTokens:       raw.InputTokens,
		OutputTokens:      raw.OutputTokens,
		TotalTokens:       raw.InputTokens + raw.OutputTokens,
		ProviderReported:  raw.ProviderReported,
		// R9.1 fix 3: this success path previously dropped
		// PromptCacheHitTokens/PromptCacheMissTokens even though the
		// adapter already populates them on RawResponse -- cache
		// telemetry was only wired for the failure path
		// (insertRecoveredUsage), so every SUCCESSFUL invocation's
		// cache_hit/miss columns read NULL. Mirror what the failure
		// path already does.
		PromptCacheHitTokens:  raw.PromptCacheHitTokens,
		PromptCacheMissTokens: raw.PromptCacheMissTokens,
	}
	return NormalizedResponse{
		Result:                result,
		Usage:                 usage,
		ProviderRequestID:     raw.ProviderRequestID,
		CancellationConfirmed: raw.CancellationConfirmed,
		RoleReasoning:         roleReasoning,
	}, nil
}

func decodeJSONWithNumbers(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return value, nil
}

// boundedReasoning keeps a provider's reasoning inside the same size budget
// the rest of its response answers to.
//
// It truncates rather than rejects: reasoning explains a decision, and losing
// the whole invocation because the explanation ran long would trade the thing
// that matters for the thing that describes it. An empty result stays empty
// rather than becoming an empty row.
func boundedReasoning(reasoning []byte, limit int) []byte {
	if len(reasoning) == 0 {
		return nil
	}
	if limit > 0 && len(reasoning) > limit {
		reasoning = reasoning[:limit]
	}
	return append([]byte(nil), reasoning...)
}
