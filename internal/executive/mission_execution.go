package executive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
)

// CodeRunnerExecutionEvidenceRequirementKey opts a campaign into the
// host-owned execution barrier. Merely provisioning an EngineeringMission is
// not execution evidence: the CodeRunner task must produce its own durable
// attempt evidence before Executive may create or accept a closure.
const CodeRunnerExecutionEvidenceRequirementKey = "code_runner_execution_evidence"

var (
	ErrCodeRunnerExecutionPending = errors.New("code-runner execution evidence is pending")
	ErrCodeRunnerExecutionInvalid = errors.New("code-runner execution evidence is invalid")
	ErrCodeRunnerExecutionFailed  = errors.New("code-runner execution failed")
)

const (
	engineeringMissionReferencePrefix = "engineering-mission://"
	codeRunnerEvidenceReferencePrefix = "code-runner-attempt-evidence://task/"
	codeRunnerEvidenceSchema          = "code-runner-attempt-evidence/v1"
)

// ensureRequiredCodeRunnerExecution is the host-owned barrier for campaigns
// that explicitly require code-runner execution evidence. It reads the
// mission task and its durable evidence; it never treats a model summary,
// implementation plan, or the mere existence of a mission as execution.
//
// A result requirement is satisfied from the immutable CodeRunner evidence
// row. An artifact requirement is satisfied from the real staging manifest
// recorded against the same CodeRunner task, while the evidence row is still
// fully validated here. This keeps Completion's artifact checker on its real
// artifact:// contract instead of inventing a new artifact namespace.
func (o *Orchestrator) ensureRequiredCodeRunnerExecution(ctx context.Context, suppliedRoot TaskRecord) error {
	root, err := o.tasks.GetTask(ctx, suppliedRoot.ID)
	if err != nil {
		return err
	}
	requirement, required := requiredRootRequirement(root, CodeRunnerExecutionEvidenceRequirementKey)
	if !required || !requirement.Required {
		return nil
	}

	missionID, err := missionTaskID(root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCodeRunnerExecutionInvalid, err)
	}
	mission, err := o.tasks.GetTask(ctx, missionID)
	if err != nil {
		return fmt.Errorf("%w: read mission task %d: %v", ErrCodeRunnerExecutionInvalid, missionID, err)
	}
	if mission.AssignedRoleID != engineeringmission.CodeRunnerRole {
		return fmt.Errorf("%w: mission task %d is assigned to %q, want %q", ErrCodeRunnerExecutionInvalid, mission.ID, mission.AssignedRoleID, engineeringmission.CodeRunnerRole)
	}
	if missionFailed(mission.Status) {
		return fmt.Errorf("%w: mission task %d status=%s reason=%s", ErrCodeRunnerExecutionFailed, mission.ID, mission.Status, mission.ReasonCode)
	}
	if missionPending(mission.Status) {
		return ErrCodeRunnerExecutionPending
	}

	attemptEvidence, manifestEvidence, err := verifiedCodeRunnerEvidence(mission)
	if err != nil {
		if missionPending(mission.Status) {
			return ErrCodeRunnerExecutionPending
		}
		return fmt.Errorf("%w: mission task %d: %v", ErrCodeRunnerExecutionInvalid, mission.ID, err)
	}

	if requirement.Type != "result" && requirement.Type != "artifact" {
		return fmt.Errorf("%w: root requirement %q has unsupported type %q", ErrCodeRunnerExecutionInvalid, requirement.Key, requirement.Type)
	}
	reference, digest, evidenceType := attemptEvidence.Reference, attemptEvidence.Digest, requirement.Type
	if requirement.Type == "artifact" {
		if manifestEvidence.Reference == "" || manifestEvidence.Digest == "" {
			return fmt.Errorf("%w: mission task %d has no real staging manifest evidence", ErrCodeRunnerExecutionInvalid, mission.ID)
		}
		reference, digest = manifestEvidence.Reference, manifestEvidence.Digest
	}
	metadata := map[string]any{
		"mission_task_id":                mission.ID,
		"code_runner_evidence_reference": attemptEvidence.Reference,
		"code_runner_evidence_digest":    attemptEvidence.Digest,
		"code_runner_attempt_id":         codeRunnerAttemptID(attemptEvidence),
	}
	if requirement.Status != "satisfied" {
		if err := o.tasks.RecordEvidence(ctx, EvidenceCommand{
			TaskID: root.ID, RequirementID: requirement.ID, Type: evidenceType,
			Reference: reference, Digest: digest, RecordedBy: orchestratorWorkerID,
			Metadata: metadata, Satisfies: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

func requiredRootRequirement(root TaskRecord, key string) (RequirementRecord, bool) {
	for _, requirement := range root.Requirements {
		if requirement.Key == key {
			return requirement, true
		}
	}
	return RequirementRecord{}, false
}

func missionTaskID(root TaskRecord) (int64, error) {
	var found int64
	for _, evidence := range root.Evidence {
		if !strings.HasPrefix(evidence.Reference, engineeringMissionReferencePrefix) {
			continue
		}
		value := strings.TrimPrefix(evidence.Reference, engineeringMissionReferencePrefix)
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("invalid engineering mission reference %q", evidence.Reference)
		}
		if found != 0 && found != id {
			return 0, fmt.Errorf("conflicting engineering mission references %d and %d", found, id)
		}
		found = id
	}
	if found == 0 {
		return 0, errors.New("implementation-mission has no durable engineering mission reference")
	}
	return found, nil
}

func missionPending(status string) bool {
	switch status {
	case "pending", "ready", "leased", "running", "retry_wait":
		return true
	default:
		return false
	}
}

func missionFailed(status string) bool {
	switch status {
	case "failed", "blocked", "dead_letter", "cancelled", "rejected", "no_action":
		return true
	default:
		return false
	}
}

// verifiedCodeRunnerEvidence validates the execution record generated by the
// CodeRunner worker. The checks are intentionally stricter than checking a
// successful attempt summary: all four host-declared gates must be present
// and successful, the sealed workspace must be identified, and every
// operation must report success without truncation.
func verifiedCodeRunnerEvidence(task TaskRecord) (EvidenceRecord, EvidenceRecord, error) {
	if len(task.Attempts) == 0 {
		return EvidenceRecord{}, EvidenceRecord{}, errors.New("no CodeRunner attempt exists")
	}
	attempt := task.Attempts[len(task.Attempts)-1]
	if attempt.State != "finished" || attempt.FailureCode != "" {
		return EvidenceRecord{}, EvidenceRecord{}, fmt.Errorf("latest attempt %d is not a successful finished attempt", attempt.ID)
	}
	expectedReference := fmt.Sprintf("%s%d/attempt/%d", codeRunnerEvidenceReferencePrefix, task.ID, attempt.ID)
	var found EvidenceRecord
	for _, evidence := range task.Evidence {
		if evidence.Reference != expectedReference {
			continue
		}
		// Promotion records a separate check requirement evidence row that
		// points at this same immutable result reference. It is a
		// verification link, not a second CodeRunner execution. Only the
		// result row is eligible for the attempt-evidence uniqueness check.
		if evidence.Type != "result" {
			continue
		}
		if found.Reference != "" {
			return EvidenceRecord{}, EvidenceRecord{}, fmt.Errorf("duplicate attempt evidence for attempt %d", attempt.ID)
		}
		found = evidence
	}
	if found.Reference == "" || len(found.Digest) != 64 {
		return EvidenceRecord{}, EvidenceRecord{}, errors.New("durable attempt evidence or digest is missing")
	}
	if err := validateAttemptEvidenceMetadata(found.Metadata, task.ID, attempt.ID); err != nil {
		return EvidenceRecord{}, EvidenceRecord{}, err
	}
	manifest, err := candidateManifestEvidence(task)
	if err != nil {
		return EvidenceRecord{}, EvidenceRecord{}, err
	}
	return found, manifest, nil
}

func candidateManifestEvidence(task TaskRecord) (EvidenceRecord, error) {
	var artifactRequirementID int64
	for _, requirement := range task.Requirements {
		if requirement.Key == "candidate-artifact" {
			artifactRequirementID = requirement.ID
			if requirement.Status != "satisfied" {
				return EvidenceRecord{}, errors.New("candidate-artifact requirement is not satisfied")
			}
			break
		}
	}
	if artifactRequirementID == 0 {
		return EvidenceRecord{}, errors.New("candidate-artifact requirement is missing")
	}
	var found EvidenceRecord
	for _, evidence := range task.Evidence {
		if evidence.RequirementID != artifactRequirementID || !strings.HasPrefix(evidence.Reference, "artifact://sha256/") {
			continue
		}
		if found.Reference != "" {
			return EvidenceRecord{}, errors.New("duplicate candidate-artifact evidence")
		}
		found = evidence
	}
	if found.Reference == "" || len(found.Digest) != 64 {
		return EvidenceRecord{}, errors.New("candidate-artifact manifest reference or digest is missing")
	}
	return found, nil
}

func validateAttemptEvidenceMetadata(metadata map[string]any, taskID, attemptID int64) error {
	if metadata == nil {
		return errors.New("attempt evidence metadata is empty")
	}
	if value, ok := metadata["schema_version"].(string); !ok || value != codeRunnerEvidenceSchema {
		return fmt.Errorf("unexpected attempt evidence schema")
	}
	if value, ok := metadataInt64(metadata["task_id"]); !ok || value != taskID {
		return errors.New("attempt evidence task_id does not match mission task")
	}
	if value, ok := metadataInt64(metadata["attempt_id"]); !ok || value != attemptID {
		return errors.New("attempt evidence attempt_id does not match latest attempt")
	}
	candidate, ok := metadata["candidate_revision"].(map[string]any)
	if !ok {
		return errors.New("attempt evidence has no candidate_revision")
	}
	if value, ok := metadataInt64(candidate["workspace_id"]); !ok || value <= 0 {
		return errors.New("attempt evidence has no valid sealed workspace")
	}
	operations, ok := metadata["operations_executed"].([]any)
	if !ok || len(operations) == 0 {
		return errors.New("attempt evidence has no executed operations")
	}
	for index, raw := range operations {
		operation, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("operation %d evidence is malformed", index+1)
		}
		if success, ok := operation["success"].(bool); !ok || !success {
			return fmt.Errorf("operation %d did not succeed", index+1)
		}
		if truncated, ok := operation["truncated"].(bool); ok && truncated {
			return fmt.Errorf("operation %d output was truncated", index+1)
		}
	}
	checks, ok := metadata["checks_run"].([]any)
	if !ok {
		return errors.New("attempt evidence has no checks_run list")
	}
	expected := map[string]bool{"GO_BUILD": false, "GO_VET": false, "GO_TEST": false, "FITNESS": false}
	for index, raw := range checks {
		check, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("check %d evidence is malformed", index+1)
		}
		name, _ := check["type"].(string)
		if _, known := expected[name]; !known {
			continue
		}
		if expected[name] {
			return fmt.Errorf("duplicate %s check evidence", name)
		}
		success, ok := check["success"].(bool)
		if !ok || !success {
			return fmt.Errorf("%s check did not succeed", name)
		}
		expected[name] = true
	}
	for name, seen := range expected {
		if !seen {
			return fmt.Errorf("required %s check evidence is missing", name)
		}
	}
	return nil
}

func metadataInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), v == float64(int64(v))
	case json.Number:
		parsed, err := v.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func codeRunnerAttemptID(evidence EvidenceRecord) int64 {
	parts := strings.Split(strings.TrimPrefix(evidence.Reference, codeRunnerEvidenceReferencePrefix), "/attempt/")
	if len(parts) != 2 {
		return 0
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	return id
}

// validateCodeRunnerExecutivePlan is an explicit host contract for the
// budgeted audit smoke. The requirement opts a goal into one bounded
// implementation line; the model cannot widen it by returning several
// departments or by routing the line to another unit.
func validateCodeRunnerExecutivePlan(root TaskRecord, plan ExecutivePlan) error {
	requirement, required := requiredRootRequirement(root, CodeRunnerExecutionEvidenceRequirementKey)
	if !required || !requirement.Required {
		return nil
	}
	if len(plan.DepartmentRequests) != 1 {
		return fmt.Errorf("%w: code-runner audit requires exactly one department, got %d", ErrCodeRunnerExecutionInvalid, len(plan.DepartmentRequests))
	}
	if plan.DepartmentRequests[0].UnitID != "ingenieria_ia" {
		return fmt.Errorf("%w: code-runner audit must be assigned to ingenieria_ia, got %q", ErrCodeRunnerExecutionInvalid, plan.DepartmentRequests[0].UnitID)
	}
	return nil
}

// validateCodeRunnerDepartmentPlan applies the same one-line boundary at the
// department-plan layer. This prevents a compliant CEO plan from expanding
// into parallel cognitive tasks before the host-owned EngineeringMission is
// created.
func validateCodeRunnerDepartmentPlan(root TaskRecord, departmentID string, plan DepartmentPlan) error {
	requirement, required := requiredRootRequirement(root, CodeRunnerExecutionEvidenceRequirementKey)
	if !required || !requirement.Required {
		return nil
	}
	if departmentID != "ingenieria_ia" {
		return fmt.Errorf("%w: code-runner audit department must be ingenieria_ia, got %q", ErrCodeRunnerExecutionInvalid, departmentID)
	}
	if len(plan.Tasks) != 1 {
		return fmt.Errorf("%w: code-runner audit requires exactly one department task, got %d", ErrCodeRunnerExecutionInvalid, len(plan.Tasks))
	}
	return nil
}
