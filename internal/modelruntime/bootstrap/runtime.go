package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	dispatchbootstrap "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egressbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelegress/bootstrap"
	identitybootstrap "github.com/Mireuz13/explorarte-organization/internal/modelidentity/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/deepseek"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/gemini"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/mimo"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/openaicompat"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/openairesponses"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/costgate"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskcontextprovider "github.com/Mireuz13/explorarte-organization/internal/tasks/contextprovider"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
)

type RegistryRuntime struct {
	Config   modelruntime.RuntimeConfig
	Registry *modelruntime.RegistryService
	Store    *modelpostgres.Store
}

func OpenRegistry(cfg config.Config, platformStore *platformpostgres.Store) (*RegistryRuntime, error) {
	if platformStore == nil {
		return nil, errors.New("model registry bootstrap requires PostgreSQL store")
	}
	runtimeCfg, err := modelruntime.LoadRuntimeConfig(os.LookupEnv, cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		return nil, err
	}
	registryRepo, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create model registry organization reader: %w", err)
	}
	modelStore, err := modelpostgres.New(platformStore)
	if err != nil {
		return nil, err
	}
	catalog := catalogAdapter{reader: registryRepo}
	service, err := modelruntime.NewRegistryService(cfg.Registry.CanonicalDir, cfg.Tasks.OrganizationID, catalog, modelStore)
	if err != nil {
		return nil, err
	}
	return &RegistryRuntime{Config: runtimeCfg, Registry: service, Store: modelStore}, nil
}

type Runtime struct {
	Config      modelruntime.RuntimeConfig
	Registry    *modelruntime.RegistryService
	Invocations *modelruntime.InvocationService
	Dispatch    *modelruntime.DispatchService
	TaskLeases  tasks.ExecutionLeaseVerifier
	Store       *modelpostgres.Store
	Dispatcher  *dispatchbootstrap.Runtime
	Identity    *identitybootstrap.Runtime
}

