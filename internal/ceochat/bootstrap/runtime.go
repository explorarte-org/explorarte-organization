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

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/campaign/executionrequirements"
	campaignpostgres "github.com/Mireuz13/explorarte-organization/internal/campaign/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	ceochatpostgres "github.com/Mireuz13/explorarte-organization/internal/ceochat/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	executionharnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	memorybootstrap "github.com/Mireuz13/explorarte-organization/internal/memory/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
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
// caller (CLI, HTTP surface) needs directly. ModelRuntime is exposed so a
// caller can prove (or reuse) exactly which Model Runtime instance backs
// this ceochat service -- see WithModelRuntime.
type Runtime struct {
	Service      *ceochat.Service
	Tasks        *tasks.Service
	ModelRuntime *modelbootstrap.Runtime
}

// OpenOption configures optional dependencies for ceochat runtime. This is
// the only supported way to configure Open: an unrecognized option cannot
// silently be dropped, because there is no other type an argument to Open
// could have.
type OpenOption func(*openConfig)

type openConfig struct {
	sharedModelRuntime *modelbootstrap.Runtime
	modelRuntimeOpts   []modelbootstrap.Option
	submitter          campaign.ExecutiveSubmitter
	promotionService   *campaign.PromotionService
}

// WithExecutiveSubmitter injects the canonical ExecutiveSubmitter into ceochat for campaign promotion.
func WithExecutiveSubmitter(submitter campaign.ExecutiveSubmitter) OpenOption {
	return func(c *openConfig) {
		c.submitter = submitter
	}
}

// WithPromotionService injects an existing PromotionService into ceochat.
func WithPromotionService(svc *campaign.PromotionService) OpenOption {
	return func(c *openConfig) {
		c.promotionService = svc
	}
}

// WithModelRuntime shares an already-opened Model Runtime instead of
// letting Open construct its own -- the fix for the one real caller that
// opens both internal/executive/bootstrap.Runtime and this package's
// Runtime in the SAME process (cmd/orgctl/executive_chat.go): without this,
// that process built two independent Model Runtimes (two provider adapter
// sets, two routers, two circuit breakers, two egress clients) even though
// both talked to the same database, which is not the same thing as being
// the same runtime. Pass the Executive runtime's own Models field here so
// the process composes exactly one.
//
// A caller that never passes this (every ceochat test fixture, and any
// future independent caller) keeps the old behavior exactly: Open opens
// its own canonical Model Runtime, as it always has.
func WithModelRuntime(runtime *modelbootstrap.Runtime) OpenOption {
	return func(c *openConfig) {
		c.sharedModelRuntime = runtime
	}
}

// WithModelRuntimeOptions passes modelbootstrap.Option values through to
// the Model Runtime Open constructs when NOT sharing one via
// WithModelRuntime (if both are supplied, WithModelRuntime wins and these
// are ignored, since there is then no local Open call for them to modify).
// Exists only so a test can register a deterministic test.fake provider
// adapter (modelbootstrap.WithExtraAdapters) alongside the real ones --
// production never passes this, so it changes nothing about what a real
// deployment does.
func WithModelRuntimeOptions(opts ...modelbootstrap.Option) OpenOption {
	return func(c *openConfig) {
		c.modelRuntimeOpts = append(c.modelRuntimeOpts, opts...)
	}
}

