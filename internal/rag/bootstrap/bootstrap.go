package bootstrap

import (
	"fmt"
	"os"

	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime/adapter/gemini"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragauthz "github.com/Mireuz13/explorarte-organization/internal/rag/authz"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
	ragroles "github.com/Mireuz13/explorarte-organization/internal/rag/roles"
)

// ragEmbeddingDimension and ragEmbeddingModelID are R29's chosen defaults —
// see docs/implementation/branch-29-embedding-retrieval/DESIGN.md. Changing
// either means every existing rag_chunk_embeddings row was produced under a
// different, incompatible configuration; that is a new model version and a
// re-embedding pass, never a silent constant change.
const (
	ragEmbeddingProviderID       = "gemini"
	ragEmbeddingModelID          = "gemini-embedding-2"
	ragEmbeddingDimension        = 768
	ragEmbeddingMaxResponseBytes = 1 << 20
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
	semantic, err := openSemanticSearch(platformStore)
	if err != nil {
		return nil, fmt.Errorf("create rag semantic search deps: %w", err)
	}
	domain := rag.NewService(nil)
	manager, err := rag.NewManager(domain, store, gate, namespaces, semantic)
	if err != nil {
		return nil, fmt.Errorf("create rag manager: %w", err)
	}
	return &Runtime{
		Manager: manager, Domain: domain, Store: store, Gate: gate, Namespaces: namespaces,
		Authorization: authRuntime.Service, Registry: registryRepository,
		OrganizationID: cfg.Tasks.OrganizationID,
	}, nil
}

// openSemanticSearch wires the optional vector retrieval channel. It
// returns (nil, nil) — Query degrades to exact+lexical only — unless
// ORG_EMBEDDING_PROVIDER_GEMINI_ENABLED is explicitly set, exactly the same
// disabled-by-default behavior gemini.LoadConfig already gives the chat
// adapter. Enabling this in production also requires a real
// gemini-embedding-2 wallet balance (orgctl budget set-balance) and a
// priced tier (migration 000027 seeds one) — a wallet with $0 will simply
// mean every query's vector channel skips itself via ErrInsufficientBalance
// (see rag.Manager.embedQuery), not an error.
func openSemanticSearch(platformStore *platformpostgres.Store) (*rag.SemanticSearchDeps, error) {
	embeddingConfig, err := gemini.LoadConfig(os.LookupEnv, ragEmbeddingMaxResponseBytes)
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

	return &rag.SemanticSearchDeps{
		OnlineAdapter: adapter, Pricing: pricingService,
		Wallet: ledger, Budgets: budgets,
		ProviderID: ragEmbeddingProviderID, ProviderModelID: ragEmbeddingModelID,
		OutputDimensionality: ragEmbeddingDimension, PromptTemplateVersion: gemini.PromptTemplateV1,
	}, nil
}
