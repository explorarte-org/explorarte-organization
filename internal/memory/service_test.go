package memory

import (
	"errors"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

func validCommand(now time.Time) ProposeCommand {
	return ProposeCommand{
		ID:             "mem-1",
		OrganizationID: "explorarte",
		RoleID:         "ingenieria_ia/orquestador",
		Category:       "incident_learning",
		Problem:        "A migration-count assertion drifted from the actual migration tip.",
		Correction:     "Derive migration-tip expectations from the migration set or update every dependent suite atomically.",
		SourceKind:     SourceOperational,
		SourceRunID:    42,
		EvidenceRefs: []EvidenceRef{
			{Reference: "task:42:evidence:2", Digest: "bbb"},
			{Reference: "task:42:evidence:1", Digest: "aaa"},
		},
		ProposedBy: "ingenieria_ia/orquestador",
		Admission: AdmissionAttestation{
			DataClass:               DataSanitized,
			AttestedBy:              "cell-gateway/clinica-online",
			SourceBoundary:          "cell_gateway",
			EvidenceRef:             "classification:42",
			SanitizationEvidenceRef: "sanitization:42",
			AttestedAt:              now.Add(-time.Minute),
		},
	}
}

func TestProposeReviewDeprecateArchive(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	service := NewService(clock)
	entry, err := service.Propose(validCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != StatusCandidate || entry.Revision != 1 {
		t.Fatalf("candidate state=%s revision=%d", entry.Status, entry.Revision)
	}
	if entry.EvidenceRefs[0].Reference != "task:42:evidence:1" {
		t.Fatalf("evidence references not deterministic: %+v", entry.EvidenceRefs)
	}
	clock.now = now.Add(time.Minute)
	entry, err = service.Review(entry, Review{Outcome: ReviewApprove, ReviewerID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != StatusApproved || entry.ReviewerID != "empresa/human" || entry.ReviewedAt == nil || entry.Revision != 2 {
		t.Fatalf("approved entry=%+v", entry)
	}
	clock.now = now.Add(2 * time.Minute)
	entry, err = service.Deprecate(entry)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(3 * time.Minute)
	entry, err = service.Archive(entry)
	if err != nil || entry.Status != StatusArchived || entry.Revision != 4 {
		t.Fatalf("archived entry=%+v err=%v", entry, err)
	}
}

func TestSourceKindIsRequiredAndSimulationIsPreserved(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	service := NewService(&fixedClock{now: now})
	command := validCommand(now)
	command.SourceKind = ""
	if _, err := service.Propose(command); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("missing source kind error=%v", err)
	}
	command.SourceKind = SourceSimulation
	entry, err := service.Propose(command)
	if err != nil {
		t.Fatal(err)
	}
	if entry.SourceKind != SourceSimulation {
		t.Fatalf("source kind=%s", entry.SourceKind)
	}
}

func TestClinicalAndSecretContentCannotBecomeCandidate(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	service := NewService(&fixedClock{now: now})
	for _, class := range []DataClass{DataClinical, DataSecret} {
		command := validCommand(now)
		command.Admission.DataClass = class
		command.Admission.SanitizationEvidenceRef = ""
		_, err := service.Propose(command)
		if !errors.Is(err, ErrForbiddenDataClass) {
			t.Fatalf("class %s error=%v", class, err)
		}
	}
}

func TestSanitizedAdmissionRequiresSanitizationEvidence(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	service := NewService(&fixedClock{now: now})
	command := validCommand(now)
	command.Admission.SanitizationEvidenceRef = ""
	if _, err := service.Propose(command); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("error=%v", err)
	}
}

func TestOrganizationalAdmissionCannotCarrySanitizationEvidence(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	service := NewService(&fixedClock{now: now})
	command := validCommand(now)
	command.Admission.DataClass = DataOrganizational
	if _, err := service.Propose(command); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("error=%v", err)
	}
	command.Admission.SanitizationEvidenceRef = ""
	if _, err := service.Propose(command); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionMustPrecedeCandidate(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	service := NewService(&fixedClock{now: now})
	command := validCommand(now)
	command.Admission.AttestedAt = now.Add(time.Second)
	if _, err := service.Propose(command); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("error=%v", err)
	}
}

func TestApprovedEntryRequiresReviewer(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	service := NewService(&fixedClock{now: now})
	entry, err := service.Propose(validCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	entry.Status = StatusApproved
	if err := entry.Validate(); err == nil {
		t.Fatal("approved entry without review provenance validated")
	}
}

func TestRejectedEntryCanOnlyArchive(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	service := NewService(clock)
	entry, err := service.Propose(validCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(time.Minute)
	entry, err = service.Review(entry, Review{Outcome: ReviewReject, ReviewerID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Deprecate(entry); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error=%v", err)
	}
	clock.now = now.Add(2 * time.Minute)
	entry, err = service.Archive(entry)
	if err != nil || entry.Status != StatusArchived {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
}

func TestTransitionMatrixIsDefaultDeny(t *testing.T) {
	states := []Status{StatusCandidate, StatusApproved, StatusDeprecated, StatusArchived, StatusRejected}
	allowed := map[[2]Status]bool{{StatusCandidate, StatusApproved}: true, {StatusCandidate, StatusRejected}: true, {StatusApproved, StatusDeprecated}: true, {StatusDeprecated, StatusArchived}: true, {StatusRejected, StatusArchived}: true}
	for _, from := range states {
		for _, to := range states {
			err := ValidateTransition(from, to)
			if allowed[[2]Status{from, to}] && err != nil {
				t.Fatalf("%s -> %s: %v", from, to, err)
			}
			if !allowed[[2]Status{from, to}] && !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("%s -> %s error=%v", from, to, err)
			}
		}
	}
}

func TestCanonicalHashCommitsContentAdmissionAndSourceKind(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	service := NewService(clock)
	entry, err := service.Propose(validCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := entry.CanonicalHash()
	clock.now = now.Add(time.Minute)
	approved, err := service.Review(entry, Review{Outcome: ReviewApprove, ReviewerID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := approved.CanonicalHash()
	if before != after {
		t.Fatal("lifecycle changed canonical hash")
	}
	changed := approved
	changed.Correction += " changed"
	h, _ := changed.CanonicalHash()
	if h == after {
		t.Fatal("content change did not change hash")
	}
	changed = approved
	changed.Admission.EvidenceRef = "changed"
	h, _ = changed.CanonicalHash()
	if h == after {
		t.Fatal("admission change did not change hash")
	}
	changed = approved
	changed.SourceKind = SourceSimulation
	h, _ = changed.CanonicalHash()
	if h == after {
		t.Fatal("source kind change did not change hash")
	}
}
