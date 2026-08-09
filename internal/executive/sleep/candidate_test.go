package sleep

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

func TestBuildCandidateIsDeterministicAndObservedOnly(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	primary := Group{Key: GroupKey{UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa", ProviderID: "deepseek", ProviderModelID: "deepseek-model"}, Experiences: []Experience{
		testExperience(3, VerificationVerified, "deepseek", now.Add(2*time.Minute)),
		testExperience(1, VerificationVerified, "deepseek", now),
		testExperience(2, VerificationContradicted, "deepseek", now.Add(time.Minute)),
	}}
	analysis := AnalyzeGroup(primary, 3)
	config := DefaultConfig()
	first, err := BuildCandidate(primary, []Group{primary}, analysis, config)
	if err != nil {
		t.Fatal(err)
	}
	reordered := primary
	reordered.Experiences = []Experience{primary.Experiences[1], primary.Experiences[2], primary.Experiences[0]}
	second, err := BuildCandidate(reordered, []Group{reordered}, AnalyzeGroup(reordered, 3), config)
	if err != nil {
		t.Fatal(err)
	}
	if first.Request.IdempotencyKey != second.Request.IdempotencyKey || first.Request.Command.ID != second.Request.Command.ID || first.Request.Command.Body != second.Request.Command.Body {
		t.Fatalf("candidate changed under input ordering:\nfirst=%+v\nsecond=%+v", first.Request, second.Request)
	}
	command := first.Request.Command
	if command.SourceKind != rag.SourceOperational || command.Admission.SourceBoundary != SourceBoundary || command.ProposedBy != ProposerRoleID {
		t.Fatalf("unexpected provenance: %+v", command)
	}
	if command.Admission.AttestedAt != now.Add(2*time.Minute) {
		t.Fatalf("attested_at=%s want latest observed %s", command.Admission.AttestedAt, now.Add(2*time.Minute))
	}
	if len(command.EvidenceRefs) != 3 || !reflect.DeepEqual(first.EvidenceRunIDs, []int64{1, 2, 3}) {
		t.Fatalf("evidence refs=%+v ids=%v", command.EvidenceRefs, first.EvidenceRunIDs)
	}
	for _, ref := range command.EvidenceRefs {
		if !strings.HasPrefix(ref.Reference, "decisiongraph:run:") {
			t.Fatalf("non-run evidence ref: %+v", ref)
		}
	}
	var body CandidateBody
	if err := json.Unmarshal([]byte(command.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != BodySchemaVersion || !body.Contradiction || !strings.Contains(strings.Join(body.ApplicabilityConditions, " "), "not an unconditional success claim") {
		t.Fatalf("body=%+v", body)
	}
	if strings.Contains(strings.ToLower(command.Body), "synthetic") || strings.Contains(strings.ToLower(command.Body), "simulation") {
		t.Fatalf("candidate falsely describes synthetic provenance: %s", command.Body)
	}
}

func TestBuildCandidateCommitsPrimaryGroupIntoIdempotency(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	groupA := Group{Key: GroupKey{UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa", ProviderID: "provider-a", ProviderModelID: "provider-a-model"}, Experiences: []Experience{
		testExperience(1, VerificationVerified, "provider-a", now),
		testExperience(2, VerificationVerified, "provider-a", now.Add(time.Minute)),
		testExperience(3, VerificationVerified, "provider-a", now.Add(2*time.Minute)),
	}}
	groupB := Group{Key: GroupKey{UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa", ProviderID: "provider-b", ProviderModelID: "provider-b-model"}, Experiences: []Experience{
		testExperience(4, VerificationVerified, "provider-b", now.Add(3*time.Minute)),
		testExperience(5, VerificationVerified, "provider-b", now.Add(4*time.Minute)),
		testExperience(6, VerificationVerified, "provider-b", now.Add(5*time.Minute)),
	}}
	recurring := []Group{groupA, groupB}
	a, err := BuildCandidate(groupA, recurring, AnalyzeGroup(groupA, 3), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildCandidate(groupB, recurring, AnalyzeGroup(groupB, 3), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(a.EvidenceRunIDs, b.EvidenceRunIDs) == false {
		t.Fatalf("expected shared portability evidence: a=%v b=%v", a.EvidenceRunIDs, b.EvidenceRunIDs)
	}
	if a.Request.IdempotencyKey == b.Request.IdempotencyKey {
		t.Fatal("different primary claims collided under one idempotency key")
	}
	assertEvidenceRole := func(candidate BuiltCandidate, primaryRunIDs, portabilityRunIDs []int64) {
		t.Helper()
		refs := make(map[string]bool, len(candidate.Request.Command.EvidenceRefs))
		for _, ref := range candidate.Request.Command.EvidenceRefs {
			refs[ref.Reference] = true
		}
		for _, runID := range primaryRunIDs {
			if !refs[fmt.Sprintf("%s%d", primaryEvidenceReferencePrefix, runID)] {
				t.Fatalf("candidate %s did not mark run %d as its primary evidence: %+v", candidate.Request.Command.ID, runID, refs)
			}
		}
		for _, runID := range portabilityRunIDs {
			if !refs[fmt.Sprintf("%s%d", portabilityEvidenceReferencePrefix, runID)] {
				t.Fatalf("candidate %s did not mark run %d as portability-only evidence: %+v", candidate.Request.Command.ID, runID, refs)
			}
			if refs[fmt.Sprintf("%s%d", primaryEvidenceReferencePrefix, runID)] {
				t.Fatalf("candidate %s consumed another group's primary run %d", candidate.Request.Command.ID, runID)
			}
		}
	}
	assertEvidenceRole(a, []int64{1, 2, 3}, []int64{4, 5, 6})
	assertEvidenceRole(b, []int64{4, 5, 6}, []int64{1, 2, 3})
}
