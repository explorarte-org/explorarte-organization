package executive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The CEO closure is shown the code-runner execution the host verified.
//
// Root 1406 (smoke #16, 2026-09-26) was the first campaign to run the whole governed chain: the design
// froze, the mission ran and the code-runner applied the patch and passed GO_BUILD, GO_VET, GO_TEST and
// FITNESS, every operation exiting 0 on one changed file. The host verified that record and satisfied
// code_runner_execution_evidence at 23:51:14.65; the closure was created at 23:51:14.66 from a summary
// that carried the plan's objective, its success criteria and the department reviews -- and nothing of
// the execution. The CEO answered, correctly for what it was shown, that the code-runner had yet to run,
// closed as partial, and the root stopped at executive_closure_not_complete.
//
// The closure summary now carries engineering_execution: the verified record, projected by the host from
// the same evidence ensureRequiredCodeRunnerExecution checks, never from a model's account of it. It
// states what ran and what it returned, the candidate it produced, and which of the mission's own
// requirements are still open, so a pending engineering review is reported as what it is.
type closureExecution struct {
	MissionTaskID  int64              `json:"mission_task_id"`
	MissionStatus  string             `json:"mission_status"`
	MissionState   string             `json:"mission_state"`
	EvidenceRef    string             `json:"evidence_ref"`
	EvidenceDigest string             `json:"evidence_digest"`
	Operations     []closureOperation `json:"operations"`
	Checks         []closureCheck     `json:"checks"`
	ChangedFiles   int64              `json:"changed_files"`
	ChangedPaths   []string           `json:"changed_paths,omitempty"`
	// AppliedPatch is the unified diff the mission applied with git apply (its APPLY_PATCH
	// operations, from the mission's own plan), so a criterion about exactly which lines change
	// can be judged against the change itself. See closureAppliedPatch.
	AppliedPatch               string   `json:"applied_patch,omitempty"`
	CandidateCommit            string   `json:"candidate_commit,omitempty"`
	PendingMissionRequirements []string `json:"pending_mission_requirements,omitempty"`
}

type closureOperation struct {
	Type     string `json:"type"`
	ExitCode *int64 `json:"exit_code,omitempty"`
	Success  bool   `json:"success"`
}

type closureCheck struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Success bool   `json:"success"`
}

// closureEngineeringExecution returns the verified code-runner record for a root that requires one, and
// nil for a root that does not. A root that requires it and whose record does not verify is an error:
// the closure is never created from a summary that omits an execution the root depends on.
func (o *Orchestrator) closureEngineeringExecution(ctx context.Context, root TaskRecord) (*closureExecution, error) {
	requirement, required := requiredRootRequirement(root, CodeRunnerExecutionEvidenceRequirementKey)
	if !required || !requirement.Required {
		return nil, nil
	}
	missionID, err := missionTaskID(root)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCodeRunnerExecutionInvalid, err)
	}
	mission, err := o.tasks.GetTask(ctx, missionID)
	if err != nil {
		return nil, fmt.Errorf("%w: read mission task %d: %v", ErrCodeRunnerExecutionInvalid, missionID, err)
	}
	attemptEvidence, _, err := verifiedCodeRunnerEvidence(mission)
	if err != nil {
		return nil, fmt.Errorf("%w: mission task %d: %v", ErrCodeRunnerExecutionInvalid, mission.ID, err)
	}
	var record struct {
		Operations []struct {
			Type     string `json:"type"`
			ExitCode *int64 `json:"exit_code"`
			Success  bool   `json:"success"`
		} `json:"operations_executed"`
		Checks []struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
			Success bool     `json:"success"`
		} `json:"checks_run"`
		ChangedFiles struct {
			Count int64    `json:"count"`
			Paths []string `json:"paths"`
		} `json:"changed_files"`
		Candidate struct {
			Commit string `json:"candidate_commit"`
		} `json:"candidate_revision"`
	}
	encoded, err := json.Marshal(attemptEvidence.Metadata)
	if err != nil {
		return nil, fmt.Errorf("%w: mission task %d evidence metadata: %v", ErrCodeRunnerExecutionInvalid, mission.ID, err)
	}
	if err = json.Unmarshal(encoded, &record); err != nil {
		return nil, fmt.Errorf("%w: mission task %d evidence metadata: %v", ErrCodeRunnerExecutionInvalid, mission.ID, err)
	}
	execution := &closureExecution{
		MissionTaskID: mission.ID, MissionStatus: mission.Status,
		EvidenceRef: attemptEvidence.Reference, EvidenceDigest: attemptEvidence.Digest,
		Operations: make([]closureOperation, 0, len(record.Operations)), Checks: make([]closureCheck, 0, len(record.Checks)),
		ChangedFiles: record.ChangedFiles.Count, CandidateCommit: record.Candidate.Commit,
	}
	// Paths are shown only when they account for every changed file: a partial list would read as
	// "only these changed".
	if int64(len(record.ChangedFiles.Paths)) == record.ChangedFiles.Count {
		execution.ChangedPaths = record.ChangedFiles.Paths
	}
	for _, operation := range record.Operations {
		execution.Operations = append(execution.Operations, closureOperation{Type: operation.Type, ExitCode: operation.ExitCode, Success: operation.Success})
	}
	for _, check := range record.Checks {
		execution.Checks = append(execution.Checks, closureCheck{Type: check.Type, Command: strings.Join(check.Command, " "), Success: check.Success})
	}
	for _, missionRequirement := range mission.Requirements {
		if missionRequirement.Required && missionRequirement.Status != "satisfied" {
			execution.PendingMissionRequirements = append(execution.PendingMissionRequirements, missionRequirement.Key)
		}
	}
	execution.MissionState = missionStateForClosure(mission.Status, execution.PendingMissionRequirements)
	execution.AppliedPatch = closureAppliedPatch(mission.Instructions)
	return execution, nil
}

