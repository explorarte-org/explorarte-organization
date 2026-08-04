package registry

import (
	"gopkg.in/yaml.v3"
	"testing"
)

func TestCrossValidationFailures(t *testing.T) {
	base := loadParsedForMutation(t)
	tests := []struct {
		name, code string
		mutate     func(*parsedDocuments)
	}{
		{"seven departments", "organization.operational_department_count", func(d *parsedDocuments) {
			d.Organization.OperationalDepartments = d.Organization.OperationalDepartments[:6]
		}},
		{"missing leader", "unit.leader_role_missing", func(d *parsedDocuments) {
			d.Organization.OperationalDepartments[0].LeaderRoleID = "comunicaciones/no_existe"
		}},
		{"leader other unit", "unit.leader_wrong_unit", func(d *parsedDocuments) {
			d.Organization.OperationalDepartments[0].LeaderRoleID = "creativo/disenador"
			d.LeaderWorker.Departments[0].Leader = "creativo/disenador"
		}},
		{"missing worker", "leader_map.worker_missing", func(d *parsedDocuments) {
			d.LeaderWorker.Departments[0].Workers = append(d.LeaderWorker.Departments[0].Workers, "comunicaciones/no_existe")
		}},
		{"missing reports to", "reporting.target_missing", func(d *parsedDocuments) { d.Roles.Roles[0].ReportsTo = []string{"empresa/no_existe"} }},
		{"self report", "reporting.self_reference", func(d *parsedDocuments) { d.Roles.Roles[0].ReportsTo = []string{d.Roles.Roles[0].ID} }},
		{"cycle", "reporting.cycle", func(d *parsedDocuments) {
			roleA := &d.Roles.Roles[0]
			roleB := &d.Roles.Roles[1]
			roleA.ReportsTo = []string{roleB.ID}
			roleB.ReportsTo = []string{roleA.ID}
		}},
		{"unknown model", "role.model_policy_unknown", func(d *parsedDocuments) { value := "missing.policy"; d.Roles.Roles[0].ModelPolicy = &value }},
		{"unknown authority", "role.authority_class_unknown", func(d *parsedDocuments) { d.Roles.Roles[0].AuthorityClass = "missing" }},
		{"counts", "counts.imported_profiles", func(d *parsedDocuments) { d.Organization.Counts.ImportedProfiles++ }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			documents := cloneParsed(t, base)
			tc.mutate(&documents)
			normalizeDocuments(&documents)
			snapshot := materialize(documents)
			report := validateDocuments(documents, snapshot)
			if !hasIssue(report, tc.code) {
				t.Fatalf("wanted issue %s, got %+v", tc.code, report)
			}
		})
	}
}

func TestProposedProfilesStayDisabled(t *testing.T) {
	snapshot := loadTestSnapshot(t, canonicalDirForTest(t))
	for _, role := range snapshot.Roles {
		if role.SourceStatus == "proposed_profile_required" && (role.Enabled || role.Executable) {
			t.Fatalf("proposed role enabled: %+v", role)
		}
	}
}

func loadParsedForMutation(t *testing.T) parsedDocuments {
	t.Helper()
	loader := mustLoader(t, canonicalDirForTest(t))
	documents, _, err := loader.readDocuments()
	if err != nil {
		t.Fatal(err)
	}
	return documents
}
func cloneParsed(t *testing.T, value parsedDocuments) parsedDocuments {
	t.Helper()
	body, err := yamlMarshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out parsedDocuments
	if err = yamlUnmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func hasIssue(report ValidationReport, code string) bool {
	for _, issue := range append(report.Errors, report.Warnings...) {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func yamlMarshal(value any) ([]byte, error)      { return yaml.Marshal(value) }
func yamlUnmarshal(body []byte, value any) error { return yaml.Unmarshal(body, value) }
