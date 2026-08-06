package bootstrap

import (
	"context"
	"fmt"
	"sort"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egresspostgres "github.com/Mireuz13/explorarte-organization/internal/modelegress/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

type Runtime struct {
	Service   *modelegress.Service
	Store     *egresspostgres.Store
	Evaluator *modelegress.Evaluator
}

func Open(cfg config.Config, platformStore *platformpostgres.Store) (*Runtime, error) {
	if platformStore == nil {
		return nil, fmt.Errorf("model egress bootstrap requires PostgreSQL store")
	}
	registryRepository, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		return nil, err
	}
	store, err := egresspostgres.New(platformStore)
	if err != nil {
		return nil, err
	}
	service, err := modelegress.NewService(cfg.Registry.CanonicalDir, cfg.Tasks.OrganizationID, organizationAdapter{reader: registryRepository}, providerAdapter{}, store)
	if err != nil {
		return nil, err
	}
	return &Runtime{Service: service, Store: store, Evaluator: modelegress.NewEvaluator()}, nil
}

type organizationAdapter struct{ reader registry.Reader }

func (a organizationAdapter) CurrentOrganization(ctx context.Context, id string) (modelegress.OrganizationRef, error) {
	organization, err := a.reader.GetOrganization(ctx, id)
	if err != nil {
		return modelegress.OrganizationRef{}, err
	}
	revision, err := a.reader.GetCurrentRevision(ctx, id)
	if err != nil {
		return modelegress.OrganizationRef{}, err
	}
	if revision == nil {
		return modelegress.OrganizationRef{}, registry.ErrNotFound
	}
	return modelegress.OrganizationRef{ID: organization.ID, RevisionID: revision.ID, PolicyDocumentHash: revision.DocumentHashes[modelegress.PolicyFileName]}, nil
}

type providerAdapter struct{}

func (providerAdapter) ProviderIDs(canonicalDir string) ([]string, error) {
	routing, err := modelruntime.LoadCanonicalRouting(canonicalDir)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, policy := range routing.Policies {
		set[policy.Provider] = struct{}{}
	}
	providers := make([]string, 0, len(set))
	for provider := range set {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers, nil
}
