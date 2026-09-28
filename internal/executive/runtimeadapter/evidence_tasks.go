package runtimeadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

const executiveEvidenceSchema = "executive-evidence.v1"

// executiveEvidenceBundleBytes bounds one recorded bundle. It was 14KiB while a worker summary was
// cut to 1200 bytes; a review now sees each summary whole (up to MaxWorkerSummaryBytes, 8000), so a
// round with its worker, a redo and a support task needs room for several. 64KiB is still a small
// share of the context a review is assembled into.
const executiveEvidenceBundleBytes = 64 << 10

// EvidenceTasks decorates the existing task port. Review and closure tasks get
// a bounded, deterministic evidence bundle recorded before CreateTask returns,
// so the canonical TaskContextProvider includes it in the Context Engine
// snapshot used by the immediately following model invocation.
type EvidenceTasks struct {
	Tasks
	Models     executive.ModelInvocationReader
	Completion executive.CompletionGate
	Limits     executive.Limits
}

func (e EvidenceTasks) CreateTask(ctx context.Context, command executive.CreateTaskCommand) (executive.TaskRecord, bool, error) {
	task, reused, err := e.Tasks.CreateTask(ctx, command)
	if err != nil {
		return executive.TaskRecord{}, false, err
	}
	if e.Models == nil || e.Completion == nil || hasExecutiveBundle(task) {
		return task, reused, nil
	}
	if strings.HasPrefix(command.Title, "Department review: ") {
		if err = e.attachDepartmentBundle(ctx, task, command.CorrelationID, strings.TrimPrefix(command.Title, "Department review: "), command.ReviewScope); err != nil {
			return executive.TaskRecord{}, false, err
		}
		refreshed, getErr := e.Tasks.GetTask(ctx, task.ID)
		return refreshed, reused, getErr
	}
	if command.Title == "CEO executive closure" {
		if err = e.attachClosureBundle(ctx, task, command.CorrelationID); err != nil {
			return executive.TaskRecord{}, false, err
		}
		refreshed, getErr := e.Tasks.GetTask(ctx, task.ID)
		return refreshed, reused, getErr
	}
	return task, reused, nil
}

type projectedWorker struct {
	TaskID       int64                       `json:"task_id"`
	RoleID       string                      `json:"role_id"`
	Status       string                      `json:"status"`
	Completion   executive.CompletionVerdict `json:"completion"`
	Summary      string                      `json:"summary"`
	EvidenceRefs []string                    `json:"evidence_refs"`
	TaskEvidence []string                    `json:"task_evidence_refs"`
	ResponseHash string                      `json:"response_hash"`
}

type departmentEvidenceBundle struct {
	SchemaVersion    string            `json:"schema_version"`
	DepartmentID     string            `json:"department_id"`
	ReviewCriteria   []string          `json:"review_criteria"`
	PlanResponseHash string            `json:"plan_response_hash"`
	Workers          []projectedWorker `json:"workers"`
}

type projectedReview struct {
	TaskID              int64                       `json:"task_id"`
	DepartmentID        string                      `json:"department_id"`
	Status              string                      `json:"status"`
	Completion          executive.CompletionVerdict `json:"completion"`
	Verdict             executive.ReviewVerdict     `json:"verdict"`
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

// A scoped review (executive.DepartmentReviewScope) is shown its round's plan
// and exactly the workers the orchestrator says it judges; without a scope the
// bundle keeps its historical shape: the first plan and every worker.
func (e EvidenceTasks) attachDepartmentBundle(ctx context.Context, target executive.TaskRecord, correlation, department string, scope *executive.DepartmentReviewScope) error {
	all, err := e.Tasks.ListByCorrelation(ctx, correlation)
	if err != nil {
		return err
	}
	var planTaskID int64
	if scope != nil {
		planTaskID = scope.PlanTaskID
	}
	criteria, planHash, err := e.projectDepartmentPlan(ctx, all, department, planTaskID)
	if err != nil {
		return err
	}
	workers := make([]projectedWorker, 0)
	for _, task := range bundledWorkers(all, department, scope) {
		item, projectErr := e.projectWorker(ctx, task)
		if projectErr != nil {
			return projectErr
		}
		workers = append(workers, item)
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].TaskID < workers[j].TaskID })
	bundle := departmentEvidenceBundle{
		SchemaVersion: executiveEvidenceSchema, DepartmentID: department,
		ReviewCriteria: boundedRefs(criteria, 32), PlanResponseHash: planHash, Workers: workers,
	}
	return e.recordBundle(ctx, target.ID, "department:"+department, bundle)
}

