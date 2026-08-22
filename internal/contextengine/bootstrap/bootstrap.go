package bootstrap

import (
	"errors"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/canonical"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	contextpostgres "github.com/Mireuz13/explorarte-organization/internal/contextengine/postgres"
	memorybootstrap "github.com/Mireuz13/explorarte-organization/internal/memory/bootstrap"
	memorycontext "github.com/Mireuz13/explorarte-organization/internal/memory/contextprovider"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	ragbootstrap "github.com/Mireuz13/explorarte-organization/internal/rag/bootstrap"
	ragcontext "github.com/Mireuz13/explorarte-organization/internal/rag/contextprovider"
)

type Runtime struct {
	Service        contextengine.Service
	Store          *contextpostgres.Store
	Registry       *registry.PostgresRepository
	Documents      *document.Loader
	Canonical      *canonical.Provider
	Skills         *canonical.SkillProvider
	Memory         *memorycontext.Provider
	RAG            *ragcontext.Provider
	Tasks          contextengine.TaskContextProvider
	OrganizationID string
}

// Open wires the context runtime. taskProvider is supplied by the caller
// because contextengine must not import the tasks domain directly; pass nil
// to fall back to contextengine.UnavailableTaskProvider.

// Open builds the context runtime.
//
// options carries capabilities the caller can supply and this package should
// not resolve for itself -- repository evidence in particular. Building the
// sensor here would mean opening the repository catalog and a git backend
// again, and a second definition of "the repository" would eventually disagree
// with the one CodeRunner checks out. A design grounded in the wrong tree is
// worse than one grounded in nothing.
func Open(cfg config.Config, platformStore *platformpostgres.Store, taskProvider contextengine.TaskContextProvider, options ...contextengine.ServiceOption) (*Runtime, error) {
	if platformStore == nil {
		return nil, errors.New("context bootstrap requires PostgreSQL store")
	}
	registryRepository, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, err
	}
	documents, err := document.NewLoader(cfg.Context.SourceRoot, int64(cfg.Context.MaxSegmentBytes))
	if err != nil {
		return nil, err
	}
	canonicalProvider, err := canonical.New(cfg.Registry.CanonicalDir, int64(cfg.Context.MaxTotalBytes))
	if err != nil {
		return nil, err
	}
	skillProvider, err := canonical.NewSkillProvider(cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, err
	}
	store, err := contextpostgres.New(platformStore)
	if err != nil {
		return nil, err
	}
	memoryRuntime, err := memorybootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, err
	}
	memoryProvider, err := memorycontext.New(memoryRuntime.Manager, cfg.Tasks.OrganizationID, cfg.Context.MaxMemorySegments)
	if err != nil {
		return nil, err
	}
	ragRuntime, err := ragbootstrap.Open(cfg, platformStore)
	if err != nil {
		return nil, err
	}
	ragProvider, err := ragcontext.New(ragRuntime.Manager, cfg.Tasks.OrganizationID, cfg.Context.MaxRAGSegments)
	if err != nil {
		return nil, err
	}
	if taskProvider == nil {
		taskProvider = contextengine.UnavailableTaskProvider{}
	}
	service, err := contextengine.NewService(contextengine.ServiceConfig{
		OrganizationAgentPath: "AGENT.md",
		MaxTotalBytes:         cfg.Context.MaxTotalBytes,
		MaxSegmentBytes:       cfg.Context.MaxSegmentBytes,
		MaxSegments:           cfg.Context.MaxSegments,
		MaxSkills:             cfg.Context.MaxSkills,
		MaxMemorySegments:     cfg.Context.MaxMemorySegments,
		MaxRAGSegments:        cfg.Context.MaxRAGSegments,
	}, registryRepository, documents, canonicalProvider, contextengine.NoopOwnerConstraintProvider{}, memoryProvider, skillProvider, contextengine.UnavailableProjectProvider{}, taskProvider, ragProvider, contextengine.NewAssembler(), contextengine.NewRenderer(), store, nil, options...)
	if err != nil {
		return nil, err
	}
	return &Runtime{Service: service, Store: store, Registry: registryRepository, Documents: documents, Canonical: canonicalProvider, Skills: skillProvider, Memory: memoryProvider, RAG: ragProvider, Tasks: taskProvider, OrganizationID: cfg.Tasks.OrganizationID}, nil
}
