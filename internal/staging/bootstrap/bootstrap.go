package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
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
	// Git is the backend this runtime already constructed. It is exposed so a
	// consumer that needs to read a ref uses the SAME backend staging itself
	// uses -- a second gitexec.Backend over the same repository would be a
	// second opinion about what a ref points at, which is precisely the
	// disagreement the retained conflicted promotion recorded.
	Git *gitexec.Backend
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
	authorizationPolicyReader, err := authorizationpostgres.New(store)
	if err != nil {
		return nil, fmt.Errorf("create authorization policy reader: %w", err)
	}
	staticAuthorizer, err := authorization.NewWithPolicyReader(authorizationPolicyReader, cfg.Tasks.OrganizationID, cfg.Registry.CanonicalDir)
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
	}, stagingStore, taskStore, catalog, stagingAuthorizationAdapter{inner: staticAuthorizer}, registryRepository, artifactStore, gitBackend)
	if err != nil {
		return nil, fmt.Errorf("create staging service: %w", err)
	}
	return &Runtime{Service: service, Catalog: catalog, Git: gitBackend}, nil
}

// stagingAuthorizationAdapter translates authorization-domain decisions at the
// staging boundary. Authorization deliberately does not import staging.
type stagingAuthorizationAdapter struct {
	inner authorization.CapabilityAuthorizer
}

func (a stagingAuthorizationAdapter) Authorize(ctx context.Context, organizationID string, revisionID int64, roleID, capability string) error {
	err := a.inner.Authorize(ctx, organizationID, revisionID, roleID, capability)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authorization.ErrPolicyRevisionMismatch):
		return fmt.Errorf("%w: %v", staging.ErrPolicyRevisionMismatch, err)
	case errors.Is(err, authorization.ErrCapabilityDenied), errors.Is(err, authorization.ErrApprovalRequired), errors.Is(err, authorization.ErrUnknownCapability), errors.Is(err, authorization.ErrUnknownAuthorityClass):
		return fmt.Errorf("%w: %v", staging.ErrCapabilityDenied, err)
	default:
		return err
	}
}
