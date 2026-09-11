package contextcompiler

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// EXECUTIVE_CONTEXT_PROFILE_SCOPING_FIX_V1.
//
// executiveRoleCatalogYAML mirrors the exact structured fields real
// docs/canonical/role-catalog.yaml entries carry (id/department/
// canonical_leader -- see role-catalog.yaml's own "ingenieria_ia/qa" and
// "ingenieria_ia/orquestador" entries) across two synthetic departments,
// matching the round's own TEST 3/4/5 fixture:
//
//	unit_a: leader_a (canonical_leader), worker_a1, worker_a2
//	unit_b: leader_b (canonical_leader), worker_b1
func executiveRoleCatalogYAML() []byte {
	return []byte(`schema_version: 0.1.0
document_status: branch_0_candidate
roles:
- id: empresa/ceo
  department: empresa
  canonical_leader: false
- id: leader_a
  department: unit_a
  canonical_leader: true
- id: worker_a1
  department: unit_a
  canonical_leader: false
- id: worker_a2
  department: unit_a
  canonical_leader: false
- id: leader_b
  department: unit_b
  canonical_leader: true
- id: worker_b1
  department: unit_b
  canonical_leader: false
`)
}

// executiveSnapshot builds a synthetic canonical snapshot carrying one
// segment per source every executive profile in this round reasons
// about, so every test below exercises the SAME shape a real snapshot
// would (see internal/contextengine/canonical/provider.go's allowlist),
// scoped down to what these tests actually need to assert.
func executiveSnapshot(executionPurpose, actorRoleID string, roleCatalogContent []byte) contextengine.Snapshot {
	segments := []contextengine.Segment{
		{AuthorityTier: contextengine.TierImmutableSafety, SourceReference: "docs/canonical/cell-boundaries.yaml", Included: true, Content: []byte("safety"), ByteCount: 6, ContentHash: "h-safety"},
		{AuthorityTier: contextengine.TierOwnerDecisions, SourceReference: decisionsRequiredSourceReference, Included: true, Content: []byte("owner decisions"), ByteCount: 15, ContentHash: "h-owner"},
		{AuthorityTier: contextengine.TierCanonicalPolicies, SourceReference: "docs/canonical/instruction-precedence.yaml", Included: true, Content: []byte("precedence"), ByteCount: 10, ContentHash: "h-prec"},
		{AuthorityTier: contextengine.TierCanonicalPolicies, SourceReference: "docs/canonical/organization.yaml", Included: true, Content: []byte("org"), ByteCount: 3, ContentHash: "h-org"},
		{AuthorityTier: contextengine.TierCanonicalPolicies, SourceReference: "docs/canonical/capability-matrix.yaml", Included: true, Content: []byte("capabilities"), ByteCount: 12, ContentHash: "h-cap", MayGrantCapabilities: true, AuthorityPriority: 1},
		{AuthorityTier: contextengine.TierCanonicalPolicies, SourceReference: leaderWorkerMapSourceReference, Included: true, Content: []byte("leader-worker map"), ByteCount: 17, ContentHash: "h-lwm"},
		{AuthorityTier: contextengine.TierCanonicalPolicies, SourceReference: modelRoutingSourceReference, Included: true, Content: []byte("routing"), ByteCount: 7, ContentHash: "h-routing"},
		{AuthorityTier: contextengine.TierCanonicalPolicies, SourceReference: RoleCatalogSourceReference, Included: true, Content: roleCatalogContent, ByteCount: len(roleCatalogContent), ContentHash: "h-roles"},
		{AuthorityTier: contextengine.TierOrganizationAgent, SourceReference: "AGENT.md", Included: true, Content: []byte("org agent"), ByteCount: 9, ContentHash: "h-orgagent"},
		{AuthorityTier: contextengine.TierDepartmentAgent, SourceReference: "unit_a/AGENT.md", Included: true, Content: []byte("dept agent"), ByteCount: 10, ContentHash: "h-deptagent"},
		{AuthorityTier: contextengine.TierRoleProfile, SourceReference: actorRoleID + "/PERFIL.md", Included: true, Content: []byte("perfil"), ByteCount: 6, ContentHash: "h-perfil"},
		{AuthorityTier: contextengine.TierTask, SourceReference: "task:1", Included: true, Content: []byte("task payload"), ByteCount: 12, ContentHash: "h-task"},
	}
	for i := range segments {
		segments[i].Ordinal = i + 1
		segments[i].RenderOrdinal = i + 1
	}
	return contextengine.Snapshot{ID: 1, ActorRoleID: actorRoleID, ExecutionPurpose: executionPurpose, Segments: segments}
}

