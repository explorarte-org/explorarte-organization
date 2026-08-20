package contextengine

import (
	"errors"
	"strings"
	"testing"
)

func adversarialRequest() BuildRequest {
	return BuildRequest{
		OrganizationID: "explorarte", OrganizationRevisionID: 7,
		ActorRoleID: AdversarialReviewerRoleID, Purpose: AdversarialReviewPurpose,
		TaskRef: "task:136",
	}
}

// The restricted mode engages on exactly one combination. Either half alone is
// refused rather than falling back to the ordinary assembly, because a silent
// fallback in either direction is the contamination this exists to prevent.
func TestAdversarialPairingIsExact(t *testing.T) {
	if err := validateAdversarialPairing(adversarialRequest()); err != nil {
		t.Fatalf("the exact pairing was refused: %v", err)
	}

	// B and C: the purpose with any other role.
	for _, actor := range []string{"empresa/ceo", "ingenieria_ia/orquestador", "ingenieria_ia/qa", "investigacion/auditor_cerebro_empresa"} {
		request := adversarialRequest()
		request.ActorRoleID = actor
		if err := validateAdversarialPairing(request); err == nil {
			t.Fatalf("%s was allowed to build an adversarial review context", actor)
		}
	}

	// D and E: the reviewer under any other purpose. This direction matters
	// just as much: it would hand the reviewer an ordinary organizational
	// context.
	for _, purpose := range []string{"department_plan", "department_review", "executive_ceo_plan", "design_adjudication", ""} {
		request := adversarialRequest()
		request.Purpose = purpose
		if err := validateAdversarialPairing(request); err == nil {
			t.Fatalf("the reviewer was allowed to build context under purpose %q", purpose)
		}
	}
}

// Requests that are not adversarial at all are untouched, so every existing
// context keeps its current behaviour.
func TestNonAdversarialRequestsDoNotEnterRestrictedMode(t *testing.T) {
	for _, request := range []BuildRequest{
		{ActorRoleID: "empresa/ceo", Purpose: "executive_ceo_plan"},
		{ActorRoleID: "ingenieria_ia/orquestador", Purpose: "department_plan"},
		{ActorRoleID: "ingenieria_ia/qa", Purpose: "department_worker"},
		{ActorRoleID: "investigacion/auditor_cerebro_empresa", Purpose: "research_audit"},
	} {
		if adversarialReviewRequested(request) {
			t.Fatalf("%s/%s was routed into the restricted mode", request.ActorRoleID, request.Purpose)
		}
	}
}

// F, G, H, K, L: the admission boundary refuses to BUILD a context carrying
// anything the reviewer's provider may not receive. Refusal rather than
// omission, because an omitted segment produces a snapshot that looks complete
// and reviewed less than it claimed.
func TestAdversarialContextRefusesInadmissibleData(t *testing.T) {
	safe := []SourceRecord{
		{Kind: SourceRoleProfile, DataClass: DataSanitized, Reference: "investigacion/revisor_adversarial/PERFIL.md"},
		{Kind: SourceTaskContext, DataClass: DataSanitized, Reference: "task:136"},
		{Kind: SourceRAGEvidence, DataClass: DataPublic, Reference: "evidence:1"},
	}
	if err := assertAdversarialEgressSafe(safe); err != nil {
		t.Fatalf("a sanitized/public source set was refused: %v", err)
	}

	// The exact classes that denied the real run, plus the two that are
	// globally hard-denied.
	for _, class := range []DataClass{DataOrganizational, DataSecret, DataClinical} {
		t.Run(string(class), func(t *testing.T) {
			contaminated := append(append([]SourceRecord(nil), safe...),
				SourceRecord{Kind: SourceCanonicalDocument, DataClass: class, Reference: "docs/canonical/capability-matrix.yaml"})
			err := assertAdversarialEgressSafe(contaminated)
			if err == nil {
				t.Fatalf("a %s source was admitted", class)
			}
			// The refusal has to say what was inadmissible, or the next
			// person debugging it is back to guessing.
			if !strings.Contains(err.Error(), string(class)) || !strings.Contains(err.Error(), "capability-matrix") {
				t.Fatalf("the refusal identifies neither the class nor the source: %v", err)
			}
		})
	}

	// An unknown or empty class is refused too: fail closed, not open.
	if err := assertAdversarialEgressSafe([]SourceRecord{{Kind: SourceTaskContext, DataClass: ""}}); err == nil {
		t.Fatal("a source with no classification was admitted")
	}
}

// G and H, stated as the concrete documents that contaminated the real run.
// Every one of these is organizational and must be refused by class.
func TestTheDocumentsThatContaminatedTheRealRunAreRefused(t *testing.T) {
	for _, reference := range []string{
		"docs/canonical/role-catalog.yaml",
		"docs/canonical/capability-matrix.yaml",
		"docs/canonical/model-routing.yaml",
		"docs/canonical/decisions-required.yaml",
		"docs/canonical/cell-boundaries.yaml",
		"AGENT.md",
	} {
		t.Run(reference, func(t *testing.T) {
			err := assertAdversarialEgressSafe([]SourceRecord{
				{Kind: SourceCanonicalDocument, DataClass: DataOrganizational, Reference: reference},
			})
			if err == nil {
				t.Fatalf("%s was admitted into an adversarial review context", reference)
			}
		})
	}
}

// The reviewer's own profile is admissible: it is the agent's operating
// contract, not organizational data about the company.
func TestTheReviewerProfileIsAdmissible(t *testing.T) {
	err := assertAdversarialEgressSafe([]SourceRecord{
		{Kind: SourceRoleProfile, DataClass: DataSanitized, Reference: "investigacion/revisor_adversarial/PERFIL.md"},
	})
	if err != nil {
		t.Fatalf("the reviewer's own contract was refused: %v", err)
	}
}

// The refusal is a Reject, so it surfaces as a build rejection with a reason
// rather than as an opaque internal error.
func TestInadmissibleDataProducesARejection(t *testing.T) {
	err := assertAdversarialEgressSafe([]SourceRecord{
		{Kind: SourceCanonicalDocument, DataClass: DataOrganizational, Reference: "docs/canonical/organization.yaml"},
	})
	var rejection *RejectionError
	if !errors.As(err, &rejection) {
		t.Fatalf("err=%T %v, want a rejection", err, err)
	}
}
