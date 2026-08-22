package repositoryevidence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

type failingSource struct{ err error }

func (f failingSource) Search(context.Context, string, string, int) ([]Match, error) {
	return nil, f.err
}
func (f failingSource) Lines(context.Context, string, string) (int, error) { return 0, f.err }
func (f failingSource) ReadRange(context.Context, string, string, int, int) (string, error) {
	return "", f.err
}

// P3: a request carrying a commit gets excerpts of THAT commit.
func TestTheProviderAnswersAboutTheCommitItWasGiven(t *testing.T) {
	provider, err := NewProvider("explorarte-organization", newSource(), DefaultLimits(), 4)
	if err != nil {
		t.Fatal(err)
	}
	records, err := provider.ListRepositoryEvidence(context.Background(), contextengine.BuildRequest{
		RepositoryBaseSHA: shaA,
		RepositoryQuery:   "improve internal/executive/orchestrator.go and its driveDepartments handling",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("a request naming a real file produced no evidence")
	}
	for _, record := range records {
		if record.Version != shaA {
			t.Fatalf("evidence cites %q but the request was about %q", record.Version, shaA)
		}
		if record.Kind != contextengine.SourceRepositoryEvidence {
			t.Fatalf("kind=%q", record.Kind)
		}
		if err := contextengine.ValidateSourceMetadata(record); err != nil {
			t.Fatalf("the provider produced a record the context engine rejects: %v", err)
		}
	}
}

// P4: a request with no commit is not an execution that observes code, and
// must not be turned into one.
func TestNoCommitMeansNoEvidence(t *testing.T) {
	provider, _ := NewProvider("explorarte-organization", newSource(), DefaultLimits(), 4)
	records, err := provider.ListRepositoryEvidence(context.Background(), contextengine.BuildRequest{})
	if err != nil || len(records) != 0 {
		t.Fatalf("records=%d err=%v: an ungrounded execution must simply get nothing", len(records), err)
	}
}

// P4: a sensor that cannot answer must say so. Returning an empty list would
// be indistinguishable from a repository with nothing in it, and the execution
// would go on to reason about code it never saw.
func TestABrokenSensorIsAnErrorNotAnEmptyRepository(t *testing.T) {
	provider, _ := NewProvider("explorarte-organization", failingSource{err: errors.New("repository unreachable")}, DefaultLimits(), 4)
	if _, err := provider.ListRepositoryEvidence(context.Background(), contextengine.BuildRequest{
		RepositoryBaseSHA: shaA, RepositoryQuery: "internal/executive/orchestrator.go",
	}); err == nil {
		t.Fatal("a sensor that cannot read must not report an empty repository")
	}

	// And a query that genuinely matches nothing is the same refusal: the
	// execution was promised a repository to look at and got none.
	quiet, _ := NewProvider("explorarte-organization", newSource(), DefaultLimits(), 4)
	_, err := quiet.ListRepositoryEvidence(context.Background(), contextengine.BuildRequest{
		RepositoryBaseSHA: shaA, RepositoryQuery: "nothing here names any real path or symbol",
	})
	if !errors.Is(err, ErrNoEvidenceFound) {
		t.Fatalf("an execution that would observe nothing must be refused, got %v", err)
	}
}

// A provider must never be able to answer about a different commit than the
// one asked for, however it obtained the excerpt.
func TestTheProviderCannotAnswerAboutAnotherCommit(t *testing.T) {
	provider, _ := NewProvider("explorarte-organization", newSource(), DefaultLimits(), 4)
	records, err := provider.ListRepositoryEvidence(context.Background(), contextengine.BuildRequest{
		RepositoryBaseSHA: shaA, RepositoryQuery: "internal/executive/orchestrator.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if strings.Contains(record.Reference, shaB) {
			t.Fatalf("a reference cites the wrong commit: %s", record.Reference)
		}
	}
	// RenderBundle is the guard on the way out: one foreign fragment and the
	// whole set is refused, because a reader cannot tell which claims rested
	// on it.
	foreign := Fragment{Repository: "explorarte-organization", BaseSHA: shaB, Path: "a.go",
		LineStart: 1, LineEnd: 1, Content: "x", Digest: DigestOf("x")}
	if _, err := RenderBundle([]Fragment{foreign}, shaA); !errors.Is(err, ErrStaleEvidence) {
		t.Fatalf("a bundle citing another commit must be refused, got %v", err)
	}
}
