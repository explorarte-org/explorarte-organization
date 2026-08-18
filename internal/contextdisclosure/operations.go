package contextdisclosure

// This file contains the pure Go input/output shapes for each context.*
// operation DESIGN.md §11 specifies -- domain types only. Validation here
// is limited to what DESIGN.md §17 calls "malformed handle syntax"/
// "malformed request" checks that never reach storage; authorization,
// membership, limits enforcement, and actual reads are a later slice's
// responsibility (M2.2+), never this one's.

// ResourceDescriptor is context.inspect's per-resource output shape --
// DESIGN.md §11, verbatim: "{handle, kind, source_reference (redacted to a
// stable label, not a raw path), byte_count, trust_class, data_class} --
// metadata only, no content."
type ResourceDescriptor struct {
	Handle          string
	Kind            ResourceKind
	SourceReference string
	ByteCount       int64
	TrustClass      string
	DataClass       DataClass
}

// SearchResult is context.search's per-result output shape -- DESIGN.md
// §11, verbatim: "ranked {handle, kind, snippet (bounded), score}[]".
type SearchResult struct {
	Handle  string
	Kind    ResourceKind
	Snippet string
	Score   float64
}

// InspectInput is context.inspect's input (DESIGN.md §11: "optional handle
// filter; if omitted, lists all addressable resources for the current
// snapshot"). Handle is empty for the list-all form.
type InspectInput struct {
	Handle string
}

// FetchInput is context.fetch's input (DESIGN.md §11: "one handle").
type FetchInput struct {
	Handle string
}

// SliceInput is context.slice's input (DESIGN.md §11: "handle + byte or
// logical-unit range").
type SliceInput struct {
	Handle string
	Offset int64
	Length int64
}

// Validate rejects an out-of-bounds range shape (negative offset, negative
// or zero length) as INVALID_REQUEST -- DESIGN.md §17: "Read exceeds
// bounds | INVALID_REQUEST" / §11's slice FAILURE MODE: "INVALID_REQUEST
// for an out-of-bounds range." Validate has no resource to check the range
// against yet (that requires the resolved resource's own ByteCount, a
// later slice's job) -- it only rejects a range that is nonsensical on its
// own terms.
func (in SliceInput) Validate() error {
	if in.Offset < 0 {
		return errOutOfBoundsRange
	}
	if in.Length <= 0 {
		return errOutOfBoundsRange
	}
	return nil
}

// SearchInput is context.search's input (DESIGN.md §11: "bounded query
// string").
type SearchInput struct {
	Query string
}

// AggregateInput is context.aggregate's input (DESIGN.md §11: "bounded
// list of handles"). Round 6's correction froze concatenation order as
// canonical (ascending resource_id), never caller-supplied order (DESIGN.md
// §11's OUTPUT correction) -- Handles here therefore represents the
// REQUESTED set, not the order results are produced in; a later slice's
// ordering happens after resolving each handle, not on this input as
// given.
type AggregateInput struct {
	Handles []string
}