func findSegment(segments []contextengine.Segment, sourceReference string) (contextengine.Segment, bool) {
	for _, seg := range segments {
		if seg.SourceReference == sourceReference {
			return seg, true
		}
	}
	return contextengine.Segment{}, false
}

func mustBeIncluded(t *testing.T, segments []contextengine.Segment, sourceReference string) contextengine.Segment {
	t.Helper()
	seg, ok := findSegment(segments, sourceReference)
	if !ok || !seg.Included {
		t.Fatalf("expected %s to be included, found=%v included=%v", sourceReference, ok, seg.Included)
	}
	return seg
}

func mustBeExcluded(t *testing.T, segments []contextengine.Segment, sourceReference string) {
	t.Helper()
	seg, ok := findSegment(segments, sourceReference)
	if !ok {
		t.Fatalf("expected segment %s to still be present (Included=false), but it is missing entirely", sourceReference)
	}
	if seg.Included {
		t.Fatalf("expected %s to be excluded (Included=false), got Included=true", sourceReference)
	}
	if len(seg.Content) != 0 {
		t.Fatalf("expected %s content cleared once excluded, got %d bytes", sourceReference, len(seg.Content))
	}
}

// TEST 1 -- CEO PLAN SELECTION.
func TestExecutiveCEOPlan_SelectionAndSources(t *testing.T) {
	snap := executiveSnapshot(ExecutiveCEOPlanV1ExecutionPurpose, "empresa/ceo", executiveRoleCatalogYAML())
	result, err := CompileForTaskClass(snap)
	if err != nil {
		t.Fatalf("CompileForTaskClass: %v", err)
	}
	if result.FellBackToCanonical {
		t.Fatal("executive_ceo_plan must resolve an explicit profile, not fall back to canonical")
	}
	if result.SelectionKind != SelectionExecutionPurpose {
		t.Fatalf("SelectionKind=%q, want %q", result.SelectionKind, SelectionExecutionPurpose)
	}
	for _, ref := range []string{
		"docs/canonical/cell-boundaries.yaml", "docs/canonical/instruction-precedence.yaml",
		"docs/canonical/organization.yaml", "docs/canonical/capability-matrix.yaml",
		leaderWorkerMapSourceReference, decisionsRequiredSourceReference,
	} {
		mustBeIncluded(t, result.Projected.Segments, ref)
	}
	roleSeg := mustBeIncluded(t, result.Projected.Segments, RoleCatalogSourceReference)
	if roleSeg.ByteCount >= len(executiveRoleCatalogYAML()) {
		t.Fatalf("expected role-catalog.yaml to be projected (shrunk), got %d bytes (original %d)", roleSeg.ByteCount, len(executiveRoleCatalogYAML()))
	}
	content := string(roleSeg.Content)
	for _, want := range []string{"empresa/ceo", "leader_a", "leader_b"} {
		if !containsString(content, want) {
			t.Fatalf("CEO-plan role-catalog projection must contain %q: %s", want, content)
		}
	}
	for _, unwanted := range []string{"worker_a1", "worker_a2", "worker_b1"} {
		if containsString(content, unwanted) {
			t.Fatalf("CEO-plan role-catalog projection must NOT contain individual worker %q: %s", unwanted, content)
		}
	}
}

