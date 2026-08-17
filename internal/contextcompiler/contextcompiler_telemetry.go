package contextcompiler

import (
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// EstimatorID/EstimatorVersion identify the ONLY token estimator M1.2
// defines. A future estimator with different semantics (provider
// calibration, a real tokenizer, language-aware counting) becomes a new
// EstimatorID/EstimatorVersion pair -- this formula, once frozen, must
// never change under this exact identity, or a historical
// ContextTokenTelemetry row would silently reinterpret its own past
// record under new semantics. See EstimateTokens.
const (
	EstimatorID      = "context_utf8_bytes_ceiling"
	EstimatorVersion = "v1"
)

// EstimateTokens is the frozen v1 deterministic, versioned, provider-
// agnostic, zero-network observational token estimator: ceil(bytes/3),
// 0 bytes -> 0 tokens. It is a conservative, documented approximation,
// not tokenizer parity -- M1.2 needs telemetry, not perfect token counts.
//
// This is NOT modelruntime's estimateTokenCount (its pre-send cost
// reservation estimator): that function has its own, deliberately
// separate financial-safety semantics and this one must never be
// confused with or substituted for it (M1.2 section 5). Coincidentally
// using the same 3-bytes-per-token constant does not make them the same
// estimator -- each has its own explicit identity and purpose.
func EstimateTokens(byteCount int) int64 {
	if byteCount <= 0 {
		return 0
	}
	return (int64(byteCount) + 2) / 3
}

// SegmentTokenEstimate is the per-segment attribution telemetry M1.2
// section 8 requires: enough to later analyze which segments dominate
// estimated token cost and which were reduced by projection, without
// ever persisting segment Content a second time (the canonical
// ContextSnapshot already owns that).
type SegmentTokenEstimate struct {
	SourceReference  string
	AuthorityTier    contextengine.AuthorityTier
	SourceKind       contextengine.SourceKind
	SourceVersion    string
	InstructionClass contextengine.InstructionClass
	TrustClass       contextengine.TrustClass
	DataClass        contextengine.DataClass
	RenderOrdinal    int
	OriginalBytes    int
	DeliveredBytes   int

	OriginalEstimatedTokens  int64
	DeliveredEstimatedTokens int64

	Projected        bool
	ProjectionReason string
}

// ContextTokenTelemetry is M1.2's required telemetry shape (section 7)
// for one durable ExecutionContextView: deterministic, versioned HOST
// ESTIMATES derived only from durable M1.1 facts -- never a re-render,
// never provider-reported usage. See M1.2 section 3: an
// EstimatedProviderVisibleTokens value must never be conflated with a
// provider's own reported InputTokens for the same invocation.
//
// Section 9: sum(SegmentTokenEstimates) is intentionally NOT required to
// equal EstimatedProviderVisibleTokens -- the exact render includes
// envelope/formatting bytes not attributable to a single segment, and
// independent ceiling operations can differ by rounding. Do not add an
// invariant that does not exist.
type ContextTokenTelemetry struct {
	ExecutionContextViewID int64
	ContextSnapshotID      int64
	ContextProfileID       string
	ContextProfileVersion  string

	EstimatorID      string
	EstimatorVersion string

	ProviderVisibleBytes           int
	EstimatedProviderVisibleTokens int64

	StablePrefixBytes           int
	EstimatedStablePrefixTokens int64

	DynamicSuffixBytes           int
	EstimatedDynamicSuffixTokens int64

	SegmentTokenEstimates []SegmentTokenEstimate
}

// ErrContextTokenTelemetryBinding is returned when a caller asks for
// telemetry for a view against a canonical snapshot the view does not
// actually belong to (M1.2 section 21.H): telemetry generation fails
// closed rather than silently measuring the wrong thing.
var ErrContextTokenTelemetryBinding = errors.New("context token telemetry: execution context view does not belong to the supplied canonical snapshot")

// ErrContextTokenTelemetryAttribution is returned when view.SegmentDiffs
// cannot be safely, positionally attributed to canonical.Segments (M1.2
// section 8: "if safe attribution cannot be proven: fail TELEMETRY
// generation for that segment/view, not productive dispatch").
var ErrContextTokenTelemetryAttribution = errors.New("context token telemetry: segment diffs cannot be safely attributed to canonical segments")

// BuildContextTokenTelemetry derives deterministic, versioned token
// telemetry for view from facts ALREADY durably recorded on it (M1.1)
// and the canonical segments those facts describe. It never re-renders,
// never re-derives provider-visible bytes, and never calls
// CompileForTaskClass/ResolveProviderContext again -- those remain the
// single resolution algorithm; this function only measures what was
// already durably decided.
//
// canonical must be the exact contextengine.Snapshot view.ContextSnapshotID
// was resolved from; a mismatch is ErrContextTokenTelemetryBinding. A view
// that fails M1.1's own ValidateIntegrity never produces telemetry either
// -- corrupt durable facts must never be measured as if they were trusted
// (M1.2 section 21.I).
func BuildContextTokenTelemetry(view ExecutionContextView, canonical contextengine.Snapshot) (ContextTokenTelemetry, error) {
	if view.ContextSnapshotID != canonical.ID {
		return ContextTokenTelemetry{}, fmt.Errorf("%w: view snapshot=%d canonical snapshot=%d", ErrContextTokenTelemetryBinding, view.ContextSnapshotID, canonical.ID)
	}
	if err := ValidateIntegrity(view); err != nil {
		return ContextTokenTelemetry{}, fmt.Errorf("context token telemetry: durable view failed integrity: %w", err)
	}
	segments, err := attributeSegmentTokenEstimates(view, canonical)
	if err != nil {
		return ContextTokenTelemetry{}, err
	}
	return ContextTokenTelemetry{
		ExecutionContextViewID:         view.ID,
		ContextSnapshotID:              view.ContextSnapshotID,
		ContextProfileID:               view.ContextProfileID,
		ContextProfileVersion:          view.ContextProfileVersion,
		EstimatorID:                    EstimatorID,
		EstimatorVersion:               EstimatorVersion,
		ProviderVisibleBytes:           view.ProviderVisibleByteCount,
		EstimatedProviderVisibleTokens: EstimateTokens(view.ProviderVisibleByteCount),
		StablePrefixBytes:              view.StablePrefixBytes,
		EstimatedStablePrefixTokens:    EstimateTokens(view.StablePrefixBytes),
		DynamicSuffixBytes:             view.DynamicSuffixBytes,
		EstimatedDynamicSuffixTokens:   EstimateTokens(view.DynamicSuffixBytes),
		SegmentTokenEstimates:          segments,
	}, nil
}

// attributeSegmentTokenEstimates builds one SegmentTokenEstimate per
// INCLUDED canonical segment (the segments that actually reached the
// delivered provider-visible view -- an excluded segment delivered
// nothing and has no delivered-token cost to attribute).
//
// Fallback (view.FellBackToCanonical, or the historically-possible empty
// SegmentDiffs -- M1.2 section 8 explicitly warns not to assume a
// non-empty SegmentDiffs array here): every included segment behaves as
// unprojected, DeliveredBytes == OriginalBytes, exactly mirroring what
// CompileForTaskClass's own fallback branch guarantees.
//
// Projected: view.SegmentDiffs is positionally aligned with
// canonical.Segments 1:1 by construction (contextcompiler_compiler.go's
// Compile iterates canonical.Segments in order to build both
// projectedSegments and diffs) -- but this function proves that alignment
// by SourceReference at every position before trusting it, rather than
// assuming it silently. Any proven misalignment fails closed with
// ErrContextTokenTelemetryAttribution.
func attributeSegmentTokenEstimates(view ExecutionContextView, canonical contextengine.Snapshot) ([]SegmentTokenEstimate, error) {
	if view.FellBackToCanonical || len(view.SegmentDiffs) == 0 {
		estimates := make([]SegmentTokenEstimate, 0, len(canonical.Segments))
		for _, seg := range canonical.Segments {
			if !seg.Included {
				continue
			}
			estimates = append(estimates, SegmentTokenEstimate{
				SourceReference: seg.SourceReference, AuthorityTier: seg.AuthorityTier,
				SourceKind: seg.SourceKind, SourceVersion: seg.SourceVersion,
				InstructionClass: seg.InstructionClass, TrustClass: seg.TrustClass, DataClass: seg.DataClass,
				RenderOrdinal:  seg.RenderOrdinal,
				OriginalBytes:  seg.ByteCount,
				DeliveredBytes: seg.ByteCount,

				OriginalEstimatedTokens:  EstimateTokens(seg.ByteCount),
				DeliveredEstimatedTokens: EstimateTokens(seg.ByteCount),
			})
		}
		return estimates, nil
	}
	if len(view.SegmentDiffs) != len(canonical.Segments) {
		return nil, fmt.Errorf("%w: %d segment diffs for %d canonical segments", ErrContextTokenTelemetryAttribution, len(view.SegmentDiffs), len(canonical.Segments))
	}
	estimates := make([]SegmentTokenEstimate, 0, len(canonical.Segments))
	for i, seg := range canonical.Segments {
		diff := view.SegmentDiffs[i]
		if diff.SourceReference != seg.SourceReference {
			return nil, fmt.Errorf("%w: position %d diff source=%q canonical source=%q", ErrContextTokenTelemetryAttribution, i, diff.SourceReference, seg.SourceReference)
		}
		if !seg.Included {
			continue
		}
		estimates = append(estimates, SegmentTokenEstimate{
			SourceReference: seg.SourceReference, AuthorityTier: seg.AuthorityTier,
			SourceKind: seg.SourceKind, SourceVersion: seg.SourceVersion,
			InstructionClass: seg.InstructionClass, TrustClass: seg.TrustClass, DataClass: seg.DataClass,
			RenderOrdinal:  seg.RenderOrdinal,
			OriginalBytes:  diff.OriginalBytes,
			DeliveredBytes: diff.ProjectedBytes,

			OriginalEstimatedTokens:  EstimateTokens(diff.OriginalBytes),
			DeliveredEstimatedTokens: EstimateTokens(diff.ProjectedBytes),

			Projected:        diff.Projected,
			ProjectionReason: diff.Reason,
		})
	}
	return estimates, nil
}
