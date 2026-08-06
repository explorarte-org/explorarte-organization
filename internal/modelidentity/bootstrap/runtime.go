package bootstrap

import (
	"context"
	"fmt"
	"time"

	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitypostgres "github.com/Mireuz13/explorarte-organization/internal/modelidentity/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

type Runtime struct {
	Policy     *modelidentity.PolicyService
	Keys       *modelidentity.KeyService
	Challenges *modelidentity.ChallengeService
	Store      *identitypostgres.Store
}

func Open(cfg config.Config, platformStore *platformpostgres.Store) (*Runtime, error) {
	if platformStore == nil {
		return nil, fmt.Errorf("model identity bootstrap requires PostgreSQL store")
	}
	registryRepo, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, err
	}
	authRuntime, err := authorizationbootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, fmt.Errorf("open authorization runtime for model identity: %w", err)
	}
	dispatchStore, err := dispatchpostgres.New(platformStore)
	if err != nil {
		return nil, err
	}
	store, err := identitypostgres.New(platformStore)
	if err != nil {
		return nil, err
	}
	policy, err := modelidentity.NewPolicyService(cfg.Registry.CanonicalDir, cfg.Tasks.OrganizationID, store)
	if err != nil {
		return nil, err
	}
	catalog := catalogAdapter{reader: registryRepo}
	keys, err := modelidentity.NewKeyService(cfg.Tasks.OrganizationID, authRuntime.Authorizer, catalog, dispatchStore, store, modelidentity.ClockFunc(time.Now))
	if err != nil {
		return nil, err
	}
	challenges, err := modelidentity.NewChallengeService(store, modelidentity.ClockFunc(time.Now))
	if err != nil {
		return nil, err
	}
	return &Runtime{Policy: policy, Keys: keys, Challenges: challenges, Store: store}, nil
}

type catalogAdapter struct{ reader registry.Reader }

func (a catalogAdapter) CurrentRevision(ctx context.Context, organizationID string) (int64, error) {
	rev, err := a.reader.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	if rev == nil {
		return 0, registry.ErrNotFound
	}
	return rev.ID, nil
}

func (a catalogAdapter) GetRole(ctx context.Context, organizationID, roleID string) (modeldispatch.RoleRef, error) {
	role, err := a.reader.GetRole(ctx, organizationID, roleID)
	if err != nil {
		return modeldispatch.RoleRef{}, err
	}
	return modeldispatch.RoleRef{ID: role.ID, Enabled: role.Enabled, Executable: role.Executable, AuthorityClass: role.AuthorityClass}, nil
}

var _ modelidentity.PrincipalResolver = (*dispatchpostgres.Store)(nil)
var _ modeldispatch.ExecutionPrincipalResolver = (*dispatchpostgres.Store)(nil)
