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
		{"wrong department count", "organization.operational_department_count", func(d *parsedDocuments) {
			d.Organization.OperationalDepartments = d.Organization.OperationalDepartments[:3]
		}},
		{"missing leader", "unit.leader_role_missing", func(d *parsedDocuments) {
			d.Organization.OperationalDepartments[0].LeaderRoleID = "comunicaciones/no_existe"
		}},
		{"leader other unit", "unit.leader_wrong_unit", func(d *parsedDocuments) {
			d.Organization.OperationalDepartments[0].LeaderRoleID = "negocio/director_negocio"
			d.LeaderWorker.Departments[0].Leader = "negocio/director_negocio"
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
		{"model egress unknown provider", "model_egress.provider_unknown", func(d *parsedDocuments) { d.ModelEgress.Rules[0].ProviderID = "missing" }},
		{"model egress productive allow", "model_egress.productive_allow_forbidden", func(d *parsedDocuments) {
			// Every current API provider (DeepSeek, Gemini and OpenAI-
			// compatible) is compiled/approved for all three non-hard-denied
			// classifications, so no existing rule can be flipped to allow
			// to exercise this rejection path anymore. Register a synthetic
			// routing policy so the provider is "known" (avoids
			// provider_unknown) but never add it to productiveEgressAllowRules,
			// so its allow rule still hits productive_allow_forbidden.
			trueVal := true
			d.ModelRouting.Policies["test.uncompiled_provider"] = modelPolicyDoc{
				Provider: "uncompiled_test_provider", Model: "test-model", Transport: "http", DirectHTTPForbidden: &trueVal,
			}
			d.ModelEgress.Rules = append(d.ModelEgress.Rules, modelEgressRuleDoc{
				ProviderID: "uncompiled_test_provider", DataClassification: "public", Effect: "allow", ReasonCode: "test_uncompiled_provider",
			})
		}},
		{"model egress hard deny duplicated as rule", "model_egress.hard_deny_rule_conflict", func(d *parsedDocuments) { d.ModelEgress.Rules[0].DataClassification = "secret" }},
		{"model egress missing clinical hard deny", "model_egress.hard_deny_missing", func(d *parsedDocuments) { d.ModelEgress.HardDenies = d.ModelEgress.HardDenies[1:] }},
		{"model invoke wrong grant", "capability.model_invoke_grant_invalid", func(d *parsedDocuments) {
			d.Capabilities.Grants["specialist"] = append(d.Capabilities.Grants["specialist"], "model.invoke")
		}},
		{"model invoke owner deny missing", "capability.model_invoke_owner_deny_missing", func(d *parsedDocuments) { d.Capabilities.HardDenies["owner"] = nil }},
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
