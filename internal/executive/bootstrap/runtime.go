package bootstrap

import (
	"fmt"
	"time"

	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/completion"
	completionpostgres "github.com/Mireuz13/explorarte-organization/internal/completion/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskcontextprovider "github.com/Mireuz13/explorarte-organization/internal/tasks/contextprovider"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
)

type Runtime struct {
	Orchestrator *executive.Orchestrator
	Tasks        *tasks.Service
	Models       *modelbootstrap.CoordinatorRuntime
}

func Open(cfg config.Config, store *platformpostgres.Store) (*Runtime, error) {
	if store == nil {
		return nil, fmt.Errorf("executive bootstrap requires PostgreSQL")
	}
	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		return nil, fmt.Errorf("create executive registry repository: %w", err)
	}
	taskStore, err := taskpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create executive task store: %w", err)
	}
	taskCatalog, err := registryadapter.New(registryRepository)
	if err != nil {
		return nil, fmt.Errorf("create executive task registry adapter: %w", err)
	}
	taskService, err := tasks.NewService(taskStore, taskCatalog, tasks.Config{
		OrganizationID:       cfg.Tasks.OrganizationID,
		DefaultMaxAttempts:   cfg.Tasks.DefaultMaxAttempts,
		DefaultLeaseDuration: cfg.Tasks.DefaultLeaseDuration,
		MaxLeaseDuration:     cfg.Tasks.MaxLeaseDuration,
		RetryPolicy: tasks.RetryPolicy{
			BaseDelay: cfg.Tasks.RetryBaseDelay,
			MaxDelay:  cfg.Tasks.RetryMaxDelay,
		},
		OutboxMaxAttempts:   cfg.Tasks.OutboxMaxAttempts,
		OutboxClaimDuration: cfg.Tasks.OutboxClaimDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("create executive task service: %w", err)
	}
	taskContextProvider, err := taskcontextprovider.New(taskStore)
	if err != nil {
		return nil, fmt.Errorf("create executive task context provider: %w", err)
	}
	contextRuntime, err := contextbootstrap.Open(cfg, store, taskContextProvider)
	if err != nil {
		return nil, fmt.Errorf("open executive context runtime: %w", err)
	}
	authorizationRuntime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open executive authorization runtime: %w", err)
	}
	modelRuntime, err := modelbootstrap.OpenCoordinator(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open executive model coordinator runtime: %w", err)
	}
	completionReader, err := completionpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create executive completion reader: %w", err)
	}
	completionService, err := completion.NewService(completionReader, completionReader, completionReader, completionReader, completionReader, nil)
	if err != nil {
		return nil, fmt.Errorf("create executive completion service: %w", err)
	}
	if err = runtimeadapter.ValidateStaticDependencies(registryRepository, modelRuntime.Dispatcher.Store, completionService, authorizationRuntime.Service); err != nil {
		return nil, err
	}
	orchestrator, err := executive.NewOrchestrator(
		cfg.Tasks.OrganizationID,
		runtimeadapter.Registry{Reader: registryRepository, OrganizationID: cfg.Tasks.OrganizationID},
		runtimeadapter.Tasks{Service: taskService, OrganizationID: cfg.Tasks.OrganizationID},
		runtimeadapter.Context{Service: contextRuntime.Service, OrganizationID: cfg.Tasks.OrganizationID},
		runtimeadapter.Assignment{Resolver: modelRuntime.Dispatcher.Store, OrganizationID: cfg.Tasks.OrganizationID},
		runtimeadapter.Models{Service: modelRuntime.Invocations, OrganizationID: cfg.Tasks.OrganizationID},
		runtimeadapter.Completion{Service: completionService},
		runtimeadapter.Authorization{Service: authorizationRuntime.Service, OrganizationID: cfg.Tasks.OrganizationID},
		executive.DefaultLimits(),
		executive.ClockFunc(time.Now),
	)
	if err != nil {
		return nil, fmt.Errorf("create executive orchestrator: %w", err)
	}
	return &Runtime{Orchestrator: orchestrator, Tasks: taskService, Models: modelRuntime}, nil
}
