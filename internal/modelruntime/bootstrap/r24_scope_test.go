package bootstrap

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

type r24ContextService struct{ snapshot contextengine.Snapshot }

func (f r24ContextService) Build(context.Context, contextengine.BuildRequest) (contextengine.BuildResult, error) {
	return contextengine.BuildResult{Snapshot: f.snapshot}, nil
}
func (f r24ContextService) Get(context.Context, int64, bool) (contextengine.Snapshot, error) {
	return f.snapshot, nil
}
func (f r24ContextService) List(context.Context, contextengine.ListFilter) ([]contextengine.Snapshot, error) {
	return []contextengine.Snapshot{f.snapshot}, nil
}
func (f r24ContextService) Render(context.Context, int64) ([]byte, error) {
	return []byte("bounded"), nil
}
func (f r24ContextService) Validate(context.Context, int64) (contextengine.SnapshotValidation, error) {
	return contextengine.SnapshotValidation{Valid: true}, nil
}
func (f r24ContextService) Invalidate(context.Context, contextengine.InvalidateCommand) (contextengine.Snapshot, error) {
	return f.snapshot, nil
}

func TestContextAdapterDerivesExecutiveScopeAndNormalizesTaskRef(t *testing.T) {
	snapshot := contextengine.Snapshot{
		ID: 8, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		ActorRoleID: "empresa/ceo", Purpose: "executive_ceo_plan",
		TaskRef: "task:123", CorrelationID: "executive:abc", Status: contextengine.SnapshotReady,
		RenderedHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Segments:     []contextengine.Segment{{Included: true, DataClass: contextengine.DataOrganizational}},
	}
	adapter := contextAdapter{service: r24ContextService{snapshot: snapshot}}
	ref, err := adapter.GetContextSnapshot(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.TaskRef != "123" {
		t.Fatalf("task_ref=%q", ref.TaskRef)
	}
	if ref.ExecutiveScope != modelegress.ScopeExecutiveCEO {
		t.Fatalf("scope=%q", ref.ExecutiveScope)
	}
	if len(ref.DataClasses) != 1 || ref.DataClasses[0] != "organizational" {
		t.Fatalf("data classes leaked scope metadata: %v", ref.DataClasses)
	}
}

func TestContextAdapterDoesNotScopeNonExecutiveSnapshot(t *testing.T) {
	snapshot := contextengine.Snapshot{
		ID: 9, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		ActorRoleID: "ingenieria_ia/qa", Purpose: "other_work",
		TaskRef: "task:124", CorrelationID: "task-flow:abc", Status: contextengine.SnapshotReady,
		Segments: []contextengine.Segment{{Included: true, DataClass: contextengine.DataPublic}},
	}
	adapter := contextAdapter{service: r24ContextService{snapshot: snapshot}}
	ref, err := adapter.GetContextSnapshot(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ExecutiveScope != "" {
		t.Fatalf("non-executive snapshot gained scope %q", ref.ExecutiveScope)
	}
}
