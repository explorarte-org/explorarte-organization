package contextengine

import (
	"strings"
	"testing"
)

func baseSnapshotForRender() Snapshot {
	return Snapshot{
		ID: 1, OrganizationID: "explorarte", OrganizationRevisionID: 1,
		ActorRoleID: "investigacion/research_worker_hourly", Purpose: "department_worker",
		Status: SnapshotReady, RequestHash: "req-hash-1", TaskRef: "task:1",
		Segments: []Segment{
			{RenderOrdinal: 1, AuthorityTier: TierImmutableSafety, SourceReference: "cell-boundaries.yaml", Included: true, Content: []byte("SAFETY")},
			{RenderOrdinal: 2, AuthorityTier: TierOwnerDecisions, SourceReference: "decisions-required.yaml", Included: true, Content: []byte("OWNER")},
			{RenderOrdinal: 3, AuthorityTier: TierCanonicalPolicies, SourceReference: "role-catalog.yaml", Included: true, Content: []byte("POLICY")},
			{RenderOrdinal: 4, AuthorityTier: TierOrganizationAgent, SourceReference: "AGENT.md", Included: true, Content: []byte("ORGAGENT")},
			{RenderOrdinal: 5, AuthorityTier: TierDepartmentAgent, SourceReference: "investigacion/AGENT.md", Included: true, Content: []byte("DEPTAGENT")},
			{RenderOrdinal: 6, AuthorityTier: TierRoleProfile, SourceReference: "PERFIL.md", Included: true, Content: []byte("PROFILE")},
			{RenderOrdinal: 7, AuthorityTier: TierTask, SourceReference: "task:1", Included: true, Content: []byte("CLUSTER_PAYLOAD_1")},
		},
	}
}

