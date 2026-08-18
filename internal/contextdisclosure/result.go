package contextdisclosure

import (
	"encoding/json"
	"fmt"
)

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
// them out. M2.0 adds Resources (context.inspect's []ResourceDescriptor),
// Results (context.search's []SearchResult), and -- round 7 correction,
// P1 finding -- Aggregate (context.aggregate's *AggregateResult, no longer
// reusing Resource: a single ContextResource cannot honestly represent
// several members with independent identity, see operations.go's
// AggregateResult doc comment) as exactly those analogous fields.
type ContextToolResult struct {
	OK      bool    `json:"ok"`
	Code    Outcome `json:"code"`
	Message string  `json:"message,omitempty"`

	// Resource is present only when Code=="ok" for context.fetch or
	// context.slice (DESIGN.md §9C/§11).
	Resource *ContextResource `json:"resource,omitempty"`

	// Resources is present only when Code=="ok" for context.inspect
	// (DESIGN.md §11: "OUTPUT: {handle, kind, source_reference, ...}[]" --
	// metadata only, no content). Round-8 correction (P1 finding): this is
	// now *[]ResourceDescriptor, not a plain slice. A plain []T with
	// `omitempty` omits the field for BOTH nil AND a legitimately-empty
	// (len-0) slice -- meaning NewOKInspectResult(nil), which DESIGN.md §11
	// says is "simply the true answer" for a snapshot with no addressable
	// resources, would have serialized as if "resources" didn't apply to
	// this outcome at all, indistinguishable from a result that never had a
	// Resources concept. A non-nil pointer to an empty slice marshals as
	// "resources":[], and only a nil pointer omits the field entirely --
	// this is what actually lets the wire format distinguish "this outcome
	// has no Resources concept" from "Resources applies and is empty."
	Resources *[]ResourceDescriptor `json:"resources,omitempty"`

	// Results is present only when Code=="ok" for context.search
	// (DESIGN.md §11: "OUTPUT: deterministically ranked {handle, kind,
	// snippet, score}[]"). Round-8 correction: same *[]T rationale as
	// Resources, above.
	Results *[]SearchResult `json:"results,omitempty"`

	// Aggregate is present only when Code=="ok" for context.aggregate
	// (round 7 correction -- see AggregateResult's own doc comment in
	// operations.go for why this is not just another *ContextResource).
	Aggregate *AggregateResult `json:"aggregate,omitempty"`
}

// Validate checks r for internal coherence -- added round 7 (P2 finding:
// "the result type still permits building impossible states," e.g.
// NewDeniedResult(OutcomeOK, "...") producing {"ok":false,"code":"ok"}). It
// is not a substitute for anything a later slice's ToolExecutor does; it
// only rejects a ContextToolResult whose own fields contradict each other,
// regardless of how it was constructed.
func (r ContextToolResult) Validate() error {
	if !r.Code.Valid() {
		return fmt.Errorf("contextdisclosure: outcome code %q is not one of the six frozen values", r.Code)
	}
	if r.OK != (r.Code == OutcomeOK) {
		return fmt.Errorf("contextdisclosure: ok=%v is inconsistent with code=%q", r.OK, r.Code)
	}
	if !r.OK {
		if r.Resource != nil || r.Resources != nil || r.Results != nil || r.Aggregate != nil {
			return fmt.Errorf("contextdisclosure: a denied result (code=%q) must not carry resource/resources/results/aggregate", r.Code)
		}
		return nil
	}
	// Round-8 fix (P1 finding): exactly one result variant must be present
	// for an OK result -- previously Validate accepted zero, one, two, or
	// even all four variants simultaneously (as long as whichever happened
	// to be non-nil individually validated), which is not a coherent
	// "successful context.<verb> result" for any single operation.
	present := 0
	if r.Resource != nil {
		present++
	}
	if r.Resources != nil {
		present++
	}
	if r.Results != nil {
		present++
	}
	if r.Aggregate != nil {
		present++
	}
	if present != 1 {
		return fmt.Errorf("contextdisclosure: an ok result must carry exactly one of resource/resources/results/aggregate, got %d", present)
	}
	if r.Resource != nil {
		if err := r.Resource.Validate(); err != nil {
			return fmt.Errorf("contextdisclosure: resource: %w", err)
		}
	}
	if r.Resources != nil {
		for i, d := range *r.Resources {
			if err := d.Validate(); err != nil {
				return fmt.Errorf("contextdisclosure: resources[%d]: %w", i, err)
			}
		}
	}
	if r.Results != nil {
		for i, s := range *r.Results {
			if err := s.Validate(); err != nil {
				return fmt.Errorf("contextdisclosure: results[%d]: %w", i, err)
			}
		}
	}
	if r.Aggregate != nil {
		if err := r.Aggregate.Validate(); err != nil {
			return fmt.Errorf("contextdisclosure: aggregate: %w", err)
		}
	}
	return nil
}