func Open(cfg config.Config, platformStore *platformpostgres.Store) (*Runtime, error) {
	if platformStore == nil {
		return nil, errors.New("model runtime bootstrap requires PostgreSQL store")
	}
	runtimeCfg, err := modelruntime.LoadRuntimeConfig(os.LookupEnv, cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		return nil, err
	}
	registryRepo, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create model registry organization reader: %w", err)
	}
	modelStore, err := modelpostgres.New(platformStore)
	if err != nil {
		return nil, err
	}
	taskStore, err := taskpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create model task reader: %w", err)
	}
	taskContextProvider, err := taskcontextprovider.New(taskStore)
	if err != nil {
		return nil, fmt.Errorf("create model task context provider: %w", err)
	}
	contextRuntime, err := contextbootstrap.Open(cfg, platformStore, taskContextProvider)
	if err != nil {
		return nil, fmt.Errorf("open context runtime for models: %w", err)
	}
	authorizationRuntime, err := authorizationbootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, fmt.Errorf("open authorization runtime for models: %w", err)
	}
	egressRuntime, err := egressbootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, fmt.Errorf("open model egress runtime: %w", err)
	}
	dispatchRuntime, err := dispatchbootstrap.Open(cfg, platformStore, taskStore)
	if err != nil {
		return nil, fmt.Errorf("open model dispatch runtime: %w", err)
	}
	identityRuntime, err := identitybootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, fmt.Errorf("open model execution identity runtime: %w", err)
	}
	executionContextViewStore, err := contextcompilerpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("open execution context view store: %w", err)
	}
	catalog := catalogAdapter{reader: registryRepo}
	tasksAdapter := taskAdapter{reader: taskStore}
	contexts := contextAdapter{service: contextRuntime.Service, store: executionContextViewStore}
	evaluator := authorizationAdapter{evaluator: authorizationRuntime.Authorizer}
	registryService, err := modelruntime.NewRegistryService(cfg.Registry.CanonicalDir, cfg.Tasks.OrganizationID, catalog, modelStore)
	if err != nil {
		return nil, err
	}
	invocationService, err := modelruntime.NewInvocationService(cfg.Tasks.OrganizationID, catalog, tasksAdapter, contexts, modelStore, egressRuntime.Store, identityRuntime.Store, dispatchRuntime.Store, modelruntime.ClockFunc(time.Now), cfg.Tasks.OutboxMaxAttempts, cfg.ModelRuntime.SingleProviderTestMode)
	if err != nil {
		return nil, err
	}

	openAIConfig, err := openaicompat.LoadConfig(os.LookupEnv, runtimeCfg.MaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("load openai-compatible provider config: %w", err)
	}
	deepseekConfig, err := deepseek.LoadConfig(os.LookupEnv, runtimeCfg.MaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("load DeepSeek provider config: %w", err)
	}
	mimoConfig, err := mimo.LoadConfig(os.LookupEnv, runtimeCfg.MaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("load MiMo provider config: %w", err)
	}
	geminiConfig, err := gemini.LoadConfig(os.LookupEnv, runtimeCfg.MaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("load Gemini provider config: %w", err)
	}
	openaiResponsesConfig, err := openairesponses.LoadConfig(os.LookupEnv, runtimeCfg.MaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("load OpenAI Responses provider config: %w", err)
	}
	registeredAdapters := make([]modelruntime.ProviderAdapter, 0, 5)
	if openAIConfig.Enabled {
		providerAdapter, providerErr := openaicompat.New(openAIConfig)
		if providerErr != nil {
			return nil, fmt.Errorf("open openai-compatible provider adapter: %w", providerErr)
		}
		registeredAdapters = append(registeredAdapters, providerAdapter)
	}
	if deepseekConfig.Enabled {
		providerAdapter, providerErr := deepseek.New(deepseekConfig)
		if providerErr != nil {
			return nil, fmt.Errorf("open DeepSeek provider adapter: %w", providerErr)
		}
		registeredAdapters = append(registeredAdapters, providerAdapter)
	}
	if mimoConfig.Enabled {
		// MiMo is deliberately not wired into docs/canonical/model-routing.yaml
		// as any role's default in this phase -- registering the adapter here
		// only makes provider=mimo dispatchable when explicitly invoked (the
		// owner's canary smoke tests), never chosen by routing defaults.
		providerAdapter, providerErr := mimo.New(mimoConfig)
		if providerErr != nil {
			return nil, fmt.Errorf("open MiMo provider adapter: %w", providerErr)
		}
		registeredAdapters = append(registeredAdapters, providerAdapter)
	}
	if geminiConfig.Enabled {
		providerAdapter, providerErr := gemini.New(geminiConfig)
		if providerErr != nil {
			return nil, fmt.Errorf("open Gemini provider adapter: %w", providerErr)
		}
		registeredAdapters = append(registeredAdapters, providerAdapter)
	}
	if openaiResponsesConfig.Enabled {
		providerAdapter, providerErr := openairesponses.New(openaiResponsesConfig)
		if providerErr != nil {
			return nil, fmt.Errorf("open OpenAI Responses provider adapter: %w", providerErr)
		}
		registeredAdapters = append(registeredAdapters, providerAdapter)
	}
	adapters := adapter.NewRegistry(registeredAdapters...)
	pricingStore, err := modelpricingpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create model pricing store: %w", err)
	}
	pricingService, err := modelpricing.NewService(pricingStore)
	if err != nil {
		return nil, fmt.Errorf("create model pricing service: %w", err)
	}
	walletLedger, err := costledgerpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create provider wallet ledger: %w", err)
	}
	budgetLedger, err := agentbudgetpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create agent budget ledger: %w", err)
	}
	// mimo.ProviderID is billed via a fixed Token Plan (subscription/quota),
	// not pay-as-you-go -- costgate.Gate skips PriceTier resolution and
	// wallet reservation entirely for it (see costgate/gate.go's Reserve).
	gate, err := costgate.New(pricingService, walletLedger, budgetLedger, mimo.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("create cost/budget gate: %w", err)
	}
	dispatchService, err := modelruntime.NewDispatchService(cfg.Tasks.OrganizationID, runtimeCfg, catalog, tasksAdapter, contexts, evaluator, egressRuntime.Store, egressRuntime.Evaluator, modelStore, dispatchRuntime.Store, dispatchRuntime.Store, identityRuntime.Challenges, modelStore, adapters, modelruntime.ClockFunc(time.Now), modelruntime.WithCostBudgetGate(gate))
	if err != nil {
		return nil, err
	}
	return &Runtime{Config: runtimeCfg, Registry: registryService, Invocations: invocationService, Dispatch: dispatchService, TaskLeases: taskStore, Store: modelStore, Dispatcher: dispatchRuntime, Identity: identityRuntime}, nil
}

