package bootstrap

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
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
	Authorization  *authorization.Service
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
	// rag.publish_approved carries approval:policy_or_human in
	// capability-matrix.yaml. The bare *authorization.Authorizer always
	// returns EffectApprovalRequired for any capability with a non-empty
	// approval mode, even for the owner acting directly; only
	// *authorization.Service resolves a consumed approval request back to
	// EffectAllow, so the gate must evaluate through the full service.
	authRuntime, err := authorizationbootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, fmt.Errorf("create rag authorization runtime: %w", err)
	}
	gate, err := ragauthz.New(authRuntime.Service, registryRepository, cfg.Tasks.OrganizationID)
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
		Authorization: authRuntime.Service, Registry: registryRepository,
		OrganizationID: cfg.Tasks.OrganizationID,
	}, nil
}