// TEST 2 -- CEO CLOSURE.
func TestExecutiveCEOClosure_SelectionAndSources(t *testing.T) {
	snap := executiveSnapshot(ExecutiveCEOClosureV1ExecutionPurpose, "empresa/ceo", executiveRoleCatalogYAML())
	result, err := CompileForTaskClass(snap)
	if err != nil {
		t.Fatalf("CompileForTaskClass: %v", err)
	}
	if result.FellBackToCanonical {
		t.Fatal("executive_ceo_closure must resolve an explicit profile, not fall back to canonical")
	}
	if result.SelectionKind != SelectionExecutionPurpose {
		t.Fatalf("SelectionKind=%q, want %q", result.SelectionKind, SelectionExecutionPurpose)
	}
	mustBeExcluded(t, result.Projected.Segments, modelRoutingSourceReference)
	mustBeIncluded(t, result.Projected.Segments, decisionsRequiredSourceReference)
	roleSeg := mustBeIncluded(t, result.Projected.Segments, RoleCatalogSourceReference)
	if roleSeg.ByteCount >= len(executiveRoleCatalogYAML()) {
		t.Fatal("CEO-closure role-catalog.yaml must be projected down from the full catalog")
	}
	if containsString(string(roleSeg.Content), "worker_a1") || containsString(string(roleSeg.Content), "leader_a") {
		t.Fatalf("CEO-closure role-catalog projection is self-only, must not contain other roles: %s", roleSeg.Content)
	}
	if !containsString(string(roleSeg.Content), "empresa/ceo") {
		t.Fatal("CEO-closure role-catalog projection must still contain the CEO's own entry")
	}
}

// TEST 3 -- DEPARTMENT PLAN ROLE SCOPE.
func TestExecutiveDepartmentPlan_RoleScope(t *testing.T) {
	snap := executiveSnapshot(ExecutiveDepartmentPlanV1ExecutionPurpose, "leader_a", executiveRoleCatalogYAML())
	result, err := CompileForTaskClass(snap)
	if err != nil {
		t.Fatalf("CompileForTaskClass: %v", err)
	}
	if result.FellBackToCanonical {
		t.Fatal("department-plan must resolve an explicit profile")
	}
	roleSeg := mustBeIncluded(t, result.Projected.Segments, RoleCatalogSourceReference)
	content := string(roleSeg.Content)
	for _, want := range []string{"leader_a", "worker_a1", "worker_a2"} {
		if !containsString(content, want) {
			t.Fatalf("department-plan (unit_a) must contain %q: %s", want, content)
		}
	}
	for _, unwanted := range []string{"leader_b", "worker_b1"} {
		if containsString(content, unwanted) {
			t.Fatalf("department-plan (unit_a) must NOT contain unit_b's %q: %s", unwanted, content)
		}
	}
}

// TEST 4 -- WORKER ROLE SCOPE.
func TestExecutiveDepartmentWorker_RoleScope(t *testing.T) {
	snap := executiveSnapshot(ExecutiveDepartmentWorkerV1ExecutionPurpose, "worker_a1", executiveRoleCatalogYAML())
	result, err := CompileForTaskClass(snap)
	if err != nil {
		t.Fatalf("CompileForTaskClass: %v", err)
	}
	if result.FellBackToCanonical {
		t.Fatal("department-worker must resolve an explicit profile")
	}
	roleSeg := mustBeIncluded(t, result.Projected.Segments, RoleCatalogSourceReference)
	content := string(roleSeg.Content)
	if !containsString(content, "worker_a1") {
		t.Fatalf("worker projection must contain self: %s", content)
	}
	if !containsString(content, "leader_a") {
		t.Fatalf("worker projection must contain its own department's leader: %s", content)
	}
	for _, unwanted := range []string{"worker_a2", "leader_b", "worker_b1"} {
		if containsString(content, unwanted) {
			t.Fatalf("worker projection must NOT contain unrelated role %q: %s", unwanted, content)
		}
	}
}

// TEST 5 -- REVIEW ROLE SCOPE.
func TestExecutiveDepartmentReview_RoleScope(t *testing.T) {
	snap := executiveSnapshot(ExecutiveDepartmentReviewV1ExecutionPurpose, "leader_a", executiveRoleCatalogYAML())
	result, err := CompileForTaskClass(snap)
	if err != nil {
		t.Fatalf("CompileForTaskClass: %v", err)
	}
	if result.FellBackToCanonical {
		t.Fatal("department-review must resolve an explicit profile")
	}
	roleSeg := mustBeIncluded(t, result.Projected.Segments, RoleCatalogSourceReference)
	content := string(roleSeg.Content)
	for _, want := range []string{"leader_a", "worker_a1", "worker_a2"} {
		if !containsString(content, want) {
			t.Fatalf("review (unit_a) must contain %q: %s", want, content)
		}
	}
	// Negative test for another unit, explicit per the round's spec.
	for _, unwanted := range []string{"leader_b", "worker_b1"} {
		if containsString(content, unwanted) {
			t.Fatalf("review (unit_a) must NOT contain unit_b's %q: %s", unwanted, content)
		}
	}
}

