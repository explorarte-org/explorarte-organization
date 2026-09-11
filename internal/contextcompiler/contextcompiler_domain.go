// Package contextcompiler implements R10 (Context & Inference Economy V1):
// a CONTEXT PROJECTION layer on top of internal/contextengine, not a
// second Context Engine. It never changes authority precedence, never
// generates text, never grants capabilities, and never mutates the
// canonical contextengine.Snapshot -- it only produces a derived,
// projected contextengine.Snapshot for a specific, narrow ContextProfile,
// leaving the canonical snapshot in the DB untouched.
//
// Scope for R10 V1: exactly one profile, "research.corpus_curate/v1".
// Any task class without a matching profile falls back to the canonical
// snapshot unmodified (see Compile).
package contextcompiler

import "github.com/Mireuz13/explorarte-organization/internal/contextengine"

// ContextProfile declares, for one TaskClass, which authority classes are
// mandatory (never excluded), which projections apply to specific
// segments, and nothing else in V1 (no conditional/excluded classes are
// exercised yet -- see R10_DESIGN_AUDIT.md section H, every tier this
// task class currently receives is required or has no safe metadata to
// exclude it by, per the fail-closed-toward-authority rule).
type ContextProfile struct {
	ID      string
	Version string
	// TaskClass registers this profile at the TASK-CLASS selector tier
	// (M1.3). ExecutionPurpose, if set, additionally/instead registers it
	// at the EXECUTION-PURPOSE tier -- see SelectorRegistry/ProfileEntry
	// in contextcompiler_selector.go, which own the applicability
	// restriction and the actual precedence resolution. A profile is
	// never selected by ActorRoleID/ActorUnitID alone.
	TaskClass        string
	ExecutionPurpose string

	// RequiredTiers must all be present and Included in the compiled
	// output; Compile returns an error if the canonical snapshot is
	// missing any of them (a missing required tier is a hard failure,
	// never a silent fallback).
	RequiredTiers []contextengine.AuthorityTier

	// Projections maps a segment's SourceReference to a registered,
	// deterministic projection function. A segment whose
	// SourceReference is not in this map is passed through unchanged
	// (still included, per the conservative-inclusion rule -- see
	// R10_DESIGN_AUDIT.md section G).
	Projections map[string]ProjectionFunc

	// ExcludedSources names segments, by exact SourceReference, that this
	// profile omits entirely (EXECUTIVE_CONTEXT_PROFILE_SCOPING_FIX_V1).
	// This is a source-level exclusion, deliberately finer-grained than
	// RequiredTiers: several canonical documents share one AuthorityTier
	// (e.g. TierCanonicalPolicies covers organization.yaml AND
	// model-routing.yaml alike), so a profile that must keep one while
	// dropping another cannot express that at the tier level.
	//
	// A ProjectionFunc can only SHRINK a segment's content -- Compile
	// rejects an empty projection and keeps the original (see
	// contextcompiler_compiler.go, and RoleCatalogSelfEntry's own
	// fail-closed branch), so it was structurally impossible to remove a
	// segment through Projections alone. ExcludedSources is the minimal,
	// explicit completion of that gap: Compile sets the matching
	// segment's Included=false and OmissionReason, exactly mirroring how
	// contextengine's own assembler already represents an omitted
	// segment (see assembler.go) -- renderer.go and providerrender.go
	// already skip !Included segments unconditionally, so no downstream
	// consumer needs to change.
	//
	// RequiredTiers is checked against the CANONICAL snapshot's own
	// segment inclusion, before any profile is applied (see Compile) --
	// excluding one source from a tier that other, still-included
	// sources also belong to never fails a RequiredTiers check for that
	// tier.
	ExcludedSources map[string]bool
}

// ProjectionFunc deterministically derives a smaller byte payload from a
// segment's original content, given the actor this compilation is for.
// It must be pure: same segment + same actorRoleID -> byte-identical
// output, every time (Compile's determinism tests depend on this).
type ProjectionFunc func(segment contextengine.Segment, actorRoleID string) (projectedContent []byte, reason string, err error)

// SegmentDiff is the audit record of what Compile decided for one
// segment -- always populated for every segment in the canonical
// snapshot, whether or not a projection applied, so "why did/didn't
// this content reach the model" is always answerable deterministically.
type SegmentDiff struct {
	SourceReference      string
	AuthorityTier        contextengine.AuthorityTier
	OriginalBytes        int
	ProjectedBytes       int
	Projected            bool
	Reason               string
	OriginalContentHash  string
	ProjectedContentHash string
}

// CompilationResult is R10's ExecutionContextView (see
// R10_DESIGN_AUDIT.md section I). Projected is ready to hand to
// contextengine's PortableRenderer unchanged -- Compile never touches
// the renderer.
type CompilationResult struct {
	ContextSnapshotID     int64
	ContextProfileID      string
	ContextProfileVersion string

	Projected contextengine.Snapshot

	SegmentDiffs []SegmentDiff

	StablePrefixBytes   int
	DynamicSuffixBytes  int
	AuthorityOrderHash  string
	CompiledContentHash string

	// FellBackToCanonical is true when TaskClass matched no registered
	// profile -- Projected is then a byte-identical copy of the
	// canonical snapshot's segments (see R10_DESIGN_AUDIT.md section M,
	// "fallback seguro"), never an arbitrarily minimal view.
	FellBackToCanonical bool

	// SelectionKind is the durable M1.3 provenance of how this
	// CompilationResult's profile (or canonical fallback) was chosen --
	// see SelectorRegistry.Select. Always set, never left blank, so a
	// persisted ExecutionContextView can always answer "why this profile"
	// after restart.
	SelectionKind SelectionKind
}
