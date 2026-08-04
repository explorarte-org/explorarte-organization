package bootstrap

import (
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/staging/artifactfs"
	"github.com/Mireuz13/explorarte-organization/internal/staging/gitexec"
	stagingpostgres "github.com/Mireuz13/explorarte-organization/internal/staging/postgres"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
)

type Runtime struct {
	Service *staging.Service
	Catalog staging.RepositoryCatalog
}

func Open(cfg config.Config, store *platformpostgres.Store) (*Runtime, error) {
	if !cfg.Staging.Enabled {
		return nil, errors.New("staging is disabled; set ORG_STAGING_ENABLED=true")
	}
	if err := staging.PrepareRoots(cfg.Staging.WorkspaceRoot, cfg.Staging.ArtifactRoot, cfg.Staging.QuarantineRoot); err != nil {
		return nil, fmt.Errorf("prepare staging roots: %w", err)
	}
	gitBackend, err := gitexec.New(cfg.Staging.GitBinary, cfg.Staging.WorkspaceRoot, cfg.Staging.CommandTimeout)
	if err != nil {
		return nil, fmt.Errorf("create Git backend: %w", err)
	}
	catalog, err := staging.LoadRepositoryCatalog(cfg.Staging.RepositoriesFile, gitBackend)
	if err != nil {
		return nil, fmt.Errorf("load repository catalog: %w", err)
	}
	if err := catalog.ValidateRootSeparation(cfg.Staging.WorkspaceRoot, cfg.Staging.ArtifactRoot, cfg.Staging.QuarantineRoot); err != nil {
		return nil, fmt.Errorf("validate repository and staging roots: %w", err)
	}
	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		return nil, fmt.Errorf("create registry repository: %w", err)
	}
	authorizer, err := authorization.New(registryRepository, cfg.Tasks.OrganizationID, cfg.Registry.CanonicalDir)
	if err != nil {
		return nil, fmt.Errorf("create capability authorizer: %w", err)
	}
	artifactStore, err := artifactfs.New(cfg.Staging.ArtifactRoot, cfg.Staging.MaxArtifactBytes)
	if err != nil {
		return nil, fmt.Errorf("create artifact store: %w", err)
	}
	taskStore, err := taskpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create task lease verifier: %w", err)
	}
	stagingStore, err := stagingpostgres.New(store, cfg.Tasks.OutboxMaxAttempts)
	if err != nil {
		return nil, fmt.Errorf("create staging store: %w", err)
	}
	service, err := staging.NewService(staging.ServiceConfig{
		OrganizationID:   cfg.Tasks.OrganizationID,
		WorkspaceRoot:    cfg.Staging.WorkspaceRoot,
		QuarantineRoot:   cfg.Staging.QuarantineRoot,
		MaxArtifactBytes: cfg.Staging.MaxArtifactBytes,
		MaxChangedFiles:  cfg.Staging.MaxChangedFiles,
		StaleAfter:       cfg.Staging.StaleAfter,
	}, stagingStore, taskStore, catalog, authorizer, registryRepository, artifactStore, gitBackend)
	if err != nil {
		return nil, fmt.Errorf("create staging service: %w", err)
	}
	return &Runtime{Service: service, Catalog: catalog}, nil
}
