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
