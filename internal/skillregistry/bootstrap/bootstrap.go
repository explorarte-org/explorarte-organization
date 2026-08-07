package bootstrap

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	skillregistryauthz "github.com/Mireuz13/explorarte-organization/internal/skillregistry/authz"
	skillregistrypostgres "github.com/Mireuz13/explorarte-organization/internal/skillregistry/postgres"
)

type Runtime struct {
	Manager        *skillregistry.Manager
	Domain         *skillregistry.Service
	Store          *skillregistrypostgres.Store
	Gate           *skillregistryauthz.Gate
	Authorizer     *authorization.Authorizer
	Registry       *registry.PostgresRepository
	OrganizationID string
}

func Open(cfg config.Config, platformStore *platformpostgres.Store) (*Runtime, error) {
	if platformStore == nil {
		return nil, fmt.Errorf("skill registry bootstrap requires PostgreSQL store")
	}
	registryRepository, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create skill registry registry repository: %w", err)
	}
	authorizationStore, err := authorizationpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create skill registry authorization store: %w", err)
	}
	authorizer, err := authorization.NewWithPolicyReader(authorizationStore, cfg.Tasks.OrganizationID, cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, fmt.Errorf("create skill registry authorizer: %w", err)
	}
	gate, err := skillregistryauthz.New(authorizer, registryRepository, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create skill registry authorization gate: %w", err)
	}
	store, err := skillregistrypostgres.New(platformStore, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create skill registry store: %w", err)
	}
	domain := skillregistry.NewService(nil)
	manager, err := skillregistry.NewManager(domain, store, gate)
	if err != nil {
		return nil, fmt.Errorf("create skill registry manager: %w", err)
	}
	return &Runtime{
		Manager: manager, Domain: domain, Store: store, Gate: gate,
		Authorizer: authorizer, Registry: registryRepository,
		OrganizationID: cfg.Tasks.OrganizationID,
	}, nil
}
