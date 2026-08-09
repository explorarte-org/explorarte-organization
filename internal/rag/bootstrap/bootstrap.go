package bootstrap

import (
	"fmt"
	"os"
	"strings"

	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime/adapter/bgem3"
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
	// ragEmbeddingModelVersion is Gemini's identity column
	// (embedding_model_version, migration 000028) — this system's own
	// versioning scheme for Gemini embeddings, not a string Google
	// assigns. Bumping it is a deliberate re-embedding decision (a new
	// row per chunk, never an UPDATE), exactly like ModelRevision is for
	// BGE-M3.
	ragEmbeddingModelVersion = "v1"
)

// R30: exactly one embedding profile is active at a time — mirrors
// internal/memory/bootstrap's constants and activeEmbeddingProfile
// exactly (internal/rag cannot share code with internal/memory's
// bootstrap package any more than their domain packages can import each
// other — see scripts/check-memory-fitness.sh/check-rag-fitness.sh).
const (
	embeddingProfileGemini768  = "gemini-768"
	embeddingProfileBGEM3Local = "bge-m3-local-1024"
)

func activeEmbeddingProfile() (string, error) {
	profile := strings.TrimSpace(os.Getenv("ORG_EMBEDDING_ACTIVE_PROFILE"))
	if profile == "" {
		return embeddingProfileGemini768, nil
	}
	if profile != embeddingProfileGemini768 && profile != embeddingProfileBGEM3Local {
		return "", fmt.Errorf("unknown ORG_EMBEDDING_ACTIVE_PROFILE %q (want %q or %q)", profile, embeddingProfileGemini768, embeddingProfileBGEM3Local)
	}
	return profile, nil
}

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

// openSemanticSearch wires the optional vector retrieval channel under
// whichever embedding profile is active (see activeEmbeddingProfile). It
// returns (nil, nil) — Query degrades to exact+lexical only — unless that
// profile's provider is explicitly enabled, exactly the same
// disabled-by-default behavior each adapter's own LoadConfig already gives
// it. Enabling either profile in production also requires a real wallet
// balance and a priced tier for that exact provider/model (migration
// 000027 seeds Gemini's; BGE-M3's is seeded operationally via `orgctl
// budget set-price`/`set-balance`, see deployments/bgem3/RUNBOOK-deploy-
// sidecar.md) — a wallet with $0 will simply mean every query's vector
// channel skips itself via ErrInsufficientBalance (see
// rag.Manager.embedQuery), not an error.
func openSemanticSearch(platformStore *platformpostgres.Store) (*rag.SemanticSearchDeps, error) {
	profile, err := activeEmbeddingProfile()
	if err != nil {
		return nil, err
	}
	switch profile {
	case embeddingProfileBGEM3Local:
		return openBGEM3SemanticSearch(platformStore)
	default:
		return openGeminiSemanticSearch(platformStore)
	}
}

func sharedSpendControls(platformStore *platformpostgres.Store) (*modelpricing.Service, *costledgerpostgres.Store, *agentbudgetpostgres.Store, error) {
	pricingStore, err := modelpricingpostgres.New(platformStore)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create model pricing store: %w", err)
	}
	pricingService, err := modelpricing.NewService(pricingStore)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create model pricing service: %w", err)
	}
	ledger, err := costledgerpostgres.New(platformStore)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create cost ledger store: %w", err)
	}
	budgets, err := agentbudgetpostgres.New(platformStore)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create agent budget store: %w", err)
	}
	return pricingService, ledger, budgets, nil
}

func openGeminiSemanticSearch(platformStore *platformpostgres.Store) (*rag.SemanticSearchDeps, error) {
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
	pricingService, ledger, budgets, err := sharedSpendControls(platformStore)
	if err != nil {
		return nil, err
	}
	return &rag.SemanticSearchDeps{
		OnlineAdapter: adapter, Pricing: pricingService,
		Wallet: ledger, Budgets: budgets,
		ProviderID: ragEmbeddingProviderID, ProviderModelID: ragEmbeddingModelID,
		OutputDimensionality: ragEmbeddingDimension, PromptTemplateVersion: gemini.PromptTemplateV1,
		Identity: rag.EmbeddingIdentity{ModelID: ragEmbeddingModelID, ModelVersion: ragEmbeddingModelVersion},
	}, nil
}

// openBGEM3SemanticSearch activates R30's local, operational profile.
// LocalComputeOnly=true: no Pricing/Wallet/Budgets — a local, unbilled
// process must never be forced through the monetary ledger (not even at
// a seeded $0 price, which would distort that ledger and give this
// profile a wallet-insufficient-balance failure mode it structurally
// cannot have). The adapter's own MaxConcurrency/MaxQueueDepth are the
// entire resource budget; Adapter.Metrics() is the accounting.
func openBGEM3SemanticSearch(platformStore *platformpostgres.Store) (*rag.SemanticSearchDeps, error) {
	embeddingConfig, err := bgem3.LoadConfig(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("load bge-m3 embedding provider config: %w", err)
	}
	adapter, err := bgem3.New(embeddingConfig)
	if err != nil {
		return nil, fmt.Errorf("create bge-m3 embedding provider adapter: %w", err)
	}
	if adapter == nil {
		return nil, nil
	}
	return &rag.SemanticSearchDeps{
		OnlineAdapter: adapter, LocalComputeOnly: true,
		ProviderID: bgem3.ProviderID, ProviderModelID: embeddingConfig.ModelRevision,
		OutputDimensionality: embeddingConfig.ExpectedDimension, PromptTemplateVersion: embeddingConfig.PromptTemplateVersion,
		Identity: rag.EmbeddingIdentity{
			ModelID: bgem3.ProviderID, ModelRevision: embeddingConfig.ModelRevision, ArtifactSHA256: embeddingConfig.ArtifactSHA256,
			TokenizerRevision: embeddingConfig.TokenizerRevision, Normalization: embeddingConfig.Normalization, Pooling: embeddingConfig.Pooling,
		},
	}, nil
}
