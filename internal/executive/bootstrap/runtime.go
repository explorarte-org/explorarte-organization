package bootstrap

import (
	"fmt"
	"time"

	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	agentmessagingpostgres "github.com/Mireuz13/explorarte-organization/internal/agentmessaging/postgres"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/completion"
	completionpostgres "github.com/Mireuz13/explorarte-organization/internal/completion/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisiongraphpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	executionharnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	executivepostgres "github.com/Mireuz13/explorarte-organization/internal/executive/postgres"
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
	// Models is the full Model Runtime, not the coordinator-only surface the
	// Executive used to open. The Harness needs Dispatch and the durable task
	// lease verifier, and both come from the same single provider stack --
	// opening a second one would mean two routing/egress/pricing paths.
	Models *modelbootstrap.Runtime
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
	// The sensor is built here and handed down, so the context runtime never
	// has to resolve a repository of its own. The same git source is also
	// handed to the orchestrator, so adjudicated obligations are probed for
	// supplyability against the pinned tree before they bind a round.
	repositoryOptions, evidenceSource, evidenceRepositoryID, err := repositoryEvidenceOption(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("configure repository evidence: %w", err)
	}
	contextRuntime, err := contextbootstrap.Open(cfg, store, taskContextProvider, repositoryOptions...)
	if err != nil {
		return nil, fmt.Errorf("open executive context runtime: %w", err)
	}
	authorizationRuntime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open executive authorization runtime: %w", err)
	}
	modelRuntime, err := modelbootstrap.Open(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open executive model runtime: %w", err)
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
	decisionGraphStore, err := decisiongraphpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create executive decision graph store: %w", err)
	}
	decisionGraphService, err := decisiongraph.NewService(decisionGraphStore, decisiongraph.SystemClock{})
	if err != nil {
		return nil, fmt.Errorf("create executive decision graph service: %w", err)
	}

	limits := executive.DefaultLimits()
	baseTasks := runtimeadapter.Tasks{Service: taskService, OrganizationID: cfg.Tasks.OrganizationID}
	baseModels := runtimeadapter.Models{Service: modelRuntime.Invocations, OrganizationID: cfg.Tasks.OrganizationID}
	completionGate := runtimeadapter.Completion{Service: completionService}
	evidenceTasks := runtimeadapter.EvidenceTasks{Tasks: baseTasks, Models: baseModels, Completion: completionGate, Limits: limits}
	dagTasks := runtimeadapter.DAGTasks{TaskCoordinator: evidenceTasks}
	modelBudget := runtimeadapter.ModelCallBudget{Models: baseModels, Tasks: dagTasks, Limits: limits}
	decisionRecorder := runtimeadapter.DecisionGraph{
		Service: decisionGraphService, Canonical: contextRuntime.Canonical,
		Limits: limits, Clock: executive.ClockFunc(time.Now),
	}

	agentBudgetLedger, err := agentbudgetpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create executive agent budget ledger: %w", err)
	}
	agentMessageLedger, err := agentmessagingpostgres.New(store, registryRepository, authorizationRuntime.Authorizer, agentMessageRateLimitMax, agentMessageRateLimitWindow)
	if err != nil {
		return nil, fmt.Errorf("create executive agent message ledger: %w", err)
	}

	// EXEC-PRINCIPAL-001: agent messaging resolves its own role-bound
	// principal per sender internally (see runtimeadapter.AgentMessages),
	// lazily provisioning one via this same store when a role sends its
	// first message. Reusing modelRuntime's dispatcher store here is still
	// correct -- model_execution_principals is one table regardless of
	// which subsystem is resolving against it -- but there is no longer a
	// single ORG_MODEL_EXECUTION_PRINCIPAL_KEY to read for this purpose.
	// modelruntime's own dispatch bootstrap still reads that env var
	// separately, for its own, different technical-process identity
	// (semantics A, not the role-bound identity below -- see the
	// EXEC-PRINCIPAL-001 handoff for the distinction).
	principalStore := modelRuntime.Dispatcher.Store

	// One resolver, one derivation: the same role-bound identity that
	// authenticates agent messaging also holds the task lease and executes the
	// Harness run, so no two subsystems can disagree about which principal a
	// role has.
	roleBoundResolver, err := runtimeadapter.NewRoleBoundPrincipalResolver(principalStore, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create executive role-bound principal resolver: %w", err)
	}

	// The Harness is composed from the Model Runtime bootstrap seams: the same
	// invocation/dispatch services production already uses, plus the canonical
	// durable lease + execution-principal authority. Nothing here constructs a
	// provider, a router, an egress policy or a wallet.
	harnessHistory, err := executionharnesspostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create executive harness history store: %w", err)
	}
	harnessAuthority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		return nil, fmt.Errorf("create executive harness authority: %w", err)
	}
	executionContextViewStore, err := contextcompilerpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create executive execution context view store: %w", err)
	}
	harness := runtimeadapter.Harness{
		OrganizationID: cfg.Tasks.OrganizationID,
		Authority:      harnessAuthority,
		History:        harnessHistory,
		NewModelExecutor: func(config modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return modelRuntime.NewHarnessModelExecutor(config)
		},
		Clock: executive.ClockFunc(time.Now),
	}

	// The runtime resolves no campaign ceilings of its own; it only refuses to
	// start pretending it still does.
	if err := rejectDeprecatedAgentBudgetEnv(); err != nil {
		return nil, err
	}
	acceptanceStore, err := executivepostgres.NewAcceptanceStore(store.Pool())
	if err != nil {
		return nil, fmt.Errorf("create executive acceptance store: %w", err)
	}
	var orchestrator *executive.Orchestrator
	dependencies := executive.Dependencies{
		OrganizationID: cfg.Tasks.OrganizationID,
		Registry:       runtimeadapter.Registry{Reader: registryRepository, OrganizationID: cfg.Tasks.OrganizationID},
		Tasks:          dagTasks,
		Contexts:       runtimeadapter.Context{Service: contextRuntime.Service, Assembly: contextcompiler.ContextAssemblyService{Store: executionContextViewStore}, OrganizationID: cfg.Tasks.OrganizationID},
		Assignments:    runtimeadapter.Assignment{Resolver: modelRuntime.Dispatcher.Store, OrganizationID: cfg.Tasks.OrganizationID},
		Principals:     runtimeadapter.RoleBoundPrincipals{Resolver: roleBoundResolver},
		Models:         baseModels,
		Harness:        harness,
		Acceptance:     acceptanceStore,
		Budget:         modelBudget,
		Completion:     completionGate,
		Decisions:      decisionRecorder,
		Authorization:  runtimeadapter.Authorization{Service: authorizationRuntime.Service, OrganizationID: cfg.Tasks.OrganizationID},
		Limits:         limits,
		Clock:          executive.ClockFunc(time.Now),
	}
	options := []executive.OrchestratorOption{
		executive.WithAgentBudgets(runtimeadapter.AgentBudgets{Ledger: agentBudgetLedger}),
		executive.WithAgentMessaging(runtimeadapter.AgentMessages{
			Ledger:         agentMessageLedger,
			MaxAttempts:    agentMessageMaxAttempts,
			PrincipalStore: principalStore,
			OrganizationID: cfg.Tasks.OrganizationID,
		}),
	}
	missionOptions, err := missionProvisioningOptions(cfg, store, taskService)
	if err != nil {
		return nil, err
	}
	options = append(options, missionOptions...)
	// Without this the host verifies no citation and the review bundle
	// authorizes none, so every repository claim reaches the reviewer
	// ungrounded. That is the safe direction to fail, and it is also a
	// circuit that never closes -- so it is wired.
	options = append(options, executive.WithSnapshotSources(snapshotSourceReader{service: contextRuntime.Service}))
	if evidenceSource != nil {
		options = append(options, executive.WithRepositoryEvidenceSource(evidenceRepositoryID, evidenceSource))
	}
	orchestrator, err = executive.NewOrchestrator(dependencies, options...)
	if err != nil {
		return nil, fmt.Errorf("create executive orchestrator: %w", err)
	}
	return &Runtime{Orchestrator: orchestrator, Tasks: taskService, Models: modelRuntime}, nil
}

const (
	agentMessageRateLimitMax    = 200
	agentMessageRateLimitWindow = time.Hour
	agentMessageMaxAttempts     = 10
)