// Open builds a ceochat.Service against the given PostgreSQL store.
func Open(cfg config.Config, store *platformpostgres.Store, opts ...OpenOption) (*Runtime, error) {
	var openCfg openConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&openCfg)
		}
	}
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
	// any execution profile. WithModelRuntime lets a caller that already
	// opened one (internal/executive/bootstrap.Runtime.Models) share it
	// instead of a second one being constructed here.
	modelRuntime := openCfg.sharedModelRuntime
	if modelRuntime == nil {
		modelRuntime, err = modelbootstrap.Open(cfg, store, openCfg.modelRuntimeOpts...)
		if err != nil {
			return nil, fmt.Errorf("open ceochat model runtime: %w", err)
		}
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
	// A chat turn's Harness run can make up to MaxTurns model invocations
	// within the SAME task attempt (one per tool-calling round) before it
	// answers -- unlike Executive's typed-task profile, which never leaves
	// the provisioner's default quota of 1. WithMaxInvocations(MaxTurns) is
	// what makes that legal: without it, InvocationService.Create's second
	// invocation in the same attempt would fail closed with
	// dispatcher_assignment_exhausted even though the first one succeeded.
	// See ceochat.DispatchProvisioner's doc comment for the full boundary.
	authorizedAttempts, err := modelRuntime.Dispatcher.NewAuthorizedAttemptProvisioner(
		modelRuntime.Config.ExecutionPrincipalKey, modeldispatch.WithMaxInvocations(ceochat.MaxTurns),
	)
	if err != nil {
		return nil, fmt.Errorf("create ceochat dispatch assignment provisioner: %w", err)
	}

	searchStore, err := searchpostgres.New(store.Pool())
	if err != nil {
		return nil, fmt.Errorf("create ceochat research store: %w", err)
	}

	conversationStore, err := ceochatpostgres.New(store, organizationID)
	if err != nil {
		return nil, fmt.Errorf("create ceochat conversation store: %w", err)
	}

	costLedger, err := costledgerpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create ceochat cost ledger reader: %w", err)
	}
	memoryRuntime, err := memorybootstrap.Open(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open ceochat memory runtime: %w", err)
	}

	// toolRegistry is the single, host-owned capability catalog every
	// ceochat tool -- research (migrated, unchanged behavior) and the new
	// read-only tasks/runs/finance/memory families -- is registered into.
	// This is what RunSpec.Tools and the Harness's ToolCatalog/ToolExecutor
	// both end up backed by: one registry, never a second tool framework.
	toolRegistry := ceochat.NewToolRegistry()
	if err = ceochat.RegisterResearchTools(toolRegistry, searchStore, searchStore); err != nil {
		return nil, fmt.Errorf("register ceochat research tools: %w", err)
	}
	if err = ceochat.RegisterTaskTools(toolRegistry, taskService); err != nil {
		return nil, fmt.Errorf("register ceochat task tools: %w", err)
	}
	if err = ceochat.RegisterRunTools(toolRegistry, organizationID, runDescriptorLister{store: harnessHistory}, harnessHistory, harnessHistory); err != nil {
		return nil, fmt.Errorf("register ceochat run tools: %w", err)
	}
	if err = ceochat.RegisterFinanceTools(toolRegistry, organizationID, costLedger, costLedger); err != nil {
		return nil, fmt.Errorf("register ceochat finance tools: %w", err)
	}
	if err = ceochat.RegisterMemoryTools(toolRegistry, organizationID, memoryRuntime.Manager); err != nil {
		return nil, fmt.Errorf("register ceochat memory tools: %w", err)
	}

	campaignStore, err := campaignpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create ceochat campaign store: %w", err)
	}
	authorizationStore, err := authorizationpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create ceochat authorization store: %w", err)
	}
	authorizerPolicy, err := authorization.NewWithPolicyReader(authorizationStore, organizationID, cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, fmt.Errorf("create ceochat capability authorizer: %w", err)
	}
	// Approval and Promotion re-check the owner-facing budget against the
	// CURRENT host execution budget floor (CAMPAIGN_EXECUTION_BUDGET_
	// FEASIBILITY_V2), derived from this process's own Model Runtime routing,
	// pricing and Context Engine bound. The Executive limits are the same
	// canonical defaults the Executive worker runs under (only an explicit
	// smoke-time WithExecutiveLimits ever differs, and it never promotes).
	requirementsProvider, err := executionrequirements.New(executionrequirements.Config{
		Registry: registryRepository, Routes: modelRuntime.Store, Costs: modelRuntime.Costs,
		Limits: executive.DefaultLimits(), ContextMaxTotalBytes: cfg.Context.MaxTotalBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("create ceochat campaign execution requirements provider: %w", err)
	}
	approvalService := campaign.NewApprovalService(campaignStore, authorizerPolicy, requirementsProvider)
	// campaign.request_financial_review/campaign.get_financial_review were
	// registered unconditionally but had no real FinanceService anywhere in
	// production to back them (campaign.NewFinanceService had zero non-test
	// callers in the whole repo) -- every real "solicita la revisión
	// financiera" turn failed closed with "finance service not configured".
	// The minimal config below (Store/Tasks/Authorizer) is exactly what
	// TestCampaignFinancialReviewTools already proves is sufficient for the
	// CEO-facing request/read surface; the richer, Harness-capable fields
	// FinanceServiceConfig also accepts belong to a separate, not-yet-built
	// autonomous finance-reviewer capability, out of this round's scope.
	financeService, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID: organizationID, Store: campaignStore,
		Tasks: financeTaskCoordinator{taskService}, Authorizer: authorizerPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("create ceochat finance service: %w", err)
	}
	campToolOpts := []ceochat.CampaignToolsOption{
		ceochat.WithApprovalService(approvalService),
		ceochat.WithFinanceService(financeService),
	}
	if openCfg.submitter != nil {
		promService := campaign.NewPromotionService(campaignStore, openCfg.submitter, authorizerPolicy, requirementsProvider)
		campToolOpts = append(campToolOpts, ceochat.WithPromotionService(promService))
	} else if openCfg.promotionService != nil {
		campToolOpts = append(campToolOpts, ceochat.WithPromotionService(openCfg.promotionService))
	}
	if err = ceochat.RegisterCampaignTools(toolRegistry, organizationID, campaignStore, authorizerPolicy, campToolOpts...); err != nil {
		return nil, fmt.Errorf("register ceochat campaign tools: %w", err)
	}

	service, err := ceochat.Open(ceochat.Service{
		OrganizationID: organizationID,
		Store:          conversationStore,
		Tasks:          taskService,
		Principals:     principalResolver{resolver: roleBoundResolver},
		Assignments:    dispatchProvisioner{provisioner: authorizedAttempts},
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
		Catalog:         ceochat.RegistryToolCatalog{Registry: toolRegistry},
		ToolExecutor:    ceochat.RegistryToolExecutor{Registry: toolRegistry},
		ToolDefinitions: toolRegistry.Definitions(),
	})
	if err != nil {
		return nil, fmt.Errorf("open ceochat service: %w", err)
	}
	return &Runtime{Service: service, Tasks: taskService, ModelRuntime: modelRuntime}, nil
}