// TEST 6 -- OWNER DECISIONS: present for ceo-plan/ceo-closure, absent for
// department-plan/department-worker/department-review. No hardcoded
// decision IDs anywhere -- this asserts source presence/absence only.
func TestExecutiveProfiles_OwnerDecisionsScope(t *testing.T) {
	for _, purpose := range []string{ExecutiveCEOPlanV1ExecutionPurpose, ExecutiveCEOClosureV1ExecutionPurpose} {
		snap := executiveSnapshot(purpose, "empresa/ceo", executiveRoleCatalogYAML())
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", purpose, err)
		}
		mustBeIncluded(t, result.Projected.Segments, decisionsRequiredSourceReference)
	}
	for _, purpose := range []string{ExecutiveDepartmentPlanV1ExecutionPurpose, ExecutiveDepartmentWorkerV1ExecutionPurpose, ExecutiveDepartmentReviewV1ExecutionPurpose} {
		actor := "leader_a"
		if purpose == ExecutiveDepartmentWorkerV1ExecutionPurpose {
			actor = "worker_a1"
		}
		snap := executiveSnapshot(purpose, actor, executiveRoleCatalogYAML())
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", purpose, err)
		}
		mustBeExcluded(t, result.Projected.Segments, decisionsRequiredSourceReference)
	}
}

// TEST 7 -- APPLICABLE OWNER DECISION PRESERVED: CEO profiles keep the
// full decisions-required.yaml content untouched -- no applicability
// filtering happens inside contextcompiler. decisionApplicabilityPolicy
// (internal/executive) remains the only semantic filter.
func TestExecutiveCEOProfiles_OwnerDecisionContentUntouched(t *testing.T) {
	synthetic := []byte("open:\n- id: SYNTH-1\n  question: a synthetic organization-global decision\n")
	for _, purpose := range []string{ExecutiveCEOPlanV1ExecutionPurpose, ExecutiveCEOClosureV1ExecutionPurpose} {
		snap := executiveSnapshot(purpose, "empresa/ceo", executiveRoleCatalogYAML())
		for i := range snap.Segments {
			if snap.Segments[i].SourceReference == decisionsRequiredSourceReference {
				snap.Segments[i].Content = synthetic
				snap.Segments[i].ByteCount = len(synthetic)
			}
		}
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", purpose, err)
		}
		seg := mustBeIncluded(t, result.Projected.Segments, decisionsRequiredSourceReference)
		if string(seg.Content) != string(synthetic) {
			t.Fatalf("%s must receive decisions-required.yaml byte-for-byte, unfiltered; got %q want %q", purpose, seg.Content, synthetic)
		}
	}
}

// TEST 8 -- INSTRUCTION PRECEDENCE PRESERVED in all 8 known profiles.
func TestExecutiveProfiles_InstructionPrecedencePresent(t *testing.T) {
	for _, tc := range allExecutiveProfileCases() {
		snap := executiveSnapshot(tc.purpose, tc.actor, executiveRoleCatalogYAML())
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", tc.purpose, err)
		}
		mustBeIncluded(t, result.Projected.Segments, "docs/canonical/instruction-precedence.yaml")
	}
}

// TEST 9 -- IMMUTABLE SAFETY PRESERVED in all 8 known profiles.
func TestExecutiveProfiles_ImmutableSafetyPresent(t *testing.T) {
	for _, tc := range allExecutiveProfileCases() {
		snap := executiveSnapshot(tc.purpose, tc.actor, executiveRoleCatalogYAML())
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", tc.purpose, err)
		}
		seg := mustBeIncluded(t, result.Projected.Segments, "docs/canonical/cell-boundaries.yaml")
		if seg.AuthorityTier != contextengine.TierImmutableSafety {
			t.Fatalf("%s: cell-boundaries.yaml tier=%q, want %q", tc.purpose, seg.AuthorityTier, contextengine.TierImmutableSafety)
		}
	}
}

