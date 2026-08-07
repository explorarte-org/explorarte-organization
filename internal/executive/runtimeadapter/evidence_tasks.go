package runtimeadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

const executiveEvidenceSchema = "executive-evidence.v1"

// EvidenceTasks decorates the existing task port. Review and closure tasks get
// a bounded, deterministic evidence bundle recorded before CreateTask returns,
// so the canonical TaskContextProvider includes it in the Context Engine
// snapshot used by the immediately following model invocation.
type EvidenceTasks struct {
	Tasks      Tasks
	Models     executive.ModelCoordinator
	Completion executive.CompletionGate
	Limits     executive.Limits
}

func (e EvidenceTasks) CreateTask(ctx context.Context, command executive.CreateTaskCommand) (executive.TaskRecord, bool, error) {
	task, reused, err := e.Tasks.CreateTask(ctx, command)
	if err != nil {
		return executive.TaskRecord{}, false, err
	}
	if e.Models == nil || e.Completion == nil {
		return task, reused, nil
	}
	if strings.HasPrefix(command.Title, "Department review: ") {
		if err = e.attachDepartmentBundle(ctx, task, command.CorrelationID, strings.TrimPrefix(command.Title, "Department review: ")); err != nil {
			return executive.TaskRecord{}, false, err
		}
		return e.Tasks.GetTask(ctx, task.ID)
	}
	if command.Title == "CEO executive closure" {
		if err = e.attachClosureBundle(ctx, task, command.CorrelationID); err != nil {
			return executive.TaskRecord{}, false, err
		}
		return e.Tasks.GetTask(ctx, task.ID)
	}
	return task, reused, nil
}

type projectedWorker struct {
	TaskID          int64                       `json:"task_id"`
	RoleID          string                      `json:"role_id"`
	Status          string                      `json:"status"`
	Completion      executive.CompletionVerdict `json:"completion"`
	Summary         string                      `json:"summary"`
	EvidenceRefs    []string                    `json:"evidence_refs"`
	TaskEvidence    []string                    `json:"task_evidence_refs"`
	ResponseHash    string                      `json:"response_hash"`
}

type departmentEvidenceBundle struct {
	SchemaVersion string            `json:"schema_version"`
	DepartmentID string            `json:"department_id"`
	Workers      []projectedWorker `json:"workers"`
}

type projectedReview struct {
	TaskID              int64                       `json:"task_id"`
	DepartmentID        string                      `json:"department_id"`
	Status              string                      `json:"status"`
	Completion          executive.CompletionVerdict `json:"completion"`
	Verdict             executive.ReviewVerdict    `json:"verdict"`
	Findings            []string                    `json:"findings"`
	UnsatisfiedCriteria []string                    `json:"unsatisfied_criteria"`
	EvidenceRefs        []string                    `json:"evidence_refs"`
	TaskEvidence        []string                    `json:"task_evidence_refs"`
	ResponseHash        string                      `json:"response_hash"`
}

type closureEvidenceBundle struct {
	SchemaVersion string            `json:"schema_version"`
	Reviews       []projectedReview `json:"reviews"`
	BlockedTasks  []int64           `json:"blocked_tasks,omitempty"`
}

func (e EvidenceTasks) attachDepartmentBundle(ctx context.Context, target executive.TaskRecord, correlation, department string) error {
	all, err := e.Tasks.ListByCorrelation(ctx, correlation)
	if err != nil {
		return err
	}
	workers := make([]projectedWorker, 0)
	for _, task := range all {
		if task.AssignedUnitID != department || !strings.Contains(task.IdempotencyKey, ":worker:"+department+":") {
			continue
		}
		if task.Status != "completed" && task.Status != "no_action" {
			continue
		}
		item, projectErr := e.projectWorker(ctx, task)
		if projectErr != nil {
			return projectErr
		}
		workers = append(workers, item)
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].TaskID < workers[j].TaskID })
	return e.recordBundle(ctx, target.ID, "department:"+department, departmentEvidenceBundle{SchemaVersion: executiveEvidenceSchema, DepartmentID: department, Workers: workers})
}

func (e EvidenceTasks) projectWorker(ctx context.Context, task executive.TaskRecord) (projectedWorker, error) {
	attemptID := latestFinishedAttempt(task.Attempts)
	if attemptID == 0 {
		return projectedWorker{}, fmt.Errorf("completed executive worker %d has no finished attempt", task.ID)
	}
	completion, err := e.Completion.Verify(ctx, task.ID, attemptID)
	if err != nil {
		return projectedWorker{}, err
	}
	if completion.Verdict != executive.CompletionPass {
		return projectedWorker{}, fmt.Errorf("completed executive worker %d no longer verifies: %s", task.ID, completion.Verdict)
	}
	invocations, err := e.Models.FindTaskAttemptInvocations(ctx, task.ID, attemptID)
	if err != nil {
		return projectedWorker{}, err
	}
	if len(invocations) != 1 || invocations[0].Status != "succeeded" {
		return projectedWorker{}, fmt.Errorf("completed executive worker %d lacks one succeeded invocation", task.ID)
	}
	result, err := e.Models.GetResult(ctx, invocations[0].ID)
	if err != nil {
		return projectedWorker{}, err
	}
	parsed, err := executive.ParseWorkerResult(result.JSONOutput, e.effectiveLimits())
	if err != nil {
		return projectedWorker{}, err
	}
	return projectedWorker{
		TaskID: task.ID, RoleID: task.AssignedRoleID, Status: task.Status,
		Completion: completion.Verdict, Summary: truncateBundleString(parsed.Summary, 1200),
		EvidenceRefs: boundedRefs(parsed.EvidenceRefs, 16), TaskEvidence: taskEvidenceRefs(task, 16),
		ResponseHash: result.ResponseHash,
	}, nil
}

