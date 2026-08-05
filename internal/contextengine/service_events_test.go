package contextengine

import (
	"context"
	"testing"
	"time"
)

type validationRecordingStore struct {
	SnapshotStore
	calls      int
	validation SnapshotValidation
}

func (s *validationRecordingStore) RecordValidationFailure(_ context.Context, _ Snapshot, validation SnapshotValidation, _ time.Time) error {
	s.calls++
	s.validation = validation
	return nil
}

func TestServiceRecordsStructuredValidationFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	recording := &validationRecordingStore{SnapshotStore: fixture.store}
	service, err := NewService(
		ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20},
		fixture.registry,
		fixture.docs,
		fixture.canonical,
		NoopOwnerConstraintProvider{},
		UnavailableMemoryProvider{},
		emptySkillProvider{},
		UnavailableProjectProvider{},
		UnavailableTaskProvider{},
		UnavailableRAGProvider{},
		NewAssembler(),
		NewRenderer(),
		recording,
		fixedClock{now: time.Date(2026, 8, 5, 6, 0, 0, 0, time.UTC)},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Build(t.Context(), fixture.request("validation-event"))
	if err != nil {
		t.Fatal(err)
	}
	profile := fixture.docs.docs["ingenieria_ia/qa/PERFIL.md"]
	profile.Hash = DigestMarkdown([]byte("changed profile"))
	fixture.docs.docs[profile.Path] = profile
	validation, err := service.Validate(t.Context(), result.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if validation.Valid || recording.calls != 1 || recording.validation.ReasonCode != string(ReasonSnapshotStale) {
		t.Fatalf("validation=%+v recorder=%+v", validation, recording)
	}
}
