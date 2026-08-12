package contextcompiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// TaskClassOf is a KNOWN, DOCUMENTED PROXY, not a real task-class field
// (there is no "task_class" concept anywhere in internal/contextengine
// or internal/tasks today -- audited directly: Snapshot.Purpose is
// "department_worker" for every r9/r9.1 invocation, an egress-scope
// value, unrelated to what kind of work the task is). For R10 V1, the
// requesting role IS the practical, conservative proxy: today
// investigacion/research_worker_hourly's entire real workload is corpus
// curation, so matching on ActorRoleID is safe and narrow exactly as
// R10_DESIGN_AUDIT.md section 54 requires ("sólo research.corpus_curate,
// no generalizar"). This must be revisited with a real task-class field
// before this profile could ever apply to more than one workflow for
// the same role -- flagged explicitly, not silently assumed solid.
func TaskClassOf(actorRoleID string) string {
	switch actorRoleID {
	case "investigacion/research_worker_hourly":
		return ResearchCorpusCurateV1TaskClass
	case "investigacion/research_worker_hourly_mimo_canary":
		// R10.2: additive, temporary canary role (docs/canonical/
		// role-catalog.yaml) that exists ONLY to route to the MiMo
		// challenger without touching the production role's default
		// model_policy. It must receive the exact same
		// ExecutionContextView semantics as the real role (section 11
		// of the R10.2 spec: "NO volver al contexto completo").
		return ResearchCorpusCurateV1TaskClass
	default:
		return ""
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
			SourceReference:     seg.SourceReference,
			AuthorityTier:       seg.AuthorityTier,
			OriginalBytes:       seg.ByteCount,
			ProjectedBytes:      seg.ByteCount,
			OriginalContentHash: seg.ContentHash,
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
func CompileForTaskClass(canonical contextengine.Snapshot) (CompilationResult, error) {
	profile, ok := Registry()[TaskClassOf(canonical.ActorRoleID)]
	if !ok {
		result := CompilationResult{
			ContextSnapshotID:  canonical.ID,
			Projected:          canonical,
			FellBackToCanonical: true,
		}
		return finalize(result, canonical)
	}
	return Compile(profile, canonical)
}

func finalize(result CompilationResult, canonical contextengine.Snapshot) (CompilationResult, error) {
	stableBytes, dynamicBytes := 0, 0
	orderInput := make([]byte, 0, 256)
	contentInput := make([]byte, 0, 4096)
	for _, seg := range result.Projected.Segments {
		if !seg.Included {
			continue
		}
		if seg.AuthorityTier == contextengine.TierTask {
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
