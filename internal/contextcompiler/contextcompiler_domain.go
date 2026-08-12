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
	ID        string
	Version   string
	TaskClass string

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
	SourceReference   string
	AuthorityTier     contextengine.AuthorityTier
	OriginalBytes     int
	ProjectedBytes    int
	Projected         bool
	Reason            string
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
}
