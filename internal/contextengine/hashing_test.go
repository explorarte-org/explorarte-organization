package contextengine

import "testing"

func TestDigestBuildRequestIsDeterministicAndSortsRequestedSkills(t *testing.T) {
	base := CanonicalBuildRequest{Request: BuildRequest{OrganizationID: "explorarte", OrganizationRevisionID: 2, ActorRoleID: "ingenieria_ia/qa", Purpose: "verify", RequestedSkillIDs: []string{"z-skill", "a-skill"}}, PrecedenceHash: DigestMarkdown([]byte("p")), CanonicalBundleHash: DigestMarkdown([]byte("c")), MaxTotalBytes: 100, MaxSegmentBytes: 50, MaxSegments: 10, MaxSkills: 2}
	base.Sources = []SourceRecord{testSource(TierRoleProfile, SourceRoleProfile, "profile", "body", InstructionRole, TrustAuthoritative, DataOrganizational, false)}
	first := DigestBuildRequest(base)
	base.Request.RequestedSkillIDs = []string{"a-skill", "z-skill"}
	if second := DigestBuildRequest(base); first != second {
		t.Fatalf("skill ordering changed hash %s != %s", first, second)
	}
	base.Request.OrganizationRevisionID++
	if third := DigestBuildRequest(base); third == first {
		t.Fatal("revision change did not change request hash")
	}
	base.Request.OrganizationRevisionID--
	base.Sources[0].ContentHash = DigestMarkdown([]byte("changed"))
	if third := DigestBuildRequest(base); third == first {
		t.Fatal("source hash change did not change request hash")
	}
}

// TestDigestBuildRequest_LegacyCompatibility is M1.3 section 9's required
// upgrade/retry compatibility proof: a request whose
// TaskClass/ExecutionPurpose/ActorUnitID are all empty (the pre-M1.3
// shape) must hash, under the frozen legacy computation, to the exact
// same digest DigestBuildRequest itself would have produced before those
// fields existed -- and requestHashCompatible must accept a stored
// pre-M1.3 hash for a resumed request that now legitimately supplies
// those fields, while still rejecting a genuinely different request under
// the same stored hash.
func TestDigestBuildRequest_LegacyCompatibility(t *testing.T) {
	base := CanonicalBuildRequest{
		Request: BuildRequest{OrganizationID: "explorarte", OrganizationRevisionID: 2, ActorRoleID: "investigacion/research_worker_hourly", Purpose: "department_worker"},
		PrecedenceHash: DigestMarkdown([]byte("p")), CanonicalBundleHash: DigestMarkdown([]byte("c")),
		MaxTotalBytes: 100, MaxSegmentBytes: 50, MaxSegments: 10, MaxSkills: 2,
	}
	// digestBuildRequestLegacy simulates exactly what an already-durable
	// pre-M1.3 snapshot's stored RequestHash actually is: it was computed
	// by DigestBuildRequest before this function ever appended
	// task_class/execution_purpose/actor_unit_id to the buffer at all --
	// not merely "DigestBuildRequest called with those fields empty",
	// which is a structurally different (and structurally later) buffer
	// shape even when the field VALUES are empty (see writeField: it
	// still writes the field name and a zero-length value marker for an
	// empty string, which the pre-M1.3 buffer never did for these three
	// fields since they did not exist in it).
	preM13Hash := digestBuildRequestLegacy(base)
	if v2WithEmptyFields := DigestBuildRequest(base); v2WithEmptyFields == preM13Hash {
		t.Fatal("DigestBuildRequest must diverge from the legacy computation by construction, even with the new fields empty -- otherwise the two hash spaces are not actually distinct")
	}

	// existingHistorical simulates the actual durable row: its stored hash
	// is the legacy-shaped one, and -- since context_snapshots' selector
	// columns are never backfilled (migration 000053) -- its own
	// TaskClass/ExecutionPurpose/ActorUnitID are genuinely empty, exactly
	// like every real pre-M1.3 snapshot.
	existingHistorical := Snapshot{RequestHash: preM13Hash}

	resumed := base
	resumed.Request.TaskClass = "research.corpus_curate"
	resumed.Request.ExecutionPurpose = "department-worker"
	resumed.Request.ActorUnitID = "investigacion"
	freshHash := DigestBuildRequest(resumed)
	if freshHash == preM13Hash {
		t.Fatal("DigestBuildRequest must diverge from the pre-M1.3 hash once the new fields are non-empty")
	}
	if !requestHashCompatible(existingHistorical, freshHash, resumed) {
		t.Fatal("a resumed pre-M1.3 snapshot must remain compatible once the caller starts supplying the new selector facts")
	}

	contradictory := resumed
	contradictory.Request.ActorRoleID = "empresa/ceo"
	contradictoryHash := DigestBuildRequest(contradictory)
	if requestHashCompatible(existingHistorical, contradictoryHash, contradictory) {
		t.Fatal("a genuinely different request must not be accepted as compatible with the stored pre-M1.3 hash")
	}
}

