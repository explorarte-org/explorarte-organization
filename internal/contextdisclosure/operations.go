package contextdisclosure

import "fmt"

// This file contains the pure Go input/output shapes for each context.*
// operation DESIGN.md §11 specifies -- domain types only. Validation here
// is limited to what DESIGN.md §17 calls "malformed handle syntax"/
// "malformed request" checks that never reach storage; authorization,
// membership, limits enforcement, and actual reads are a later slice's
// responsibility (M2.2+), never this one's.

// ResourceDescriptor is context.inspect's per-resource output shape --
// DESIGN.md §11, verbatim: "{handle, kind, source_reference (redacted to a
// stable label, not a raw path), byte_count, trust_class, data_class} --
// metadata only, no content." Round-7 correction (P1 finding): explicit
// json tags matching §11's own snake_case field names -- the Go field
// names alone would otherwise leak PascalCase into the model-visible wire
// format.
type ResourceDescriptor struct {
	Handle          string       `json:"handle"`
	Kind            ResourceKind `json:"kind"`
	SourceReference string       `json:"source_reference"`
	ByteCount       int64        `json:"byte_count"`
	TrustClass      TrustClass   `json:"trust_class"`
	DataClass       DataClass    `json:"data_class"`
}

// Validate checks d for internal coherence, mirroring ContextResource.
// Validate's rationale (domain.go) -- a wire object that never went
// through a database still shouldn't be able to represent an impossible
// M2a resource.
func (d ResourceDescriptor) Validate() error {
	if d.Handle == "" {
		return fmt.Errorf("contextdisclosure: descriptor handle is required")
	}
	if !ValidResourceKind(d.Kind) {
		return fmt.Errorf("contextdisclosure: descriptor kind %q is not one of M2a's admitted kinds", d.Kind)
	}
	if !validBoundedText(d.SourceReference, sourceReferenceMaxLen) {
		return fmt.Errorf("contextdisclosure: descriptor source reference must be 1..%d characters", sourceReferenceMaxLen)
	}
	if d.ByteCount < 0 {
		return fmt.Errorf("contextdisclosure: descriptor byte count must be >= 0")
	}
	if d.TrustClass != TrustClassM2a {
		return fmt.Errorf("contextdisclosure: descriptor trust class must be %q, got %q", TrustClassM2a, d.TrustClass)
	}
	if !ValidDataClassM2a(d.DataClass) {
		return fmt.Errorf("contextdisclosure: descriptor data class %q is not one of M2a's admitted values", d.DataClass)
	}
	// Round-8 fix (P1 finding): the handle's own encoded Kind must agree
	// with this descriptor's Kind -- see validateHandleKind's doc comment
	// (domain.go).
	if err := validateHandleKind(d.Handle, d.Kind); err != nil {
		return fmt.Errorf("contextdisclosure: %w", err)
	}
	return nil
}

// SearchResult is context.search's per-result output shape -- DESIGN.md
// §11, verbatim: "ranked {handle, kind, snippet (bounded), score}[]".
//
// Round-7 correction (P1 finding): adds DataClass, required by TEST_PLAN.md
// L6 ("assert the ContextResource/SearchResult shape carries data_class
// alongside the snippet, never dropping it") -- a design/test-plan gap
// §11's own table didn't carry forward but L6 explicitly froze as a test
// requirement; treating M2.0 as a genuinely frozen contract means closing
// this now rather than deferring it to M2.4, where it would already be a
// regression against L6.
type SearchResult struct {
	Handle    string       `json:"handle"`
	Kind      ResourceKind `json:"kind"`
	Snippet   string       `json:"snippet"`
	Score     float64      `json:"score"`
	DataClass DataClass    `json:"data_class"`
}

// Validate mirrors ResourceDescriptor.Validate's rationale.
func (s SearchResult) Validate() error {
	if s.Handle == "" {
		return fmt.Errorf("contextdisclosure: search result handle is required")
	}
	if !ValidResourceKind(s.Kind) {
		return fmt.Errorf("contextdisclosure: search result kind %q is not one of M2a's admitted kinds", s.Kind)
	}
	if !ValidDataClassM2a(s.DataClass) {
		return fmt.Errorf("contextdisclosure: search result data class %q is not one of M2a's admitted values", s.DataClass)
	}
	// Round-8 fix (P1 finding): see ResourceDescriptor.Validate's identical
	// call.
	if err := validateHandleKind(s.Handle, s.Kind); err != nil {
		return fmt.Errorf("contextdisclosure: %w", err)
	}
	return nil
}

// AggregateMember is one constituent resource's identity/provenance within
// an AggregateResult -- everything ContextResource carries EXCEPT Content
// itself, since a member's (already-wrapped) bytes live concatenated
// inside AggregateResult.Content, not duplicated per member.
//
// Added round 7 (P1 finding: "context.aggregate cannot be represented
// honestly by the current ContextResource"). ContextResource's identity
// fields (Handle, Kind, SourceReference, SourceVersion, AuthorityTier,
// DataClass, ContentDigest) are all single-resource concepts -- there is
// no valid single answer for any of them when context.aggregate combines
// several resources that can legitimately differ in Kind (one
// approved_memory, one rag_evidence) and DataClass (DESIGN.md §11 permits
// this: "each still wrapped with its own trust/provenance markers"). This
// type gives every member its own identity instead of forcing an
// impossible choice onto a single outer ContextResource.
type AggregateMember struct {
	Handle          string       `json:"handle"`
	Kind            ResourceKind `json:"kind"`
	SourceReference string       `json:"source_reference"`
	SourceVersion   string       `json:"source_version"`

	AuthorityTier        AuthorityTier    `json:"authority_tier"`
	InstructionClass     InstructionClass `json:"instruction_class"`
	TrustClass           TrustClass       `json:"trust_class"`
	DataClass            DataClass        `json:"data_class"`
	MayGrantCapabilities bool             `json:"may_grant_capabilities"`

	ContentDigest string `json:"content_digest"`
	ByteCount     int64  `json:"byte_count"`
}

