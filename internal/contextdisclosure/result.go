package contextdisclosure

import "encoding/json"

// Outcome is the frozen, closed set of context.* operation outcome codes --
// exactly the vocabulary DESIGN.md §17's failure model and
// context_disclosure_events.outcome already use (DESIGN.md §9C: "the exact
// same vocabulary ... never a second, parallel taxonomy").
type Outcome string

const (
	OutcomeOK                 Outcome = "ok"
	OutcomeInvalidRequest     Outcome = "invalid_request"
	OutcomeNotFound           Outcome = "not_found"
	OutcomeForbidden          Outcome = "forbidden"
	OutcomeStaleDrift         Outcome = "stale_drift"
	OutcomeOperationalFailure Outcome = "operational_failure"
)

// Valid reports whether o is one of the six frozen outcome codes.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeOK, OutcomeInvalidRequest, OutcomeNotFound, OutcomeForbidden, OutcomeStaleDrift, OutcomeOperationalFailure:
		return true
	default:
		return false
	}
}

// ContextToolResult is the frozen structured payload every context.*
// operation returns as ToolExecutionResult.Content (JSON-encoded),
// regardless of whether the operation succeeded from the model's point of
// view -- DESIGN.md §9C's literal Go struct, the headline fix of
// independent review round 6: it is ALWAYS returned with a nil error from
// ToolExecutor.Execute in a later slice, because the model needs to see
// FORBIDDEN/NOT_FOUND/etc. as an ordinary tool result it can read and react
// to, never as a run-ending Go error.
//
// §9C's own struct only wrote out the Resource field explicitly and noted
// "analogous fields for inspect/search/aggregate results" without spelling
// them out. M2.0 adds Resources (context.inspect's []ResourceDescriptor)
// and Results (context.search's []SearchResult) as exactly those analogous
// fields -- context.aggregate's output remains a single ContextResource
// per DESIGN.md §11 ("one concatenated/bounded ContextResource"), so it
// reuses Resource, not a fourth field.
type ContextToolResult struct {
	OK      bool    `json:"ok"`
	Code    Outcome `json:"code"`
	Message string  `json:"message,omitempty"`

	// Resource is present only when Code=="ok" for context.fetch,
	// context.slice, or context.aggregate (DESIGN.md §9C/§11).
	Resource *ContextResource `json:"resource,omitempty"`

	// Resources is present only when Code=="ok" for context.inspect
	// (DESIGN.md §11: "OUTPUT: {handle, kind, source_reference, ...}[]" --
	// metadata only, no content).
	Resources []ResourceDescriptor `json:"resources,omitempty"`

	// Results is present only when Code=="ok" for context.search
	// (DESIGN.md §11: "OUTPUT: deterministically ranked {handle, kind,
	// snippet, score}[]").
	Results []SearchResult `json:"results,omitempty"`
}

// Marshal encodes r as the JSON payload a later slice's ToolExecutor places
// into ToolExecutionResult.Content.
func (r ContextToolResult) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalContextToolResult is the inverse of Marshal -- exposed as a
// package function (mirroring Decode's shape for ContextHandle) so a caller
// need not construct a zero-value ContextToolResult first.
func UnmarshalContextToolResult(data []byte) (ContextToolResult, error) {
	var result ContextToolResult
	if err := json.Unmarshal(data, &result); err != nil {
		return ContextToolResult{}, err
	}
	return result, nil
}

// NewOKResourceResult builds a successful ContextToolResult carrying a
// single ContextResource -- the shape context.fetch/slice/aggregate all
// return on success (DESIGN.md §11).
func NewOKResourceResult(resource ContextResource) ContextToolResult {
	return ContextToolResult{OK: true, Code: OutcomeOK, Resource: &resource}
}

// NewOKInspectResult builds a successful ContextToolResult carrying
// context.inspect's []ResourceDescriptor (DESIGN.md §11). descriptors may
// legitimately be empty -- DESIGN.md §11 is explicit that an empty list is
// "simply the true answer for a snapshot with no addressable resources,"
// never itself a FORBIDDEN outcome.
func NewOKInspectResult(descriptors []ResourceDescriptor) ContextToolResult {
	return ContextToolResult{OK: true, Code: OutcomeOK, Resources: descriptors}
}

// NewOKSearchResult builds a successful ContextToolResult carrying
// context.search's []SearchResult (DESIGN.md §11/§12A). results may
// legitimately be empty for the same reason NewOKInspectResult's can be.
func NewOKSearchResult(results []SearchResult) ContextToolResult {
	return ContextToolResult{OK: true, Code: OutcomeOK, Results: results}
}

// NewDeniedResult builds a non-OK ContextToolResult for any of the five
// non-"ok" outcomes (DESIGN.md §17). message MUST be bounded and
// non-sensitive -- DESIGN.md §9C: "never echoes raw content or a
// credential-adjacent value (§12B)" -- this constructor does not itself
// enforce that bound; callers in a later slice are responsible for it.
func NewDeniedResult(code Outcome, message string) ContextToolResult {
	return ContextToolResult{OK: false, Code: code, Message: message}
}
