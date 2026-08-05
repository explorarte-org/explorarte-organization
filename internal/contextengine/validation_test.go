package contextengine

import (
	"strings"
	"testing"
)

func TestDataClassPolicy(t *testing.T) {
	for _, allowed := range []DataClass{DataPublic, DataOrganizational, DataSanitized} {
		if err := ValidateDataClass(allowed, "x"); err != nil {
			t.Fatalf("%s rejected: %v", allowed, err)
		}
	}
	if err := ValidateDataClass(DataSecret, "x"); ReasonOf(err) != ReasonSecretDataRejected {
		t.Fatalf("secret error=%v", err)
	}
	if err := ValidateDataClass(DataClinical, "x"); ReasonOf(err) != ReasonClinicalDataRejected {
		t.Fatalf("clinical error=%v", err)
	}
	if err := ValidateDataClass(DataClass("unknown"), "x"); ReasonOf(err) != ReasonForbiddenDataClass {
		t.Fatalf("unknown error=%v", err)
	}
}

func TestProfileIdentityAndLegacyFieldsDoNotGrantAuthority(t *testing.T) {
	doc := LoadedDocument{Path: "ingenieria_ia/qa/PERFIL.md", Frontmatter: map[string]any{"departamento": "ingenieria_ia", "rol": "qa", "dominio_memoria": "ingenieria_ia", "agente_base": true, "lider": true, "modelo": "override", "authority_class": "owner"}, Body: []byte("# QA")}
	if err := ValidateProfile(doc, "ingenieria_ia", "qa", "ingenieria_ia"); err != nil {
		t.Fatal(err)
	}
	doc.Frontmatter["rol"] = "frontend"
	if err := ValidateProfile(doc, "ingenieria_ia", "qa", "ingenieria_ia"); ReasonOf(err) != ReasonSourceIdentityMismatch {
		t.Fatalf("identity error=%v", err)
	}
}

func TestSkillLifecycleRequiresActiveAndAssigned(t *testing.T) {
	for _, state := range []SkillLifecycle{SkillDraft, SkillHumanApproved, SkillCandidate, SkillSuspended, SkillRetired} {
		if err := ValidateSkillLifecycle(SkillRecord{ID: "x", Lifecycle: state, Assigned: true}); ReasonOf(err) != ReasonSkillNotActive {
			t.Fatalf("state %s error=%v", state, err)
		}
	}
	if err := ValidateSkillLifecycle(SkillRecord{ID: "x", Lifecycle: SkillActive, Assigned: false}); ReasonOf(err) != ReasonSkillNotAssigned {
		t.Fatalf("unassigned error=%v", err)
	}
	if err := ValidateSkillLifecycle(SkillRecord{ID: "x", Lifecycle: SkillActive, Assigned: true}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSkillRealSchemaAndReferences(t *testing.T) {
	record := SkillRecord{ID: "auditar-ux-y-accesibilidad", RoleID: "ingenieria_ia/frontend", Department: "ingenieria_ia", MemoryDomain: "ingenieria_ia", Lifecycle: SkillActive, Assigned: true}
	doc := LoadedDocument{Path: "ingenieria_ia/frontend/skills/auditar/SKILL.md", Frontmatter: map[string]any{"name": record.ID, "description": "Audita interfaces de producto antes de darlas por terminadas.", "departamento": "ingenieria_ia", "rol": "frontend", "dominio_memoria": "ingenieria_ia", "verificador": nil, "origen": "interno", "protocolo_base": "verificacion_estado", "agente_base": true, "references": []any{"checklists/a11y.md"}}, Body: []byte("# Procedure\nDo work."), Normalized: []byte("x\n")}
	if warnings, err := ValidateSkill(doc, record); err != nil || len(warnings) != 0 {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
	doc.Frontmatter["origen"] = "http://untrusted"
	if _, err := ValidateSkill(doc, record); ReasonOf(err) != ReasonInvalidFrontmatter {
		t.Fatalf("origin error=%v", err)
	}
	doc.Frontmatter["origen"] = "interno"
	doc.Frontmatter["references"] = []any{"../escape.md"}
	if _, err := ValidateSkill(doc, record); ReasonOf(err) != ReasonSourcePathEscape {
		t.Fatalf("reference error=%v", err)
	}
}

func TestValidateSkillWarnsOverFiveHundredLines(t *testing.T) {
	record := SkillRecord{ID: "large-skill", RoleID: "ingenieria_ia/qa", Department: "ingenieria_ia", MemoryDomain: "ingenieria_ia"}
	doc := LoadedDocument{Path: "ingenieria_ia/qa/skills/large-skill/SKILL.md", Frontmatter: map[string]any{"name": "large-skill", "description": "A sufficiently descriptive skill definition for validation.", "departamento": "ingenieria_ia", "rol": "qa", "dominio_memoria": "ingenieria_ia", "verificador": nil, "origen": "interno", "protocolo_base": "verificacion_estado"}, Body: []byte("body"), Normalized: []byte(strings.Repeat("line\n", 501))}
	warnings, err := ValidateSkill(doc, record)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings=%v", warnings)
	}
}
