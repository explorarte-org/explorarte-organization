package contextengine

import (
	"context"
	"errors"
	"testing"
)

func TestAssemblerAuthorityIsSeparateFromRenderOrder(t *testing.T) {
	sources := []SourceRecord{
		testSource(TierApprovedSkill, SourceApprovedSkill, "skill-z", "skill", InstructionProcedure, TrustApproved, DataOrganizational, false),
		testSource(TierApprovedMemory, SourceApprovedMemory, "memory-a", "memory", InstructionData, TrustUntrusted, DataOrganizational, false),
		testSource(TierTask, SourceTaskContext, "task-a", "task", InstructionScoped, TrustScoped, DataOrganizational, false),
		testSource(TierImmutableSafety, SourceCanonicalDocument, "safety", "safety", InstructionImmutableConstraint, TrustImmutable, DataOrganizational, false),
	}
	assembly, err := NewAssembler().Assemble(context.Background(), AssemblyInput{Sources: sources, MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 20, MaxSkills: 10, MaxMemorySegments: 10, MaxRAGSegments: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := assembly.Segments[0].AuthorityTier; got != TierImmutableSafety {
		t.Fatalf("first tier=%s", got)
	}
	if got := assembly.Segments[1].AuthorityTier; got != TierApprovedMemory {
		t.Fatalf("memory render position tier=%s", got)
	}
	if got := assembly.Segments[2].AuthorityTier; got != TierApprovedSkill {
		t.Fatalf("skill render position tier=%s", got)
	}
	if assembly.Segments[1].AuthorityPriority != 6 || assembly.Segments[2].AuthorityPriority != 4 {
		t.Fatalf("memory/skill authority priorities=%d/%d", assembly.Segments[1].AuthorityPriority, assembly.Segments[2].AuthorityPriority)
	}
}

func TestAssemblerRejectsCapabilityAuthorityFromTaskMemoryAndRAG(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tier  AuthorityTier
		kind  SourceKind
		class InstructionClass
		trust TrustClass
	}{
		{"task", TierTask, SourceTaskContext, InstructionScoped, TrustScoped},
		{"memory", TierApprovedMemory, SourceApprovedMemory, InstructionData, TrustUntrusted},
		{"rag", TierRAGEvidence, SourceRAGEvidence, InstructionData, TrustUntrusted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := testSource(tc.tier, tc.kind, tc.name, "content", tc.class, tc.trust, DataOrganizational, true)
			_, err := NewAssembler().Assemble(context.Background(), AssemblyInput{Sources: []SourceRecord{source}, MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 20})
			if err == nil || ReasonOf(err) != ReasonUnsafeInstructionSource {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAssemblerOmitsMemoryAndRAGWholeAndDeterministically(t *testing.T) {
	mandatory := testSource(TierRoleProfile, SourceRoleProfile, "profile", string(make([]byte, 40)), InstructionRole, TrustAuthoritative, DataOrganizational, false)
	low := testSource(TierApprovedMemory, SourceApprovedMemory, "memory-a", string(make([]byte, 30)), InstructionData, TrustUntrusted, DataOrganizational, false)
	low.Relevance = 1
	high := testSource(TierRAGEvidence, SourceRAGEvidence, "rag-z", string(make([]byte, 30)), InstructionData, TrustUntrusted, DataSanitized, false)
	high.Relevance = 10
	assembly, err := NewAssembler().Assemble(context.Background(), AssemblyInput{Sources: []SourceRecord{high, mandatory, low}, MaxTotalBytes: 70, MaxSegmentBytes: 1024, MaxSegments: 20, MaxMemorySegments: 10, MaxRAGSegments: 10})
	if err != nil {
		t.Fatal(err)
	}
	var omitted Segment
	for _, segment := range assembly.Segments {
		if !segment.Included {
			omitted = segment
		}
	}
	if omitted.SourceReference != "memory-a" || omitted.Content != nil || omitted.ByteCount != 0 || omitted.OmissionReason == "" {
		t.Fatalf("omitted=%+v", omitted)
	}
	if assembly.TotalBytes != 70 {
		t.Fatalf("total=%d", assembly.TotalBytes)
	}
}

func TestAssemblerDoesNotTruncateMandatorySources(t *testing.T) {
	source := testSource(TierRoleProfile, SourceRoleProfile, "profile", string(make([]byte, 2048)), InstructionRole, TrustAuthoritative, DataOrganizational, false)
	_, err := NewAssembler().Assemble(context.Background(), AssemblyInput{Sources: []SourceRecord{source}, MaxTotalBytes: 65536, MaxSegmentBytes: 1024, MaxSegments: 20})
	if !errors.Is(err, ErrRejected) || ReasonOf(err) != ReasonContextBudgetExceeded {
		t.Fatalf("error=%v", err)
	}
}

func testSource(tier AuthorityTier, kind SourceKind, reference, content string, class InstructionClass, trust TrustClass, data DataClass, mayGrant bool) SourceRecord {
	priority, _ := AuthorityPriority(tier)
	return SourceRecord{Kind: kind, Reference: reference, Version: "v1", AuthorityTier: tier, AuthorityPriority: priority, InstructionClass: class, TrustClass: trust, DataClass: data, MayGrantCapabilities: mayGrant, Content: []byte(content), ContentHash: DigestMarkdown([]byte(content)), Included: true}
}
