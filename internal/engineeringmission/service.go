package engineeringmission

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type TaskPort interface {
	tasks.TaskService
	tasks.TaskReader
}

// PromotionPort intentionally excludes ApplyPromotion and PromoteRef.
type PromotionPort interface {
	GetWorkspace(context.Context, int64) (staging.Workspace, error)
	GetPromotion(context.Context, int64) (staging.Promotion, error)
	RecordCheck(context.Context, staging.RecordCheckCommand) (staging.Check, error)
	RequestPromotion(context.Context, staging.RequestPromotionCommand) (staging.Promotion, error)
	SubmitReview(context.Context, staging.SubmitReviewCommand) (staging.Promotion, error)
}

// Guard binds a resolved policy to CodeRunner's generic mutation seam. It is
// deliberately independent of task persistence and staging implementation.
type Guard struct{ Policy MissionPolicy }

func (g Guard) ValidatePlan(p coderunner.Plan) error {
	for _, op := range p.Operations {
		if op.Type == coderunner.ApplyPatch {
			if err := ValidateMutationPaths(g.Policy, op.Patch); err != nil {
				return err
			}
		}
		if op.Type == coderunner.Gofmt && op.Path != "" && !PathAllowed(g.Policy.AllowedPaths, op.Path) {
			return fmt.Errorf("gofmt path outside allowed paths")
		}
	}
	return nil
}
func (g Guard) ValidateChangedFiles(files []staging.ChangedFile) error {
	for _, f := range files {
		if !PathAllowed(g.Policy.AllowedPaths, f.Path) {
			return fmt.Errorf("changed path %q outside allowed paths", f.Path)
		}
	}
	return nil
}

// WorkspaceResolver carries mission BaseSHA to the existing staging service;
// repository and target remain trusted worker configuration.
type WorkspaceResolver struct {
	Tasks                   tasks.TaskReader
	Mission                 Service
	RepositoryID, TargetRef string
}

func (r WorkspaceResolver) ResolveWorkspaceIntent(ctx context.Context, item tasks.ClaimedTask) (coderunner.WorkspaceIntent, error) {
	if r.Tasks == nil {
		return coderunner.WorkspaceIntent{}, fmt.Errorf("task reader required")
	}
	p, err := r.Mission.Resolve(ctx, item.Task.ID)
	if err != nil {
		return coderunner.WorkspaceIntent{}, err
	}
	if r.RepositoryID == "" || r.TargetRef == "" {
		return coderunner.WorkspaceIntent{}, fmt.Errorf("trusted repository configuration required")
	}
	return coderunner.WorkspaceIntent{RepositoryID: r.RepositoryID, BaseCommit: p.BaseSHA, TargetRef: r.TargetRef}, nil
}

type Service struct {
	Tasks     TaskPort
	Promotion PromotionPort
}

type Verdict string

const (
	Approve   Verdict = "APPROVE"
	Remediate Verdict = "REMEDIATE"
	Block     Verdict = "BLOCK"
)

