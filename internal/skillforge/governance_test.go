package skillforge_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/skillforge"
)

const (
	governanceRole    = "recursos_agenticos/disenador_skills"
	governanceProfile = "worker/skill-forge/v1"
)

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		srcF, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcF.Close()
		dstF, err := os.Create(target)
		if err != nil {
			return err
		}
		defer dstF.Close()
		_, err = io.Copy(dstF, srcF)
		return err
	})
}

func canonicalDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "docs", "canonical")
}

func copiedCanonicalDir(t *testing.T, mutateDecisions func(string) string) string {
	t.Helper()
	dir := t.TempDir()
	if err := copyDir(canonicalDir(t), dir); err != nil {
		t.Fatal(err)
	}
	if mutateDecisions == nil {
		return dir
	}
	path := filepath.Join(dir, "decisions-required.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mutated := mutateDecisions(string(body))
	if mutated == string(body) {
		t.Fatal("test mutation did not change decisions-required.yaml")
	}
	if err := os.WriteFile(path, []byte(mutated), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newReader(t *testing.T, dir string) *skillforge.CanonicalSkillForgeGovernanceReader {
	t.Helper()
	reader, err := skillforge.NewCanonicalSkillForgeGovernanceReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func expectDenied(t *testing.T, reader *skillforge.CanonicalSkillForgeGovernanceReader, organizationID, roleID, profileID string) {
	t.Helper()
	_, err := reader.ResolveAuthoringAuthorization(context.Background(), organizationID, roleID, profileID)
	if err == nil || !errors.Is(err, skillforge.ErrGovernanceDenied) {
		t.Fatalf("expected ErrGovernanceDenied, got: %v", err)
	}
}

func TestCanonicalSkillForgeGovernanceReaderAllowsOnlyTypedD006Scope(t *testing.T) {
	reader := newReader(t, canonicalDir(t))
	for _, role := range []string{
		"recursos_agenticos/disenador_skills",
		"recursos_agenticos/evaluador_agentes",
	} {
		auth, err := reader.ResolveAuthoringAuthorization(context.Background(), "explorarte", role, governanceProfile)
		if err != nil {
			t.Fatalf("role %s: expected approval, got err: %v", role, err)
		}
		if !auth.Approved || auth.ResolvedDecisionID != "D-006" || auth.Scope != "skill_authoring_evaluation" {
			t.Fatalf("role %s: unexpected auth: %+v", role, auth)
		}
	}
}

func TestCanonicalSkillForgeGovernanceReaderDeniesIneligibleInputs(t *testing.T) {
	tests := []struct {
		name         string
		organization string
		role         string
		profile      string
	}{
		{
			name:         "wrong organization",
			organization: "other-org",
			role:         governanceRole,
			profile:      governanceProfile,
		},
		{
			name:         "profile not explicitly approved",
			organization: "explorarte",
			role:         governanceRole,
			profile:      "worker/skill-forge/v99",
		},
		{
			name:         "imported specialist absent from D006",
			organization: "explorarte",
			role:         "servicios/analista_calidad",
			profile:      governanceProfile,
		},
		{
			name:         "unimported role",
			organization: "explorarte",
			role:         "recursos_agenticos/curador_catalogo",
			profile:      governanceProfile,
		},
		{
			name:         "non-specialist role",
			organization: "explorarte",
			role:         "recursos_agenticos/desarrollo_organizacional",
			profile:      governanceProfile,
		},
		{
			name:         "disabled role",
			organization: "explorarte",
			role:         "empresa/ceo_observer",
			profile:      governanceProfile,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expectDenied(t, newReader(t, canonicalDir(t)), tc.organization, tc.role, tc.profile)
		})
	}
}

func TestCanonicalSkillForgeGovernanceReaderFailsClosedForMalformedOrUnresolvedD006(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) string
	}{
		{
			name: "duplicate D006",
			mutate: func(value string) string {
				start := strings.Index(value, "- id: D-006\n")
				end := strings.Index(value[start+1:], "\n- id:")
				if end < 0 {
					return value + "\n" + value[start:]
				}
				return value[:start] + value[start:start+1+end] + "\n" + value[start:]
			},
		},
		{
			name: "profile substring is not a grant",
			mutate: func(value string) string {
				return strings.Replace(value, "    - worker/skill-forge/v1\n", "    - worker/skill-forge/v10\n", 1)
			},
		},
		{
			name: "role substring is not a grant",
			mutate: func(value string) string {
				return strings.Replace(value, "    - recursos_agenticos/disenador_skills\n", "    - recursos_agenticos/disenador_skills_extra\n", 1)
			},
		},
		{
			name: "denied prose mention does not grant profile",
			mutate: func(value string) string {
				value = strings.Replace(value, "    - worker/skill-forge/v1\n", "    - worker/skill-forge/v99\n", 1)
				return value + "\n# DENIED worker/skill-forge/v1 recursos_agenticos/disenador_skills\n"
			},
		},
		{
			name: "missing D006",
			mutate: func(value string) string {
				return strings.Replace(value, "- id: D-006\n", "- id: D-006-missing\n", 1)
			},
		},
		{
			name: "open D006",
			mutate: func(value string) string {
				return strings.Replace(value, "open:\n- id: D-002", "open:\n- id: D-006", 1)
			},
		},
		{
			name: "missing typed authorization",
			mutate: func(value string) string {
				block := "  authorization:\n    scope: skill_authoring_evaluation\n    organization_ids:\n    - explorarte\n    role_ids:\n    - recursos_agenticos/disenador_skills\n    - recursos_agenticos/evaluador_agentes\n    execution_profile_ids:\n    - worker/skill-forge/v1\n"
				return strings.Replace(value, block, "", 1)
			},
		},
		{
			name: "profile list changed",
			mutate: func(value string) string {
				return strings.Replace(value, "    - worker/skill-forge/v1\n", "    - worker/skill-forge/v99\n", 1)
			},
		},
		{
			name: "role list changed",
			mutate: func(value string) string {
				return strings.Replace(value, "    - recursos_agenticos/disenador_skills\n", "    - recursos_agenticos/disenador_perfiles\n", 1)
			},
		},
		{
			name: "duplicate profile grant",
			mutate: func(value string) string {
				return strings.Replace(value, "    - worker/skill-forge/v1\n", "    - worker/skill-forge/v1\n    - worker/skill-forge/v1\n", 1)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := copiedCanonicalDir(t, tc.mutate)
			expectDenied(t, newReader(t, dir), "explorarte", governanceRole, governanceProfile)
		})
	}
}

func TestCanonicalSkillForgeGovernanceReaderFailsClosedOnCanonicalParseFailure(t *testing.T) {
	dir := copiedCanonicalDir(t, nil)
	if err := os.WriteFile(filepath.Join(dir, "organization.yaml"), []byte("corrupt yaml: [["), 0644); err != nil {
		t.Fatal(err)
	}
	expectDenied(t, newReader(t, dir), "explorarte", governanceRole, governanceProfile)
}
