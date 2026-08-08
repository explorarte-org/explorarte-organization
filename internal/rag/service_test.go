package rag

import (
	"errors"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

func validProposeCommand(now time.Time) ProposeCommand {
	return ProposeCommand{
		ID: "know-1", DocumentID: "gestion-riesgos-modelos", OrganizationID: "explorarte", NamespaceKind: NamespaceDepartment, NamespaceID: "ingenieria_ia",
		Version: 1, Title: "Gestión de riesgos en despliegues de modelos", Body: "Antes de desplegar un modelo nuevo, valida la política de egress y el owner del dataset.\n\nRegistra la evidencia de validación en el ticket de staging.",
		SourceKind: SourceResearch, SourceReference: "investigacion:report:41", ProposedBy: "investigacion/research_worker_hourly",
		EvidenceRefs: []EvidenceRef{{Reference: "evidence:a", Digest: "aaa"}, {Reference: "evidence:b", Digest: "bbb"}},
		Admission:    AdmissionAttestation{DataClass: DataOrganizational, AttestedBy: "investigacion/research_worker_hourly", SourceBoundary: "organization", EvidenceRef: "admission:know-1", AttestedAt: now.Add(-time.Second)},
	}
}

func proposeVersion(t *testing.T, svc *Service, clock *fixedClock, now time.Time) KnowledgeVersion {
	t.Helper()
	clock.now = now
	version, err := svc.Propose(validProposeCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func advanceToApproved(t *testing.T, svc *Service, clock *fixedClock, now time.Time) KnowledgeVersion {
	t.Helper()
	version := proposeVersion(t, svc, clock, now)
	clock.now = now.Add(time.Minute)
	version, err := svc.Review(version, ReviewApprove, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestLifecycleDefaultDenyAndReviewProvenance(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	candidate := proposeVersion(t, svc, clock, now)
	if _, err := svc.Deprecate(candidate); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("candidate -> deprecated error=%v", err)
	}
	approved := advanceToApproved(t, svc, clock, now)
	if approved.Lifecycle != LifecycleApproved || approved.Revision != 2 || approved.ReviewerID != "empresa/human" {
		t.Fatalf("approved=%+v", approved)
	}
	clock.now = now.Add(2 * time.Minute)
	deprecated, err := svc.Deprecate(approved)
	if err != nil {
		t.Fatal(err)
	}
	if deprecated.ReviewerID != approved.ReviewerID || deprecated.ReviewedAt == nil || !deprecated.ReviewedAt.Equal(*approved.ReviewedAt) {
		t.Fatal("review provenance changed across a later transition")
	}
	clock.now = now.Add(3 * time.Minute)
	archived, err := svc.Archive(deprecated)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Lifecycle != LifecycleArchived {
		t.Fatalf("archived=%+v", archived)
	}
	if _, err := svc.Archive(archived); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("archived -> archived error=%v", err)
	}
}

func TestTransitionMatrixIsDefaultDeny(t *testing.T) {
	states := []Lifecycle{LifecycleCandidate, LifecycleApproved, LifecycleRejected, LifecycleDeprecated, LifecycleArchived}
	allowed := map[[2]Lifecycle]bool{
		{LifecycleCandidate, LifecycleApproved}:  true,
		{LifecycleCandidate, LifecycleRejected}:  true,
		{LifecycleApproved, LifecycleDeprecated}: true,
		{LifecycleDeprecated, LifecycleArchived}: true,
		{LifecycleRejected, LifecycleArchived}:   true,
	}
	for _, from := range states {
		for _, to := range states {
			err := ValidateTransition(from, to)
			if allowed[[2]Lifecycle{from, to}] && err != nil {
				t.Fatalf("%s->%s err=%v", from, to, err)
			}
			if !allowed[[2]Lifecycle{from, to}] && !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("%s->%s err=%v", from, to, err)
			}
		}
	}
}

func TestAdmissionRejectsClinicalAndSecretFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	svc := NewService(&fixedClock{now: now})
	for _, class := range []DataClass{DataClinical, DataSecret, DataClass("unknown")} {
		command := validProposeCommand(now)
		command.Admission.DataClass = class
		if _, err := svc.Propose(command); !errors.Is(err, ErrForbiddenDataClass) && !errors.Is(err, ErrInvalidAdmission) {
			t.Fatalf("data class %s accepted: %v", class, err)
		}
	}
}

func TestSanitizedRequiresExplicitEvidence(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	svc := NewService(&fixedClock{now: now})
	command := validProposeCommand(now)
	command.Admission.DataClass = DataSanitized
	if _, err := svc.Propose(command); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("sanitized without evidence accepted: %v", err)
	}
	command.Admission.SanitizationEvidenceRef = "sanitization:know-1"
	if _, err := svc.Propose(command); err != nil {
		t.Fatalf("sanitized with evidence rejected: %v", err)
	}
}

func TestCanonicalHashDetectsTampering(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	svc := NewService(&fixedClock{now: now})
	version := proposeVersion(t, svc, &fixedClock{now: now}, now)
	version.Title += " tampered"
	if err := version.Validate(); !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("tampered title err=%v", err)
	}
}

func TestContentHashMustMatchBody(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	svc := NewService(&fixedClock{now: now})
	version := proposeVersion(t, svc, &fixedClock{now: now}, now)
	version.Body += " tampered"
	if err := version.Validate(); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("tampered body err=%v", err)
	}
}

func TestDeterministicChunkingIsStableAcrossRuns(t *testing.T) {
	body := validProposeCommand(time.Now()).Body
	a, err := ChunkBody("know-1", DefaultChunkerID, DefaultChunkerVersion, body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ChunkBody("know-1", DefaultChunkerID, DefaultChunkerVersion, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("chunk counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ContentHash != b[i].ContentHash || a[i].Ordinal != b[i].Ordinal || a[i].StartOffset != b[i].StartOffset {
			t.Fatalf("chunk %d differs across identical runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestProposeRejectsSecretContentDeclaredAsLowerDataClass(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	command := validProposeCommand(now)
	command.Body = "Rotate the deploy key.\n\napi_key: sk_live_abcdefghijklmnop\n\nUse it for the staging pipeline."
	if _, err := svc.Propose(command); !errors.Is(err, ErrForbiddenDataClass) {
		t.Fatalf("propose secret-shaped body under %q: err=%v want ErrForbiddenDataClass", command.Admission.DataClass, err)
	}
}

func TestProposeRejectsClinicalContentDeclaredAsLowerDataClass(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	command := validProposeCommand(now)
	command.Body = "El paciente presenta síntomas relacionados con el nuevo despliegue de modelos."
	if _, err := svc.Propose(command); !errors.Is(err, ErrForbiddenDataClass) {
		t.Fatalf("propose clinical-shaped body under %q: err=%v want ErrForbiddenDataClass", command.Admission.DataClass, err)
	}
}

func TestProposeAllowsOrdinaryContentUnderDeclaredDataClass(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	if _, err := svc.Propose(validProposeCommand(now)); err != nil {
		t.Fatalf("propose ordinary body: %v", err)
	}
}

func TestChunkingBoundsLargeParagraphs(t *testing.T) {
	long := ""
	for i := 0; i < 2000; i++ {
		long += "x"
	}
	chunks, err := ChunkBody("know-big", DefaultChunkerID, DefaultChunkerVersion, long)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected an oversized paragraph to split into multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk.Content) > maxChunkBytes {
			t.Fatalf("chunk exceeds max bytes: %d", len(chunk.Content))
		}
	}
}