func (s Service) RequestPromotion(ctx context.Context, taskID, workspaceID int64, actorRole string) (staging.Promotion, error) {
	if s.Promotion == nil {
		return staging.Promotion{}, fmt.Errorf("promotion port required")
	}
	p, err := s.Resolve(ctx, taskID)
	if err != nil {
		return staging.Promotion{}, err
	}
	w, err := s.Promotion.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return staging.Promotion{}, err
	}
	if w.TaskID != taskID || w.Status != staging.WorkspaceSealed {
		return staging.Promotion{}, fmt.Errorf("workspace is not a sealed mission workspace")
	}
	detail, err := s.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return staging.Promotion{}, err
	}
	// Round-2 review fix (P1-2): resolve the ONE attempt-evidence entry
	// whose own candidate_revision.workspace_id matches the workspace being
	// promoted, and verify every required gate against THAT evidence's own
	// checks_run -- never scanning across every attempt evidence the task
	// has ever accumulated (which would let a build check from one attempt
	// and a test check from a different, unrelated attempt satisfy the same
	// promotion) and never trusting whichever evidence happened to be
	// iterated last (which previously supplied RecordCheck's Reference/
	// Digest without ever proving it belonged to this workspace at all).
	evidence, err := resolveWorkspaceAttemptEvidence(detail, workspaceID)
	if err != nil {
		return staging.Promotion{}, err
	}
	if err := verifyRequiredGatesAgainstEvidence(evidence, p); err != nil {
		return staging.Promotion{}, err
	}
	var reqID int64
	for _, r := range detail.Requirements {
		if r.Key == "engineering-required-gates" {
			if reqID != 0 {
				return staging.Promotion{}, fmt.Errorf("duplicate gate requirement")
			}
			reqID = r.ID
		}
	}
	if reqID == 0 {
		return staging.Promotion{}, fmt.Errorf("gate requirement missing")
	}
	if evidence.Digest == nil || *evidence.Digest == "" {
		return staging.Promotion{}, fmt.Errorf("attempt evidence missing digest")
	}
	if _, err := s.Promotion.RecordCheck(ctx, staging.RecordCheckCommand{WorkspaceID: workspaceID, RequirementID: reqID, Name: "engineering-required-gates", Status: staging.CheckPassed, Reference: evidence.Reference, Digest: *evidence.Digest, ActorRoleID: actorRole}); err != nil {
		return staging.Promotion{}, err
	}
	return s.Promotion.RequestPromotion(ctx, staging.RequestPromotionCommand{WorkspaceID: workspaceID, ActorRoleID: actorRole})
}

func (s Service) ReviewMission(ctx context.Context, promotionID, approvalRequirementID int64, reviewerRole string, verdict Verdict, reasonCode, reason string) (staging.Promotion, error) {
	if s.Promotion == nil || strings.TrimSpace(reviewerRole) == "" || strings.TrimSpace(reasonCode) == "" || strings.TrimSpace(reason) == "" {
		return staging.Promotion{}, fmt.Errorf("invalid review")
	}
	p, err := s.Promotion.GetPromotion(ctx, promotionID)
	if err != nil {
		return staging.Promotion{}, err
	}
	w, err := s.Promotion.GetWorkspace(ctx, p.WorkspaceID)
	if err != nil {
		return staging.Promotion{}, err
	}
	if reviewerRole == w.ActorRoleID {
		return staging.Promotion{}, fmt.Errorf("engineering self-review denied")
	}
	decision := staging.ReviewReject
	encoded := "reject"
	switch verdict {
	case Approve:
		decision = staging.ReviewApprove
		encoded = "approve"
	case Remediate, Block:
		encoded = strings.ToLower(string(verdict))
	default:
		return staging.Promotion{}, fmt.Errorf("unknown review verdict")
	}
	ref := fmt.Sprintf("engineering-review://task/%d/promotion/%d/%s/%s", p.TaskID, p.ID, encoded, reasonCode)
	return s.Promotion.SubmitReview(ctx, staging.SubmitReviewCommand{PromotionID: p.ID, RequirementID: approvalRequirementID, Decision: decision, ActorRoleID: reviewerRole, Reason: reason, Reference: ref})
}

