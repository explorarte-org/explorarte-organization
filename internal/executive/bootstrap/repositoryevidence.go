package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence/gitsource"
	stagingbootstrap "github.com/Mireuz13/explorarte-organization/internal/staging/bootstrap"
)

// repositoryEvidenceOption builds the sensor that lets a design observe the
// code it is authorized to change.
//
// It uses the SAME repository catalog and git backend the promotion path uses,
// through the staging runtime that already exposes both. A second definition
// of "the repository" would eventually disagree with the one CodeRunner checks
// out, and a design grounded in the wrong tree is worse than one grounded in
// nothing.
//
// Absent, any execution carrying a pinned commit fails closed rather than
// producing a context with no code in it. That is why this wiring is worth
// having a guard: a deployment that forgot it would not degrade quietly, it
// would refuse every grounded design and say why.
func repositoryEvidenceOption(cfg config.Config, store *platformpostgres.Store) ([]contextengine.ServiceOption, error) {
	repositoryID := strings.TrimSpace(os.Getenv(missionRepositoryEnv))
	if repositoryID == "" || !cfg.Staging.Enabled {
		return nil, nil
	}
	stagingRuntime, err := stagingbootstrap.Open(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open staging runtime for repository evidence: %w", err)
	}
	repository, _, err := stagingRuntime.Catalog.Get(context.Background(), repositoryID)
	if err != nil {
		return nil, fmt.Errorf("resolve repository %q for evidence: %w", repositoryID, err)
	}
	binary := strings.TrimSpace(cfg.Staging.GitBinary)
	if binary == "" {
		binary = "/usr/bin/git"
	}
	source, err := gitsource.New(repository.Path, binary, 2<<20)
	if err != nil {
		return nil, err
	}
	provider, err := repositoryevidence.NewProvider(repositoryID, source, repositoryevidence.DefaultLimits(), 24)
	if err != nil {
		return nil, err
	}
	return []contextengine.ServiceOption{contextengine.WithRepositoryEvidence(provider)}, nil
}

// snapshotSourceReader lets the host confirm that a repository citation was
// really in front of the model that made it.
//
// It reads the durable snapshot by ID, which is the only thing that answers
// the question asked: rebuilding "the same" context would answer a different
// one, and would answer it wrongly whenever anything had changed since.
type snapshotSourceReader struct{ service contextengine.Service }

func (r snapshotSourceReader) SnapshotSources(ctx context.Context, snapshotID int64) ([]executive.SnapshotSource, error) {
	snapshot, err := r.service.Get(ctx, snapshotID, true)
	if err != nil {
		return nil, err
	}
	sources := make([]executive.SnapshotSource, 0, len(snapshot.Segments))
	for _, segment := range snapshot.Segments {
		sources = append(sources, executive.SnapshotSource{
			Kind:      string(segment.SourceKind),
			Reference: segment.SourceReference,
			Version:   segment.SourceVersion,
			// Included is the whole point: a segment that was known and then
			// dropped for budget is not something the model read, and
			// certifying a claim on it would ground a design in code nobody
			// saw.
			Included: segment.Included,
			// Only included segments carry payload: an omitted one was
			// never shown, so it is neither citable nor something the
			// candidate could have copied.
			Content: string(segment.Content),
		})
	}
	return sources, nil
}

var _ executive.SnapshotSourceReader = snapshotSourceReader{}
