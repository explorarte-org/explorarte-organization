package contextcompiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// BuildSelector builds the durable, host-validated semantic selector
// identity M1.3 resolves ContextProfiles from, from ALREADY-DURABLE
// contextengine.Snapshot facts only (M1.3 section 8): it never queries
// the Task Engine, never parses TaskRef, never infers TaskClass from
// ActorRoleID or Instructions. Snapshot.ExecutionPurpose is the semantic
// enum value (M1.3), deliberately distinct from Snapshot.Purpose (the
// separate, byte-identical legacy egress-scope string Context
// Engine/Model Runtime compatibility still depends on) -- selection must
// never key off the legacy string.
func BuildSelector(canonical contextengine.Snapshot) SemanticSelector {
	return SemanticSelector{
		TaskClass:        canonical.TaskClass,
		ExecutionPurpose: canonical.ExecutionPurpose,
		ActorRoleID:      canonical.ActorRoleID,
		ActorUnitID:      canonical.ActorUnitID,
	}
}

// Compile projects a canonical contextengine.Snapshot for one
// ContextProfile. It NEVER mutates the input snapshot (canonical stays
// the source of truth, unchanged in the DB) and NEVER reorders segments
// or changes AuthorityPriority/RenderOrdinal -- only per-segment Content
// can shrink, via a registered ProjectionFunc, and only for segments
// whose SourceReference matches one.
//
// Fallback (R10_DESIGN_AUDIT.md section M): if profile.TaskClass does
// not match how this snapshot was built, or any RequiredTiers segment is
// missing/excluded in the canonical snapshot, Compile returns the
// canonical snapshot's segments byte-for-byte, FellBackToCanonical=true.
// A missing required tier is never silently tolerated by proceeding with
// a partial view.
func Compile(profile ContextProfile, canonical contextengine.Snapshot) (CompilationResult, error) {
	result := CompilationResult{
		ContextSnapshotID:     canonical.ID,
		ContextProfileID:      profile.ID,
		ContextProfileVersion: profile.Version,
	}

	presentTiers := make(map[contextengine.AuthorityTier]bool, len(canonical.Segments))
	for _, seg := range canonical.Segments {
		if seg.Included {
			presentTiers[seg.AuthorityTier] = true
		}
	}
	for _, required := range profile.RequiredTiers {
		if !presentTiers[required] {
			// Fail closed to the canonical, unprojected view rather
			// than silently proceeding with a required tier missing.
			result.Projected = canonical
			result.FellBackToCanonical = true
			return finalize(result, canonical)
		}
	}

	projectedSegments := make([]contextengine.Segment, len(canonical.Segments))
	diffs := make([]SegmentDiff, 0, len(canonical.Segments))
	for i, seg := range canonical.Segments {
		projectedSegments[i] = seg
		diff := SegmentDiff{
			SourceReference:      seg.SourceReference,
			AuthorityTier:        seg.AuthorityTier,
			OriginalBytes:        seg.ByteCount,
			ProjectedBytes:       seg.ByteCount,
			OriginalContentHash:  seg.ContentHash,
			ProjectedContentHash: seg.ContentHash,
		}
		if seg.Included {
			if fn, ok := profile.Projections[seg.SourceReference]; ok {
				projectedContent, reason, err := fn(seg, canonical.ActorRoleID)
				if err != nil {
					return CompilationResult{}, fmt.Errorf("contextcompiler: projection failed for %s: %w", seg.SourceReference, err)
				}
				if len(projectedContent) > 0 && len(projectedContent) < len(seg.Content) {
					hash := sha256.Sum256(projectedContent)
					projectedSegments[i].Content = projectedContent
					projectedSegments[i].ByteCount = len(projectedContent)
					projectedSegments[i].ContentHash = hex.EncodeToString(hash[:])
					diff.Projected = true
					diff.Reason = reason
					diff.ProjectedBytes = len(projectedContent)
					diff.ProjectedContentHash = projectedSegments[i].ContentHash
				} else {
					diff.Reason = reason // e.g. "..._not_found_fell_back_to_full_catalog"
				}
			}
		}
		diffs = append(diffs, diff)
	}

	result.Projected = canonical
	result.Projected.Segments = projectedSegments
	result.SegmentDiffs = diffs
	return finalize(result, canonical)
}