// bundledWorkers returns the completed workers of department a review's bundle
// projects: those scope names, or, unscoped, every worker of the department.
func bundledWorkers(all []executive.TaskRecord, department string, scope *executive.DepartmentReviewScope) []executive.TaskRecord {
	var inScope map[int64]bool
	if scope != nil {
		inScope = make(map[int64]bool, len(scope.WorkerTaskIDs))
		for _, id := range scope.WorkerTaskIDs {
			inScope[id] = true
		}
	}
	out := []executive.TaskRecord{}
	for _, task := range all {
		if task.AssignedUnitID != department || !strings.Contains(task.IdempotencyKey, ":worker:"+department+":") {
			continue
		}
		if inScope != nil && !inScope[task.ID] {
			continue
		}
		if task.Status != "completed" && task.Status != "no_action" {
			continue
		}
		out = append(out, task)
	}
	return out
}

// bundledPlan returns the completed department plan a review's bundle reads its
// criteria from: the one planTaskID names, or, when it is zero, the first.
func bundledPlan(all []executive.TaskRecord, department string, planTaskID int64) *executive.TaskRecord {
	marker := ":leader-plan:" + department
	for i := range all {
		if planTaskID != 0 && all[i].ID != planTaskID {
			continue
		}
		if strings.Contains(all[i].IdempotencyKey, marker) && all[i].Status == "completed" {
			return &all[i]
		}
	}
	return nil
}

func (e EvidenceTasks) projectDepartmentPlan(ctx context.Context, all []executive.TaskRecord, department string, planTaskID int64) ([]string, string, error) {
	planTask := bundledPlan(all, department, planTaskID)
	if planTask == nil {
		return nil, "", fmt.Errorf("completed department plan for %s is missing", department)
	}
	attemptID := latestFinishedAttempt(planTask.Attempts)
	if attemptID == 0 {
		return nil, "", fmt.Errorf("department plan %d has no finished attempt", planTask.ID)
	}
	invocations, err := e.Models.FindTaskAttemptInvocations(ctx, planTask.ID, attemptID)
	if err != nil {
		return nil, "", err
	}
	if len(invocations) != 1 || invocations[0].Status != "succeeded" {
		return nil, "", fmt.Errorf("department plan %d lacks one succeeded invocation", planTask.ID)
	}
	result, err := e.Models.GetResult(ctx, invocations[0].ID)
	if err != nil {
		return nil, "", err
	}
	parsed, err := executive.ParseDepartmentPlan(result.JSONOutput, e.effectiveLimits())
	if err != nil {
		return nil, "", err
	}
	return parsed.ReviewCriteria, result.ResponseHash, nil
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
		Completion: completion.Verdict, Summary: boundedWorkerSummary(parsed.Summary, e.effectiveLimits().WorkerSummaryBytes()),
		EvidenceRefs: boundedRefs(parsed.EvidenceRefs, 16), TaskEvidence: taskEvidenceRefs(task, 16),
		ResponseHash: result.ResponseHash,
	}, nil
}

func (e EvidenceTasks) attachClosureBundle(ctx context.Context, target executive.TaskRecord, correlation string) error {
	all, err := e.Tasks.ListByCorrelation(ctx, correlation)
	if err != nil {
		return err
	}
	latest := map[string]executive.TaskRecord{}
	blocked := make([]int64, 0)
	for _, task := range all {
		if task.Status == "blocked" {
			blocked = append(blocked, task.ID)
		}
		if !strings.Contains(task.IdempotencyKey, ":leader-review:") || task.Status != "completed" {
			continue
		}
		previous, ok := latest[task.AssignedUnitID]
		if !ok || task.ID > previous.ID {
			latest[task.AssignedUnitID] = task
		}
	}
	reviews := make([]projectedReview, 0, len(latest))
	departments := make([]string, 0, len(latest))
	for department := range latest {
		departments = append(departments, department)
	}
	sort.Strings(departments)
	for _, department := range departments {
		item, projectErr := e.projectReview(ctx, latest[department])
		if projectErr != nil {
			return projectErr
		}
		reviews = append(reviews, item)
	}
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
	return projectedReview{
		TaskID: task.ID, DepartmentID: task.AssignedUnitID, Status: task.Status, Completion: completion.Verdict,
		Verdict: parsed.Verdict, Findings: boundedRefs(parsed.Findings, 24),
		UnsatisfiedCriteria: boundedRefs(parsed.UnsatisfiedCriteria, 24), EvidenceRefs: boundedRefs(parsed.EvidenceRefs, 24),
		TaskEvidence: taskEvidenceRefs(task, 16), ResponseHash: result.ResponseHash,
	}, nil
}