// Create records a durable engineering-mission/v1 policy and dispatches a
// real CodeRunner attempt against it. plan is the actual
// code-runner-execution/v1 JSON CodeRunner's worker will parse via
// coderunner.ParsePlan and execute (worker.go: `ParsePlan([]byte(item.Task.
// Instructions))`) -- it is validated here (well-formed, non-empty
// operations) so a malformed plan fails at mission-creation time, not
// silently at claim time. plan is NOT part of MissionPolicy: the policy is
// the governance envelope (BaseSHA/AllowedPaths/AcceptanceCriteria/
// RequiredGates) Guard/WorkspaceResolver/VerifyRequiredGates enforce against
// whatever plan is submitted; the plan itself is the ordinary CodeRunner
// task payload every other CodeRunner caller already uses.
func (s Service) Create(ctx context.Context, policy MissionPolicy, plan string, organization, requestedBy, actorType, actorID string) (tasks.Task, error) {
	if s.Tasks == nil {
		return tasks.Task{}, fmt.Errorf("task service required")
	}
	policy, err := policy.Normalize()
	if err != nil {
		return tasks.Task{}, err
	}
	parsedPlan, err := coderunner.ParsePlan([]byte(plan))
	if err != nil || len(parsedPlan.Operations) == 0 {
		return tasks.Task{}, fmt.Errorf("invalid engineering mission execution plan: %w", err)
	}
	meta, digest, err := policy.MarshalEvidence()
	if err != nil {
		return tasks.Task{}, err
	}
	reqs := []tasks.RequirementSpec{{Key: "candidate-artifact", Type: tasks.RequirementArtifact, Description: "sealed engineering candidate", Required: boolPtr(true)}, {Key: "engineering-required-gates", Type: tasks.RequirementCheck, Description: "all declared engineering gates pass", Required: boolPtr(true)}, {Key: "review", Type: tasks.RequirementApproval, Description: "independent engineering review", Required: boolPtr(true)}}
	task, inserted, err := s.Tasks.CreateTask(ctx, tasks.CreateRequest{OrganizationID: organization, RequestedByRoleID: requestedBy, AssignedRoleID: CodeRunnerRole, Title: policy.Objective, Instructions: plan, AcceptanceCriteria: policy.AcceptanceCriteria, IdempotencyKey: "engineering-mission/" + digest, Requirements: reqs}, actorType, actorID)
	if err != nil {
		return tasks.Task{}, err
	}
	// Round-2 review fix (P1-3): IdempotencyKey is derived from the
	// normalized policy's own content digest, so CreateTask reusing an
	// existing task (inserted == false) means that task's original Create
	// call already recorded this EXACT policy as its engineering-mission://
	// evidence. Recording it again here would insert a second, identical
	// evidence row under the same reference -- exactly the ambiguity
	// Resolve()'s "duplicate engineering policies" fail-closed check exists
	// to catch, which previously turned a legitimate retry of the same
	// Create call into a broken mission. A retry is therefore a no-op past
	// this point: return the (reused) task as-is.
	if !inserted {
		return task, nil
	}
	policy.TaskID = task.ID
	meta, digest, err = policy.MarshalEvidence()
	if err != nil {
		return tasks.Task{}, err
	}
	_, err = s.Tasks.RecordEvidence(ctx, tasks.RecordEvidenceCommand{TaskID: task.ID, Type: tasks.RequirementResult, Reference: "engineering-mission://" + fmt.Sprint(task.ID), Digest: digest, RecordedBy: actorID, Metadata: meta})
	if err != nil {
		return tasks.Task{}, err
	}
	return task, nil
}

func (s Service) Resolve(ctx context.Context, taskID int64) (MissionPolicy, error) {
	if s.Tasks == nil {
		return MissionPolicy{}, fmt.Errorf("task service required")
	}
	d, err := s.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return MissionPolicy{}, err
	}
	var found *MissionPolicy
	for _, e := range d.Evidence {
		if e.Reference != "engineering-mission://"+fmt.Sprint(taskID) {
			continue
		}
		p, e := DecodeEvidence(e.Metadata)
		if e != nil {
			return MissionPolicy{}, fmt.Errorf("invalid engineering policy: %w", e)
		}
		if found != nil {
			return MissionPolicy{}, fmt.Errorf("duplicate engineering policies")
		}
		found = &p
	}
	if found == nil {
		return MissionPolicy{}, fmt.Errorf("engineering policy missing")
	}
	return *found, nil
}

