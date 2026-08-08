package postrun

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// TaskRoleResolver adapts *tasks.Service to RoleResolver — its own package
// import stays isolated here so ports.go/service.go don't need to know
// about internal/tasks at all.
type TaskRoleResolver struct {
	Service *tasks.Service
}

func (a TaskRoleResolver) AssignedRoleID(ctx context.Context, taskID int64) (string, error) {
	detail, err := a.Service.GetTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	return detail.Task.AssignedRoleID, nil
}

var _ RoleResolver = TaskRoleResolver{}
