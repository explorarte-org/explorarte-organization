package coderunner

import (
	"context"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"path/filepath"
)

type StagingAdapter struct {
	Service                                            staging.WorkspaceService
	WorkspaceRoot, RepositoryID, BaseCommit, TargetRef string
}

func (a StagingAdapter) Open(ctx context.Context, item tasks.ClaimedTask, actor string) (string, int64, error) {
	w, err := a.Service.CreateWorkspace(ctx, staging.CreateWorkspaceCommand{TaskID: item.Task.ID, AttemptID: item.Attempt.ID, RepositoryID: a.RepositoryID, BaseCommit: a.BaseCommit, TargetRef: a.TargetRef, HolderID: actor, ActorRoleID: RoleID, LeaseToken: item.LeaseToken})
	if err != nil {
		return "", 0, err
	}
	return filepath.Join(a.WorkspaceRoot, w.WorkspaceKey), w.ID, nil
}
func (a StagingAdapter) Seal(ctx context.Context, id int64, item tasks.ClaimedTask, actor string) (any, error) {
	return a.Service.SealWorkspace(ctx, staging.SealWorkspaceCommand{WorkspaceID: id, HolderID: actor, ActorRoleID: RoleID, LeaseToken: item.LeaseToken})
}