// TEST 10 -- CAPABILITY AUTHORITY UNCHANGED. Wherever capability-matrix.yaml
// is included, its metadata (Tier/Class/Trust/DataClass/MayGrantCapabilities)
// must be byte-identical to the canonical original, and it is never
// projected/shrunk by any V1 profile.
func TestExecutiveProfiles_CapabilityMatrixMetadataUnchanged(t *testing.T) {
	for _, tc := range allExecutiveProfileCases() {
		snap := executiveSnapshot(tc.purpose, tc.actor, executiveRoleCatalogYAML())
		original, ok := findSegment(snap.Segments, "docs/canonical/capability-matrix.yaml")
		if !ok {
			t.Fatal("fixture must carry capability-matrix.yaml")
		}
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", tc.purpose, err)
		}
		seg, ok := findSegment(result.Projected.Segments, "docs/canonical/capability-matrix.yaml")
		if !ok || !seg.Included {
			t.Fatalf("%s: capability-matrix.yaml must remain included in V1 (no profile excludes it)", tc.purpose)
		}
		if seg.AuthorityTier != original.AuthorityTier || seg.InstructionClass != original.InstructionClass ||
			seg.TrustClass != original.TrustClass || seg.DataClass != original.DataClass ||
			seg.MayGrantCapabilities != original.MayGrantCapabilities || seg.AuthorityPriority != original.AuthorityPriority {
			t.Fatalf("%s: capability-matrix.yaml metadata changed: got %+v want %+v", tc.purpose, seg, original)
		}
		if len(seg.Content) != len(original.Content) {
			t.Fatalf("%s: capability-matrix.yaml content size changed (%d != %d) -- V1 must never project it", tc.purpose, len(seg.Content), len(original.Content))
		}
	}
}

// TEST 11 -- UNKNOWN PURPOSE falls back to full canonical context, never a
// partial view.
func TestExecutiveProfiles_UnknownPurposeFallsBackToCanonical(t *testing.T) {
	snap := executiveSnapshot("synthetic-unregistered-purpose", "empresa/ceo", executiveRoleCatalogYAML())
	result, err := CompileForTaskClass(snap)
	if err != nil {
		t.Fatalf("CompileForTaskClass: %v", err)
	}
	if !result.FellBackToCanonical {
		t.Fatal("an unregistered ExecutionPurpose must fall back to canonical, never a partial view")
	}
	if result.SelectionKind != SelectionCanonical {
		t.Fatalf("SelectionKind=%q, want %q", result.SelectionKind, SelectionCanonical)
	}
	if len(result.Projected.Segments) != len(snap.Segments) {
		t.Fatalf("canonical fallback must carry every segment unchanged, got %d want %d", len(result.Projected.Segments), len(snap.Segments))
	}
	for _, seg := range result.Projected.Segments {
		original, ok := findSegment(snap.Segments, seg.SourceReference)
		if !ok || string(seg.Content) != string(original.Content) {
			t.Fatalf("canonical fallback must not alter %s", seg.SourceReference)
		}
	}
}

// TEST 12 -- ALL 8 KNOWN PURPOSES resolve to an explicit profile, never
// canonical fallback. Reads the purpose list from the same exported
// ExecutionPurpose constants the profiles themselves are registered
// under (see allExecutiveProfileCases), so there is exactly one place
// that enumerates "the known executive purposes" for this test.
func TestExecutiveProfiles_AllKnownPurposesUseExplicitProfile(t *testing.T) {
	for _, tc := range allExecutiveProfileCases() {
		snap := executiveSnapshot(tc.purpose, tc.actor, executiveRoleCatalogYAML())
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", tc.purpose, err)
		}
		if result.FellBackToCanonical {
			t.Errorf("known purpose %q unexpectedly fell back to canonical", tc.purpose)
		}
		if result.SelectionKind != SelectionExecutionPurpose {
			t.Errorf("known purpose %q SelectionKind=%q, want %q", tc.purpose, result.SelectionKind, SelectionExecutionPurpose)
		}
	}
}

