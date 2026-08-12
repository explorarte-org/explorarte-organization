package contextprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type stubGate struct {
	deny map[string]bool
}

func (g stubGate) Authorize(_ context.Context, request rag.AuthorizationRequest) error {
	if g.deny[request.CapabilityID] {
		return authorization.ErrCapabilityDenied
	}
	return nil
}

type stubNamespaces struct{}

func (stubNamespaces) ResolveNamespace(_ context.Context, _, _ string, kind rag.NamespaceKind) (string, error) {
	if kind == rag.NamespaceOwn {
		return "ingenieria_ia/frontend", nil
	}
	return "ingenieria_ia", nil
}

type stubRepository struct {
	results    []rag.QueryResult
	generation rag.IndexGeneration
	hasActive  bool
}

func (stubRepository) CreateCandidate(context.Context, rag.CreateCandidateCommand) (rag.KnowledgeVersion, bool, error) {
	return rag.KnowledgeVersion{}, false, errors.New("unused")
}
func (r stubRepository) Get(_ context.Context, _, versionID string) (rag.KnowledgeVersion, error) {
	if versionID != "know-1" {
		return rag.KnowledgeVersion{}, rag.ErrNotFound
	}
	return rag.KnowledgeVersion{ID: "know-1", OrganizationID: "explorarte", Lifecycle: rag.LifecycleApproved, CanonicalHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}, nil
}
func (stubRepository) Save(context.Context, rag.SaveCommand) (rag.KnowledgeVersion, error) {
	return rag.KnowledgeVersion{}, errors.New("unused")
}
func (stubRepository) List(context.Context, rag.ListFilter) ([]rag.KnowledgeVersion, error) {
	return nil, errors.New("unused")
}
func (stubRepository) ApprovedForNamespace(context.Context, string, rag.NamespaceKind, string) ([]rag.KnowledgeVersion, error) {
	return nil, errors.New("unused")
}
func (stubRepository) Reindex(context.Context, rag.ReindexCommand) (rag.IndexGeneration, error) {
	return rag.IndexGeneration{}, errors.New("unused")
}
func (r stubRepository) Query(_ context.Context, _ rag.QueryCommand) ([]rag.QueryResult, error) {
	return r.results, nil
}
func (r stubRepository) ActiveGeneration(_ context.Context, _ string, _ rag.NamespaceKind, _ string) (rag.IndexGeneration, bool, error) {
	return r.generation, r.hasActive, nil
}
func (stubRepository) ExistingEvidenceReferences(context.Context, string, string) (map[string]bool, error) {
	return nil, nil
}

func newTestProvider(t *testing.T, repo rag.Repository, gate rag.AuthorizationGate) *Provider {
	t.Helper()
	manager, err := rag.NewManager(rag.NewService(nil), repo, gate, stubNamespaces{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := New(manager, "explorarte", 5)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func sampleResult() rag.QueryResult {
	return rag.QueryResult{
		Chunk:      rag.Chunk{ID: "chunk-1", VersionID: "know-1", Content: "valida la política de egress", ContentHash: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
		DocumentID: "gestion-riesgos-modelos", Title: "Gestión de riesgos", NamespaceKind: rag.NamespaceDepartment, NamespaceID: "ingenieria_ia",
		SourceReference: "investigacion:report:41", CanonicalHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", DataClass: rag.DataOrganizational, GenerationID: "department-ingenieria_ia-1", Score: 0.5,
	}
}

func TestListApprovedEvidenceProducesUntrustedNonCapabilityRecords(t *testing.T) {
	repo := stubRepository{results: []rag.QueryResult{sampleResult()}}
	provider := newTestProvider(t, repo, stubGate{})
	records, err := provider.ListApprovedEvidence(context.Background(), contextengine.BuildRequest{OrganizationID: "explorarte", ActorRoleID: "ingenieria_ia/frontend", Purpose: "riesgos de despliegue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected at least one rag evidence record")
	}
	for _, record := range records {
		if record.Kind != contextengine.SourceRAGEvidence {
			t.Fatalf("kind=%s", record.Kind)
		}
		if record.AuthorityTier != contextengine.TierRAGEvidence {
			t.Fatalf("tier=%s", record.AuthorityTier)
		}
		if record.InstructionClass != contextengine.InstructionData {
			t.Fatalf("instruction class=%s", record.InstructionClass)
		}
		if record.TrustClass != contextengine.TrustUntrusted {
			t.Fatalf("trust class=%s", record.TrustClass)
		}
		if record.MayGrantCapabilities {
			t.Fatal("rag evidence must never grant capabilities")
		}
	}
}

func TestListApprovedEvidenceSkipsUnauthorizedScopesInsteadOfFailing(t *testing.T) {
	repo := stubRepository{results: []rag.QueryResult{sampleResult()}}
	provider := newTestProvider(t, repo, stubGate{deny: map[string]bool{rag.CapabilityReadDepartment: true}})
	records, err := provider.ListApprovedEvidence(context.Background(), contextengine.BuildRequest{OrganizationID: "explorarte", ActorRoleID: "ingenieria_ia/frontend", Purpose: "riesgos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("own-namespace scope should still be authorized and return evidence")
	}
}

func TestListApprovedEvidenceEmptyPurposeReturnsNoEvidence(t *testing.T) {
	provider := newTestProvider(t, stubRepository{}, stubGate{})
	records, err := provider.ListApprovedEvidence(context.Background(), contextengine.BuildRequest{OrganizationID: "explorarte", ActorRoleID: "ingenieria_ia/frontend", Purpose: "  "})
	if err != nil || len(records) != 0 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestValidateVersionDetectsDeprecationAndGenerationDrift(t *testing.T) {
	result := sampleResult()
	repo := stubRepository{results: []rag.QueryResult{result}, generation: rag.IndexGeneration{ID: result.GenerationID}, hasActive: true}
	provider := newTestProvider(t, repo, stubGate{})
	records, err := provider.ListApprovedEvidence(context.Background(), contextengine.BuildRequest{OrganizationID: "explorarte", ActorRoleID: "ingenieria_ia/frontend", Purpose: "riesgos"})
	if err != nil || len(records) == 0 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	if err := provider.ValidateVersion(context.Background(), "ingenieria_ia/orquestador", records[0]); err != nil {
		t.Fatalf("expected no drift, got %v", err)
	}

	staleRepo := stubRepository{generation: rag.IndexGeneration{ID: "department-ingenieria_ia-2"}, hasActive: true}
	staleProvider := newTestProvider(t, staleRepo, stubGate{})
	if err := staleProvider.ValidateVersion(context.Background(), "ingenieria_ia/orquestador", records[0]); err == nil {
		t.Fatal("expected drift when the active index generation changed")
	}
}

func TestValidateVersionRejectsWrongSourceKind(t *testing.T) {
	provider := newTestProvider(t, stubRepository{}, stubGate{})
	err := provider.ValidateVersion(context.Background(), "ingenieria_ia/orquestador", contextengine.SourceRecord{Kind: contextengine.SourceApprovedMemory})
	if err == nil {
		t.Fatal("expected rejection of a non-rag source kind")
	}
}
