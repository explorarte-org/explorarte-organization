package contextprovider

import (
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

func TestTaskContextRendersOnlyExecutiveEvidenceMetadata(t *testing.T) {
	detail := tasks.TaskDetail{Task: tasks.Task{
		ID: 7, OrganizationID: "explorarte", OrganizationRevisionID: 1,
		AssignedRoleID: "ingenieria_ia/orquestador", AssignedUnitID: "ingenieria_ia",
		Title: "Department review: ingenieria_ia", Instructions: "review", Status: tasks.StatusReady,
		Version: 1, RequestHash: "hash",
	}}
	detail.Evidence = []tasks.Evidence{
		{ID: 1, TaskID: 7, Type: tasks.RequirementResult, Reference: "executive-evidence:department:ingenieria_ia:abc", Metadata: map[string]any{"bundle": map[string]any{"summary": "visible-r23-bundle"}}},
		{ID: 2, TaskID: 7, Type: tasks.RequirementResult, Reference: "unrelated:evidence", Metadata: map[string]any{"secretish": "must-not-enter-context"}},
	}
	record, err := sourceRecord(detail)
	if err != nil {
		t.Fatal(err)
	}
	body := string(record.Content)
	if !strings.Contains(body, "visible-r23-bundle") {
		t.Fatalf("executive evidence metadata missing: %s", body)
	}
	if strings.Contains(body, "must-not-enter-context") || strings.Contains(body, "secretish") {
		t.Fatalf("unrelated evidence metadata leaked into task context: %s", body)
	}
	if record.MayGrantCapabilities {
		t.Fatal("task context metadata must never grant capabilities")
	}
}