// TEST 13 -- SELECTOR PRECEDENCE: EXACT beats TASK-CLASS beats
// EXECUTION-PURPOSE beats CANONICAL, pinned with a local registry (the
// package-level defaultSelectorRegistry has no EXACT entries to exercise
// that tier against).
func TestSelectorRegistry_PrecedenceOrder(t *testing.T) {
	taskClassOnly := ContextProfile{ID: "test.task-class", Version: "v1", TaskClass: "test.class"}
	executionPurposeOnly := ContextProfile{ID: "test.purpose", Version: "v1", ExecutionPurpose: "test-purpose"}
	exactProfile := ContextProfile{ID: "test.exact", Version: "v1"}

	selector := SemanticSelector{TaskClass: "test.class", ExecutionPurpose: "test-purpose", ActorRoleID: "role/x", ActorUnitID: "unit/x"}

	registry, err := BuildSelectorRegistry(
		[]ProfileEntry{{Profile: taskClassOnly}},
		[]ProfileEntry{{Profile: executionPurposeOnly}},
		[]ExactRegistration{{Selector: selector, Entry: ProfileEntry{Profile: exactProfile}}},
	)
	if err != nil {
		t.Fatalf("BuildSelectorRegistry: %v", err)
	}

	// EXACT wins when the full four-axis tuple matches.
	got := registry.Select(selector)
	if !got.Matched || got.Kind != SelectionExact || got.Profile.ID != "test.exact" {
		t.Fatalf("EXACT precedence: got %+v", got)
	}

	// TASK-CLASS wins over EXECUTION-PURPOSE when both would match but
	// the selector no longer hits the EXACT tuple (different actor).
	tcSelector := selector
	tcSelector.ActorRoleID = "role/other"
	got = registry.Select(tcSelector)
	if !got.Matched || got.Kind != SelectionTaskClass || got.Profile.ID != "test.task-class" {
		t.Fatalf("TASK-CLASS precedence over EXECUTION-PURPOSE: got %+v", got)
	}

	// EXECUTION-PURPOSE wins when TaskClass does not match anything.
	epSelector := tcSelector
	epSelector.TaskClass = "unregistered.class"
	got = registry.Select(epSelector)
	if !got.Matched || got.Kind != SelectionExecutionPurpose || got.Profile.ID != "test.purpose" {
		t.Fatalf("EXECUTION-PURPOSE fallback: got %+v", got)
	}

	// CANONICAL when nothing matches.
	got = registry.Select(SemanticSelector{TaskClass: "unregistered.class", ExecutionPurpose: "unregistered-purpose"})
	if got.Matched || got.Kind != SelectionCanonical {
		t.Fatalf("CANONICAL fallback: got %+v", got)
	}
}

// TEST 14 -- PROVENANCE: projection/exclusion never changes a segment's
// SourceReference/Tier/Class/Trust/DataClass/MayGrant, whether included,
// projected, or excluded.
func TestExecutiveProfiles_ProvenancePreserved(t *testing.T) {
	for _, tc := range allExecutiveProfileCases() {
		snap := executiveSnapshot(tc.purpose, tc.actor, executiveRoleCatalogYAML())
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", tc.purpose, err)
		}
		for _, original := range snap.Segments {
			seg, ok := findSegment(result.Projected.Segments, original.SourceReference)
			if !ok {
				t.Fatalf("%s: segment %s disappeared entirely -- exclusion must set Included=false, never remove the segment", tc.purpose, original.SourceReference)
			}
			if seg.SourceReference != original.SourceReference || seg.AuthorityTier != original.AuthorityTier ||
				seg.InstructionClass != original.InstructionClass || seg.TrustClass != original.TrustClass ||
				seg.DataClass != original.DataClass || seg.MayGrantCapabilities != original.MayGrantCapabilities {
				t.Fatalf("%s: provenance changed for %s: got %+v want tier/class/trust/data/maygrant of %+v", tc.purpose, original.SourceReference, seg, original)
			}
		}
	}
}

