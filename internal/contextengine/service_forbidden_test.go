package contextengine

import (
	"context"
	"errors"
	"testing"
	"time"
)

type secretMemoryProvider struct{}

func (secretMemoryProvider) ListApproved(context.Context, BuildRequest) ([]SourceRecord, error) {
	content := []byte("classified")
	return []SourceRecord{{Reference: "memory/secret", Version: "1", DataClass: DataSecret, Content: content, ContentHash: DigestMarkdown(content), Included: true}}, nil
}
func (secretMemoryProvider) ValidateVersion(context.Context, SourceRecord) error { return nil }

type forbiddenRecordingStore struct {
	SnapshotStore
	calls  int
	reason ReasonCode
}

func (s *forbiddenRecordingStore) RecordForbiddenSourceRejection(_ context.Context, _ BuildRequest, reason ReasonCode, _ time.Time) error {
	s.calls++
	s.reason = reason
	return nil
}

func TestServiceRecordsForbiddenSourceWithoutChangingRejection(t *testing.T) {
	fixture := newServiceFixture(t)
	recording := &forbiddenRecordingStore{SnapshotStore: fixture.store}
	service, err := NewService(
		ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20},
		fixture.registry,
		fixture.docs,
		fixture.canonical,
		NoopOwnerConstraintProvider{},
		secretMemoryProvider{},
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
	_, err = service.Build(t.Context(), fixture.request("forbidden-source"))
	if !errors.Is(err, ErrRejected) || ReasonOf(err) != ReasonSecretDataRejected {
		t.Fatalf("error=%v", err)
	}
	if recording.calls != 1 || recording.reason != ReasonSecretDataRejected {
		t.Fatalf("recorder calls=%d reason=%s", recording.calls, recording.reason)
	}
}
