// Package bootstrap wires internal/ceochat to the same durable
// infrastructure the Executive uses: Task Engine, Model Runtime, Execution
// Harness, and Context Engine. It opens nothing a second time that already
// has a canonical home -- no second provider stack, no second tool
// framework, no second execution-authority mechanism.
package bootstrap

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	ceochatpostgres "github.com/Mireuz13/explorarte-organization/internal/ceochat/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	executionharnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	searchpostgres "github.com/Mireuz13/explorarte-organization/internal/search/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskcontextprovider "github.com/Mireuz13/explorarte-organization/internal/tasks/contextprovider"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
)

// Runtime bundles the opened ceochat service together with the pieces a
// caller (CLI, HTTP surface) needs directly.
type Runtime struct {
	Service *ceochat.Service
	Tasks   *tasks.Service
}

// Open builds a ceochat.Service against the given PostgreSQL store. It is
// safe to call this alongside internal/executive/bootstrap.Open in the same
// process: every dependency here is a stateless adapter over the same
// store/registry, exactly the pattern internal/executive/bootstrap already
// uses to open Model Runtime a second time for its own purposes.
func Open(cfg config.Config, store *platformpostgres.Store) (*Runtime, error) {
	if store == nil {
		return nil, fmt.Errorf("ceochat bootstrap requires PostgreSQL")
	}
	organizationID := cfg.Tasks.OrganizationID

	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		return nil, fmt.Errorf("create ceochat registry repository: %w", err)
	}
	taskStore, err := taskpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create ceochat task store: %w", err)
	}
	taskCatalog, err := registryadapter.New(registryRepository)
	if err != nil {
		return nil, fmt.Errorf("create ceochat task registry adapter: %w", err)
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
		return nil, fmt.Errorf("create ceochat task service: %w", err)
	}
	taskContextProvider, err := taskcontextprovider.New(taskStore)
	if err != nil {
		return nil, fmt.Errorf("create ceochat task context provider: %w", err)
	}
	contextRuntime, err := contextbootstrap.Open(cfg, store, taskContextProvider)
	if err != nil {
		return nil, fmt.Errorf("open ceochat context runtime: %w", err)
	}
	executionContextViewStore, err := contextcompilerpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create ceochat execution context view store: %w", err)
	}

	// Model Runtime, the durable Harness history/descriptor store, and
	// execution authority all reuse the exact seams
	// internal/executive/bootstrap already opens for typed-task work. None
	// of that is Executive-specific: it is org-scoped adapter code shared by
	// any execution profile.
	modelRuntime, err := modelbootstrap.Open(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open ceochat model runtime: %w", err)
	}
	harnessAuthority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		return nil, fmt.Errorf("create ceochat harness authority: %w", err)
	}
	harnessHistory, err := executionharnesspostgres.New(store, organizationID)
	if err != nil {
		return nil, fmt.Errorf("create ceochat harness history store: %w", err)
	}
	principalStore := modelRuntime.Dispatcher.Store
	roleBoundResolver, err := runtimeadapter.NewRoleBoundPrincipalResolver(principalStore, organizationID)
	if err != nil {
		return nil, fmt.Errorf("create ceochat role-bound principal resolver: %w", err)
	}

	searchStore, err := searchpostgres.New(store.Pool())
	if err != nil {
		return nil, fmt.Errorf("create ceochat research store: %w", err)
	}

	conversationStore, err := ceochatpostgres.New(store, organizationID)
	if err != nil {
		return nil, fmt.Errorf("create ceochat conversation store: %w", err)
	}

	service, err := ceochat.Open(ceochat.Service{
		OrganizationID: organizationID,
		Store:          conversationStore,
		Tasks:          taskService,
		Principals:     principalResolver{resolver: roleBoundResolver},
		Contexts: contextSeam{
			service:        contextRuntime.Service,
			assembly:       contextcompiler.ContextAssemblyService{Store: executionContextViewStore},
			organizationID: organizationID,
		},
		Authority:       harnessAuthority,
		HarnessHistory:  harnessHistory,
		DescriptorStore: harnessHistory,
		NewModelExecutor: func(config modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return modelRuntime.NewHarnessModelExecutor(config)
		},
		Catalog:      ceochat.NewToolCatalog(),
		ToolExecutor: ceochat.ToolExecutor{Topics: searchStore, Findings: searchStore},
	})
	if err != nil {
		return nil, fmt.Errorf("open ceochat service: %w", err)
	}
	return &Runtime{Service: service, Tasks: taskService}, nil
}

