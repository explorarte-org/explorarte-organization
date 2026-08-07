package bootstrap

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	memoryauthz "github.com/Mireuz13/explorarte-organization/internal/memory/authz"
	memorypostgres "github.com/Mireuz13/explorarte-organization/internal/memory/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

type Runtime struct {
	Manager        *memory.Manager
	Domain         *memory.Service
	Store          *memorypostgres.Store
	Gate           *memoryauthz.Gate
	Authorizer     *authorization.Authorizer
	Registry       *registry.PostgresRepository
	OrganizationID string
}

func Open(cfg config.Config, platformStore *platformpostgres.Store) (*Runtime, error) {
	if platformStore == nil {
		return nil, fmt.Errorf("memory bootstrap requires PostgreSQL store")
	}
	registryRepository, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create memory registry repository: %w", err)
	}
	authorizationStore, err := authorizationpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create memory authorization store: %w", err)
	}
	authorizer, err := authorization.NewWithPolicyReader(authorizationStore, cfg.Tasks.OrganizationID, cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, fmt.Errorf("create memory authorizer: %w", err)
	}
	gate, err := memoryauthz.New(authorizer, registryRepository, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create memory authorization gate: %w", err)
	}
	store, err := memorypostgres.New(platformStore, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create memory store: %w", err)
	}
	domain := memory.NewService(memory.SystemClock{})
	manager, err := memory.NewManager(domain, store, gate)
	if err != nil {
		return nil, fmt.Errorf("create memory manager: %w", err)
	}
	return &Runtime{
		Manager: manager, Domain: domain, Store: store, Gate: gate,
		Authorizer: authorizer, Registry: registryRepository,
		OrganizationID: cfg.Tasks.OrganizationID,
	}, nil
}