// TEST 15 -- SIZE: every optimized profile's compiled StablePrefixBytes
// must be strictly smaller than canonical's, with a generous, documented
// upper bound (not a brittle exact byte count).
func TestExecutiveProfiles_ProfiledSmallerThanCanonical(t *testing.T) {
	for _, tc := range allExecutiveProfileCases() {
		snap := executiveSnapshot(tc.purpose, tc.actor, executiveRoleCatalogYAML())
		canonicalBytes := 0
		for _, seg := range snap.Segments {
			if seg.Included {
				canonicalBytes += seg.ByteCount
			}
		}
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", tc.purpose, err)
		}
		if result.StablePrefixBytes+result.DynamicSuffixBytes > canonicalBytes {
			t.Fatalf("%s: profiled total (%d) must never exceed canonical total (%d)", tc.purpose, result.StablePrefixBytes+result.DynamicSuffixBytes, canonicalBytes)
		}
		if tc.expectSmaller && result.StablePrefixBytes+result.DynamicSuffixBytes >= canonicalBytes {
			t.Fatalf("%s: expected a real reduction (role-catalog projection and/or source exclusion), got %d >= canonical %d", tc.purpose, result.StablePrefixBytes+result.DynamicSuffixBytes, canonicalBytes)
		}
	}
}

// TEST 16 -- RESEARCH PROFILE remains semantically/byte equivalent:
// re-runs the pre-existing positive-path assertion this package already
// had (TestCompile_RoleCatalogProjectedToSelfEntry) to pin that this
// round did not touch research.corpus_curate/v1's behavior.
func TestExecutiveProfiles_ResearchProfileUnaffected(t *testing.T) {
	profile := ResearchCorpusCurateV1()
	snap := testSnapshot("investigacion/research_worker_hourly", roleCatalogYAML())
	result, err := Compile(profile, snap)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if result.FellBackToCanonical {
		t.Fatal("research.corpus_curate/v1 must still resolve a real projection")
	}
	seg := mustBeIncluded(t, result.Projected.Segments, RoleCatalogSourceReference)
	if !containsString(string(seg.Content), "research_worker_hourly") {
		t.Fatal("research profile must still project to the actor's own entry")
	}
	if containsString(string(seg.Content), "empresa/ceo") || containsString(string(seg.Content), "otro/rol") {
		t.Fatal("research profile must still exclude other roles")
	}
}

// TEST 17 -- FAIL CLOSED: removing a required tier from the canonical
// snapshot must still force Compile to fall back to canonical, never
// silently degrade to a smaller view for a profile that no longer has
// what it declared mandatory.
func TestExecutiveProfiles_FailClosedOnMissingRequiredTier(t *testing.T) {
	for _, tc := range allExecutiveProfileCases() {
		snap := executiveSnapshot(tc.purpose, tc.actor, executiveRoleCatalogYAML())
		// Drop the immutable-safety segment entirely -- required by every
		// executive profile.
		kept := snap.Segments[:0]
		for _, seg := range snap.Segments {
			if seg.AuthorityTier != contextengine.TierImmutableSafety {
				kept = append(kept, seg)
			}
		}
		snap.Segments = kept
		result, err := CompileForTaskClass(snap)
		if err != nil {
			t.Fatalf("CompileForTaskClass(%s): %v", tc.purpose, err)
		}
		if !result.FellBackToCanonical {
			t.Fatalf("%s: missing required tier must fail closed to canonical, not degrade silently", tc.purpose)
		}
	}
}

// executiveProfileCase names one known executive purpose plus the actor
// to drive it with (an actor whose role-catalog.yaml entry exists in
// executiveRoleCatalogYAML and, for department purposes, belongs to
// unit_a). expectSmaller marks whether THIS purpose's V1 profile is
// expected to produce a real reduction (all five optimized purposes) or
// is deliberately left materially equivalent to canonical (the three
// explicit-full-context purposes).
type executiveProfileCase struct {
	purpose       string
	actor         string
	expectSmaller bool
}

func allExecutiveProfileCases() []executiveProfileCase {
	return []executiveProfileCase{
		{ExecutiveCEOPlanV1ExecutionPurpose, "empresa/ceo", true},
		{ExecutiveCEOClosureV1ExecutionPurpose, "empresa/ceo", true},
		{ExecutiveDepartmentPlanV1ExecutionPurpose, "leader_a", true},
		{ExecutiveDepartmentWorkerV1ExecutionPurpose, "worker_a1", true},
		{ExecutiveDepartmentReviewV1ExecutionPurpose, "leader_a", true},
		{ExecutiveAdversarialReviewV1ExecutionPurpose, "empresa/ceo", false},
		{ExecutiveDesignAdjudicationV1ExecutionPurpose, "empresa/ceo", false},
		{ExecutiveImplementationPlanV1ExecutionPurpose, "leader_a", false},
	}
}
