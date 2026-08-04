package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReportingKeyIsPostgresJSONBCompatible(t *testing.T) {
	t.Parallel()

	line := ReportingLine{
		OrganizationID:  "explorarte",
		RoleID:          "ingenieria_ia/qa",
		ReportsToRoleID: "ingenieria_ia/orquestador",
		Relationship:    "reports_to",
	}
	key := reportingKey(line)
	if strings.ContainsRune(key, '\x00') {
		t.Fatalf("reporting key contains NUL: %q", key)
	}

	body, err := json.Marshal(Diff{
		ReportingLines: EntityChanges{Added: []string{key}},
	})
	if err != nil {
		t.Fatalf("marshal diff: %v", err)
	}
	if strings.Contains(string(body), `\u0000`) {
		t.Fatalf("diff JSON contains PostgreSQL-incompatible Unicode NUL escape: %s", body)
	}
}

func TestReportingKeyIsUnambiguous(t *testing.T) {
	t.Parallel()

	left := reportingKey(ReportingLine{
		OrganizationID: "ab", RoleID: "c", ReportsToRoleID: "d", Relationship: "e",
	})
	right := reportingKey(ReportingLine{
		OrganizationID: "a", RoleID: "bc", ReportsToRoleID: "d", Relationship: "e",
	})
	if left == right {
		t.Fatalf("distinct reporting lines produced the same key: %q", left)
	}
}
