package bootstrap

import (
	"fmt"
	"os"

	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime/adapter/gemini"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	memoryauthz "github.com/Mireuz13/explorarte-organization/internal/memory/authz"
	memorypostgres "github.com/Mireuz13/explorarte-organization/internal/memory/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// See internal/rag/bootstrap for why these exact constants — same model,
// same dimension, same reasoning about not changing them silently.
const (
	memoryEmbeddingProviderID       = "gemini"
	memoryEmbeddingModelID          = "gemini-embedding-2"
	memoryEmbeddingDimension        = 768
	memoryEmbeddingMaxResponseBytes = 1 << 20
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
	semantic, err := openSemanticSearch(platformStore, store)
	if err != nil {
		return nil, fmt.Errorf("create memory semantic search deps: %w", err)
	}
	domain := memory.NewService(memory.SystemClock{})
	manager, err := memory.NewManager(domain, store, gate, semantic)
	if err != nil {
		return nil, fmt.Errorf("create memory manager: %w", err)
	}
	return &Runtime{
		Manager: manager, Domain: domain, Store: store, Gate: gate,
		Authorizer: authorizer, Registry: registryRepository,
		OrganizationID: cfg.Tasks.OrganizationID,
	}, nil
}

// openSemanticSearch mirrors internal/rag/bootstrap's function of the same
// name — see that file's doc comment for the full rationale. Disabled by
// default via the same ORG_EMBEDDING_PROVIDER_GEMINI_ENABLED flag; memory
// and rag each construct their own adapter/circuit-breaker instance rather
// than sharing one, consistent with internal/memory never importing
// internal/rag.
func openSemanticSearch(platformStore *platformpostgres.Store, embeddings memory.EmbeddingRepository) (*memory.SemanticSearchDeps, error) {
	embeddingConfig, err := gemini.LoadConfig(os.LookupEnv, memoryEmbeddingMaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("load embedding provider config: %w", err)
	}
	adapter, err := gemini.New(embeddingConfig)
	if err != nil {
		return nil, fmt.Errorf("create embedding provider adapter: %w", err)
	}
	if adapter == nil {
		return nil, nil
	}

	pricingStore, err := modelpricingpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create model pricing store: %w", err)
	}
	pricingService, err := modelpricing.NewService(pricingStore)
	if err != nil {
		return nil, fmt.Errorf("create model pricing service: %w", err)
	}
	ledger, err := costledgerpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create cost ledger store: %w", err)
	}
	budgets, err := agentbudgetpostgres.New(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create agent budget store: %w", err)
	}

	return &memory.SemanticSearchDeps{
		Embeddings: embeddings, OnlineAdapter: adapter, Pricing: pricingService,
		Wallet: ledger, Budgets: budgets,
		ProviderID: memoryEmbeddingProviderID, ProviderModelID: memoryEmbeddingModelID,
		OutputDimensionality: memoryEmbeddingDimension, PromptTemplateVersion: gemini.PromptTemplateV1,
	}, nil
}
