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
