package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime/adapter/bgem3"
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
	// memoryEmbeddingModelVersion mirrors internal/rag/bootstrap's
	// ragEmbeddingModelVersion exactly — this system's own versioning
	// scheme for Gemini embeddings (embedding_model_version, migration
	// 000028), not a string Google assigns.
	memoryEmbeddingModelVersion = "v1"
)

// R30: exactly one embedding profile is active at a time — ORG_EMBEDDING_
// ACTIVE_PROFILE selects it, defaulting to Gemini (R29's already-vetted
// behavior) when unset, so an existing deployment's configuration keeps
// working unchanged. "gemini-768" and "bge-m3-local-1024" are the only
// two valid values; anything else is a hard startup error, never a silent
// fallback — an operator who misspells the profile must find out at
// process start, not by an unexplained absence of the vector channel.
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

// openSemanticSearch wires the optional vector retrieval channel under
// whichever embedding profile is active. It returns (nil, nil) — Search
// degrades to exact+recency only — unless that profile's provider is
// explicitly enabled, exactly the same disabled-by-default behavior each
// adapter's own LoadConfig already gives it.
func openSemanticSearch(platformStore *platformpostgres.Store, store *memorypostgres.Store) (*memory.SemanticSearchDeps, error) {
	profile, err := activeEmbeddingProfile()
	if err != nil {
		return nil, err
	}
	switch profile {
	case embeddingProfileBGEM3Local:
		return openBGEM3SemanticSearch(platformStore, store)
	default:
		return openGeminiSemanticSearch(platformStore, store)
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

func openGeminiSemanticSearch(platformStore *platformpostgres.Store, store *memorypostgres.Store) (*memory.SemanticSearchDeps, error) {
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
	pricingService, ledger, budgets, err := sharedSpendControls(platformStore)
	if err != nil {
		return nil, err
	}
	return &memory.SemanticSearchDeps{
		InsertVector: func(ctx context.Context, organizationID, entryID, inputHash string, vector []float32, createdAt time.Time) error {
			return store.InsertEntryEmbedding(ctx, memory.EntryEmbedding{
				OrganizationID: organizationID, EntryID: entryID,
				EmbeddingModelID: memoryEmbeddingModelID, EmbeddingModelVersion: "v1", EmbeddingDimension: memoryEmbeddingDimension,
				PromptTemplateVersion: gemini.PromptTemplateV1, InputHash: inputHash, Vector: vector, CreatedAt: createdAt,
			})
		},
		OnlineAdapter: adapter, Pricing: pricingService, Wallet: ledger, Budgets: budgets,
		ProviderID: memoryEmbeddingProviderID, ProviderModelID: memoryEmbeddingModelID,
		OutputDimensionality: memoryEmbeddingDimension, PromptTemplateVersion: gemini.PromptTemplateV1,
		Identity: memory.EmbeddingIdentity{ModelID: memoryEmbeddingModelID, ModelVersion: memoryEmbeddingModelVersion},
	}, nil
}

// openBGEM3SemanticSearch activates R30's local, operational profile.
// LocalComputeOnly=true: no Pricing/Wallet/Budgets — mirrors
// internal/rag/bootstrap's openBGEM3SemanticSearch exactly (see that
// function's doc comment for why a local, unbilled process must never be
// forced through the monetary ledger, not even at a seeded $0 price).
func openBGEM3SemanticSearch(platformStore *platformpostgres.Store, store *memorypostgres.Store) (*memory.SemanticSearchDeps, error) {
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
	return &memory.SemanticSearchDeps{
		InsertVector: func(ctx context.Context, organizationID, entryID, inputHash string, vector []float32, createdAt time.Time) error {
			return store.InsertBGEM3EntryEmbedding(ctx, memory.BGEM3EntryEmbedding{
				OrganizationID: organizationID, EntryID: entryID, EmbeddingModelID: bgem3.ProviderID,
				ModelRevision: embeddingConfig.ModelRevision, ArtifactSHA256: embeddingConfig.ArtifactSHA256,
				TokenizerRevision: embeddingConfig.TokenizerRevision, EmbeddingDimension: embeddingConfig.ExpectedDimension,
				Normalization: embeddingConfig.Normalization, Pooling: embeddingConfig.Pooling,
				PromptTemplateVersion: embeddingConfig.PromptTemplateVersion, InputHash: inputHash, Vector: vector, CreatedAt: createdAt,
			})
		},
		OnlineAdapter: adapter, LocalComputeOnly: true,
		ProviderID: bgem3.ProviderID, ProviderModelID: embeddingConfig.ModelRevision,
		OutputDimensionality: embeddingConfig.ExpectedDimension, PromptTemplateVersion: embeddingConfig.PromptTemplateVersion,
		Identity: memory.EmbeddingIdentity{
			ModelID: bgem3.ProviderID, ModelRevision: embeddingConfig.ModelRevision, ArtifactSHA256: embeddingConfig.ArtifactSHA256,
			TokenizerRevision: embeddingConfig.TokenizerRevision, Normalization: embeddingConfig.Normalization, Pooling: embeddingConfig.Pooling,
		},
	}, nil
}
