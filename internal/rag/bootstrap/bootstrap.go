package bootstrap

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragauthz "github.com/Mireuz13/explorarte-organization/internal/rag/authz"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
	ragroles "github.com/Mireuz13/explorarte-organization/internal/rag/roles"
)

type Runtime struct {
	Manager        *rag.Manager
	Domain         *rag.Service
	Store          *ragpostgres.Store
	Gate           *ragauthz.Gate
	Namespaces     *ragroles.Resolver
	Authorizer     *authorization.Authorizer
	Registry       *registry.PostgresRepository
	OrganizationID string
}

func Open(cfg config.Config, platformStore *platformpostgres.Store) (*Runtime, error) {
	if platformStore == nil {
		return nil, fmt.Errorf("rag bootstrap requires PostgreSQL store")
	}
	registryRepository, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create rag registry repository: %w", err)
	}
	authorizationStore, err := authorizationpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create rag authorization store: %w", err)
	}
	authorizer, err := authorization.NewWithPolicyReader(authorizationStore, cfg.Tasks.OrganizationID, cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, fmt.Errorf("create rag authorizer: %w", err)
	}
	gate, err := ragauthz.New(authorizer, registryRepository, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create rag authorization gate: %w", err)
	}
	namespaces, err := ragroles.New(registryRepository)
	if err != nil {
		return nil, fmt.Errorf("create rag namespace resolver: %w", err)
	}
	store, err := ragpostgres.New(platformStore, cfg.Tasks.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("create rag store: %w", err)
	}
	domain := rag.NewService(nil)
	manager, err := rag.NewManager(domain, store, gate, namespaces)
	if err != nil {
		return nil, fmt.Errorf("create rag manager: %w", err)
	}
	return &Runtime{
		Manager: manager, Domain: domain, Store: store, Gate: gate, Namespaces: namespaces,
		Authorizer: authorizer, Registry: registryRepository,
		OrganizationID: cfg.Tasks.OrganizationID,
	}, nil
}