func (e EvidenceTasks) attachClosureBundle(ctx context.Context, target executive.TaskRecord, correlation string) error {
	all, err := e.Tasks.ListByCorrelation(ctx, correlation)
	if err != nil {
		return err
	}
	reviews := make([]projectedReview, 0)
	blocked := make([]int64, 0)
	for _, task := range all {
		if task.Status == "blocked" {
			blocked = append(blocked, task.ID)
		}
		if !strings.Contains(task.IdempotencyKey, ":leader-review:") || task.Status != "completed" {
			continue
		}
		item, projectErr := e.projectReview(ctx, task)
		if projectErr != nil {
			return projectErr
		}
		reviews = append(reviews, item)
	}
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].TaskID < reviews[j].TaskID })
	sort.Slice(blocked, func(i, j int) bool { return blocked[i] < blocked[j] })
	return e.recordBundle(ctx, target.ID, "closure", closureEvidenceBundle{SchemaVersion: executiveEvidenceSchema, Reviews: reviews, BlockedTasks: blocked})
}

func (e EvidenceTasks) projectReview(ctx context.Context, task executive.TaskRecord) (projectedReview, error) {
	attemptID := latestFinishedAttempt(task.Attempts)
	if attemptID == 0 {
		return projectedReview{}, fmt.Errorf("completed department review %d has no finished attempt", task.ID)
	}
	completion, err := e.Completion.Verify(ctx, task.ID, attemptID)
	if err != nil {
		return projectedReview{}, err
	}
	if completion.Verdict != executive.CompletionPass {
		return projectedReview{}, fmt.Errorf("completed department review %d no longer verifies: %s", task.ID, completion.Verdict)
	}
	invocations, err := e.Models.FindTaskAttemptInvocations(ctx, task.ID, attemptID)
	if err != nil {
		return projectedReview{}, err
	}
	if len(invocations) != 1 || invocations[0].Status != "succeeded" {
		return projectedReview{}, fmt.Errorf("completed department review %d lacks one succeeded invocation", task.ID)
	}
	result, err := e.Models.GetResult(ctx, invocations[0].ID)
	if err != nil {
		return projectedReview{}, err
	}
	parsed, err := executive.ParseDepartmentReview(result.JSONOutput, e.effectiveLimits())
	if err != nil {
		return projectedReview{}, err
	}
	department := task.AssignedUnitID
	return projectedReview{
		TaskID: task.ID, DepartmentID: department, Status: task.Status, Completion: completion.Verdict,
		Verdict: parsed.Verdict, Findings: boundedRefs(parsed.Findings, 24),
		UnsatisfiedCriteria: boundedRefs(parsed.UnsatisfiedCriteria, 24), EvidenceRefs: boundedRefs(parsed.EvidenceRefs, 24),
		TaskEvidence: taskEvidenceRefs(task, 16), ResponseHash: result.ResponseHash,
	}, nil
}

func (e EvidenceTasks) recordBundle(ctx context.Context, taskID int64, scope string, bundle any) error {
	body, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if len(body) > 14<<10 {
		return fmt.Errorf("executive evidence bundle exceeds 14KiB bound")
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	var decoded any
	if err = json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	return e.Tasks.RecordEvidence(ctx, executive.EvidenceCommand{
		TaskID: taskID, Type: "result", Reference: "executive-evidence:" + scope + ":" + digest[:16],
		Digest: digest, RecordedBy: serviceActor, Metadata: map[string]any{"bundle": decoded}, Satisfies: false,
	})
}

func (e EvidenceTasks) effectiveLimits() executive.Limits {
	if e.Limits.MaxInputBytes <= 0 {
		return executive.DefaultLimits()
	}
	return e.Limits
}

func latestFinishedAttempt(attempts []executive.AttemptRecord) int64 {
	var id int64
	var ordinal int
	for _, attempt := range attempts {
		if attempt.State == "finished" && attempt.Ordinal >= ordinal {
			id = attempt.ID
			ordinal = attempt.Ordinal
		}
	}
	return id
}

func taskEvidenceRefs(task executive.TaskRecord, max int) []string {
	refs := make([]string, 0, len(task.Evidence))
	for _, evidence := range task.Evidence {
		if evidence.Reference != "" {
			refs = append(refs, evidence.Reference)
		}
	}
	sort.Strings(refs)
	return boundedRefs(refs, max)
}

func boundedRefs(values []string, max int) []string {
	if max <= 0 || len(values) <= max {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:max]...)
}

func truncateBundleString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

var _ executive.TaskCoordinator = EvidenceTasks{}
