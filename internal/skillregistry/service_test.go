package skillregistry

import (
	"errors"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

func validDraftCommand(now time.Time) CreateDraftCommand {
	return CreateDraftCommand{
		SkillID:       "auditar-ux-y-accesibilidad",
		VersionID:     "skillver-1",
		OrganizationID: "explorarte",
		Version:       1,
		CreatedByRole: "recursos_agenticos/disenador_skills",
		Manifest: Manifest{
			Name:                 "auditar-ux-y-accesibilidad",
			Description:          "Audita interfaces por UX, accesibilidad y rendimiento antes de terminarlas.",
			Department:           "ingenieria_ia",
			OwnerRoleID:          "ingenieria_ia/frontend",
			MemoryDomain:         "ingenieria_ia",
			BaseProtocol:         "verificacion_estado",
			RequiredCapabilities: []string{"code.run_tests", "organization.read_registry"},
		},
		Source: SourceRecord{
			Path:       "skills/auditar-ux-y-accesibilidad/SKILL.md",
			SHA256:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Origin:     OriginInternal,
			RecordedBy: "recursos_agenticos/disenador_skills",
			RecordRef:  "staging:artifact:41",
		},
		ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func TestLifecycleDefaultDenyAndActivationEvidence(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	_, version, err := svc.CreateDraft(validDraftCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Activate(version, ApprovalEvidence{}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("draft -> active error=%v", err)
	}
	clock.now = now.Add(time.Minute)
	version, err = svc.HumanApprove(version, ApprovalEvidence{DecisionRef: "authz:1", ApprovedBy: "empresa/human", ApprovedAt: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Minute)
	version, err = svc.QualifyCandidate(version, ValidationEvidence{SchemaValidationRef: "schema:1", CapabilityReviewRef: "capreview:1", SourceRecordRef: "staging:artifact:41", ValidatedBy: "recursos_agenticos/disenador_skills", ValidatedAt: clock.now, CapabilitiesPass: true})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(3 * time.Minute)
	version, err = svc.Activate(version, ApprovalEvidence{DecisionRef: "authz:2", ApprovedBy: "empresa/human", ApprovedAt: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if version.Lifecycle != LifecycleActive || version.Revision != 4 {
		t.Fatalf("version=%+v", version)
	}
}

func TestTransitionMatrixIsDefaultDeny(t *testing.T) {
	states := []Lifecycle{LifecycleDraft, LifecycleHumanApproved, LifecycleCandidate, LifecycleActive, LifecycleSuspended, LifecycleRetired}
	allowed := map[[2]Lifecycle]bool{
		{LifecycleDraft, LifecycleHumanApproved}:         true,
		{LifecycleDraft, LifecycleRetired}:               true,
		{LifecycleHumanApproved, LifecycleCandidate}:     true,
		{LifecycleHumanApproved, LifecycleRetired}:       true,
		{LifecycleCandidate, LifecycleActive}:            true,
		{LifecycleCandidate, LifecycleRetired}:           true,
		{LifecycleActive, LifecycleSuspended}:            true,
		{LifecycleActive, LifecycleRetired}:              true,
		{LifecycleSuspended, LifecycleActive}:            true,
		{LifecycleSuspended, LifecycleRetired}:           true,
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

func TestAssignmentPinsExactActiveVersion(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	_, version, err := svc.CreateDraft(validDraftCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(time.Minute)
	version, err = svc.HumanApprove(version, ApprovalEvidence{DecisionRef: "authz:1", ApprovedBy: "empresa/human", ApprovedAt: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Minute)
	version, err = svc.QualifyCandidate(version, ValidationEvidence{SchemaValidationRef: "schema:1", CapabilityReviewRef: "capreview:1", SourceRecordRef: "staging:artifact:41", ValidatedBy: "recursos_agenticos/disenador_skills", ValidatedAt: clock.now, CapabilitiesPass: true})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(3 * time.Minute)
	version, err = svc.Activate(version, ApprovalEvidence{DecisionRef: "authz:2", ApprovedBy: "empresa/human", ApprovedAt: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(4 * time.Minute)
	assignment, err := svc.Assign(version, AssignCommand{AssignmentID: "assign-1", OrganizationID: "explorarte", RoleID: "ingenieria_ia/frontend", AssignedBy: "empresa/human", AssignmentDecisionRef: "authz:3", CapabilityReviewRef: "role-capreview:1"})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.SkillVersionID != version.ID || assignment.Status != AssignmentActive {
		t.Fatalf("assignment=%+v", assignment)
	}
	clock.now = now.Add(5 * time.Minute)
	assignment, err = svc.RevokeAssignment(assignment, "role_restructured")
	if err != nil {
		t.Fatal(err)
	}
	if assignment.Status != AssignmentRevoked || assignment.RevokedAt == nil {
		t.Fatalf("assignment=%+v", assignment)
	}
}

func TestCapabilityReviewCannotBeSkipped(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	svc := NewService(clock)
	_, version, err := svc.CreateDraft(validDraftCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(time.Minute)
	version, err = svc.HumanApprove(version, ApprovalEvidence{DecisionRef: "authz:1", ApprovedBy: "empresa/human", ApprovedAt: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(2 * time.Minute)
	_, err = svc.QualifyCandidate(version, ValidationEvidence{SchemaValidationRef: "schema:1", CapabilityReviewRef: "capreview:1", SourceRecordRef: "staging:artifact:41", ValidatedBy: "recursos_agenticos/disenador_skills", ValidatedAt: clock.now, CapabilitiesPass: false})
	if !errors.Is(err, ErrCapabilityReviewFailed) {
		t.Fatalf("err=%v", err)
	}
}

func TestManifestHashCanonicalizesCapabilityOrder(t *testing.T) {
	a := validDraftCommand(time.Now()).Manifest
	b := a
	b.RequiredCapabilities = []string{"organization.read_registry", "code.run_tests"}
	ha, err := HashManifest(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := HashManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hash differs: %s %s", ha, hb)
	}
}
