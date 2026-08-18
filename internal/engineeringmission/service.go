package engineeringmission

import (
	"context"
	"fmt"
	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"strings"
)

type TaskPort interface {
	tasks.TaskService
	tasks.TaskReader
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

type Service struct{ Tasks TaskPort }

func (s Service) Create(ctx context.Context, policy MissionPolicy, organization, requestedBy, actorType, actorID string) (tasks.Task, error) {
	if s.Tasks == nil {
		return tasks.Task{}, fmt.Errorf("task service required")
	}
	policy, err := policy.Normalize()
	if err != nil {
		return tasks.Task{}, err
	}
	meta, digest, err := policy.MarshalEvidence()
	if err != nil {
		return tasks.Task{}, err
	}
	reqs := []tasks.RequirementSpec{{Key: "candidate-artifact", Type: tasks.RequirementArtifact, Description: "sealed engineering candidate", Required: boolPtr(true)}, {Key: "engineering.required_gates", Type: tasks.RequirementCheck, Description: "all declared engineering gates pass", Required: boolPtr(true)}, {Key: "review", Type: tasks.RequirementApproval, Description: "independent engineering review", Required: boolPtr(true)}}
	task, _, err := s.Tasks.CreateTask(ctx, tasks.CreateRequest{OrganizationID: organization, RequestedByRoleID: requestedBy, AssignedRoleID: CodeRunnerRole, Title: policy.Objective, Instructions: "code-runner-execution/v1", AcceptanceCriteria: policy.AcceptanceCriteria, IdempotencyKey: "engineering-mission/" + digest, Requirements: reqs}, actorType, actorID)
	if err != nil {
		return tasks.Task{}, err
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
func (s Service) VerifyRequiredGates(ctx context.Context, taskID int64, policy MissionPolicy) error {
	detail, err := s.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	for _, wanted := range policy.RequiredGates {
		matched := false
		for _, ev := range detail.Evidence {
			if !strings.HasPrefix(ev.Reference, "code-runner-attempt-evidence://") {
				continue
			}
			checks, ok := ev.Metadata["checks_run"].([]any)
			if !ok {
				continue
			}
			for _, raw := range checks {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				typ, _ := m["type"].(string)
				success, _ := m["success"].(bool)
				if typ == string(wanted.Type) && success {
					matched = true
				}
			}
		}
		if !matched {
			return fmt.Errorf("required gate %s is not durably satisfied", wanted.Type)
		}
	}
	return nil
}
func boolPtr(v bool) *bool { return &v }