// Marshal encodes r as the JSON payload a later slice's ToolExecutor places
// into ToolExecutionResult.Content. Marshal refuses to encode an
// internally-incoherent ContextToolResult (round 7, P2 finding) -- it calls
// Validate first and returns its error instead of ever producing bytes for
// a result that could never legitimately occur.
func (r ContextToolResult) Marshal() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("contextdisclosure: refusing to marshal incoherent ContextToolResult: %w", err)
	}
	return json.Marshal(r)
}

// UnmarshalContextToolResult is the inverse of Marshal -- exposed as a
// package function (mirroring Decode's shape for ContextHandle) so a caller
// need not construct a zero-value ContextToolResult first. Round-8 fix (P2
// finding): previously returned any JSON-well-formed result unconditionally,
// so a caller could Unmarshal a payload Marshal itself would have refused to
// produce (e.g. an ok=false/code=ok contradiction, or an ok=true result
// carrying zero or more than one variant) and receive no error at all.
// UnmarshalContextToolResult now calls Validate before returning, so decode
// and encode enforce the identical coherence contract.
func UnmarshalContextToolResult(data []byte) (ContextToolResult, error) {
	var result ContextToolResult
	if err := json.Unmarshal(data, &result); err != nil {
		return ContextToolResult{}, err
	}
	if err := result.Validate(); err != nil {
		return ContextToolResult{}, fmt.Errorf("contextdisclosure: refusing to unmarshal incoherent ContextToolResult: %w", err)
	}
	return result, nil
}

// NewOKResourceResult builds a successful ContextToolResult carrying a
// single ContextResource -- the shape context.fetch/slice return on
// success (DESIGN.md §11).
func NewOKResourceResult(resource ContextResource) ContextToolResult {
	return ContextToolResult{OK: true, Code: OutcomeOK, Resource: &resource}
}

// NewOKInspectResult builds a successful ContextToolResult carrying
// context.inspect's []ResourceDescriptor (DESIGN.md §11). descriptors may
// legitimately be empty -- DESIGN.md §11 is explicit that an empty list is
// "simply the true answer for a snapshot with no addressable resources,"
// never itself a FORBIDDEN outcome. Round-8 fix: descriptors is always
// wrapped in a non-nil pointer, even when nil/empty itself, so the wire
// result always carries "resources":[...] (never omitted) for this
// outcome -- see Resources' own doc comment for why a nil pointer and a
// pointer-to-empty-slice must be distinguished.
func NewOKInspectResult(descriptors []ResourceDescriptor) ContextToolResult {
	if descriptors == nil {
		descriptors = []ResourceDescriptor{}
	}
	return ContextToolResult{OK: true, Code: OutcomeOK, Resources: &descriptors}
}

// NewOKSearchResult builds a successful ContextToolResult carrying
// context.search's []SearchResult (DESIGN.md §11/§12A). results may
// legitimately be empty for the same reason NewOKInspectResult's can be.
// Round-8 fix: same non-nil-pointer rationale as NewOKInspectResult's.
func NewOKSearchResult(results []SearchResult) ContextToolResult {
	if results == nil {
		results = []SearchResult{}
	}
	return ContextToolResult{OK: true, Code: OutcomeOK, Results: &results}
}

// NewOKAggregateResult builds a successful ContextToolResult carrying
// context.aggregate's *AggregateResult (round 7 correction -- DESIGN.md
// §11).
func NewOKAggregateResult(aggregate AggregateResult) ContextToolResult {
	return ContextToolResult{OK: true, Code: OutcomeOK, Aggregate: &aggregate}
}

// NewDeniedResult builds a non-OK ContextToolResult for any of the five
// non-"ok" outcomes (DESIGN.md §17). code MUST NOT be OutcomeOK --
// Validate/Marshal (above) reject that combination, but callers should not
// rely on catching the mistake only at marshal time. message MUST be
// bounded and non-sensitive -- DESIGN.md §9C: "never echoes raw content or
// a credential-adjacent value (§12B)" -- this constructor does not itself
// enforce that bound; callers in a later slice are responsible for it.
func NewDeniedResult(code Outcome, message string) ContextToolResult {
	return ContextToolResult{OK: false, Code: code, Message: message}
}