type catalogAdapter struct{ reader registry.Reader }

func (a catalogAdapter) CurrentOrganization(ctx context.Context, id string) (modelruntime.OrganizationRef, error) {
	org, err := a.reader.GetOrganization(ctx, id)
	if err != nil {
		return modelruntime.OrganizationRef{}, err
	}
	rev, err := a.reader.GetCurrentRevision(ctx, id)
	if err != nil {
		return modelruntime.OrganizationRef{}, err
	}
	if rev == nil {
		return modelruntime.OrganizationRef{}, registry.ErrNotFound
	}
	return modelruntime.OrganizationRef{ID: org.ID, RevisionID: rev.ID, ModelRoutingHash: rev.DocumentHashes["model-routing.yaml"], ModelEgressPolicyHash: rev.DocumentHashes["model-egress-policy.yaml"], CapabilityMatrixHash: rev.DocumentHashes["capability-matrix.yaml"]}, nil
}

func (a catalogAdapter) GetRole(ctx context.Context, org, id string) (modelruntime.RoleRef, error) {
	r, err := a.reader.GetRole(ctx, org, id)
	if err != nil {
		return modelruntime.RoleRef{}, err
	}
	policy := ""
	if r.ModelPolicy != nil {
		policy = *r.ModelPolicy
	}
	return modelruntime.RoleRef{ID: r.ID, ModelPolicy: policy, Enabled: r.Enabled, Executable: r.Executable, AuthorityClass: r.AuthorityClass, UnitID: r.UnitID}, nil
}

func (a catalogAdapter) ListRoles(ctx context.Context, org string) ([]modelruntime.RoleRef, error) {
	values, err := a.reader.ListRoles(ctx, org, registry.RoleFilter{})
	if err != nil {
		return nil, err
	}
	result := make([]modelruntime.RoleRef, 0, len(values))
	for _, r := range values {
		policy := ""
		if r.ModelPolicy != nil {
			policy = *r.ModelPolicy
		}
		result = append(result, modelruntime.RoleRef{ID: r.ID, ModelPolicy: policy, Enabled: r.Enabled, Executable: r.Executable, AuthorityClass: r.AuthorityClass, UnitID: r.UnitID})
	}
	return result, nil
}

type taskAdapter struct{ reader tasks.TaskReader }

