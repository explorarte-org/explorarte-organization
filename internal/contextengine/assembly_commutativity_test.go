package contextengine

import (
	"context"
	"fmt"
	"testing"
)

// The assembled context is order-sensitive by design and must stay that way:
// what a later segment means can come from the ones before it. That property
// is about the RENDERED artifact, not about the set of sources that produced
// it, and the two get confused easily.
//
// This test pins the distinction. Registering sources is administrative and
// commutative -- A then B and B then A are the same set. Rendering that set
// is deterministic and total. So the snapshot is a pure function of the
// source SET, and the order in which sources arrived is not an input to it.
//
// That is what makes it safe to manage sources independently upstream while
// keeping precedence absolute downstream. If this test ever fails, the
// arrival order has leaked into meaning, and any upstream component that
// registers sources concurrently has become order-dependent without saying so.
func TestTheAssembledSnapshotIsAPureFunctionOfTheSourceSet(t *testing.T) {
	sources := []SourceRecord{
		testSource(TierImmutableSafety, SourceCanonicalDocument, "safety", "safety", InstructionImmutableConstraint, TrustImmutable, DataOrganizational, false),
		testSource(TierApprovedSkill, SourceApprovedSkill, "skill-b", "skill-b", InstructionProcedure, TrustApproved, DataOrganizational, false),
		testSource(TierApprovedSkill, SourceApprovedSkill, "skill-a", "skill-a", InstructionProcedure, TrustApproved, DataOrganizational, false),
		testSource(TierTask, SourceTaskContext, "task", "task", InstructionScoped, TrustScoped, DataOrganizational, false),
		testSource(TierApprovedMemory, SourceApprovedMemory, "memory-b", "memory-b", InstructionData, TrustUntrusted, DataOrganizational, false),
		testSource(TierApprovedMemory, SourceApprovedMemory, "memory-a", "memory-a", InstructionData, TrustUntrusted, DataOrganizational, false),
		testSource(TierRAGEvidence, SourceRAGEvidence, "rag-b", "rag-b", InstructionData, TrustUntrusted, DataOrganizational, false),
		testSource(TierRAGEvidence, SourceRAGEvidence, "rag-a", "rag-a", InstructionData, TrustUntrusted, DataOrganizational, false),
	}

	// The limits are deliberately below the supply. Truncation runs AFTER
	// the sort, so it is the sharpest place arrival order could leak in:
	// WHICH memory and WHICH evidence get dropped has to be a property of
	// the set, not of who registered first.
	input := AssemblyInput{
		MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 20,
		MaxSkills: 10, MaxMemorySegments: 1, MaxRAGSegments: 1,
	}

	render := func(order []SourceRecord) string {
		in := input
		in.Sources = order
		assembly, err := NewAssembler().Assemble(context.Background(), in)
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		return fmt.Sprintf("%+v", assembly)
	}

	want := render(sources)
	permutations := 0
	permute(sources, func(order []SourceRecord) {
		permutations++
		if got := render(order); got != want {
			t.Fatalf("arrival order changed the snapshot\n permutation: %s\n      wanted: %s\n         got: %s",
				references(order), want, got)
		}
	})
	if permutations != 40320 {
		t.Fatalf("expected every permutation of 8 sources to be checked, got %d", permutations)
	}

	// The guard is only meaningful if the render itself is ordered. If the
	// assembler ever stopped imposing precedence, the test above would
	// still pass -- trivially -- so assert the order it is protecting.
	assembly, err := NewAssembler().Assemble(context.Background(), AssemblyInput{
		Sources: sources, MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 20,
		MaxSkills: 10, MaxMemorySegments: 10, MaxRAGSegments: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if assembly.Segments[0].SourceReference != "safety" {
		t.Fatalf("immutable safety must render first, got %q", assembly.Segments[0].SourceReference)
	}
	for i := 1; i < len(assembly.Segments); i++ {
		if assembly.Segments[i].RenderOrdinal <= assembly.Segments[i-1].RenderOrdinal {
			t.Fatalf("render ordinals must be strictly increasing: %d then %d",
				assembly.Segments[i-1].RenderOrdinal, assembly.Segments[i].RenderOrdinal)
		}
	}
}

func permute(in []SourceRecord, visit func([]SourceRecord)) {
	work := append([]SourceRecord{}, in...)
	var recurse func(k int)
	recurse = func(k int) {
		if k == 1 {
			visit(append([]SourceRecord{}, work...))
			return
		}
		for i := 0; i < k; i++ {
			recurse(k - 1)
			if k%2 == 0 {
				work[i], work[k-1] = work[k-1], work[i]
			} else {
				work[0], work[k-1] = work[k-1], work[0]
			}
		}
	}
	recurse(len(work))
}

func references(in []SourceRecord) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += ","
		}
		out += s.Reference
	}
	return out
}