// TestRequestHashCompatible_SelectorOnlyContradictionFailsClosed is the
// independent review's required proof that changing ONLY a new M1.3
// selector field -- with every pre-M1.3 field held identical -- is
// detected as a contradiction, never silently accepted merely because
// digestBuildRequestLegacy strips those fields from ITS comparison.
//
// A genuinely v2-created existing row (DigestBuildRequest, not the legacy
// function) already rejects any field-level mismatch through the direct
// top-level hash comparison alone -- ANY field, selector or not, is part
// of that hash. The gap the independent review found only exists on the
// legacy-shaped fallback path (existing.RequestHash ==
// digestBuildRequestLegacy(canonical)), because that computation omits
// TaskClass/ExecutionPurpose/ActorUnitID entirely: this test constructs
// exactly that path -- an existing record whose stored hash is
// legacy-shaped yet which already durably knows a specific, non-empty
// selector fact -- and proves a resumed request proposing a different one
// is rejected, not silently waved through.
func TestRequestHashCompatible_SelectorOnlyContradictionFailsClosed(t *testing.T) {
	base := CanonicalBuildRequest{
		Request: BuildRequest{
			OrganizationID: "explorarte", OrganizationRevisionID: 2, ActorRoleID: "investigacion/research_worker_hourly", Purpose: "department_worker",
			TaskClass: "research.corpus_curate", ExecutionPurpose: "department-worker", ActorUnitID: "investigacion",
		},
		PrecedenceHash: DigestMarkdown([]byte("p")), CanonicalBundleHash: DigestMarkdown([]byte("c")),
		MaxTotalBytes: 100, MaxSegmentBytes: 50, MaxSegments: 10, MaxSkills: 2,
	}
	existing := Snapshot{
		RequestHash:      digestBuildRequestLegacy(base),
		TaskClass:        base.Request.TaskClass,
		ExecutionPurpose: base.Request.ExecutionPurpose,
		ActorUnitID:      base.Request.ActorUnitID,
	}

	identical := base
	if !requestHashCompatible(existing, DigestBuildRequest(identical), identical) {
		t.Fatal("an identical resumed request must remain compatible")
	}

	cases := map[string]func(*CanonicalBuildRequest){
		"task_class":        func(c *CanonicalBuildRequest) { c.Request.TaskClass = "general.work" },
		"execution_purpose": func(c *CanonicalBuildRequest) { c.Request.ExecutionPurpose = "ceo-plan" },
		"actor_unit_id":     func(c *CanonicalBuildRequest) { c.Request.ActorUnitID = "marketing" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mutated := base
			mutate(&mutated)
			if requestHashCompatible(existing, DigestBuildRequest(mutated), mutated) {
				t.Fatalf("changing only %s against an existing snapshot with a known, different value must fail closed", name)
			}
		})
	}
}

// TestRequestHashCompatible_EmptyExistingSelectorAcceptsResume proves the
// companion property: a TRUE pre-M1.3 row (legacy-shaped hash, empty
// selector columns -- exactly what migration 000053 leaves every
// historical context_snapshots row as) still accepts a resumed request
// that now supplies real selector facts, since an empty existing value
// records no assertion to contradict.
func TestRequestHashCompatible_EmptyExistingSelectorAcceptsResume(t *testing.T) {
	base := CanonicalBuildRequest{
		Request:        BuildRequest{OrganizationID: "explorarte", OrganizationRevisionID: 2, ActorRoleID: "investigacion/research_worker_hourly", Purpose: "department_worker"},
		PrecedenceHash: DigestMarkdown([]byte("p")), CanonicalBundleHash: DigestMarkdown([]byte("c")),
		MaxTotalBytes: 100, MaxSegmentBytes: 50, MaxSegments: 10, MaxSkills: 2,
	}
	existing := Snapshot{RequestHash: digestBuildRequestLegacy(base)} // no TaskClass/ExecutionPurpose/ActorUnitID: genuine historical row.
	resumed := base
	resumed.Request.TaskClass, resumed.Request.ExecutionPurpose, resumed.Request.ActorUnitID = "research.corpus_curate", "department-worker", "investigacion"
	if !requestHashCompatible(existing, DigestBuildRequest(resumed), resumed) {
		t.Fatal("a true historical (empty-selector) row must remain resumable once the caller starts supplying real selector facts")
	}
}

func TestPortableRenderIsStableAndReconstructible(t *testing.T) {
	snapshot := Snapshot{ID: 42, OrganizationID: "explorarte", OrganizationRevisionID: 3, ActorRoleID: "ingenieria_ia/qa", Purpose: "test", Status: SnapshotReady, RequestHash: DigestMarkdown([]byte("r")), PrecedenceHash: DigestMarkdown([]byte("p")), CanonicalBundleHash: DigestMarkdown([]byte("c")), Segments: []Segment{{Ordinal: 1, RenderOrdinal: 1, AuthorityPriority: 4, AuthorityTier: TierRoleProfile, SourceKind: SourceRoleProfile, SourceReference: "perfil.md", SourceVersion: "v1", InstructionClass: InstructionRole, TrustClass: TrustAuthoritative, DataClass: DataOrganizational, Included: true, ContentHash: DigestMarkdown([]byte("hello")), ByteCount: 5, Content: []byte("hello")}}}
	renderer := NewRenderer()
	first, err := renderer.Render(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderer.Render(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("render is not stable")
	}
	if DigestCanonicalBytes(first) != DigestCanonicalBytes(second) {
		t.Fatal("rendered hash changed")
	}
}
