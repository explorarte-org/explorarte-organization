package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	dispatchbootstrap "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egressbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelegress/bootstrap"
	identitybootstrap "github.com/Mireuz13/explorarte-organization/internal/modelidentity/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
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
	contextRuntime, err := contextbootstrap.Open(cfg, platformStore)
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
	catalog := catalogAdapter{reader: registryRepo}
	tasksAdapter := taskAdapter{reader: taskStore}
	contexts := contextAdapter{service: contextRuntime.Service}
	evaluator := authorizationAdapter{evaluator: authorizationRuntime.Authorizer}
	registryService, err := modelruntime.NewRegistryService(cfg.Registry.CanonicalDir, cfg.Tasks.OrganizationID, catalog, modelStore)
	if err != nil {
		return nil, err
	}
	invocationService, err := modelruntime.NewInvocationService(cfg.Tasks.OrganizationID, catalog, tasksAdapter, contexts, modelStore, egressRuntime.Store, identityRuntime.Store, dispatchRuntime.Store, modelruntime.ClockFunc(time.Now), cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		return nil, err
	}
	adapters := adapter.NewRegistry()
	dispatchService, err := modelruntime.NewDispatchService(cfg.Tasks.OrganizationID, runtimeCfg, catalog, tasksAdapter, contexts, evaluator, egressRuntime.Store, egressRuntime.Evaluator, modelStore, dispatchRuntime.Store, dispatchRuntime.Store, identityRuntime.Challenges, modelStore, adapters, modelruntime.ClockFunc(time.Now))
	if err != nil {
		return nil, err
	}
	return &Runtime{Config: runtimeCfg, Registry: registryService, Invocations: invocationService, Dispatch: dispatchService, Store: modelStore, Dispatcher: dispatchRuntime, Identity: identityRuntime}, nil
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

type contextAdapter struct{ service contextengine.Service }

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
	return modelruntime.ContextSnapshotRef{ID: snapshot.ID, OrganizationID: snapshot.OrganizationID, OrganizationRevisionID: snapshot.OrganizationRevisionID, ActorRoleID: snapshot.ActorRoleID, TaskRef: snapshot.TaskRef, Status: string(snapshot.Status), RenderedHash: snapshot.RenderedHash, DataClasses: classes}, nil
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
	return a.service.Render(ctx, id)
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