// missionStateForClosure says in words what the mission's status means for this root. Root 1428 (smoke
// #17) was shown the raw "awaiting_verification" and read it as "terminal completion is not
// established": a mission whose code-runner run finished and verified waits only for what its pending
// requirements name, and a pending engineering review is the gate before any promotion, which is a
// separate owner decision.
func missionStateForClosure(status string, pending []string) string {
	if status != "awaiting_verification" {
		return "mission status " + status
	}
	if len(pending) == 0 {
		return "the code-runner execution finished and the host verified it; nothing is pending on the mission"
	}
	if len(pending) == 1 && pending[0] == "review" {
		return "the code-runner execution finished and the host verified it; the only thing pending is the independent engineering review that precedes any promotion of the candidate, a separate owner decision"
	}
	return "the code-runner execution finished and the host verified it; still pending on the mission: " + strings.Join(pending, ", ")
}

// closureAppliedPatchBytes bounds the diff shown to the closure; a longer one is cut and says so.
const closureAppliedPatchBytes = 6000

// closureAppliedPatch returns the patches a mission's plan applied, in order, as the closure is shown
// them. Smoke #23 (root 1554, 2026-09-27) was refused because the record said one file changed without
// showing the change, and the goal said exactly one case was added and no other line changed.
//
// The mission task's instructions ARE the code-runner plan (missionplan.Derive encodes it and the
// code-runner parses the same bytes), so the diff shown is the one git apply ran, not a model's
// account of it. gofmt runs after it; changed_paths still bounds which files the sealed candidate
// touched. A plan that cannot be read shows no diff rather than a guessed one.
func closureAppliedPatch(planJSON string) string {
	var plan struct {
		Operations []struct {
			Type  string `json:"type"`
			Patch string `json:"patch"`
		} `json:"operations"`
	}
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return ""
	}
	patches := []string{}
	for _, operation := range plan.Operations {
		if operation.Type == "APPLY_PATCH" && strings.TrimSpace(operation.Patch) != "" {
			patches = append(patches, operation.Patch)
		}
	}
	joined := strings.Join(patches, "\n")
	if len(joined) <= closureAppliedPatchBytes {
		return joined
	}
	cut := closureAppliedPatchBytes
	for cut > 0 && !utf8.RuneStart(joined[cut]) {
		cut--
	}
	return joined[:cut] + fmt.Sprintf("\n[diff cut by the host: first %d of %d bytes shown]", cut, len(joined))
}
