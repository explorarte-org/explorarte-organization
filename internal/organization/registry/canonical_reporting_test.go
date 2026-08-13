package registry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The consolidation of comunicaciones/creativo/finanzas/marketing into a
// single `negocio` department left two organizational models superimposed:
// leader-worker-map.yaml and negocio/AGENT.md described one authority
// namespace under negocio/director_negocio, while role-catalog.yaml still
// encoded the pre-consolidation chain in reports_to -- copywriter reporting
// to disenador, and twelve more like it. The shadow verifier detected the
// contradiction; nothing prevented it from being introduced.
//
// These tests close that gap at the source. They deliberately do NOT encode
// "every worker always reports directly to its leader" as a universal law of
// the architecture: they derive the expectation from leader-worker-map.yaml,
// which is the document that actually declares who leads whom. If a future
// organization legitimately introduces intermediate authority, that document
// changes first and these tests follow it, instead of blocking it.

type canonicalLeaderWorkerMap struct {
	Departments []struct {
		ID      string   `yaml:"id"`
		Leader  string   `yaml:"leader"`
		Workers []string `yaml:"workers"`
	} `yaml:"departments"`
}

type canonicalRoleCatalog struct {
	Roles []struct {
		ID                  string   `yaml:"id"`
		ReportsTo           []string `yaml:"reports_to"`
		ReportsToSourceText *string  `yaml:"reports_to_source_text"`
	} `yaml:"roles"`
}

func canonicalDocPath(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "..", "docs", "canonical"}, parts...)...)
}

func loadCanonicalLeaderWorkerMap(t *testing.T) canonicalLeaderWorkerMap {
	t.Helper()
	body, err := os.ReadFile(canonicalDocPath("leader-worker-map.yaml"))
	if err != nil {
		t.Fatalf("read leader-worker-map.yaml: %v", err)
	}
	var value canonicalLeaderWorkerMap
	if err := yaml.Unmarshal(body, &value); err != nil {
		t.Fatalf("parse leader-worker-map.yaml: %v", err)
	}
	if len(value.Departments) == 0 {
		t.Fatal("leader-worker-map.yaml declares no departments")
	}
	return value
}

func loadCanonicalReportsTo(t *testing.T) map[string][]string {
	t.Helper()
	body, err := os.ReadFile(canonicalDocPath("role-catalog.yaml"))
	if err != nil {
		t.Fatalf("read role-catalog.yaml: %v", err)
	}
	var value canonicalRoleCatalog
	if err := yaml.Unmarshal(body, &value); err != nil {
		t.Fatalf("parse role-catalog.yaml: %v", err)
	}
	reportsTo := make(map[string][]string, len(value.Roles))
	for _, role := range value.Roles {
		reportsTo[role.ID] = role.ReportsTo
	}
	return reportsTo
}

// TestDeclaredWorkersReportToTheirDeclaredLeader is the authority-graph
// control. Every role that leader-worker-map.yaml lists as a worker of a
// department must carry a formal reports_to edge to that same department's
// declared leader. Both sides are read from canonical documents; nothing is
// hardcoded here.
func TestDeclaredWorkersReportToTheirDeclaredLeader(t *testing.T) {
	departments := loadCanonicalLeaderWorkerMap(t)
	reportsTo := loadCanonicalReportsTo(t)

	for _, department := range departments.Departments {
		for _, worker := range department.Workers {
			edges, known := reportsTo[worker]
			if !known {
				t.Errorf("%s: leader-worker-map lists worker %q, absent from role-catalog.yaml",
					department.ID, worker)
				continue
			}
			var found bool
			for _, edge := range edges {
				if edge == department.Leader {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: worker %q has reports_to %v, missing its declared leader %q. "+
					"reports_to is formal authority; functional coordination belongs in "+
					"reports_to_source_text and the department AGENT.md, not here.",
					department.ID, worker, edges, department.Leader)
			}
		}
	}
}

// TestNoIntermediateAuthorityInsideADepartment states the other half of the
// same contract: a worker must not additionally report to a peer worker of
// its own department. Without this, the test above is satisfiable by adding
// the leader edge while leaving the superseded chain in place, which is
// precisely the state this branch found.
func TestNoIntermediateAuthorityInsideADepartment(t *testing.T) {
	departments := loadCanonicalLeaderWorkerMap(t)
	reportsTo := loadCanonicalReportsTo(t)

	for _, department := range departments.Departments {
		peers := make(map[string]struct{}, len(department.Workers))
		for _, worker := range department.Workers {
			peers[worker] = struct{}{}
		}
		for _, worker := range department.Workers {
			for _, edge := range reportsTo[worker] {
				if _, isPeer := peers[edge]; isPeer {
					t.Errorf("%s: worker %q reports to peer worker %q. A department has one "+
						"canonical leader; a peer coordinating this role is functional, not "+
						"authority, and must not appear as a reports_to edge.",
						department.ID, worker, edge)
				}
			}
		}
	}
}

// TestRoleProfilesDoNotContradictFormalAuthority is the behavioral half the
// review asked for: reverting a single PERFIL.md to the pre-consolidation
// wording must fail.
//
// It matters that this is asserted separately. PERFIL.md is not parsed into
// the reporting graph -- the registry builds that from role-catalog.yaml
// alone -- so the authority tests above cannot see profile drift at all. But
// a profile IS assembled into the role's context as an authoritative role
// instruction, so a profile still claiming "Reporta a: negocio/disenador"
// tells the agent it answers to someone the organization says it does not.
// Wrong instructions to the model are not a documentation nit.
func TestRoleProfilesDoNotContradictFormalAuthority(t *testing.T) {
	departments := loadCanonicalLeaderWorkerMap(t)
	reportsTo := loadCanonicalReportsTo(t)
	repoRoot := filepath.Join("..", "..", "..")

	for _, department := range departments.Departments {
		for _, worker := range department.Workers {
			profilePath := filepath.Join(repoRoot, worker, "PERFIL.md")
			body, err := os.ReadFile(profilePath)
			if err != nil {
				continue // roles without a profile are covered by source_status elsewhere
			}
			section := reportaHeadingSection(string(body))
			if section == "" {
				continue
			}
			for _, candidate := range department.Workers {
				if candidate == worker || !strings.Contains(section, candidate) {
					continue
				}
				var declared bool
				for _, edge := range reportsTo[worker] {
					if edge == candidate {
						declared = true
						break
					}
				}
				if !declared {
					t.Errorf("%s: its \"Reporta a\" section names peer %q, which is not a formal "+
						"reports_to edge in role-catalog.yaml. Functional coordination belongs "+
						"under its own heading, not under \"Reporta a\".", profilePath, candidate)
				}
			}
			if !strings.Contains(section, department.Leader) {
				t.Errorf("%s: its \"Reporta a\" section does not name the declared leader %q",
					profilePath, department.Leader)
			}
		}
	}
}

// reportaHeadingSection returns the body of the "## Reporta a" section, or "" when
// the profile has none.
func reportaHeadingSection(body string) string {
	const heading = "## Reporta a"
	start := strings.Index(body, heading)
	if start < 0 {
		return ""
	}
	rest := body[start+len(heading):]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		return rest[:next]
	}
	return rest
}
