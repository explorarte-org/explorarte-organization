package webevidence

import (
	"strings"
	"testing"
	"time"
)

func validEvidence(now time.Time) Evidence {
	return Evidence{
		ID: "ev-1", OrganizationID: "explorarte", TaskID: 42, URL: "https://example.com/page",
		ContentHash: strings.Repeat("a", 64), CapturedAt: now, ExpiresAt: now.Add(time.Hour),
		Chunks: []Chunk{{Ordinal: 0, Text: "hello world"}},
	}
}

func TestEvidenceValidateAcceptsWellFormed(t *testing.T) {
	now := time.Now().UTC()
	if err := validEvidence(now).Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEvidenceValidateRequiresTaskID(t *testing.T) {
	now := time.Now().UTC()
	e := validEvidence(now)
	e.TaskID = 0
	if err := e.Validate(); err == nil {
		t.Fatal("expected error for missing task id — web evidence must always be task-scoped")
	}
}

func TestEvidenceValidateRejectsMissingOrUnboundedTTL(t *testing.T) {
	now := time.Now().UTC()
	noExpiry := validEvidence(now)
	noExpiry.ExpiresAt = time.Time{}
	if err := noExpiry.Validate(); err == nil {
		t.Fatal("expected error for zero expires_at")
	}

	tooShort := validEvidence(now)
	tooShort.ExpiresAt = now.Add(time.Second)
	if err := tooShort.Validate(); err == nil {
		t.Fatal("expected error for a ttl below MinTTL")
	}

	tooLong := validEvidence(now)
	tooLong.ExpiresAt = now.Add(365 * 24 * time.Hour)
	if err := tooLong.Validate(); err == nil {
		t.Fatal("expected error for a ttl above MaxTTL")
	}

	expiresBeforeCaptured := validEvidence(now)
	expiresBeforeCaptured.ExpiresAt = now.Add(-time.Minute)
	if err := expiresBeforeCaptured.Validate(); err == nil {
		t.Fatal("expected error for expires_at before captured_at")
	}
}

func TestEvidenceValidateRejectsEmptyOrDuplicateChunks(t *testing.T) {
	now := time.Now().UTC()
	noChunks := validEvidence(now)
	noChunks.Chunks = nil
	if err := noChunks.Validate(); err == nil {
		t.Fatal("expected error for no chunks")
	}

	dup := validEvidence(now)
	dup.Chunks = []Chunk{{Ordinal: 0, Text: "a"}, {Ordinal: 0, Text: "b"}}
	if err := dup.Validate(); err == nil {
		t.Fatal("expected error for duplicate chunk ordinals")
	}
}

func TestEvidenceExpired(t *testing.T) {
	now := time.Now().UTC()
	e := validEvidence(now)
	if e.Expired(now) {
		t.Fatal("evidence should not be expired at capture time")
	}
	if !e.Expired(e.ExpiresAt) {
		t.Fatal("evidence should be expired exactly at its expiry time")
	}
	if !e.Expired(e.ExpiresAt.Add(time.Hour)) {
		t.Fatal("evidence should be expired well past its expiry time")
	}
}

func TestNewCitationBoundsExcerptAndCarriesProvenance(t *testing.T) {
	now := time.Now().UTC()
	e := validEvidence(now)
	e.Chunks = []Chunk{{Ordinal: 0, Text: strings.Repeat("x", maxCitationExcerptBytes*2)}}
	citation, err := NewCitation(e, 0)
	if err != nil {
		t.Fatal(err)
	}
	if citation.URL != e.URL || citation.ContentHash != e.ContentHash || !citation.CapturedAt.Equal(e.CapturedAt) {
		t.Fatalf("citation provenance mismatch: %+v", citation)
	}
	if len(citation.Excerpt) != maxCitationExcerptBytes {
		t.Fatalf("excerpt length=%d want %d", len(citation.Excerpt), maxCitationExcerptBytes)
	}
}

func TestNewCitationRejectsUnknownOrdinal(t *testing.T) {
	now := time.Now().UTC()
	if _, err := NewCitation(validEvidence(now), 99); err == nil {
		t.Fatal("expected error for an ordinal that does not exist")
	}
}
