package rag

import (
	"testing"
	"time"
)

func TestCanonicalPersistenceTimeTruncatesToMicroseconds(t *testing.T) {
	in := time.Date(2026, 8, 14, 1, 0, 0, 123456789, time.UTC)
	got := canonicalPersistenceTime(in)
	if got.Nanosecond()%1000 != 0 {
		t.Fatalf("canonicalPersistenceTime(%v) = %v, still carries sub-microsecond precision", in, got)
	}
	want := time.Date(2026, 8, 14, 1, 0, 0, 123456000, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("canonicalPersistenceTime(%v) = %v, want %v", in, got, want)
	}
}

func TestCanonicalPersistenceTimeConvertsToUTC(t *testing.T) {
	loc := time.FixedZone("UTC-3", -3*60*60)
	in := time.Date(2026, 8, 14, 1, 0, 0, 0, loc)
	got := canonicalPersistenceTime(in)
	if got.Location() != time.UTC {
		t.Fatalf("canonicalPersistenceTime(%v) location = %v, want UTC", in, got.Location())
	}
	if !got.Equal(in) {
		t.Fatalf("canonicalPersistenceTime(%v) = %v, want the same instant in UTC", in, got)
	}
}

func TestProposeCanonicalizesAttestedAtBeforeHashing(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	command := validProposeCommand(now)
	loc := time.FixedZone("UTC-3", -3*60*60)
	command.Admission.AttestedAt = time.Date(2026, 8, 7, 6, 0, 0, 987654321, loc)

	version, err := svc.Propose(command)
	if err != nil {
		t.Fatalf("Propose with nanosecond-precision, non-UTC AttestedAt: %v", err)
	}
	if version.Admission.AttestedAt.Location() != time.UTC {
		t.Fatalf("stored AttestedAt location = %v, want UTC", version.Admission.AttestedAt.Location())
	}
	if version.Admission.AttestedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("stored AttestedAt = %v, still carries sub-microsecond precision", version.Admission.AttestedAt)
	}
	// The hash Propose computed must be the hash of the CANONICALIZED
	// value, not the raw input -- recomputing ComputeCanonicalHash on the
	// returned version (which already carries the canonicalized
	// AttestedAt) must reproduce the same CanonicalHash Propose set.
	recomputed, err := version.ComputeCanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	if recomputed != version.CanonicalHash {
		t.Fatalf("CanonicalHash = %s, recomputing from the returned (canonicalized) version gives %s", version.CanonicalHash, recomputed)
	}
}

func TestAdmissionValidateRejectsNonUTCAttestedAt(t *testing.T) {
	loc := time.FixedZone("UTC-3", -3*60*60)
	admission := AdmissionAttestation{
		DataClass: DataOrganizational, AttestedBy: "investigacion/research_worker_hourly",
		SourceBoundary: "organization", EvidenceRef: "admission:x",
		AttestedAt: time.Date(2026, 8, 7, 10, 0, 0, 0, loc),
	}
	if err := admission.Validate(); err == nil {
		t.Fatal("expected non-UTC AttestedAt to be rejected")
	}
}

func TestAdmissionValidateRejectsSubMicrosecondAttestedAt(t *testing.T) {
	admission := AdmissionAttestation{
		DataClass: DataOrganizational, AttestedBy: "investigacion/research_worker_hourly",
		SourceBoundary: "organization", EvidenceRef: "admission:x",
		AttestedAt: time.Date(2026, 8, 7, 10, 0, 0, 123456789, time.UTC),
	}
	if err := admission.Validate(); err == nil {
		t.Fatal("expected sub-microsecond-precision AttestedAt to be rejected")
	}
}

func TestAdmissionValidateAcceptsCanonicalizedAttestedAt(t *testing.T) {
	admission := AdmissionAttestation{
		DataClass: DataOrganizational, AttestedBy: "investigacion/research_worker_hourly",
		SourceBoundary: "organization", EvidenceRef: "admission:x",
		AttestedAt: canonicalPersistenceTime(time.Date(2026, 8, 7, 10, 0, 0, 123456789, time.UTC)),
	}
	if err := admission.Validate(); err != nil {
		t.Fatalf("canonicalized AttestedAt rejected: %v", err)
	}
}

func TestProposeReplayWithIdenticalInputProducesIdenticalCanonicalHash(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	loc := time.FixedZone("UTC-3", -3*60*60)
	attestedAt := time.Date(2026, 8, 7, 6, 0, 0, 555555555, loc)

	command1 := validProposeCommand(now)
	command1.Admission.AttestedAt = attestedAt
	first, err := svc.Propose(command1)
	if err != nil {
		t.Fatal(err)
	}

	command2 := validProposeCommand(now)
	command2.Admission.AttestedAt = attestedAt
	second, err := svc.Propose(command2)
	if err != nil {
		t.Fatal(err)
	}

	if first.CanonicalHash != second.CanonicalHash {
		t.Fatalf("identical input produced different canonical hashes across two Propose calls: %s vs %s -- this is exactly the idempotency-breaking nondeterminism RAG-INTEGRITY-001 describes", first.CanonicalHash, second.CanonicalHash)
	}
}