func (a taskAdapter) GetTaskAttempt(ctx context.Context, taskID, attemptID int64) (modelruntime.TaskAttemptRef, error) {
	detail, err := a.reader.GetTask(ctx, taskID)
	if err != nil {
		return modelruntime.TaskAttemptRef{}, err
	}
	var attempt *tasks.Attempt
	for i := range detail.Attempts {
		if detail.Attempts[i].ID == attemptID {
			attempt = &detail.Attempts[i]
			break
		}
	}
	if attempt == nil {
		return modelruntime.TaskAttemptRef{}, tasks.ErrNotFound
	}
	if detail.ActiveLease == nil || detail.ActiveLease.AttemptID != attemptID {
		return modelruntime.TaskAttemptRef{}, modelruntime.ErrTaskAttemptRejected
	}
	return modelruntime.TaskAttemptRef{TaskID: detail.Task.ID, AttemptID: attempt.ID, OrganizationID: detail.Task.OrganizationID, OrganizationRevisionID: detail.Task.OrganizationRevisionID, AssignedRoleID: detail.Task.AssignedRoleID, TaskStatus: string(detail.Task.Status), AttemptStatus: string(attempt.State), LeaseHolderID: detail.ActiveLease.HolderID, LeaseExpiresAt: detail.ActiveLease.ExpiresAt}, nil
}

type contextAdapter struct {
	service contextengine.Service
	// store persists the durable ExecutionContextView (M1.1) through the
	// same contextcompiler.ContextAssemblyService Executive's context
	// adapter uses (internal/executive/runtimeadapter/context.go), so the
	// two resolve/persist the identical view identity for the same
	// canonical snapshot rather than two independently reconstructed byte
	// slices.
	store contextcompiler.ExecutionContextViewStore
}

// resolveRender is a trivial delegating adapter to
// contextcompiler.ContextAssemblyService.ResolveAndPersist, which itself
// wraps the single shared deterministic provider-visible render resolution
// algorithm (contextcompiler.ResolveProviderContext, also used by
// Executive's context adapter) with durable persistence. It exists only so
// GetContextSnapshot (pre-dispatch integrity hash), RenderContextSnapshot
// (the bytes actually sent to the provider), and GetProviderRenderTelemetry
// (observability) below keep a single, local call site rather than three
// separate calls. This is the same single-source-of-truth invariant that
// fixed the R10 context_render_hash_mismatch bug, now extended across
// package boundaries so Executive and Model Runtime can never independently
// diverge either -- and, since M1.1, extended to durable identity too.
func resolveRender(ctx context.Context, store contextcompiler.ExecutionContextViewStore, snapshot contextengine.Snapshot) (contextcompiler.ExecutionContextView, error) {
	return contextcompiler.ContextAssemblyService{Store: store}.ResolveAndPersist(ctx, snapshot)
}

func (a contextAdapter) GetContextSnapshot(ctx context.Context, id int64) (modelruntime.ContextSnapshotRef, error) {
	snapshot, err := a.service.Get(ctx, id, true)
	if err != nil {
		return modelruntime.ContextSnapshotRef{}, err
	}
	classes := []string{}
	seen := map[string]struct{}{}
	for _, segment := range snapshot.Segments {
		if !segment.Included {
			continue
		}
		value := string(segment.DataClass)
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			classes = append(classes, value)
		}
	}
	scope := modelegress.ExecutiveScopeMarker(snapshot.ActorRoleID, snapshot.Purpose, snapshot.CorrelationID, snapshot.TaskRef)
	// Context Engine task references are canonical task:<id> strings, while the
	// modelruntime task-attempt contract intentionally stores only the numeric
	// task scope. Normalize only at this adapter boundary; scope derivation above
	// retains the canonical task:<id> reference.
	taskRef := strings.TrimPrefix(snapshot.TaskRef, "task:")
	renderedHash := snapshot.RenderedHash
	if render, renderErr := resolveRender(ctx, a.store, snapshot); renderErr == nil {
		renderedHash = render.ProviderVisibleDigest
	}
	return modelruntime.ContextSnapshotRef{
		ID: snapshot.ID, OrganizationID: snapshot.OrganizationID, OrganizationRevisionID: snapshot.OrganizationRevisionID,
		ActorRoleID: snapshot.ActorRoleID, TaskRef: taskRef, Status: string(snapshot.Status), RenderedHash: renderedHash,
		DataClasses: classes, ExecutiveScope: scope,
	}, nil
}

