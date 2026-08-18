// Package contextdisclosure implements M2 -- Addressable Context +
// Progressive Disclosure. This package owns addressable-resource identity,
// the disclosure event log, and the executionharness ToolCatalog/ToolExecutor
// implementation that exposes context.* operations as Harness tools (see
// docs/implementation/m2-addressable-context/DESIGN.md §5).
//
// This file (M2.0 slice) contains only the pure Go contract/domain types the
// frozen design requires. It has no persistence, no Harness wiring, no
// authorization, and no I/O of any kind -- see DESIGN.md §27's M2.0
// description and the mission that produced this package.
package contextdisclosure

import (
	"fmt"
	"regexp"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// ResourceKind, AuthorityTier, InstructionClass, TrustClass, DataClass are
// aliases to contextengine's own taxonomy, never a second, parallel one.
//
// Round-7 correction (independent implementation review, P1 finding):
// M2.0's first draft declared its own `type ResourceKind string`/`type
// DataClass string` with its own string constants -- even though
// contextengine already owns exactly this vocabulary
// (contextengine.SourceKind/AuthorityTier/InstructionClass/TrustClass/
// DataClass, and contextengine.SourceApprovedMemory/SourceRAGEvidence/
// InstructionData/TrustUntrusted). DESIGN.md §22/M2 CONTRACT is explicit:
// "MUST NOT duplicate IsDynamicProviderTier, ContextTokenTelemetry, or
// ExecutionPrincipal -- reuse via FK/shared function, never redeclare a
// parallel classification" -- the same principle this repo already has a
// documented history of getting wrong once (R31 hardening, cited
// throughout DESIGN.md) applies identically to this taxonomy.
// contextdisclosure CONSUMES contextengine's semantics; it does not own a
// second copy of them, even one that happens to share the same string
// values today.
type (
	ResourceKind     = contextengine.SourceKind
	AuthorityTier    = contextengine.AuthorityTier
	InstructionClass = contextengine.InstructionClass
	TrustClass       = contextengine.TrustClass
	DataClass        = contextengine.DataClass
)

// M2a's closed resource-kind subset -- exactly the two
// contextengine.SourceKind values DESIGN.md §6.1's DB CHECK constraint
// allows (resource_kind IN ('approved_memory','rag_evidence')).
// web_evidence is deliberately excluded (§6.1's source-kind decision,
// round 4/6) and every other contextengine.SourceKind value (role_profile,
// approved_skill, task_context, project_context, canonical_document,
// organization_agent, department_agent, owner_constraint) is structurally
// impossible to admit here, not merely discouraged.
const (
	ResourceKindApprovedMemory = contextengine.SourceApprovedMemory
	ResourceKindRAGEvidence    = contextengine.SourceRAGEvidence
)

// ValidResourceKind reports whether k is one of M2a's exactly-two admitted
// resource kinds. This mirrors context_addressable_resources.resource_kind
// 's CHECK constraint (DESIGN.md §6.1) at the Go level -- it is not a
// substitute for that CHECK (M2.0 has no database), but code that
// constructs a ContextHandle/ContextResource by hand (e.g. tests) should
// still be caught early rather than only failing much later against a
// real schema. A package-level function, not a method on
// contextengine.SourceKind, since contextdisclosure does not own that
// type and must not attach methods to a type it doesn't own outside this
// package's own alias scope (Go permits it here only because these are
// local aliases, but the M2a-specific *closed subset* check belongs to
// this package's own domain logic, not contextengine's).
func ValidResourceKind(k ResourceKind) bool {
	return k == ResourceKindApprovedMemory || k == ResourceKindRAGEvidence
}

// M2a's closed data-class subset -- context_addressable_resources.data_class
// 's own CHECK constraint (DESIGN.md §6.1) allows exactly ('public',
// 'organizational','sanitized'), the same three values context_segments
// already uses (migration 000006) -- narrower than contextengine.DataClass
// 's full five-value set (which also includes DataSecret/DataClinical, not
// admissible for any M2a resource).
func ValidDataClassM2a(d DataClass) bool {
	switch d {
	case contextengine.DataPublic, contextengine.DataOrganizational, contextengine.DataSanitized:
		return true
	default:
		return false
	}
}

// Frozen M2a authority-ceiling constants (DESIGN.md I-4/§6.1, corrected
// round 6.1: instruction_class is exactly "data", never "no higher than
// data/scoped" -- scoped is not an admissible value for any M2a resource;
// trust_class is exactly "untrusted"; may_grant_capabilities is always
// false; authority_tier mirrors resource_kind 1:1, §6.1's authority_tier =
// resource_kind CHECK). Every ContextResource this package ever produces
// MUST carry exactly these values -- there is no code path that varies
// them, because no source admitted into M2a's addressable set
// (ValidResourceKind, above) ever had a higher class to begin with. Reuse
// contextengine's own constants, never redeclare them as new string
// literals (round-7 correction, same finding as above).
const (
	InstructionClassM2a     = contextengine.InstructionData
	TrustClassM2a           = contextengine.TrustUntrusted
	MayGrantCapabilitiesM2a = false
)

// contentDigestPattern mirrors context_addressable_resources.content_digest
// 's own format CHECK (DESIGN.md §6.1: content_digest ~ '^[0-9a-f]{64}$').
// Shared by ContextResource.Validate and ContextHandle.Validate (handle.go)
// -- one pattern, not two independently-maintained copies.
var contentDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ContextResource is the frozen result shape a resolved, readable
// addressable resource carries -- returned by context.fetch/slice
// (DESIGN.md §11) and carrying the same provenance vocabulary
// context_segments/SourceRecord already use, projected onto a dynamic read
// (DESIGN.md §14, I-4/I-8). DESIGN.md itself never wrote out this struct's
// literal Go shape (referenced throughout only as "(§R)"); M2.0 synthesizes
// it here from every field §11/§14/§24 attribute to "ContextResource",
// cited per field below.
//
// context.aggregate does NOT reuse this type (round-7 correction, P1
// finding) -- see AggregateResult in operations.go for why a single
// ContextResource cannot honestly represent several members with
// independent identity/provenance.
//
// Round-7 correction (P1 finding): every field below now carries an
// explicit `json:"snake_case"` tag matching DESIGN.md §11's own
// model-visible field names literally (handle, kind, source_reference,
// byte_count, trust_class, data_class, ...) -- the Go field names alone
// would otherwise leak PascalCase into the wire format the model actually
// sees, which is not the frozen §11 contract.
//
// Round-7 correction (P1 finding): Content is now `string`, not `[]byte`.
// encoding/json represents []byte as base64 -- for M2a's textual
// approved_memory/rag_evidence content (no generic binary artifacts are
// addressable, §4B), a []byte field would have made the model receive
// contextengine.RenderUntrustedContextResource's escaped [authority:...]
// structural markers as opaque base64 rather than the plaintext §24
// requires it to see and (correctly) never execute as instructions.
type ContextResource struct {
	// Handle is the opaque encoded ContextHandle string this resource
	// corresponds to -- present so a caller combining several resources
	// can still tell which member is which, and so an audit/debug
	// consumer never has to separately correlate a bare digest back to a
	// handle.
	Handle string `json:"handle"`

	// Kind, SourceReference, SourceVersion: identity fields (DESIGN.md
	// §14). SourceReference is always the same redacted/stable label
	// context_segments.source_reference already uses -- DESIGN.md §11
	// explicit: "never a raw disk path."
	Kind            ResourceKind `json:"kind"`
	SourceReference string       `json:"source_reference"`
	SourceVersion   string       `json:"source_version"`

	// AuthorityTier, InstructionClass, TrustClass, DataClass,
	// MayGrantCapabilities: the same provenance vocabulary
	// context_segments/SourceRecord already use (DESIGN.md §14). For every
	// M2a resource, InstructionClass/TrustClass/MayGrantCapabilities are
	// always exactly InstructionClassM2a/TrustClassM2a/false (I-4, §6.1) --
	// AuthorityTier and DataClass are the only provenance fields that can
	// legitimately vary between resources (DataClass is a property of the
	// underlying content, not of M2a's authority ceiling; AuthorityTier
	// mirrors Kind 1:1 for M2a per §6.1's authority_tier = resource_kind
	// CHECK).
	AuthorityTier        AuthorityTier    `json:"authority_tier"`
	InstructionClass     InstructionClass `json:"instruction_class"`
	TrustClass           TrustClass       `json:"trust_class"`
	DataClass            DataClass        `json:"data_class"`
	MayGrantCapabilities bool             `json:"may_grant_capabilities"`

	// ContentDigest is the sha256 of Content, matching
	// context_addressable_resources.content_digest's own format CHECK
	// (^[0-9a-f]{64}$, DESIGN.md §6.1).
	ContentDigest string `json:"content_digest"`

	// Content is the resource's bytes, as UTF-8 text -- the whole resource
	// for context.fetch, a bounded range for context.slice (DESIGN.md
	// §11). Content is captured durably at snapshot-build time and never
	// re-read from a live source at disclosure time (DESIGN.md §6.1/§13,
	// round 5/6 correction) -- a fact this type doesn't enforce (M2.0 has
	// no read path at all yet) but that governs how a later slice must
	// populate this field. A later slice's ToolExecutor is responsible for
	// wrapping Content with contextengine.RenderUntrustedContextResource's
	// structural markers (DESIGN.md §9B/§24) BEFORE assigning it here --
	// this field is not a raw, unwrapped read.
	Content string `json:"content"`

	// ByteCount reflects the actual byte length of Content -- the slice's
	// byte_count, not the whole resource's, when this ContextResource
	// represents a context.slice result (DESIGN.md §11).
	ByteCount int64 `json:"byte_count"`
}

// Validate checks r's fields for internal coherence -- the same
// well-formedness bounds the underlying schema/domain already enforce for
// a persisted row (DESIGN.md §6.1's CHECK constraints), applied here to a
// wire object that has not gone through any database at all. This is a
// round-7 addition (P2 finding: "the wire object can represent an
// impossible state after construction, and nothing catches it"). Validate
// never touches storage and never proves authority (I-2) -- it only
// rejects a ContextResource that could never legitimately correspond to
// any real context_addressable_resources row, regardless of which one.
func (r ContextResource) Validate() error {
	if r.Handle == "" {
		return fmt.Errorf("contextdisclosure: resource handle is required")
	}
	if !ValidResourceKind(r.Kind) {
		return fmt.Errorf("contextdisclosure: resource kind %q is not one of M2a's admitted kinds", r.Kind)
	}
	if AuthorityTier(r.Kind) != r.AuthorityTier {
		return fmt.Errorf("contextdisclosure: authority tier %q must equal resource kind %q (§6.1)", r.AuthorityTier, r.Kind)
	}
	if r.InstructionClass != InstructionClassM2a {
		return fmt.Errorf("contextdisclosure: instruction class must be %q, got %q", InstructionClassM2a, r.InstructionClass)
	}
	if r.TrustClass != TrustClassM2a {
		return fmt.Errorf("contextdisclosure: trust class must be %q, got %q", TrustClassM2a, r.TrustClass)
	}
	if r.MayGrantCapabilities != MayGrantCapabilitiesM2a {
		return fmt.Errorf("contextdisclosure: may_grant_capabilities must be false")
	}
	if !ValidDataClassM2a(r.DataClass) {
		return fmt.Errorf("contextdisclosure: data class %q is not one of M2a's admitted values", r.DataClass)
	}
	if !contentDigestPattern.MatchString(r.ContentDigest) {
		return fmt.Errorf("contextdisclosure: content digest must be a 64-character hex sha256 digest")
	}
	if r.SourceReference == "" {
		return fmt.Errorf("contextdisclosure: source reference is required")
	}
	if r.SourceVersion == "" {
		return fmt.Errorf("contextdisclosure: source version is required")
	}
	if r.ByteCount != int64(len(r.Content)) {
		return fmt.Errorf("contextdisclosure: byte count %d does not match content length %d", r.ByteCount, len(r.Content))
	}
	return nil
}
