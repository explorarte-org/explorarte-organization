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

// ResourceKind is the closed set of source kinds M2a's addressable universe
// admits -- exactly the two DESIGN.md §6.1's DB CHECK constraint allows
// (resource_kind IN ('approved_memory','rag_evidence')). web_evidence is
// deliberately excluded (§6.1's source-kind decision, round 4/6) and every
// other context_segments.source_kind value (role_profile, approved_skill,
// task_context, project_context, canonical_document, organization_agent,
// department_agent, owner_constraint) is structurally impossible to
// represent as a ResourceKind here, not merely discouraged.
type ResourceKind string

const (
	ResourceKindApprovedMemory ResourceKind = "approved_memory"
	ResourceKindRAGEvidence    ResourceKind = "rag_evidence"
)

// Valid reports whether k is one of M2a's exactly-two admitted resource
// kinds. This mirrors context_addressable_resources.resource_kind's CHECK
// constraint (DESIGN.md §6.1) at the Go level -- it is not a substitute for
// that CHECK (M2.0 has no database), but code that constructs a
// ContextHandle/ContextResource by hand (e.g. tests) should still be caught
// early rather than only failing much later against a real schema.
func (k ResourceKind) Valid() bool {
	switch k {
	case ResourceKindApprovedMemory, ResourceKindRAGEvidence:
		return true
	default:
		return false
	}
}

// DataClass mirrors context_segments.data_class's own closed set
// (migrations/000006_create_context_engine.up.sql), reused verbatim per
// DESIGN.md §14 ("this is not new provenance vocabulary").
type DataClass string

const (
	DataClassPublic         DataClass = "public"
	DataClassOrganizational DataClass = "organizational"
	DataClassSanitized      DataClass = "sanitized"
)

func (d DataClass) Valid() bool {
	switch d {
	case DataClassPublic, DataClassOrganizational, DataClassSanitized:
		return true
	default:
		return false
	}
}

// Frozen M2a authority-ceiling constants (DESIGN.md I-4/§6.1, corrected
// round 6.1: instruction_class is exactly "data", never "no higher than
// data/scoped" -- scoped is not an admissible value for any M2a resource;
// trust_class is exactly "untrusted"; may_grant_capabilities is always
// false). Every ContextResource this package ever produces MUST carry
// exactly these three values -- there is no code path that varies them,
// because no source admitted into M2a's addressable set (ResourceKind,
// above) ever had a higher class to begin with.
const (
	InstructionClassData    = "data"
	TrustClassUntrusted     = "untrusted"
	MayGrantCapabilitiesM2a = false
)

// ContextResource is the frozen result shape a resolved, readable
// addressable resource carries -- returned by context.fetch/slice/
// aggregate (DESIGN.md §11) and carrying the same provenance vocabulary
// context_segments/SourceRecord already use, projected onto a dynamic read
// (DESIGN.md §14, I-4/I-8). DESIGN.md itself never wrote out this struct's
// literal Go shape (referenced throughout only as "(§R)"); M2.0 synthesizes
// it here from every field §11/§14/§24 attribute to "ContextResource",
// cited per field below. Reuses vocabulary/types other M2a domain types
// already define (ResourceKind, DataClass) rather than redeclaring a
// parallel one.
type ContextResource struct {
	// Handle is the opaque encoded ContextHandle string this resource
	// corresponds to -- present so a caller combining several
	// ContextResources (context.aggregate, DESIGN.md §11) can still tell
	// which member is which after concatenation, and so an audit/debug
	// consumer never has to separately correlate a bare digest back to a
	// handle.
	Handle string

	// Kind, SourceReference, SourceVersion: identity fields (DESIGN.md
	// §14). SourceReference is always the same redacted/stable label
	// context_segments.source_reference already uses -- DESIGN.md §11
	// explicit: "never a raw disk path."
	Kind            ResourceKind
	SourceReference string
	SourceVersion   string

	// AuthorityTier, InstructionClass, TrustClass, DataClass,
	// MayGrantCapabilities: the same provenance vocabulary
	// context_segments/SourceRecord already use (DESIGN.md §14). For every
	// M2a resource, InstructionClass/TrustClass/MayGrantCapabilities are
	// always exactly InstructionClassData/TrustClassUntrusted/false (I-4,
	// §6.1) -- AuthorityTier and DataClass are the only provenance fields
	// that can legitimately vary between resources (DataClass is a
	// property of the underlying content, not of M2a's authority ceiling;
	// AuthorityTier mirrors ResourceKind 1:1 for M2a per §6.1's
	// authority_tier = resource_kind CHECK).
	AuthorityTier        string
	InstructionClass     string
	TrustClass           string
	DataClass            DataClass
	MayGrantCapabilities bool

	// ContentDigest is the sha256 of Content, matching
	// context_addressable_resources.content_digest's own format CHECK
	// (^[0-9a-f]{64}$, DESIGN.md §6.1).
	ContentDigest string

	// Content is the resource's bytes -- the whole resource for
	// context.fetch, a bounded range for context.slice (DESIGN.md §11).
	// Content is captured durably at snapshot-build time and never
	// re-read from a live source at disclosure time (DESIGN.md §6.1/§13,
	// round 5/6 correction) -- a fact this type doesn't enforce (M2.0 has
	// no read path at all yet) but that governs how a later slice must
	// populate this field.
	Content []byte

	// ByteCount reflects len(Content) for the actual bytes returned --
	// the slice's byte_count, not the whole resource's, when this
	// ContextResource represents a context.slice result (DESIGN.md §11).
	ByteCount int64
}