// principalResolver adapts runtimeadapter.RoleBoundPrincipalResolver's
// int64 principal ID to the plain string ceochat.PrincipalResolver expects
// (the same string form RunIdentity.ExecutionPrincipalID carries
// throughout the Harness).
type principalResolver struct {
	resolver runtimeadapter.RoleBoundPrincipalResolver
}

func (r principalResolver) Resolve(ctx context.Context, roleID string) (string, error) {
	principal, err := r.resolver.Resolve(ctx, roleID)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(principal.ID, 10), nil
}

var _ ceochat.PrincipalResolver = principalResolver{}

// contextSeam is the minimal Context Engine composition ceochat needs: the
// same Build -> Get -> Validate -> ResolveAndPersist sequence
// internal/executive/runtimeadapter.Context uses, reimplemented here (not
// imported from there) so ceochat never depends on internal/executive
// itself, only on the Context Engine seams both sit on top of.
type contextSeam struct {
	service        contextengine.Service
	assembly       contextcompiler.ContextAssemblyService
	organizationID string
}

func (c contextSeam) Build(ctx context.Context, request ceochat.ContextRequest) (ceochat.ContextSnapshot, error) {
	built, err := c.service.Build(ctx, contextengine.BuildRequest{
		OrganizationID:         c.organizationID,
		OrganizationRevisionID: request.OrganizationRevisionID,
		ActorRoleID:            request.ActorRoleID,
		Purpose:                "executive.ceo_chat",
		TaskRef:                request.TaskRef,
		TaskClass:              ceochat.TaskClass,
		ExecutionPurpose:       "executive-ceo-chat-turn",
		IdempotencyKey:         request.IdempotencyKey,
		CorrelationID:          request.CorrelationID,
		CausationID:            request.CausationID,
	})
	if err != nil {
		return ceochat.ContextSnapshot{}, err
	}
	snapshot, err := c.service.Get(ctx, built.Snapshot.ID, true)
	if err != nil {
		return ceochat.ContextSnapshot{}, fmt.Errorf("get ceochat context snapshot %d: %w", built.Snapshot.ID, err)
	}
	if snapshot.Status == contextengine.SnapshotInvalidated {
		return ceochat.ContextSnapshot{}, contextengine.ErrSnapshotInvalidated
	}
	validation, err := c.service.Validate(ctx, built.Snapshot.ID)
	if err != nil {
		return ceochat.ContextSnapshot{}, fmt.Errorf("validate ceochat context snapshot %d: %w", built.Snapshot.ID, err)
	}
	if !validation.Valid {
		return ceochat.ContextSnapshot{}, contextengine.ErrSnapshotStale
	}
	view, err := c.assembly.ResolveAndPersist(ctx, snapshot)
	if err != nil {
		return ceochat.ContextSnapshot{}, fmt.Errorf("resolve ceochat provider-visible context %d: %w", built.Snapshot.ID, err)
	}
	return ceochat.ContextSnapshot{
		ID:      built.Snapshot.ID,
		Version: strconv.FormatInt(built.Snapshot.Version, 10),
		Digest:  view.ProviderVisibleDigest,
		Content: string(view.ProviderVisibleBytes),
	}, nil
}

var _ ceochat.ContextBuilder = contextSeam{}
