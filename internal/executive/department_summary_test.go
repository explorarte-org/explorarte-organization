package executive

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBoundedDepartmentSummaryReportsFailedWorkersHostGrounded covers the
// FASE C evidence question this round's worker-terminal-isolation fix
// depends on: once driveDepartments lets a department reach review despite
// a terminal worker failure, does the reviewer actually get told? The
// mechanism boundedDepartmentSummary already builds (and createReviewTask
// already embeds into the review task's own instructions) turns out to
// already answer it correctly -- this test locks that in.
func TestBoundedDepartmentSummaryReportsFailedWorkersHostGrounded(t *testing.T) {
	const root, dept = int64(1), "ingenieria_ia"
	all := []TaskRecord{
		{
			ID: 10, AssignedRoleID: dept + "/qa", Status: "completed",
			IdempotencyKey: childKey(root, "worker:"+dept+":a"),
			Attempts:       []AttemptRecord{{Ordinal: 1, State: "finished"}},
		},
		{
			ID: 11, AssignedRoleID: dept + "/qa", Status: "failed", ReasonCode: "model_invocation_failed",
			IdempotencyKey: childKey(root, "worker:"+dept+":b"),
			Attempts:       []AttemptRecord{{Ordinal: 1, State: "failed"}},
		},
	}
	summary := boundedDepartmentSummary(all, root, dept, 1<<16)

	var decoded struct {
		DepartmentID string `json:"department_id"`
		Tasks        []struct {
			TaskID int64  `json:"task_id"`
			Role   string `json:"role"`
			Status string `json:"status"`
			Result string `json:"result_summary,omitempty"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(summary), &decoded); err != nil {
		t.Fatalf("summary is not valid JSON: %v\n%s", err, summary)
	}
	if len(decoded.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in the summary, got %d: %s", len(decoded.Tasks), summary)
	}
	var completedEntry, failedEntry *struct {
		TaskID int64  `json:"task_id"`
		Role   string `json:"role"`
		Status string `json:"status"`
		Result string `json:"result_summary,omitempty"`
	}
	for i := range decoded.Tasks {
		switch decoded.Tasks[i].TaskID {
		case 10:
			completedEntry = &decoded.Tasks[i]
		case 11:
			failedEntry = &decoded.Tasks[i]
		}
	}
	if completedEntry == nil || completedEntry.Status != "completed" || completedEntry.Result != "verified result recorded" {
		t.Fatalf("completed worker entry wrong: %+v", completedEntry)
	}
	if failedEntry == nil || failedEntry.Status != "failed" {
		t.Fatalf("failed worker entry must report its real durable status, got: %+v", failedEntry)
	}
	// The failure must never be dressed up as a success: no "verified
	// result recorded" (or any non-empty result_summary at all) for a
	// worker whose only attempt failed -- the summary text distinguishing
	// "failed" from "completed" is the whole point of this being
	// host-grounded rather than a synthetic WorkerResult.
	if failedEntry.Result != "" {
		t.Fatalf("a failed worker must not carry a synthetic result_summary, got %q", failedEntry.Result)
	}
	if strings.Contains(summary, `"result_summary":"verified result recorded"`) && strings.Count(summary, `"result_summary"`) != 1 {
		t.Fatalf("only the completed worker may carry the success marker: %s", summary)
	}
}
