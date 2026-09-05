package contextprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/canonical"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

type fakeRepo struct {
	skills      map[string]skillregistry.Skill
	versions    map[string]skillregistry.SkillVersion
	assignments map[string][]skillregistry.SkillAssignment
	err         error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		skills:      map[string]skillregistry.Skill{},
		versions:    map[string]skillregistry.SkillVersion{},
		assignments: map[string][]skillregistry.SkillAssignment{},
	}
}

func (f *fakeRepo) CreateSkill(_ context.Context, s skillregistry.Skill, v skillregistry.SkillVersion, _ string, _ skillregistry.GovernanceEvidence) (skillregistry.Skill, skillregistry.SkillVersion, bool, error) {
	if f.err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, f.err
	}
	f.skills[s.ID] = s
	f.versions[v.ID] = v
	return s, v, false, nil
}

func (f *fakeRepo) GetSkill(_ context.Context, _, skillID string) (skillregistry.Skill, error) {
	if f.err != nil {
		return skillregistry.Skill{}, f.err
	}
	s, ok := f.skills[skillID]
	if !ok {
		return skillregistry.Skill{}, skillregistry.ErrNotFound
	}
	return s, nil
}

func (f *fakeRepo) GetVersion(_ context.Context, _, versionID string) (skillregistry.SkillVersion, error) {
	if f.err != nil {
		return skillregistry.SkillVersion{}, f.err
	}
	v, ok := f.versions[versionID]
	if !ok {
		return skillregistry.SkillVersion{}, skillregistry.ErrNotFound
	}
	return v, nil
}

