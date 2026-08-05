package bootstrap

import (
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

type Runtime struct {
	Service    *authorization.Service
	Authorizer *authorization.Authorizer
	Registry   *registry.PostgresRepository
	Store      *authorizationpostgres.Store
}

func Open(cfg config.Config, store *platformpostgres.Store) (*Runtime, error) {
	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		return nil, fmt.Errorf("create authorization registry repository: %w", err)
	}
	authorizationStore, err := authorizationpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create authorization store: %w", err)
	}
	policy, err := authorization.NewWithPolicyReader(authorizationStore, cfg.Tasks.OrganizationID, cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, fmt.Errorf("create static capability evaluator: %w", err)
	}
	service, err := authorization.NewService(authorization.ServiceConfig{
		OrganizationID:    cfg.Tasks.OrganizationID,
		DefaultTTL:        cfg.Authorization.DefaultTTL,
		MaxTTL:            cfg.Authorization.MaxTTL,
		ExpireBatchSize:   cfg.Authorization.ExpireBatchSize,
		OutboxMaxAttempts: cfg.Tasks.OutboxMaxAttempts,
	}, policy, authorizationStore, authorization.ClockFunc(time.Now))
	if err != nil {
		return nil, fmt.Errorf("create authorization service: %w", err)
	}
	return &Runtime{Service: service, Authorizer: policy, Registry: registryRepository, Store: authorizationStore}, nil
}
