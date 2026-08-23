package contextengine

import (
	"context"
	"sort"
	"strings"
)

type DeterministicAssembler struct{}

func NewAssembler() *DeterministicAssembler { return &DeterministicAssembler{} }

func (a *DeterministicAssembler) Assemble(ctx context.Context, input AssemblyInput) (Assembly, error) {
	if err := ctx.Err(); err != nil {
		return Assembly{}, err
	}
	if input.MaxTotalBytes <= 0 || input.MaxSegmentBytes <= 0 || input.MaxSegments <= 0 {
		return Assembly{}, Reject(ReasonInvalidRequest, "assembly_limits", "assembly limits must be positive")
	}
	if len(input.Sources) > input.MaxSegments {
		return Assembly{}, Reject(ReasonContextBudgetExceeded, "max_segments", "source count exceeds maximum segment count")
	}
	sources := make([]SourceRecord, len(input.Sources))
	copy(sources, input.Sources)
	for index := range sources {
		if err := ctx.Err(); err != nil {
			return Assembly{}, err
		}
		if err := ValidateSourceMetadata(sources[index]); err != nil {
			return Assembly{}, err
		}
		if untrustedDataSource(sources[index].Kind) {
			if sources[index].InstructionClass != InstructionData || sources[index].TrustClass != TrustUntrusted || sources[index].MayGrantCapabilities {
				return Assembly{}, Reject(ReasonUnsafeInstructionSource, sources[index].Reference, "memory, RAG, web and repository evidence must remain untrusted data without capability authority")
			}
		}
	}
	sort.SliceStable(sources, func(i, j int) bool {
		ri, _ := RenderRank(sources[i].AuthorityTier)
		rj, _ := RenderRank(sources[j].AuthorityTier)
		if ri != rj {
			return ri < rj
		}
		if sources[i].Reference != sources[j].Reference {
			return sources[i].Reference < sources[j].Reference
		}
		if sources[i].Version != sources[j].Version {
			return sources[i].Version < sources[j].Version
		}
		return sources[i].ContentHash < sources[j].ContentHash
	})

	omitted := make(map[int]string)
	memoryIndexes, ragIndexes := optionalIndexes(sources)
	applyCountLimit(sources, memoryIndexes, input.MaxMemorySegments, omitted, "memory_segment_limit")
	applyCountLimit(sources, ragIndexes, input.MaxRAGSegments, omitted, "rag_segment_limit")

	for index, source := range sources {
		if _, isOmitted := omitted[index]; isOmitted {
			continue
		}
		if len(source.Content) > input.MaxSegmentBytes {
			if optionalSource(source) {
				omitted[index] = string(ReasonSourceTooLarge)
				continue
			}
			return Assembly{}, Reject(ReasonContextBudgetExceeded, source.Reference, "mandatory source exceeds maximum segment bytes")
		}
	}

	total := includedBytes(sources, omitted)
	if total > input.MaxTotalBytes {
		candidates := omissionCandidates(sources, omitted)
		for _, index := range candidates {
			if total <= input.MaxTotalBytes {
				break
			}
			omitted[index] = "context_budget_omission"
			total -= len(sources[index].Content)
		}
	}
	if total > input.MaxTotalBytes {
		return Assembly{}, Reject(ReasonContextBudgetExceeded, "max_total_bytes", "mandatory context exceeds total byte budget")
	}

	segments := make([]Segment, 0, len(sources))
	includedCount := 0
	for index, source := range sources {
		reason, isOmitted := omitted[index]
		segment := Segment{
			Ordinal:              index + 1,
			RenderOrdinal:        index + 1,
			AuthorityPriority:    source.AuthorityPriority,
			AuthorityTier:        source.AuthorityTier,
			SourceKind:           source.Kind,
			SourceReference:      source.Reference,
			SourceVersion:        source.Version,
			InstructionClass:     source.InstructionClass,
			TrustClass:           source.TrustClass,
			DataClass:            source.DataClass,
			MayGrantCapabilities: source.MayGrantCapabilities,
			Included:             !isOmitted,
			ContentHash:          source.ContentHash,
		}
		if isOmitted {
			segment.OmissionReason = reason
		} else {
			segment.Content = append([]byte(nil), source.Content...)
			segment.ByteCount = len(source.Content)
			includedCount++
		}
		segments = append(segments, segment)
	}
	return Assembly{
		Segments:             segments,
		SegmentCount:         len(segments),
		IncludedSegmentCount: includedCount,
		OmittedSegmentCount:  len(segments) - includedCount,
		TotalBytes:           int64(total),
	}, nil
}

func optionalIndexes(sources []SourceRecord) (memory, rag []int) {
	for index, source := range sources {
		switch source.Kind {
		case SourceApprovedMemory:
			memory = append(memory, index)
		case SourceRAGEvidence:
			rag = append(rag, index)
		}
	}
	return
}

func applyCountLimit(sources []SourceRecord, indexes []int, maximum int, omitted map[int]string, reason string) {
	if maximum < 0 || len(indexes) <= maximum {
		return
	}
	ordered := append([]int(nil), indexes...)
	sort.Slice(ordered, func(i, j int) bool { return lessOmittable(sources[ordered[i]], sources[ordered[j]]) })
	for _, index := range ordered[:len(ordered)-maximum] {
		omitted[index] = reason
	}
}

func omissionCandidates(sources []SourceRecord, omitted map[int]string) []int {
	values := []int{}
	for index, source := range sources {
		if optionalSource(source) {
			if _, exists := omitted[index]; !exists {
				values = append(values, index)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return lessOmittable(sources[values[i]], sources[values[j]]) })
	return values
}

func lessOmittable(left, right SourceRecord) bool {
	if left.Relevance != right.Relevance {
		return left.Relevance < right.Relevance
	}
	if left.ProviderPriority != right.ProviderPriority {
		return left.ProviderPriority < right.ProviderPriority
	}
	return strings.Compare(left.Reference, right.Reference) < 0
}

func optionalSource(source SourceRecord) bool {
	return untrustedDataSource(source.Kind)
}

// untrustedDataSource names the kinds that are things the organization READ,
// never things it was told. One list, used by the gate that enforces it and by
// the omission rule that treats them as droppable -- two lists would drift,
// and the drift would appear as a source that may be dropped for size but not
// checked for authority, or the reverse.
func untrustedDataSource(kind SourceKind) bool {
	switch kind {
	case SourceApprovedMemory, SourceRAGEvidence, SourceWebEvidence, SourceRepositoryEvidence:
		return true
	default:
		return false
	}
}

func includedBytes(sources []SourceRecord, omitted map[int]string) int {
	total := 0
	for index, source := range sources {
		if _, exists := omitted[index]; !exists {
			total += len(source.Content)
		}
	}
	return total
}
