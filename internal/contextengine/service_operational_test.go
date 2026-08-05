package contextengine

import (
	"context"
	"errors"
	"testing"
)

type switchableDocumentLoader struct {
	delegate DocumentLoader
	failPath string
	err      error
}

func (l *switchableDocumentLoader) Load(ctx context.Context, path string, maxBytes int64) (LoadedDocument, error) {
	if path == l.failPath && l.err != nil {
		return LoadedDocument{}, l.err
	}
	return l.delegate.Load(ctx, path, maxBytes)
}

func TestServiceValidatePreservesOperationalDocumentError(t *testing.T) {
	fixture := newServiceFixture(t)
	documents := &switchableDocumentLoader{delegate: fixture.docs}
	service, err := NewService(
		ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20},
		fixture.registry,
		documents,
		fixture.canonical,
		NoopOwnerConstraintProvider{},
		UnavailableMemoryProvider{},
		emptySkillProvider{},
		UnavailableProjectProvider{},
		UnavailableTaskProvider{},
		UnavailableRAGProvider{},
		NewAssembler(),
		NewRenderer(),
		fixture.store,
		fixedClock{},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Build(t.Context(), fixture.request("operational-validation"))
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("filesystem unavailable")
	documents.failPath = "ingenieria_ia/qa/PERFIL.md"
	documents.err = sentinel
	validation, err := service.Validate(t.Context(), result.Snapshot.ID)
	if !errors.Is(err, sentinel) {
		t.Fatalf("validation=%+v error=%v, want wrapped operational error", validation, err)
	}
}
