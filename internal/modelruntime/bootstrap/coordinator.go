package bootstrap

import (
	"fmt"
	"os"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	dispatchbootstrap "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/bootstrap"
	egressbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelegress/bootstrap"
	identitybootstrap "github.com/Mireuz13/explorarte-organization/internal/modelidentity/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	taskcontextprovider "github.com/Mireuz13/explorarte-organization/internal/tasks/contextprovider"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
)

// CoordinatorRuntime is the model-runtime surface needed by orchestration code.
// It deliberately does not construct DispatchService, provider adapters, HTTP
// clients, CLI transports, or provider credential configuration. R13 owns
// dispatch and execution identity use at the provider boundary.
type CoordinatorRuntime struct {
	Config      modelruntime.RuntimeConfig
	Registry    *modelruntime.RegistryService
	Invocations *modelruntime.InvocationService
	Store       *modelpostgres.Store
	Dispatcher  *dispatchbootstrap.Runtime
	Identity    *identitybootstrap.Runtime
}

func OpenCoordinator(cfg config.Config, platformStore *platformpostgres.Store) (*CoordinatorRuntime, error) {
	if platformStore == nil {
		return nil, fmt.Errorf("model coordinator runtime requires PostgreSQL store")
	}
	runtimeCfg, err := modelruntime.LoadRuntimeConfig(os.LookupEnv, cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		return nil, err
	}
	registryRepo, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create model coordinator organization reader: %w", err)
	}
	modelStore, err := modelpostgres.New(platformStore)
	if err != nil {
		return nil, err
	}
	taskStore, err := taskpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create model coordinator task reader: %w", err)
	}
	taskContextProvider, err := taskcontextprovider.New(taskStore)
	if err != nil {
		return nil, fmt.Errorf("create model coordinator task context provider: %w", err)
	}
	contextRuntime, err := contextbootstrap.Open(cfg, platformStore, taskContextProvider)
	if err != nil {
		return nil, fmt.Errorf("open context runtime for model coordinator: %w", err)
	}
	egressRuntime, err := egressbootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, fmt.Errorf("open model egress runtime for coordinator: %w", err)
	}
	dispatchRuntime, err := dispatchbootstrap.Open(cfg, platformStore, taskStore)
	if err != nil {
		return nil, fmt.Errorf("open model dispatch assignment runtime for coordinator: %w", err)
	}
	identityRuntime, err := identitybootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, fmt.Errorf("open model execution identity metadata runtime for coordinator: %w", err)
	}
	catalog := catalogAdapter{reader: registryRepo}
	tasksAdapter := taskAdapter{reader: taskStore}
	contexts := contextAdapter{service: contextRuntime.Service}
	registryService, err := modelruntime.NewRegistryService(cfg.Registry.CanonicalDir, cfg.Tasks.OrganizationID, catalog, modelStore)
	if err != nil {
		return nil, err
	}
	invocationService, err := modelruntime.NewInvocationService(
		cfg.Tasks.OrganizationID,
		catalog,
		tasksAdapter,
		contexts,
		modelStore,
		egressRuntime.Store,
		identityRuntime.Store,
		dispatchRuntime.Store,
		modelruntime.ClockFunc(time.Now),
		cfg.Tasks.OutboxMaxAttempts,
	)
	if err != nil {
		return nil, err
	}
	return &CoordinatorRuntime{
		Config: runtimeCfg, Registry: registryService, Invocations: invocationService,
		Store: modelStore, Dispatcher: dispatchRuntime, Identity: identityRuntime,
	}, nil
}