func (a contextAdapter) ValidateContextSnapshot(ctx context.Context, id int64) error {
	result, err := a.service.Validate(ctx, id)
	if err != nil {
		return err
	}
	if !result.Valid {
		return fmt.Errorf("%w: %s", modelruntime.ErrContextRejected, result.ReasonCode)
	}
	return nil
}

func (a contextAdapter) RenderContextSnapshot(ctx context.Context, id int64) ([]byte, error) {
	snapshot, err := a.service.Get(ctx, id, true)
	if err != nil {
		return nil, err
	}
	// Replicate contextengine.Service.Render's exact pre-render checks
	// (status + Validate) -- this adapter fetches the snapshot itself to
	// project/render it, so it can no longer rely on Service.Render to do
	// that validation for it.
	if snapshot.Status == contextengine.SnapshotInvalidated {
		return nil, contextengine.ErrSnapshotInvalidated
	}
	validation, err := a.service.Validate(ctx, id)
	if err != nil {
		return nil, err
	}
	if !validation.Valid {
		return nil, contextengine.ErrSnapshotStale
	}
	render, err := resolveRender(ctx, a.store, snapshot)
	if err != nil {
		return nil, err
	}
	return render.ProviderVisibleBytes, nil
}

// GetProviderRenderTelemetry implements modelruntime.ProviderRenderTelemetryReader
// (R10.4, optional capability). Derives from the exact same resolveRender
// call as RenderContextSnapshot/GetContextSnapshot above -- never a
// separate computation.
func (a contextAdapter) GetProviderRenderTelemetry(ctx context.Context, id int64) (modelruntime.ProviderRenderTelemetry, error) {
	snapshot, err := a.service.Get(ctx, id, true)
	if err != nil {
		return modelruntime.ProviderRenderTelemetry{}, err
	}
	render, err := resolveRender(ctx, a.store, snapshot)
	if err != nil {
		return modelruntime.ProviderRenderTelemetry{}, err
	}
	if render.FellBackToCanonical {
		return modelruntime.ProviderRenderTelemetry{
			FallbackToLegacy: true, FallbackReason: render.FallbackReason,
			ProviderRenderHash: render.ProviderVisibleDigest, ProviderVisibleBytes: render.ProviderVisibleByteCount,
		}, nil
	}
	pr := render
	return modelruntime.ProviderRenderTelemetry{
		Version: pr.ProviderRenderVersion, FallbackToLegacy: false,
		StablePrefixHash: pr.StablePrefixHash, StablePrefixBytes: pr.StablePrefixBytes,
		DynamicSuffixHash: pr.DynamicSuffixHash, DynamicSuffixBytes: pr.DynamicSuffixBytes,
		ProviderRenderHash: pr.ProviderVisibleDigest, ProviderVisibleBytes: pr.ProviderVisibleByteCount,
	}, nil
}

type authorizationAdapter struct{ evaluator authorization.Evaluator }

func (a authorizationAdapter) EvaluateDispatch(ctx context.Context, org string, revision int64, actor, resourceID, digest string) (modelruntime.AuthorizationDecision, error) {
	result, err := a.evaluator.Evaluate(ctx, authorization.EvaluationRequest{OrganizationID: org, OrganizationRevisionID: revision, ActorRoleID: actor, CapabilityID: "model.invoke", ResourceType: "model_invocation", ResourceID: resourceID, ActionDigest: digest})
	if err != nil {
		return modelruntime.AuthorizationDecision{}, err
	}
	effect := modelegress.AuthorizationDeny
	if result.Effect == authorization.EffectAllow {
		effect = modelegress.AuthorizationAllow
	}
	return modelruntime.AuthorizationDecision{Effect: effect, Allowed: result.Effect == authorization.EffectAllow, ReasonCode: string(result.ReasonCode), MatrixHash: result.MatrixHash}, nil
}
