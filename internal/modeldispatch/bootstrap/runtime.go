package bootstrap

import (
	"context"
	"fmt"
	"os"
	"time"

	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type Runtime struct {
	Config      modeldispatch.DispatchConfig
	Principals  *modeldispatch.PrincipalService
	Assignments *modeldispatch.AssignmentService
	Store       *dispatchpostgres.Store
}

func Open(cfg config.Config, platformStore *platformpostgres.Store, taskReader tasks.TaskReader) (*Runtime, error) {
	if platformStore == nil {
		return nil, fmt.Errorf("model dispatch bootstrap requires PostgreSQL store")
	}
	dispatchCfg, err := modeldispatch.LoadDispatchConfig(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	registryRepository, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, err
	}
	authorizationRuntime, err := authorizationbootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, fmt.Errorf("open authorization runtime for model dispatch: %w", err)
	}
	store, err := dispatchpostgres.New(platformStore)
	if err != nil {
		return nil, err
	}
	catalog := catalogAdapter{reader: registryRepository}
	tasksAdapter := taskAdapter{reader: taskReader}
	principals, err := modeldispatch.NewPrincipalService(cfg.Tasks.OrganizationID, authorizationRuntime.Authorizer, catalog, store, modeldispatch.ClockFunc(time.Now))
	if err != nil {
		return nil, err
	}
	assignments, err := modeldispatch.NewAssignmentService(cfg.Tasks.OrganizationID, authorizationRuntime.Authorizer, catalog, tasksAdapter, store, store, modeldispatch.ClockFunc(time.Now), dispatchCfg.AssignmentDefaultTTL, dispatchCfg.AssignmentMaxTTL)
	if err != nil {
		return nil, err
	}
	return &Runtime{Config: dispatchCfg, Principals: principals, Assignments: assignments, Store: store}, nil
}

type catalogAdapter struct{ reader registry.Reader }

func (a catalogAdapter) CurrentRevision(ctx context.Context, organizationID string) (int64, error) {
	revision, err := a.reader.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	if revision == nil {
		return 0, registry.ErrNotFound
	}
	return revision.ID, nil
}

func (a catalogAdapter) GetRole(ctx context.Context, organizationID, roleID string) (modeldispatch.RoleRef, error) {
	r, err := a.reader.GetRole(ctx, organizationID, roleID)
	if err != nil {
		return modeldispatch.RoleRef{}, err
	}
	return modeldispatch.RoleRef{ID: r.ID, Enabled: r.Enabled, Executable: r.Executable, AuthorityClass: r.AuthorityClass}, nil
}

type taskAdapter struct{ reader tasks.TaskReader }

func (a taskAdapter) GetTaskAttempt(ctx context.Context, taskID, attemptID int64) (modeldispatch.TaskAttemptRef, error) {
	detail, err := a.reader.GetTask(ctx, taskID)
	if err != nil {
		return modeldispatch.TaskAttemptRef{}, err
	}
	var attempt *tasks.Attempt
	for i := range detail.Attempts {
		if detail.Attempts[i].ID == attemptID {
			attempt = &detail.Attempts[i]
			break
		}
	}
	if attempt == nil {
		return modeldispatch.TaskAttemptRef{}, tasks.ErrNotFound
	}
	if detail.ActiveLease == nil || detail.ActiveLease.AttemptID != attemptID {
		return modeldispatch.TaskAttemptRef{}, modeldispatch.ErrTaskAttemptRejected
	}
	return modeldispatch.TaskAttemptRef{
		TaskID: detail.Task.ID, AttemptID: attempt.ID, OrganizationID: detail.Task.OrganizationID,
		OrganizationRevisionID: detail.Task.OrganizationRevisionID, AssignedRoleID: detail.Task.AssignedRoleID,
		TaskStatus: string(detail.Task.Status), AttemptStatus: string(attempt.State),
		LeaseHolderID: detail.ActiveLease.HolderID, LeaseExpiresAt: detail.ActiveLease.ExpiresAt,
	}, nil
}
