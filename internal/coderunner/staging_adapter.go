package coderunner

import (
	"context"
	"fmt"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"path/filepath"
)

// TaskReader is the minimal read capability StagingAdapter needs to resolve
// a task's own artifact requirement. *tasks.Service already satisfies this.
type TaskReader interface {
	GetTask(context.Context, int64) (tasks.TaskDetail, error)
}

type StagingAdapter struct {
	Service                                            staging.WorkspaceService
	Tasks                                              TaskReader
	WorkspaceRoot, RepositoryID, BaseCommit, TargetRef string
	IntentResolver                                     WorkspaceIntentResolver
}

type WorkspaceIntent struct{ RepositoryID, BaseCommit, TargetRef string }
type WorkspaceIntentResolver interface {
	ResolveWorkspaceIntent(context.Context, tasks.ClaimedTask) (WorkspaceIntent, error)
}

func (a StagingAdapter) Open(ctx context.Context, item tasks.ClaimedTask, actor string) (string, int64, error) {
	requirementID, err := a.artifactRequirementID(ctx, item.Task.ID)
	if err != nil {
		return "", 0, err
	}
	repo, base, target := a.RepositoryID, a.BaseCommit, a.TargetRef
	if a.IntentResolver != nil {
		intent, err := a.IntentResolver.ResolveWorkspaceIntent(ctx, item)
		if err != nil {
			return "", 0, err
		}
		repo, base, target = intent.RepositoryID, intent.BaseCommit, intent.TargetRef
	}
	w, err := a.Service.CreateWorkspace(ctx, staging.CreateWorkspaceCommand{TaskID: item.Task.ID, AttemptID: item.Attempt.ID, RepositoryID: repo, BaseCommit: base, TargetRef: target, HolderID: actor, ActorRoleID: RoleID, ArtifactRequirementID: requirementID, LeaseToken: item.LeaseToken})
	if err != nil {
		return "", 0, err
	}
	return filepath.Join(a.WorkspaceRoot, w.WorkspaceKey), w.ID, nil
}

func (a StagingAdapter) Inspect(ctx context.Context, id int64) (staging.WorkspaceInspection, error) {
	return a.Service.InspectWorkspace(ctx, id)
}

// artifactRequirementID resolves this specific task's own RequirementArtifact
// requirement. It is per-task (assigned by the task service at creation
// time), not worker configuration, so it must be looked up here rather than
// carried as a static StagingAdapter field like RepositoryID/BaseCommit are.
func (a StagingAdapter) artifactRequirementID(ctx context.Context, taskID int64) (int64, error) {
	if a.Tasks == nil {
		return 0, fmt.Errorf("code-runner staging adapter requires a task reader to resolve the artifact requirement")
	}
	detail, err := a.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return 0, fmt.Errorf("resolve artifact requirement: %w", err)
	}
	for _, r := range detail.Requirements {
		if r.Type == tasks.RequirementArtifact {
			return r.ID, nil
		}
	}
	return 0, fmt.Errorf("code-runner task %d has no artifact requirement", taskID)
}
func (a StagingAdapter) Seal(ctx context.Context, id int64, item tasks.ClaimedTask, actor string) (staging.Workspace, error) {
	return a.Service.SealWorkspace(ctx, staging.SealWorkspaceCommand{WorkspaceID: id, HolderID: actor, ActorRoleID: RoleID, LeaseToken: item.LeaseToken})
}
