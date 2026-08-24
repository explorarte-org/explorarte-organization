package contextprovider

import (
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// RETRY CONTEXT PROOF (R7 fix).
//
// The orchestrator closes a contract-rejected attempt with
// RecordAttemptFailed(..., "model_result_contract_rejected", reason, ...)
// which persists `reason` as the attempt's result_summary. TaskContextProvider
// renders every prior attempt back into the task context the NEXT attempt
// builds from -- the transport AUTONOMY-SMOKE-017-R7 showed carries
// result_summary. This guard pins that the measured rejection format
// ("invalid summary: 4200 UTF-8 bytes exceeds maximum 4000") survives that
// transport verbatim, so the retry can act on the measurement instead of
// guessing.
func TestMeasuredContractRejectionReachesTheNextAttemptContext(t *testing.T) {
	reason := "contract rejected: invalid summary: 4200 UTF-8 bytes exceeds maximum 4000"
	detail := tasks.TaskDetail{Task: tasks.Task{
		ID: 7, OrganizationID: "explorarte", OrganizationRevisionID: 1,
		AssignedRoleID: "ingenieria_ia/orquestador", AssignedUnitID: "ingenieria_ia",
		Title: "Worker task", Instructions: "work", Status: tasks.StatusReady,
		Version: 1, RequestHash: "hash",
	}}
	detail.Attempts = []tasks.Attempt{{
		ID: 1, TaskID: 7, Ordinal: 1, State: tasks.AttemptFailed, WorkerID: "service",
		ResultSummary: &reason, FailureCode: strPtr("model_result_contract_rejected"),
	}}
	record, err := sourceRecord(detail)
	if err != nil {
		t.Fatal(err)
	}
	body := string(record.Content)
	for _, want := range []string{"result_summary", "invalid summary", "4200", "4000", "UTF-8 bytes", "model_result_contract_rejected"} {
		if !strings.Contains(body, want) {
			t.Fatalf("next attempt's context must carry the measured reason (%q missing): %s", want, body)
		}
	}
}

func strPtr(v string) *string { return &v }