func (e EvidenceTasks) recordBundle(ctx context.Context, taskID int64, scope string, bundle any) error {
	return recordBundleWith(ctx, e.Tasks, taskID, scope, bundle)
}

func recordBundleWith(ctx context.Context, store interface {
	RecordEvidence(context.Context, executive.EvidenceCommand) error
}, taskID int64, scope string, bundle any) error {
	body, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if len(body) > executiveEvidenceBundleBytes {
		return fmt.Errorf("executive evidence bundle exceeds %d-byte bound", executiveEvidenceBundleBytes)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	var decoded any
	if err = json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	return store.RecordEvidence(ctx, executive.EvidenceCommand{
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

// boundedWorkerSummary is the worker summary a review is shown: whole up to the limit the worker
// was held to, so a validated summary always arrives whole.
//
// It was cut at 1200 bytes, written when a summary was a line; the worker limit is 8000, and in a
// design phase the summary IS the deliverable. Local smoke #34 (root 1773, 2026-09-27): the
// round-3 department review said the summary it held was "truncated mid-sentence exactly where the
// design starts to describe" the change, could not see the file list or the regression test, and
// asked for the replan that exhausted the round. A longer value (none validates) is cut on a rune
// boundary and says so.
func boundedWorkerSummary(summary string, max int) string {
	if len(summary) <= max {
		return summary
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(summary[cut]) {
		cut--
	}
	return summary[:cut] + fmt.Sprintf(" [cut by the host: first %d of %d bytes shown]", cut, len(summary))
}

func hasExecutiveBundle(task executive.TaskRecord) bool {
	for _, evidence := range task.Evidence {
		if strings.HasPrefix(evidence.Reference, "executive-evidence:") {
			return true
		}
	}
	return false
}

type prerequisiteEvidenceBundle struct {
	SchemaVersion string            `json:"schema_version"`
	Note          string            `json:"note"`
	Prerequisites []projectedWorker `json:"prerequisites"`
}

// prerequisiteStore is what attaching prerequisite results needs from the task port.
type prerequisiteStore interface {
	GetTask(context.Context, int64) (executive.TaskRecord, error)
	RecordEvidence(context.Context, executive.EvidenceCommand) error
}

// AttachPrerequisiteResults records on task the verified results of the tasks it depends on, once:
// the bundle is shown to the worker the way a department review is shown its workers. A
// prerequisite that is not completed and verified is an error, never an empty bundle: the task
// engine runs a dependent only after its prerequisites complete.
func (e EvidenceTasks) AttachPrerequisiteResults(ctx context.Context, task executive.TaskRecord) (executive.TaskRecord, error) {
	return e.attachPrerequisites(ctx, e.Tasks, task)
}

func (e EvidenceTasks) attachPrerequisites(ctx context.Context, store prerequisiteStore, task executive.TaskRecord) (executive.TaskRecord, error) {
	if len(task.DependsOn) == 0 || e.Models == nil || e.Completion == nil || hasExecutiveBundle(task) {
		return task, nil
	}
	bundle := prerequisiteEvidenceBundle{
		SchemaVersion: executiveEvidenceSchema,
		Note:          "Results of the tasks this task depends on, verified by the host. Work from them; do not redo them.",
	}
	for _, id := range task.DependsOn {
		prerequisite, err := store.GetTask(ctx, id)
		if err != nil {
			return task, err
		}
		if prerequisite.Status != "completed" {
			return task, fmt.Errorf("executive worker %d runs before its prerequisite %d completed (%s)", task.ID, id, prerequisite.Status)
		}
		item, err := e.projectWorker(ctx, prerequisite)
		if err != nil {
			return task, err
		}
		bundle.Prerequisites = append(bundle.Prerequisites, item)
	}
	sort.Slice(bundle.Prerequisites, func(i, j int) bool { return bundle.Prerequisites[i].TaskID < bundle.Prerequisites[j].TaskID })
	if err := recordBundleWith(ctx, store, task.ID, "prerequisites:"+strconv.FormatInt(task.ID, 10), bundle); err != nil {
		return task, err
	}
	return store.GetTask(ctx, task.ID)
}

var _ executive.TaskCoordinator = EvidenceTasks{}
var _ executive.PrerequisiteAttacher = EvidenceTasks{}