// Validate mirrors ContextResource.Validate's per-field checks, minus the
// Content/ByteCount cross-check ContextResource does (a member's own
// ByteCount describes its contribution to AggregateResult.Content, which
// this type alone cannot verify -- AggregateResult.Validate, below, checks
// the sum instead).
func (m AggregateMember) Validate() error {
	if m.Handle == "" {
		return fmt.Errorf("contextdisclosure: aggregate member handle is required")
	}
	if !ValidResourceKind(m.Kind) {
		return fmt.Errorf("contextdisclosure: aggregate member kind %q is not one of M2a's admitted kinds", m.Kind)
	}
	if AuthorityTier(m.Kind) != m.AuthorityTier {
		return fmt.Errorf("contextdisclosure: aggregate member authority tier %q must equal kind %q (§6.1)", m.AuthorityTier, m.Kind)
	}
	if m.InstructionClass != InstructionClassM2a {
		return fmt.Errorf("contextdisclosure: aggregate member instruction class must be %q, got %q", InstructionClassM2a, m.InstructionClass)
	}
	if m.TrustClass != TrustClassM2a {
		return fmt.Errorf("contextdisclosure: aggregate member trust class must be %q, got %q", TrustClassM2a, m.TrustClass)
	}
	if m.MayGrantCapabilities != MayGrantCapabilitiesM2a {
		return fmt.Errorf("contextdisclosure: aggregate member may_grant_capabilities must be false")
	}
	if !ValidDataClassM2a(m.DataClass) {
		return fmt.Errorf("contextdisclosure: aggregate member data class %q is not one of M2a's admitted values", m.DataClass)
	}
	if !contentDigestPattern.MatchString(m.ContentDigest) {
		return fmt.Errorf("contextdisclosure: aggregate member content digest must be a 64-character hex sha256 digest")
	}
	if !validBoundedText(m.SourceReference, sourceReferenceMaxLen) {
		return fmt.Errorf("contextdisclosure: aggregate member source reference must be 1..%d characters", sourceReferenceMaxLen)
	}
	if !validBoundedText(m.SourceVersion, sourceVersionMaxLen) {
		return fmt.Errorf("contextdisclosure: aggregate member source version must be 1..%d characters", sourceVersionMaxLen)
	}
	if m.ByteCount < 0 {
		return fmt.Errorf("contextdisclosure: aggregate member byte count must be >= 0")
	}
	// Round-8 fix (P1 finding, demonstrated by a real bug in this package's
	// own sampleAggregateMember test helper): the handle must agree with
	// this member's own Kind/SourceVersion/ContentDigest, or the wire
	// object asserts two contradictory identities at once.
	if err := validateHandleIdentity(m.Handle, m.Kind, m.SourceVersion, m.ContentDigest); err != nil {
		return fmt.Errorf("contextdisclosure: %w", err)
	}
	return nil
}

// AggregateResult is context.aggregate's output shape -- DESIGN.md §11:
// "one concatenated/bounded ContextResource combining several resources'
// content, each still wrapped with its own trust/provenance markers."
// Round 6's correction froze concatenation order as canonical
// (resource_id-ascending, never caller-supplied); Members here is expected
// to already be in that order by the time a later slice populates it.
type AggregateResult struct {
	Members   []AggregateMember `json:"members"`
	Content   string            `json:"content"`
	ByteCount int64             `json:"byte_count"`
}

// Validate checks internal coherence: at least one member, every member
// individually valid, and ByteCount equal to the SUM of each member's own
// raw ByteCount -- per the frozen digest/byte-count/content semantics
// (round-8 P1 finding), ByteCount always describes RAW bytes disclosed,
// never the wrapped, model-visible Content's length (Content is the
// canonical concatenation of each member's WRAPPED representation, whose
// length has no enforceable relationship to the sum of raw byte counts,
// since wrapping adds authority-marker bytes). Round-7's check compared
// ByteCount against len(Content) directly -- that was always the wrong
// comparison; this method now actually performs the sum-of-members check
// its doc comment claimed to perform.
//
// Validate does NOT check that Content is the correct wrapped
// concatenation of each member's own bytes in resource_id order -- that
// requires the members' actual content, which AggregateMember deliberately
// does not carry (see its own doc comment); enforcing the concatenation
// itself is a later slice's (M2.4's) responsibility.
func (a AggregateResult) Validate() error {
	if len(a.Members) == 0 {
		return fmt.Errorf("contextdisclosure: aggregate result must have at least one member")
	}
	var memberByteSum int64
	for i, m := range a.Members {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("contextdisclosure: aggregate member[%d]: %w", i, err)
		}
		memberByteSum += m.ByteCount
	}
	if a.ByteCount != memberByteSum {
		return fmt.Errorf("contextdisclosure: aggregate byte count %d does not match sum of member byte counts %d", a.ByteCount, memberByteSum)
	}
	return nil
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