// 1. StablePrefix deterministic
func TestBuildProviderRender_StablePrefixDeterministic(t *testing.T) {
	s := baseSnapshotForRender()
	r1, err := BuildProviderRender(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r2, err := BuildProviderRender(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r1.StablePrefixHash != r2.StablePrefixHash {
		t.Fatalf("expected identical stable_prefix_hash, got %q vs %q", r1.StablePrefixHash, r2.StablePrefixHash)
	}
	if string(r1.StablePrefix) != string(r2.StablePrefix) {
		t.Fatal("expected byte-identical StablePrefix across repeated builds")
	}
}

// 2. DynamicSuffix deterministic
func TestBuildProviderRender_DynamicSuffixDeterministic(t *testing.T) {
	s := baseSnapshotForRender()
	r1, _ := BuildProviderRender(s)
	r2, _ := BuildProviderRender(s)
	if r1.DynamicSuffixHash != r2.DynamicSuffixHash {
		t.Fatalf("expected identical dynamic_suffix_hash, got %q vs %q", r1.DynamicSuffixHash, r2.DynamicSuffixHash)
	}
}

// 3. same profile/task class -> same prefix (same actor/org/purpose, only snapshot ID / task ref differ)
func TestBuildProviderRender_SameProfileSameActor_SamePrefix(t *testing.T) {
	s1 := baseSnapshotForRender()
	s2 := baseSnapshotForRender()
	s2.ID = 999
	s2.TaskRef = "task:999"
	s2.RequestHash = "completely-different-hash"
	r1, _ := BuildProviderRender(s1)
	r2, _ := BuildProviderRender(s2)
	if r1.StablePrefixHash != r2.StablePrefixHash {
		t.Fatalf("snapshot_id/task_ref/request_hash must never affect stable_prefix_hash, got %q vs %q", r1.StablePrefixHash, r2.StablePrefixHash)
	}
}

// 4. different task payload -> same prefix
func TestBuildProviderRender_DifferentTaskPayload_SamePrefix(t *testing.T) {
	s1 := baseSnapshotForRender()
	s2 := baseSnapshotForRender()
	s2.Segments[6].Content = []byte("COMPLETELY_DIFFERENT_CLUSTER_PAYLOAD")
	r1, _ := BuildProviderRender(s1)
	r2, _ := BuildProviderRender(s2)
	if r1.StablePrefixHash != r2.StablePrefixHash {
		t.Fatal("changing only task_context content must never change stable_prefix_hash")
	}
	if r1.DynamicSuffixHash == r2.DynamicSuffixHash {
		t.Fatal("changing task_context content must change dynamic_suffix_hash")
	}
}

// 5. different rubric version -> different prefix (rubric lives inside a
// stable-tier segment's content in this system -- role_profile/canonical
// policies -- so a rubric version bump changes that segment's content)
func TestBuildProviderRender_DifferentRubricContent_DifferentPrefix(t *testing.T) {
	s1 := baseSnapshotForRender()
	s2 := baseSnapshotForRender()
	s2.Segments[2].Content = []byte("POLICY_V2_DIFFERENT_RUBRIC")
	r1, _ := BuildProviderRender(s1)
	r2, _ := BuildProviderRender(s2)
	if r1.StablePrefixHash == r2.StablePrefixHash {
		t.Fatal("a changed stable-tier segment content must change stable_prefix_hash")
	}
}

// 6. different role -> different prefix
func TestBuildProviderRender_DifferentRole_DifferentPrefix(t *testing.T) {
	s1 := baseSnapshotForRender()
	s2 := baseSnapshotForRender()
	s2.ActorRoleID = "investigacion/research_worker_hourly_mimo_canary"
	r1, _ := BuildProviderRender(s1)
	r2, _ := BuildProviderRender(s2)
	if r1.StablePrefixHash == r2.StablePrefixHash {
		t.Fatal("a different actor_role_id must change stable_prefix_hash (it is part of the header)")
	}
}

// 7. different applicable policy -> different prefix
func TestBuildProviderRender_DifferentPolicyContent_DifferentPrefix(t *testing.T) {
	s1 := baseSnapshotForRender()
	s2 := baseSnapshotForRender()
	s2.Segments[0].Content = []byte("SAFETY_V2")
	r1, _ := BuildProviderRender(s1)
	r2, _ := BuildProviderRender(s2)
	if r1.StablePrefixHash == r2.StablePrefixHash {
		t.Fatal("a changed policy segment content must change stable_prefix_hash")
	}
}

// 8. audit timestamp cannot alter StablePrefix (no CreatedAt/InvalidatedAt field used anywhere in the build)
func TestBuildProviderRender_AuditTimestampNeverAffectsPrefix(t *testing.T) {
	s1 := baseSnapshotForRender()
	s2 := baseSnapshotForRender()
	s2.CreatedAt = s1.CreatedAt.AddDate(1, 0, 0)
	r1, _ := BuildProviderRender(s1)
	r2, _ := BuildProviderRender(s2)
	if r1.StablePrefixHash != r2.StablePrefixHash || r1.ProviderRenderHash != r2.ProviderRenderHash {
		t.Fatal("CreatedAt must never influence any render hash")
	}
}

// 9. invocation ID cannot alter StablePrefix -- BuildProviderRender never
// takes an invocation ID at all, only a Snapshot; this test documents that
// invariant by construction (no field exists to smuggle one in via Snapshot).
func TestBuildProviderRender_NoInvocationScopedFieldInSnapshot(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	if strings.Contains(string(r.StablePrefix), "task:1") || strings.Contains(string(r.DynamicSuffix), "task:1") {
		t.Fatal("task_ref/invocation-scoped identifiers must never appear in provider-visible bytes")
	}
}

// 10. serialization order deterministic -- segment order preserved exactly
// as given (never resorted), so two snapshots with segments in a different
// slice order but identical semantic set produce different bytes -- proving
// order is not silently normalized/hidden.
func TestBuildProviderRender_SegmentOrderPreservedNotResorted(t *testing.T) {
	s1 := baseSnapshotForRender()
	s2 := baseSnapshotForRender()
	s2.Segments[0], s2.Segments[1] = s2.Segments[1], s2.Segments[0]
	r1, _ := BuildProviderRender(s1)
	r2, _ := BuildProviderRender(s2)
	if r1.StablePrefixHash == r2.StablePrefixHash {
		t.Fatal("BuildProviderRender must preserve the exact incoming segment order, not resort it")
	}
}

// 11. ProviderRender hash matches exact dispatch render
func TestBuildProviderRender_HashMatchesBytes(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	if r.ProviderRenderHash != DigestCanonicalBytes(r.Bytes()) {
		t.Fatal("provider_render_hash must always equal the digest of Bytes()")
	}
}

// 12. tampered StablePrefix rejected/detected (hash mismatch is observable)
func TestBuildProviderRender_TamperedStablePrefixDetected(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	tampered := append([]byte(nil), r.StablePrefix...)
	tampered[0] ^= 0xFF
	if DigestCanonicalBytes(tampered) == r.StablePrefixHash {
		t.Fatal("tampering a single byte of StablePrefix must change its hash")
	}
}

// 13. tampered DynamicSuffix rejected/detected
func TestBuildProviderRender_TamperedDynamicSuffixDetected(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	tampered := append([]byte(nil), r.DynamicSuffix...)
	tampered[0] ^= 0xFF
	if DigestCanonicalBytes(tampered) == r.DynamicSuffixHash {
		t.Fatal("tampering a single byte of DynamicSuffix must change its hash")
	}
}

// 14. canonical lineage preserved -- Snapshot.ID/RequestHash/RenderedHash
// fields are untouched by BuildProviderRender (it only reads Segments plus
// the three header fields; it never mutates the Snapshot).
func TestBuildProviderRender_CanonicalSnapshotUnmodified(t *testing.T) {
	s := baseSnapshotForRender()
	original := s
	_, _ = BuildProviderRender(s)
	if s.ID != original.ID || s.RequestHash != original.RequestHash || len(s.Segments) != len(original.Segments) {
		t.Fatal("BuildProviderRender must never mutate the input Snapshot")
	}
}

// 15. R10 context_render_hash_mismatch regression remains protected --
// documented via the single-source-of-truth property: calling
// BuildProviderRender twice on the identical snapshot always yields the
// identical ProviderRenderHash, which is the property dispatch_service.go
// relies on when comparing the pre-computed hash against the render used
// for the real dispatch.
func TestBuildProviderRender_RepeatedBuildSameHash_RegressionGuard(t *testing.T) {
	s := baseSnapshotForRender()
	r1, _ := BuildProviderRender(s)
	r2, _ := BuildProviderRender(s)
	if r1.ProviderRenderHash != r2.ProviderRenderHash {
		t.Fatal("BuildProviderRender must be a pure function of its input snapshot -- non-determinism here would reopen the R10 context_render_hash_mismatch bug")
	}
}

// 16. safety preserved -- immutable_safety content always present in StablePrefix
func TestBuildProviderRender_SafetyContentPreserved(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	if !strings.Contains(string(r.StablePrefix), "SAFETY") {
		t.Fatal("immutable_safety segment content must appear in StablePrefix")
	}
}

// 17. owner decisions preserved
func TestBuildProviderRender_OwnerDecisionsPreserved(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	if !strings.Contains(string(r.StablePrefix), "OWNER") {
		t.Fatal("owner_decisions segment content must appear in StablePrefix")
	}
}

// 18. applicable policies preserved
func TestBuildProviderRender_ApplicablePoliciesPreserved(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	if !strings.Contains(string(r.StablePrefix), "POLICY") {
		t.Fatal("canonical_registry_and_policies segment content must appear in StablePrefix")
	}
}

// 19. role authority preserved (role_profile + department/organization agent)
func TestBuildProviderRender_RoleAuthorityPreserved(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	for _, want := range []string{"ORGAGENT", "DEPTAGENT", "PROFILE"} {
		if !strings.Contains(string(r.StablePrefix), want) {
			t.Fatalf("expected %q in StablePrefix", want)
		}
	}
}

// 20. task_context preserved (in DynamicSuffix, not dropped, not moved to prefix)
func TestBuildProviderRender_TaskContextPreservedInSuffix(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	if !strings.Contains(string(r.DynamicSuffix), "CLUSTER_PAYLOAD_1") {
		t.Fatal("task_context content must appear in DynamicSuffix")
	}
	if strings.Contains(string(r.StablePrefix), "CLUSTER_PAYLOAD_1") {
		t.Fatal("task_context content must never leak into StablePrefix")
	}
}

// 21. excluded (non-included) segments never contribute content
func TestBuildProviderRender_OmittedSegmentsExcluded(t *testing.T) {
	s := baseSnapshotForRender()
	s.Segments = append(s.Segments, Segment{RenderOrdinal: 8, AuthorityTier: TierRAGEvidence, Included: false, OmissionReason: "context_budget_omission", Content: []byte("SHOULD_NOT_APPEAR")})
	r, _ := BuildProviderRender(s)
	if strings.Contains(string(r.Bytes()), "SHOULD_NOT_APPEAR") {
		t.Fatal("an omitted (Included=false) segment must never contribute content to the render")
	}
}

// 22. invalidated snapshot rejected
func TestBuildProviderRender_InvalidatedSnapshotRejected(t *testing.T) {
	s := baseSnapshotForRender()
	s.Status = SnapshotInvalidated
	_, err := BuildProviderRender(s)
	if err != ErrSnapshotInvalidated {
		t.Fatalf("expected ErrSnapshotInvalidated, got %v", err)
	}
}

// 23. AuditEnvelope fields never appear in provider-visible bytes
func TestBuildProviderRender_AuditEnvelopeFieldsNeverInBytes(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	full := string(r.Bytes())
	for _, forbidden := range []string{s.RequestHash, "req-hash-1"} {
		if strings.Contains(full, forbidden) {
			t.Fatalf("request_hash must never appear in provider-visible bytes, found %q", forbidden)
		}
	}
}

// 24. header (org/actor/purpose) is present and stable
func TestBuildProviderRender_HeaderPresent(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	for _, want := range []string{"explorarte", "investigacion/research_worker_hourly", "department_worker"} {
		if !strings.Contains(string(r.StablePrefix), want) {
			t.Fatalf("expected header field %q in StablePrefix", want)
		}
	}
}

// 25. dynamic tiers beyond task_context are also routed to DynamicSuffix
// (provider-agnostic, tier-driven rule, not task-class-specific)
func TestBuildProviderRender_OtherDynamicTiersRouteToSuffix(t *testing.T) {
	s := baseSnapshotForRender()
	s.Segments = append(s.Segments, Segment{RenderOrdinal: 8, AuthorityTier: TierRAGEvidence, Included: true, Content: []byte("RAG_EVIDENCE_CONTENT")})
	r, _ := BuildProviderRender(s)
	if !strings.Contains(string(r.DynamicSuffix), "RAG_EVIDENCE_CONTENT") {
		t.Fatal("rag_evidence tier content must route to DynamicSuffix")
	}
	if strings.Contains(string(r.StablePrefix), "RAG_EVIDENCE_CONTENT") {
		t.Fatal("rag_evidence tier content must never leak into StablePrefix")
	}
}

// 26. version constant is stable/explicit
func TestBuildProviderRender_VersionSet(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	if r.Version != ProviderRenderVersion {
		t.Fatalf("expected version %q, got %q", ProviderRenderVersion, r.Version)
	}
}

// 27. ProviderVisibleBytes matches len(Bytes())
func TestBuildProviderRender_ProviderVisibleBytesMatchesBytesLength(t *testing.T) {
	s := baseSnapshotForRender()
	r, _ := BuildProviderRender(s)
	if r.ProviderVisibleBytes != len(r.Bytes()) {
		t.Fatalf("expected provider_visible_bytes=%d, got %d", len(r.Bytes()), r.ProviderVisibleBytes)
	}
}

// 28. unknown/unready status rejected (mirrors legacy Render's own check)
func TestBuildProviderRender_UnknownStatusRejected(t *testing.T) {
	s := baseSnapshotForRender()
	s.Status = "some_unknown_status"
	if _, err := BuildProviderRender(s); err == nil {
		t.Fatal("expected an error for an unrecognized snapshot status")
	}
}
