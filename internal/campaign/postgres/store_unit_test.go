package postgres

import (
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

func TestNewRequiresStore(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error with nil store")
	}
}

func TestValidateCreateProposalCommand(t *testing.T) {
	valid := campaign.CreateProposalCommand{
		OrganizationID:       "org-1",
		ConversationID:       10,
		CreatedByRoleID:      "empresa/human",
		CreatedFromMessageID: 20,
		TaskID:               30,
		AttemptID:            1,
		ToolCallID:           "call_123",
		IdempotencyKey:       "key_123",
		CanonicalHash:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Title:                "Test Campaign",
		Goal:                 "Test Goal",
		AcceptanceCriteria:   []string{"Criteria 1"},
	}

	if err := validateCreateProposalCommand(valid); err != nil {
		t.Fatalf("expected valid command to pass, got: %v", err)
	}

	// Missing Org
	bad := valid
	bad.OrganizationID = ""
	if err := validateCreateProposalCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// Bad conversation ID
	bad = valid
	bad.ConversationID = 0
	if err := validateCreateProposalCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// Bad Hash
	bad = valid
	bad.CanonicalHash = "invalid-hash"
	if err := validateCreateProposalCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// Empty Title
	bad = valid
	bad.Title = "   "
	if err := validateCreateProposalCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// Empty Goal
	bad = valid
	bad.Goal = ""
	if err := validateCreateProposalCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// Empty Criteria
	bad = valid
	bad.AcceptanceCriteria = []string{}
	if err := validateCreateProposalCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got: %v", err)
	}

	// Negative Budget
	bad = valid
	bad.Budget = &campaign.ProposalBudget{
		Currency:  "USD",
		MaxAmount: -10,
	}
	if err := validateCreateProposalCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got: %v", err)
	}
}

func TestValidateCreateReviewRequestCommand(t *testing.T) {
	valid := campaign.CreateReviewRequestCommand{
		OrganizationID:        "org-1",
		ProposalID:            10,
		ProposalCanonicalHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		RequestedByRoleID:     "empresa/ceo",
		ReviewerRoleID:        "negocio/administrador_financiero",
		ReviewTaskID:          100,
		IdempotencyKey:        "cfinreq:10:1:call1",
	}

	if err := validateCreateReviewRequestCommand(valid); err != nil {
		t.Fatalf("expected valid command to pass, got: %v", err)
	}

	bad := valid
	bad.OrganizationID = ""
	if err := validateCreateReviewRequestCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for missing org, got: %v", err)
	}

	bad = valid
	bad.ProposalID = 0
	if err := validateCreateReviewRequestCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for non-positive proposal ID, got: %v", err)
	}

	bad = valid
	bad.ProposalCanonicalHash = "bad-hash"
	if err := validateCreateReviewRequestCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for invalid hash, got: %v", err)
	}

	bad = valid
	bad.RequestedByRoleID = ""
	if err := validateCreateReviewRequestCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for missing requester, got: %v", err)
	}

	bad = valid
	bad.ReviewerRoleID = ""
	if err := validateCreateReviewRequestCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for missing reviewer, got: %v", err)
	}

	bad = valid
	bad.ReviewTaskID = 0
	if err := validateCreateReviewRequestCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for non-positive task ID, got: %v", err)
	}

	bad = valid
	bad.IdempotencyKey = ""
	if err := validateCreateReviewRequestCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for missing idempotency key, got: %v", err)
	}
}

func TestValidateRecordFinancialReviewCommand(t *testing.T) {
	valid := campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-1",
		ReviewRequestID:       5,
		ProposalID:            10,
		ProposalCanonicalHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ReviewerRoleID:        "negocio/administrador_financiero",
		ReviewTaskID:          100,
		ReviewAttemptID:       1,
		Verdict:               campaign.VerdictRecommended,
		Summary:               "Viable under limits",
		CanonicalHash:         "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
	}

	if err := validateRecordFinancialReviewCommand(valid); err != nil {
		t.Fatalf("expected valid command to pass, got: %v", err)
	}

	bad := valid
	bad.Verdict = "invalid_verdict"
	if err := validateRecordFinancialReviewCommand(bad); !errors.Is(err, campaign.ErrInvalidVerdict) {
		t.Fatalf("expected ErrInvalidVerdict, got: %v", err)
	}

	bad = valid
	bad.Summary = ""
	if err := validateRecordFinancialReviewCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty summary, got: %v", err)
	}

	bad = valid
	bad.ReviewRequestID = 0
	if err := validateRecordFinancialReviewCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for invalid request ID, got: %v", err)
	}

	bad = valid
	bad.CanonicalHash = "short"
	if err := validateRecordFinancialReviewCommand(bad); !errors.Is(err, campaign.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for invalid canonical hash, got: %v", err)
	}
}