func (f *fakeRepo) ListVersions(_ context.Context, _, skillID string) ([]skillregistry.SkillVersion, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []skillregistry.SkillVersion
	for _, v := range f.versions {
		if v.SkillID == skillID {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *fakeRepo) SaveVersion(_ context.Context, v skillregistry.SkillVersion, _ int64, _ skillregistry.LifecycleEvent) (skillregistry.SkillVersion, error) {
	if f.err != nil {
		return skillregistry.SkillVersion{}, f.err
	}
	f.versions[v.ID] = v
	return v, nil
}

func (f *fakeRepo) CreateAssignment(_ context.Context, a skillregistry.SkillAssignment, _ string, _ skillregistry.AssignmentEvent) (skillregistry.SkillAssignment, bool, error) {
	if f.err != nil {
		return skillregistry.SkillAssignment{}, false, f.err
	}
	f.assignments[a.RoleID] = append(f.assignments[a.RoleID], a)
	return a, false, nil
}

func (f *fakeRepo) GetAssignment(_ context.Context, _, assignmentID string) (skillregistry.SkillAssignment, error) {
	if f.err != nil {
		return skillregistry.SkillAssignment{}, f.err
	}
	for _, list := range f.assignments {
		for _, a := range list {
			if a.ID == assignmentID {
				return a, nil
			}
		}
	}
	return skillregistry.SkillAssignment{}, skillregistry.ErrNotFound
}

func (f *fakeRepo) ListActiveAssignmentsForRole(_ context.Context, _, roleID string) ([]skillregistry.SkillAssignment, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []skillregistry.SkillAssignment
	for _, a := range f.assignments[roleID] {
		if a.Status == skillregistry.AssignmentActive {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeRepo) SaveAssignment(_ context.Context, a skillregistry.SkillAssignment, _ int64, _ skillregistry.AssignmentEvent) (skillregistry.SkillAssignment, error) {
	if f.err != nil {
		return skillregistry.SkillAssignment{}, f.err
	}
	list := f.assignments[a.RoleID]
	for i, existing := range list {
		if existing.ID == a.ID {
			list[i] = a
			return a, nil
		}
	}
	return a, nil
}

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docs", "canonical", "capability-matrix.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if _, err := os.Stat("/workspace/docs/canonical/capability-matrix.yaml"); err == nil {
		return "/workspace"
	}
	return "/home/ubuntu/explorarte-organization"
}

func TestTrivialRealCanonicalEmptyParity(t *testing.T) {
	ctx := context.Background()
	canonicalRoot := filepath.Join(findRepoRoot(), "docs", "canonical")
	canProvider, err := canonical.NewSkillProvider(canonicalRoot)
	if err != nil {
		t.Fatalf("NewSkillProvider failed: %v", err)
	}

	repo := newFakeRepo()
	regProvider, err := New(repo, "explorarte")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := NewMemoryDivergenceRecorder()
	parity, err := NewParityProvider(canProvider, regProvider, sink)
	if err != nil {
		t.Fatalf("NewParityProvider failed: %v", err)
	}

	// Roles in canonical
	testRoles := []string{
		"ingenieria_ia/lider_arquitectura",
		"investigacion/investigador_principal",
		"empresa/director_general",
	}

	for _, role := range testRoles {
		canActive, err := canProvider.ListActiveForRole(ctx, "explorarte", role)
		if err != nil {
			t.Fatalf("canonical list failed: %v", err)
		}
		regActive, err := regProvider.ListActiveForRole(ctx, "explorarte", role)
		if err != nil {
			t.Fatalf("registry list failed: %v", err)
		}

		if len(canActive) != 0 {
			t.Fatalf("expected 0 canonical active skills, got %d", len(canActive))
		}
		if len(regActive) != 0 {
			t.Fatalf("expected 0 registry active skills, got %d", len(regActive))
		}

		parityActive, err := parity.ListActiveForRole(ctx, "explorarte", role)
		if err != nil {
			t.Fatalf("parity list failed: %v", err)
		}
		if len(parityActive) != 0 {
			t.Fatalf("expected 0 parity active skills, got %d", len(parityActive))
		}
	}

	divs, _ := sink.ListDivergences(ctx)
	if len(divs) != 0 {
		t.Fatalf("expected 0 divergences in trivial parity, got %d", len(divs))
	}
	t.Log("TRIVIAL_PARITY = PASS (canonical active=0, registry active=0)")
}

func TestNontrivialActiveFixtureParity(t *testing.T) {
	ctx := context.Background()
	// Create temporary canonical directory with active skills in capability-matrix.yaml
	tmpDir := t.TempDir()
	capMatrixContent := `schema_version: "canonical/v1"
skill_lifecycle:
  states:
    - draft
    - human_approved
    - candidate
    - active
    - suspended
    - retired
imported_skills:
  - id: skill-alpha
    owner_role_id: ingenieria_ia/lider_arquitectura
    memory_domain: ingenieria_ia
    source_file: "skills/skill-alpha/SKILL.md"
    source_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    status: active
  - id: skill-beta
    owner_role_id: investigacion/investigador_principal
    memory_domain: investigacion
    source_file: "skills/skill-beta/SKILL.md"
    source_sha256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    status: active
`
	if err := os.WriteFile(filepath.Join(tmpDir, "capability-matrix.yaml"), []byte(capMatrixContent), 0644); err != nil {
		t.Fatalf("write capability matrix fixture: %v", err)
	}

	canProvider, err := canonical.NewSkillProvider(tmpDir)
	if err != nil {
		t.Fatalf("NewSkillProvider fixture: %v", err)
	}

	repo := newFakeRepo()
	// Populate equivalent skills and active assignments in registry
	vAlpha := skillregistry.SkillVersion{
		ID:             "ver-alpha-1",
		SkillID:        "skill-alpha",
		OrganizationID: "explorarte",
		Version:        1,
		Lifecycle:      skillregistry.LifecycleActive,
		Manifest: skillregistry.Manifest{
			Name:         "skill-alpha",
			Department:   "ingenieria_ia",
			OwnerRoleID:  "ingenieria_ia/lider_arquitectura",
			MemoryDomain: "ingenieria_ia",
		},
		Source: skillregistry.SourceRecord{
			Path:             "skills/skill-alpha/SKILL.md",
			SHA256:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			NormalizedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			LegacyImported:   true,
		},
	}
	repo.skills["skill-alpha"] = skillregistry.Skill{ID: "skill-alpha", OrganizationID: "explorarte"}
	repo.versions["ver-alpha-1"] = vAlpha
	repo.assignments["ingenieria_ia/lider_arquitectura"] = []skillregistry.SkillAssignment{
		{
			ID:             "assign-alpha",
			OrganizationID: "explorarte",
			RoleID:         "ingenieria_ia/lider_arquitectura",
			SkillID:        "skill-alpha",
			SkillVersionID: "ver-alpha-1",
			Status:         skillregistry.AssignmentActive,
		},
	}

	vBeta := skillregistry.SkillVersion{
		ID:             "ver-beta-1",
		SkillID:        "skill-beta",
		OrganizationID: "explorarte",
		Version:        1,
		Lifecycle:      skillregistry.LifecycleActive,
		Manifest: skillregistry.Manifest{
			Name:         "skill-beta",
			Department:   "investigacion",
			OwnerRoleID:  "investigacion/investigador_principal",
			MemoryDomain: "investigacion",
		},
		Source: skillregistry.SourceRecord{
			Path:             "skills/skill-beta/SKILL.md",
			SHA256:           "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			NormalizedSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			LegacyImported:   true,
		},
	}
	repo.skills["skill-beta"] = skillregistry.Skill{ID: "skill-beta", OrganizationID: "explorarte"}
	repo.versions["ver-beta-1"] = vBeta
	repo.assignments["investigacion/investigador_principal"] = []skillregistry.SkillAssignment{
		{
			ID:             "assign-beta",
			OrganizationID: "explorarte",
			RoleID:         "investigacion/investigador_principal",
			SkillID:        "skill-beta",
			SkillVersionID: "ver-beta-1",
			Status:         skillregistry.AssignmentActive,
		},
	}

	regProvider, err := New(repo, "explorarte")
	if err != nil {
		t.Fatalf("New registry provider failed: %v", err)
	}

	sink := NewMemoryDivergenceRecorder()
	parity, err := NewParityProvider(regProvider, canProvider, sink)
	if err != nil {
		t.Fatalf("NewParityProvider failed: %v", err)
	}

	// 1. Check alpha for role
	records, err := parity.ListActiveForRole(ctx, "explorarte", "ingenieria_ia/lider_arquitectura")
	if err != nil {
		t.Fatalf("parity list alpha failed: %v", err)
	}
	if len(records) != 1 || records[0].ID != "skill-alpha" {
		t.Fatalf("expected 1 skill-alpha record, got %+v", records)
	}
	if records[0].Version != "canonical-import-v1" {
		t.Fatalf("expected version canonical-import-v1, got %s", records[0].Version)
	}

	// 2. Check beta for role
	recordsBeta, err := parity.ListActiveForRole(ctx, "explorarte", "investigacion/investigador_principal")
	if err != nil {
		t.Fatalf("parity list beta failed: %v", err)
	}
	if len(recordsBeta) != 1 || recordsBeta[0].ID != "skill-beta" {
		t.Fatalf("expected 1 skill-beta record, got %+v", recordsBeta)
	}

	// 3. Assert zero divergences between primary and shadow
	divs, _ := sink.ListDivergences(ctx)
	if len(divs) != 0 {
		t.Fatalf("expected 0 divergences in nontrivial parity, got %+v", divs)
	}

	t.Log("NONTRIVIAL_PARITY = PASS")
}

func TestDivergenceDetection(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	regProvider, _ := New(repo, "explorarte")

	// Create shadow provider with different attributes
	shadowRepo := newFakeRepo()
	shadowProvider, _ := New(shadowRepo, "explorarte")

	sink := NewMemoryDivergenceRecorder()
	parity, _ := NewParityProvider(regProvider, shadowProvider, sink)

	roleID := "ingenieria_ia/lider_arquitectura"

	// 1. MISSING_REGISTRY_ASSIGNMENT_DIVERGES (shadow has skill, primary doesn't)
	shadowRepo.skills["skill-1"] = skillregistry.Skill{ID: "skill-1", OrganizationID: "explorarte"}
	shadowRepo.versions["v-1"] = skillregistry.SkillVersion{
		ID: "v-1", SkillID: "skill-1", OrganizationID: "explorarte", Version: 1, Lifecycle: skillregistry.LifecycleActive,
		Source: skillregistry.SourceRecord{Path: "SKILL.md", SHA256: "aaa", NormalizedSHA256: "aaa"},
	}
	shadowRepo.assignments[roleID] = []skillregistry.SkillAssignment{
		{ID: "a1", OrganizationID: "explorarte", RoleID: roleID, SkillID: "skill-1", SkillVersionID: "v-1", Status: skillregistry.AssignmentActive},
	}

	_, _ = parity.ListActiveForRole(ctx, "explorarte", roleID)
	divs, _ := sink.ListDivergences(ctx)
	if len(divs) == 0 {
		t.Fatal("expected divergence for missing assignment")
	}
	found := false
	for _, d := range divs {
		if d.Field == "Presence" && d.PrimaryValue == "absent" && d.ShadowValue == "present" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("MISSING_REGISTRY_ASSIGNMENT_DIVERGES failed, divs: %+v", divs)
	}
}

func TestLifecycleVisibilityInvariants(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	p, err := New(repo, "explorarte")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	roleID := "ingenieria_ia/lider_arquitectura"

	// Helper to add version and assignment
	addVersionAndAssign := func(skillID, versionID string, life skillregistry.Lifecycle, assignStatus skillregistry.AssignmentStatus, assignVersionID string) {
		repo.skills[skillID] = skillregistry.Skill{ID: skillID, OrganizationID: "explorarte"}
		repo.versions[versionID] = skillregistry.SkillVersion{
			ID:             versionID,
			SkillID:        skillID,
			OrganizationID: "explorarte",
			Version:        1,
			Lifecycle:      life,
			Manifest:       skillregistry.Manifest{Name: skillID, Department: "ingenieria_ia"},
			Source:         skillregistry.SourceRecord{Path: "SKILL.md", SHA256: "1111111111111111111111111111111111111111111111111111111111111111", NormalizedSHA256: "1111111111111111111111111111111111111111111111111111111111111111"},
		}
		if assignStatus != "" {
			repo.assignments[roleID] = append(repo.assignments[roleID], skillregistry.SkillAssignment{
				ID:             "assign-" + skillID,
				OrganizationID: "explorarte",
				RoleID:         roleID,
				SkillID:        skillID,
				SkillVersionID: assignVersionID,
				Status:         assignStatus,
			})
		}
	}

	// Invariant 1: DRAFT_NOT_VISIBLE
	addVersionAndAssign("skill-draft", "v-draft", skillregistry.LifecycleDraft, skillregistry.AssignmentActive, "v-draft")
	// Invariant 2: HUMAN_APPROVED_NOT_VISIBLE
	addVersionAndAssign("skill-approved", "v-approved", skillregistry.LifecycleHumanApproved, skillregistry.AssignmentActive, "v-approved")
	// Invariant 3: CANDIDATE_NOT_VISIBLE
	addVersionAndAssign("skill-candidate", "v-candidate", skillregistry.LifecycleCandidate, skillregistry.AssignmentActive, "v-candidate")
	// Invariant 4: SUSPENDED_NOT_VISIBLE
	addVersionAndAssign("skill-suspended", "v-suspended", skillregistry.LifecycleSuspended, skillregistry.AssignmentActive, "v-suspended")
	// Invariant 5: ACTIVE_UNASSIGNED_NOT_VISIBLE
	addVersionAndAssign("skill-unassigned", "v-unassigned", skillregistry.LifecycleActive, "", "")
	// Invariant 6: ACTIVE_WRONG_ASSIGNMENT_VERSION_NOT_VISIBLE
	// Skill has active v2, but assignment points to retired v1
	repo.skills["skill-wrong-v"] = skillregistry.Skill{ID: "skill-wrong-v", OrganizationID: "explorarte"}
	repo.versions["v-active-2"] = skillregistry.SkillVersion{
		ID: "v-active-2", SkillID: "skill-wrong-v", OrganizationID: "explorarte", Version: 2, Lifecycle: skillregistry.LifecycleActive,
		Manifest: skillregistry.Manifest{Name: "skill-wrong-v", Department: "ingenieria_ia"},
		Source:   skillregistry.SourceRecord{Path: "SKILL.md", SHA256: "222", NormalizedSHA256: "222"},
	}
	repo.versions["v-retired-1"] = skillregistry.SkillVersion{
		ID: "v-retired-1", SkillID: "skill-wrong-v", OrganizationID: "explorarte", Version: 1, Lifecycle: skillregistry.LifecycleRetired,
		Manifest: skillregistry.Manifest{Name: "skill-wrong-v", Department: "ingenieria_ia"},
		Source:   skillregistry.SourceRecord{Path: "SKILL.md", SHA256: "111", NormalizedSHA256: "111"},
	}
	repo.assignments[roleID] = append(repo.assignments[roleID], skillregistry.SkillAssignment{
		ID: "assign-wrong-v", OrganizationID: "explorarte", RoleID: roleID, SkillID: "skill-wrong-v", SkillVersionID: "v-retired-1", Status: skillregistry.AssignmentActive,
	})

	// Invariant 7: ACTIVE_EXACT_ASSIGNED_VISIBLE
	addVersionAndAssign("skill-active-ok", "v-active-ok", skillregistry.LifecycleActive, skillregistry.AssignmentActive, "v-active-ok")

	records, err := p.ListActiveForRole(ctx, "explorarte", roleID)
	if err != nil {
		t.Fatalf("ListActiveForRole failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected exactly 1 visible skill, got %d: %+v", len(records), records)
	}
	if records[0].ID != "skill-active-ok" {
		t.Fatalf("expected skill-active-ok, got %s", records[0].ID)
	}
}

func TestRegistryOperationalFailureFailsClosed(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	repo.err = errors.New("connection to postgresql refused")

	p, err := New(repo, "explorarte")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = p.ListActiveForRole(ctx, "explorarte", "ingenieria_ia/lider_arquitectura")
	if err == nil {
		t.Fatal("expected operational failure, got nil")
	}

	// Must NOT be ErrSourceProviderUnavailable
	if errors.Is(err, contextengine.ErrSourceProviderUnavailable) {
		t.Fatal("fail closed violated: operational error must NOT map to ErrSourceProviderUnavailable")
	}

	// In a ParityProvider, operational failure must propagate from primary
	sink := NewMemoryDivergenceRecorder()
	parity, _ := NewParityProvider(p, nil, sink)
	_, err = parity.ListActiveForRole(ctx, "explorarte", "ingenieria_ia/lider_arquitectura")
	if err == nil {
		t.Fatal("expected operational failure from parity primary, got nil")
	}
	if errors.Is(err, contextengine.ErrSourceProviderUnavailable) {
		t.Fatal("fail closed violated in parity provider")
	}
}

func TestLoaderHashMatchesRuntimeSourceHash(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skills", "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	// Write with CRLF to verify normalization
	crlfContent := []byte("# Test Skill\r\n\r\nProcedure details.\r\n")
	if err := os.WriteFile(skillPath, crlfContent, 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Document loader
	loader, err := document.NewLoader(tmpDir, 65536)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loader.Load(context.Background(), "skills/test-skill/SKILL.md", 65536)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Normalized hash
	normText, err := document.NormalizeText(crlfContent)
	if err != nil {
		t.Fatal(err)
	}
	normSum := sha256.Sum256(normText)
	normHash := hex.EncodeToString(normSum[:])

	if doc.Hash != normHash {
		t.Fatalf("loader hash %s != normHash %s", doc.Hash, normHash)
	}

	// 3. Provider record
	repo := newFakeRepo()
	repo.skills["test-skill"] = skillregistry.Skill{ID: "test-skill", OrganizationID: "explorarte"}
	repo.versions["v-1"] = skillregistry.SkillVersion{
		ID:             "v-1",
		SkillID:        "test-skill",
		OrganizationID: "explorarte",
		Version:        1,
		Lifecycle:      skillregistry.LifecycleActive,
		Manifest:       skillregistry.Manifest{Name: "test-skill", Department: "ingenieria_ia"},
		Source: skillregistry.SourceRecord{
			Path:             "skills/test-skill/SKILL.md",
			SHA256:           "rawhash...",
			NormalizedSHA256: normHash,
		},
	}
	repo.assignments["role-1"] = []skillregistry.SkillAssignment{
		{ID: "a1", OrganizationID: "explorarte", RoleID: "role-1", SkillID: "test-skill", SkillVersionID: "v-1", Status: skillregistry.AssignmentActive},
	}

	p, _ := New(repo, "explorarte")
	records, err := p.ListActiveForRole(context.Background(), "explorarte", "role-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].SourceHash != doc.Hash {
		t.Fatalf("runtime SourceHash (%s) != doc.Hash (%s)", records[0].SourceHash, doc.Hash)
	}
}

func TestPinnedCandidateEvaluationIsolation(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	baseProvider, _ := New(repo, "explorarte")

	candidate := skillregistry.SkillVersion{
		ID:             "cand-v1",
		SkillID:        "skill-candidate",
		OrganizationID: "explorarte",
		Version:        2,
		Lifecycle:      skillregistry.LifecycleCandidate,
		Manifest: skillregistry.Manifest{
			Name:         "skill-candidate",
			Department:   "ingenieria_ia",
			OwnerRoleID:  "ingenieria_ia/lider_arquitectura",
			MemoryDomain: "ingenieria_ia",
		},
		Source: skillregistry.SourceRecord{
			Path:             "skills/skill-candidate/SKILL.md",
			SHA256:           "craw...",
			NormalizedSHA256: "cnorm...",
		},
	}

	evalProvider, err := NewPinnedCandidateSkillProvider(baseProvider, candidate, "ingenieria_ia/lider_arquitectura")
	if err != nil {
		t.Fatalf("NewPinnedCandidateSkillProvider: %v", err)
	}

	// 1. In evaluation provider for allowed role: candidate IS resolved
	records, err := evalProvider.ListActiveForRole(ctx, "explorarte", "ingenieria_ia/lider_arquitectura")
	if err != nil {
		t.Fatalf("eval provider list: %v", err)
	}
	if len(records) != 1 || records[0].ID != "skill-candidate" {
		t.Fatalf("expected candidate in eval provider, got %+v", records)
	}
	if records[0].Lifecycle != contextengine.SkillCandidate {
		t.Fatalf("expected lifecycle candidate, got %s", records[0].Lifecycle)
	}

	// 2. In evaluation provider for OTHER role: candidate is NOT resolved
	otherRecords, err := evalProvider.ListActiveForRole(ctx, "explorarte", "empresa/director_general")
	if err != nil {
		t.Fatalf("eval provider other role list: %v", err)
	}
	if len(otherRecords) != 0 {
		t.Fatalf("candidate leaked to other role in eval provider: %+v", otherRecords)
	}

	// 3. In standard base provider (production): candidate is NOT visible
	baseRecords, err := baseProvider.ListActiveForRole(ctx, "explorarte", "ingenieria_ia/lider_arquitectura")
	if err != nil {
		t.Fatalf("base provider list: %v", err)
	}
	if len(baseRecords) != 0 {
		t.Fatalf("candidate visible in standard production provider: %+v", baseRecords)
	}
}