// VerifyRequiredGates reads the existing CodeRunner attempt evidence from the
// task ledger. It never creates a second gate ledger.
//
// Round-2 review fix (P1-1 and P1-2): VerifyRequiredGates now takes
// workspaceID and verifies every required gate against the SINGLE attempt
// evidence entry whose own candidate_revision.workspace_id matches it
// (resolveWorkspaceAttemptEvidence, below) -- not against whichever
// checks_run entries happen to appear anywhere across every attempt
// evidence the task has ever accumulated. Each candidate check is also now
// compared field-for-field against the policy's own RequiredGate (Type,
// Packages as a set, Race, Integration), not merely Type+success -- a gate
// declared as "GO_TEST ./... race=true" is no longer satisfiable by
// evidence recording "GO_TEST ./internal/foo race=false".
func (s Service) VerifyRequiredGates(ctx context.Context, taskID, workspaceID int64, policy MissionPolicy) error {
	detail, err := s.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	evidence, err := resolveWorkspaceAttemptEvidence(detail, workspaceID)
	if err != nil {
		return err
	}
	return verifyRequiredGatesAgainstEvidence(evidence, policy)
}

// resolveWorkspaceAttemptEvidence finds the ONE code-runner-attempt-evidence
// entry on detail whose candidate_revision.workspace_id equals workspaceID.
// This is what ties gate verification and the RecordCheck Reference/Digest
// RequestPromotion submits to the exact evidence that sealed THIS workspace
// -- never to an unrelated attempt's evidence, and never to "whichever
// attempt evidence was iterated last."
func resolveWorkspaceAttemptEvidence(detail tasks.TaskDetail, workspaceID int64) (tasks.Evidence, error) {
	var found *tasks.Evidence
	for i := range detail.Evidence {
		e := detail.Evidence[i]
		if !strings.HasPrefix(e.Reference, "code-runner-attempt-evidence://") {
			continue
		}
		candidate, ok := e.Metadata["candidate_revision"].(map[string]any)
		if !ok {
			continue
		}
		id, ok := candidate["workspace_id"].(float64)
		if !ok || int64(id) != workspaceID {
			continue
		}
		if found != nil {
			return tasks.Evidence{}, fmt.Errorf("multiple attempt evidence entries claim workspace %d", workspaceID)
		}
		found = &e
	}
	if found == nil {
		return tasks.Evidence{}, fmt.Errorf("no attempt evidence found for workspace %d", workspaceID)
	}
	return *found, nil
}

// verifyRequiredGatesAgainstEvidence checks every policy.RequiredGates entry
// against evidence.Metadata["checks_run"] field-for-field (Type, Packages,
// Race, Integration, success) -- see VerifyRequiredGates' own doc comment
// for why Type+success alone is not enough.
func verifyRequiredGatesAgainstEvidence(evidence tasks.Evidence, policy MissionPolicy) error {
	checks, _ := evidence.Metadata["checks_run"].([]any)
	for _, wanted := range policy.RequiredGates {
		matched := false
		for _, raw := range checks {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			success, _ := m["success"].(bool)
			race, _ := m["race"].(bool)
			integration, _ := m["integration"].(bool)
			var packages []string
			if rawPackages, ok := m["packages"].([]any); ok {
				for _, rawPackage := range rawPackages {
					if s, ok := rawPackage.(string); ok {
						packages = append(packages, s)
					}
				}
			}
			if typ == string(wanted.Type) && success && race == wanted.Race && integration == wanted.Integration && samePackageSet(packages, wanted.Packages) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("required gate %s (packages=%v race=%v integration=%v) is not durably satisfied by this workspace's attempt evidence", wanted.Type, wanted.Packages, wanted.Race, wanted.Integration)
		}
	}
	return nil
}

// samePackageSet reports whether a and b contain the same package
// specifiers, ignoring order -- MissionPolicy.RequiredGates and
// coderunner's own checkEvidence both carry Packages as an unordered set
// of specifiers, never a meaningful sequence.
func samePackageSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := append([]string(nil), a...)
	sortedB := append([]string(nil), b...)
	sort.Strings(sortedA)
	sort.Strings(sortedB)
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

func boolPtr(v bool) *bool { return &v }