// runDescriptorLister adapts *executionharnesspostgres.Store.ListRunDescriptors
// to ceochat.RunLister. It is a type conversion only -- both
// RunDescriptorSummary and RunDescriptorRecord embed the exact same
// executionharness.RunDescriptor and add exactly the same CreatedAt field,
// so this adapter reads through the canonical descriptor store and nothing
// else.
type runDescriptorLister struct {
	store *executionharnesspostgres.Store
}

func (r runDescriptorLister) ListRunDescriptors(ctx context.Context, filter ceochat.RunDescriptorFilter) ([]ceochat.RunDescriptorRecord, error) {
	summaries, err := r.store.ListRunDescriptors(ctx, executionharnesspostgres.RunDescriptorFilter{
		TaskID: filter.TaskID, ExecutionProfileID: filter.ExecutionProfileID, Limit: filter.Limit, Offset: filter.Offset,
	})
	if err != nil {
		return nil, err
	}
	records := make([]ceochat.RunDescriptorRecord, len(summaries))
	for i, summary := range summaries {
		records[i] = ceochat.RunDescriptorRecord{RunDescriptor: summary.RunDescriptor, CreatedAt: summary.CreatedAt}
	}
	return records, nil
}

var _ ceochat.RunLister = runDescriptorLister{}

// principalResolver adapts runtimeadapter.RoleBoundPrincipalResolver's
// int64 principal ID to the plain string ceochat.PrincipalResolver expects
// (the same string form RunIdentity.ExecutionPrincipalID carries
// throughout the Harness).
// financeTaskCoordinator adapts *tasks.Service to campaign.TaskCoordinator.
// *tasks.Service already implements every method that interface needs
// (CreateTask, ClaimTaskByID, StartAttempt, RecordAttemptResult,
// FinalizeTask) with an identical signature except GetTask, which returns
// the richer tasks.TaskDetail rather than the bare tasks.Task
// campaign.TaskCoordinator expects -- embedding covers the rest, this
// overrides only that one method.
type financeTaskCoordinator struct {
	*tasks.Service
}

func (f financeTaskCoordinator) GetTask(ctx context.Context, taskID int64) (tasks.Task, error) {
	detail, err := f.Service.GetTask(ctx, taskID)
	if err != nil {
		return tasks.Task{}, err
	}
	return detail.Task, nil
}

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

// dispatchProvisioner adapts *modeldispatch.AuthorizedAttemptProvisioner's
// (CreateAssignmentResult, error) return to the bare error
// ceochat.DispatchProvisioner expects: ceochat only needs to know whether
// it may proceed to the Harness, never the assignment's own identity or
// quota.
type dispatchProvisioner struct {
	provisioner *modeldispatch.AuthorizedAttemptProvisioner
}

func (d dispatchProvisioner) EnsureAuthorizedAssignmentForRunningAttempt(ctx context.Context, taskID, attemptID int64) error {
	_, err := d.provisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, taskID, attemptID)
	return err
}

var _ ceochat.DispatchProvisioner = dispatchProvisioner{}

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
