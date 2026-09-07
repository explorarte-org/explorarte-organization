package contextengine

import (
	"bytes"
	"context"
	"testing"
	"time"
)

type testMutableSkillProvider struct {
	skills map[string]SkillRecord
}

func (m *testMutableSkillProvider) ListActiveForRole(_ context.Context, _, roleID string) ([]SkillRecord, error) {
	var out []SkillRecord
	for _, s := range m.skills {
		if s.RoleID == roleID && s.Lifecycle == SkillActive && s.Assigned {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *testMutableSkillProvider) GetActiveForRole(_ context.Context, _, roleID, skillID string) (SkillRecord, error) {
	s, ok := m.skills[skillID]
	if !ok || s.RoleID != roleID || s.Lifecycle != SkillActive || !s.Assigned {
		return SkillRecord{}, Reject(ReasonSkillNotFound, skillID, "not found or not active/assigned")
	}
	return s, nil
}

func (m *testMutableSkillProvider) ValidateVersion(ctx context.Context, expected SkillRecord) error {
	cur, ok := m.skills[expected.ID]
	if !ok || cur.Lifecycle != SkillActive || !cur.Assigned {
		return Reject(ReasonSkillStateDrift, expected.ID, "not active/assigned")
	}
	if cur.SourceHash != expected.SourceHash || cur.Version != expected.Version || cur.Path != expected.Path {
		return Reject(ReasonSkillSourceDrift, expected.ID, "source changed")
	}
	return nil
}

func TestServiceValidateDetectsSkillSourceDriftWithoutMutatingHistoricalSnapshot(t *testing.T) {
	f := newServiceFixture(t)
	ctx := t.Context()

	v1Hash := DigestMarkdown([]byte("skill-body-v1\n"))
	skillPath := "ingenieria_ia/qa/skills/skill-qa/SKILL.md"

	f.docs.docs[skillPath] = LoadedDocument{
		Path:       skillPath,
		Body:       []byte("# Procedure\nPerform QA procedure v1.\n"),
		Normalized: []byte("skill-body-v1\n"),
		Hash:       v1Hash,
		Frontmatter: map[string]any{
			"name":            "skill-qa",
			"description":     "QA procedure test skill with sufficient length",
			"departamento":    "ingenieria_ia",
			"rol":             "qa",
			"dominio_memoria": "ingenieria_ia",
			"origen":          "interno",
			"protocolo_base":  "none",
			"verificador":     nil,
		},
	}

	skillProv := &testMutableSkillProvider{
		skills: map[string]SkillRecord{
			"skill-qa": {
				ID:           "skill-qa",
				RoleID:       "ingenieria_ia/qa",
				Department:   "ingenieria_ia",
				MemoryDomain: "ingenieria_ia",
				Lifecycle:    SkillActive,
				Assigned:     true,
				Version:      "registry-v1",
				SourceHash:   v1Hash,
				Path:         skillPath,
			},
		},
	}

	// Recreate service with mutable skill provider
	store := newMemoryStore()
	service, err := NewService(
		ServiceConfig{
			OrganizationAgentPath: "AGENT.md",
			MaxTotalBytes:         65536,
			MaxSegmentBytes:       8192,
			MaxSegments:           64,
			MaxSkills:             16,
			MaxMemorySegments:     32,
			MaxRAGSegments:        20,
		},
		f.registry, f.docs, f.canonical,
		NoopOwnerConstraintProvider{},
		UnavailableMemoryProvider{},
		skillProv,
		UnavailableProjectProvider{},
		UnavailableTaskProvider{},
		UnavailableRAGProvider{},
		NewAssembler(),
		NewRenderer(),
		store,
		fixedClock{now: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)},
	)
	if err != nil {
		t.Fatal(err)
	}

	req := f.request("historical-skill-snap")
	req.RequestedSkillIDs = []string{"skill-qa"}

	buildResult, err := service.Build(ctx, req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	snapID := buildResult.Snapshot.ID

	// Initial validation must pass
	val1, err := service.Validate(ctx, snapID)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if !val1.Valid {
		t.Fatalf("Expected valid initial snapshot, got %+v", val1)
	}

	// Read stored snapshot before any registry updates
	snapBefore, err := store.Get(ctx, snapID, true)
	if err != nil {
		t.Fatal(err)
	}
	var skillSegBefore Segment
	foundSkill := false
	for _, seg := range snapBefore.Segments {
		if seg.SourceReference == "skill-qa" {
			skillSegBefore = seg
			foundSkill = true
			break
		}
	}
	if !foundSkill {
		t.Fatalf("Expected skill segment for skill-qa, got %+v", snapBefore.Segments)
	}
	if skillSegBefore.ContentHash != v1Hash {
		t.Fatalf("Expected ContentHash %s, got %s", v1Hash, skillSegBefore.ContentHash)
	}

	// --- REGISTRY / RUNTIME STATE CHANGES: v2 is now active/assigned with different hash ---
	v2Hash := DigestMarkdown([]byte("skill-body-v2-updated\n"))
	f.docs.docs[skillPath] = LoadedDocument{
		Path:       skillPath,
		Body:       []byte("# Procedure\nPerform QA procedure v2 updated.\n"),
		Normalized: []byte("skill-body-v2-updated\n"),
		Hash:       v2Hash,
		Frontmatter: map[string]any{
			"name":            "skill-qa",
			"description":     "QA procedure test skill with sufficient length",
			"departamento":    "ingenieria_ia",
			"rol":             "qa",
			"dominio_memoria": "ingenieria_ia",
			"origen":          "interno",
			"protocolo_base":  "none",
			"verificador":     nil,
		},
	}

	skillProv.skills["skill-qa"] = SkillRecord{
		ID:           "skill-qa",
		RoleID:       "ingenieria_ia/qa",
		Department:   "ingenieria_ia",
		MemoryDomain: "ingenieria_ia",
		Lifecycle:    SkillActive,
		Assigned:     true,
		Version:      "registry-v2",
		SourceHash:   v2Hash,
		Path:         skillPath,
	}

	// Reload Snapshot S: verify byte/segment immutability
	snapAfter, err := store.Get(ctx, snapID, true)
	if err != nil {
		t.Fatal(err)
	}
	if snapAfter.ID != snapBefore.ID || snapAfter.PrecedenceHash != snapBefore.PrecedenceHash ||
		snapAfter.TotalBytes != snapBefore.TotalBytes || len(snapAfter.Segments) != len(snapBefore.Segments) {
		t.Fatalf("Historical snapshot mutated! Before: %+v, After: %+v", snapBefore, snapAfter)
	}
	for i, seg := range snapAfter.Segments {
		bSeg := snapBefore.Segments[i]
		if seg.ContentHash != bSeg.ContentHash || !bytes.Equal(seg.Content, bSeg.Content) ||
			seg.SourceReference != bSeg.SourceReference || seg.SourceVersion != bSeg.SourceVersion {
			t.Fatalf("Segment %d mutated! Before: %+v, After: %+v", i, bSeg, seg)
		}
	}

	// Validate(S) deterministically reports skill drift/stale
	val2, err := service.Validate(ctx, snapID)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if val2.Valid {
		t.Fatalf("Expected snapshot to be invalid after skill source drift")
	}
	if val2.ReasonCode != string(ReasonSnapshotStale) {
		t.Fatalf("Expected ReasonSnapshotStale, got %s", val2.ReasonCode)
	}

	foundFinding := false
	for _, finding := range val2.Drift {
		if finding.ReasonCode == string(ReasonSkillSourceDrift) && finding.Reference == "skill-qa" {
			foundFinding = true
			if finding.Expected != v1Hash || finding.Actual != v2Hash {
				t.Fatalf("Expected drift hashes (%s -> %s), got (%s -> %s)", v1Hash, v2Hash, finding.Expected, finding.Actual)
			}
			break
		}
	}
	if !foundFinding {
		t.Fatalf("Expected ReasonSkillSourceDrift in drift findings: %+v", val2.Drift)
	}

	// Snapshot in store must NOT be mutated
	snapFinal, err := store.Get(ctx, snapID, false)
	if err != nil {
		t.Fatal(err)
	}
	if snapFinal.Status != SnapshotReady {
		t.Fatalf("Validation mutated status to %s", snapFinal.Status)
	}
}
