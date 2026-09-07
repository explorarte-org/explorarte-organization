package need

import (
	"context"
	"testing"
	"time"
)

func TestProcedureNeedLifecycleAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()

	now := time.Now().UTC()
	n := ProcedureNeed{
		ID:               "need-qa-regression",
		OrganizationID:   "explorarte",
		RoleID:           "ingenieria_ia/qa",
		TaskClass:        "qa.regression",
		ProblemStatement: "Detect and isolate flaky test cases in integration suites.",
		Status:           StatusOpen,
		Revision:         1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// 1. Validate and create open need
	created, err := repo.CreateNeed(ctx, n)
	if err != nil {
		t.Fatalf("CreateNeed failed: %v", err)
	}
	if created.CanonicalDigest == "" {
		t.Fatal("expected non-empty canonical digest")
	}

	// 2. Open need cannot have acceptance evidence
	nWithInvalidAcc := created
	nWithInvalidAcc.Acceptance = &Acceptance{
		DecisionRef: "dec-1",
		AcceptedBy:  "empresa/director_general",
		AcceptedAt:  now,
	}
	if err := nWithInvalidAcc.Validate(); err == nil {
		t.Fatal("open need with acceptance must fail validation")
	}

	// 3. Accept need with decision ref
	created.Status = StatusAccepted
	created.Acceptance = &Acceptance{
		DecisionRef: "owner-decision:forge:qa-regression:v1",
		AcceptedBy:  "empresa/director_general",
		AcceptedAt:  now,
	}
	saved, err := repo.SaveNeed(ctx, created, 1)
	if err != nil {
		t.Fatalf("SaveNeed (accept) failed: %v", err)
	}
	if saved.Status != StatusAccepted || saved.Revision != 2 {
		t.Fatalf("unexpected saved need: %+v", saved)
	}

	// 4. Incomplete acceptance fails validation
	badAcceptance := saved
	badAcceptance.Acceptance = &Acceptance{
		DecisionRef: "",
		AcceptedBy:  "empresa/director_general",
		AcceptedAt:  now,
	}
	if err := badAcceptance.Validate(); err == nil {
		t.Fatal("expected empty decision ref in acceptance to fail")
	}

	// 5. Canonical digest is idempotent and deterministic
	digest1, err := HashNeedIdentity(n)
	if err != nil {
		t.Fatal(err)
	}
	digest2, err := HashNeedIdentity(n)
	if err != nil {
		t.Fatal(err)
	}
	if digest1 != digest2 {
		t.Fatalf("digest not deterministic: %s != %s", digest1, digest2)
	}
}