// CompileForTaskClass looks up a registered ContextProfile by the
// snapshot's Purpose (TaskClassOf) and compiles against it. An unknown
// task class is NOT an error and NEVER produces an arbitrarily minimal
// view -- it returns the canonical snapshot unmodified,
// FellBackToCanonical=true (R10_DESIGN_AUDIT.md section M/41: only
// research.corpus_curate is affected by R10 V1, every other task class
// behaves exactly as it did before this package existed).
// CompileForTaskClass resolves canonical's ContextProfile through the
// deterministic M1.3 selector precedence (EXACT, TASK-CLASS,
// EXECUTION-PURPOSE, CANONICAL fallback -- see SelectorRegistry.Select),
// built ONLY from durable Snapshot facts (BuildSelector). ActorRoleID
// alone can never activate a profile: with M1.3, the removed
// ActorRoleID-only proxy (formerly TaskClassOf) no longer exists in this
// resolution path at all.
func CompileForTaskClass(canonical contextengine.Snapshot) (CompilationResult, error) {
	selector := BuildSelector(canonical)
	selection := defaultSelectorRegistry.Select(selector)
	if !selection.Matched {
		result := CompilationResult{
			ContextSnapshotID:   canonical.ID,
			Projected:           canonical,
			FellBackToCanonical: true,
			SelectionKind:       selection.Kind,
		}
		return finalize(result, canonical)
	}
	result, err := Compile(selection.Profile, canonical)
	if err != nil {
		return CompilationResult{}, err
	}
	// A required-tier failure inside Compile fails closed to canonical
	// (FellBackToCanonical=true) without Compile itself knowing which
	// selection tier chose the profile it fell back from -- so the
	// provenance recorded here always reflects what ACTUALLY happened,
	// not merely what was attempted.
	if result.FellBackToCanonical {
		result.SelectionKind = SelectionCanonical
	} else {
		result.SelectionKind = selection.Kind
	}
	return result, nil
}

func finalize(result CompilationResult, canonical contextengine.Snapshot) (CompilationResult, error) {
	stableBytes, dynamicBytes := 0, 0
	orderInput := make([]byte, 0, 256)
	contentInput := make([]byte, 0, 4096)
	for _, seg := range result.Projected.Segments {
		if !seg.Included {
			continue
		}
		// R31 hardening §18.3: use the same single source of truth
		// ProviderRender uses for its own StablePrefix/DynamicSuffix
		// partition, instead of re-declaring a narrower, independently
		// maintained rule here. Before this fix, this telemetry only
		// treated TierTask as dynamic -- silently disagreeing with
		// ProviderRender's real partition for any snapshot carrying
		// TierProject/TierRAGEvidence/TierApprovedMemory/TierApprovedSkill
		// content (research.corpus_curate/v1 does not use those tiers
		// today, so the divergence was latent, not yet observed in
		// production telemetry -- but a real, confirmed inconsistency
		// regardless of whether it had yet produced a visibly wrong number).
		if contextengine.IsDynamicProviderTier(seg.AuthorityTier) {
			dynamicBytes += seg.ByteCount
		} else {
			stableBytes += seg.ByteCount
		}
		orderInput = append(orderInput, []byte(fmt.Sprintf("%d:%s:%d|", seg.AuthorityPriority, seg.AuthorityTier, seg.RenderOrdinal))...)
		contentInput = append(contentInput, []byte(seg.ContentHash)...)
	}
	result.StablePrefixBytes = stableBytes
	result.DynamicSuffixBytes = dynamicBytes
	orderHash := sha256.Sum256(orderInput)
	result.AuthorityOrderHash = hex.EncodeToString(orderHash[:])
	contentHash := sha256.Sum256(contentInput)
	result.CompiledContentHash = hex.EncodeToString(contentHash[:])
	return result, nil
}
