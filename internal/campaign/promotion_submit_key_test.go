package campaign_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

// TestCampaignPromotionSubmitKey_ExactKey tests both:
// 1. CampaignPromotionSubmitKey (durable submission identity):
//   - approval ID = 42
//   - canonical hash = 0123456789abcdef...
//   - expected exactly: campaign-promotion:42:0123456789abcdef
//
// 2. CampaignPromotionTrustedRootCausationKey (trusted-root causation token):
//   - expected exactly: campaign-promotion-42-0123456789abcdef
//   - assert no ":"
//   - assert length <= 200
func TestCampaignPromotionSubmitKey_ExactKey(t *testing.T) {
	const (
		testApprovalID    = int64(42)
		testCanonicalHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		wantSubmitKey     = "campaign-promotion:42:0123456789abcdef"
		wantCausationKey  = "campaign-promotion-42-0123456789abcdef"
	)

	// Test submission idempotency key:
	submitKey, err := campaign.CampaignPromotionSubmitKey(testApprovalID, testCanonicalHash)
	if err != nil {
		t.Fatalf("CampaignPromotionSubmitKey failed: %v", err)
	}
	if submitKey != wantSubmitKey {
		t.Fatalf("submitKey = %q, want %q", submitKey, wantSubmitKey)
	}
	if len(submitKey) > 200 {
		t.Fatalf("submitKey length %d exceeds 200 bytes", len(submitKey))
	}

	// Test trusted-root causation key:
	causationKey, err := campaign.CampaignPromotionTrustedRootCausationKey(testApprovalID, testCanonicalHash)
	if err != nil {
		t.Fatalf("CampaignPromotionTrustedRootCausationKey failed: %v", err)
	}
	if causationKey != wantCausationKey {
		t.Fatalf("causationKey = %q, want %q", causationKey, wantCausationKey)
	}
	if strings.Contains(causationKey, ":") {
		t.Fatalf("causationKey %q contains forbidden colon", causationKey)
	}
	if len(causationKey) > 200 {
		t.Fatalf("causationKey length %d exceeds 200 bytes", len(causationKey))
	}

	// Determinism:
	submitKeyAgain, _ := campaign.CampaignPromotionSubmitKey(testApprovalID, testCanonicalHash)
	if submitKey != submitKeyAgain {
		t.Fatalf("non-deterministic submitKey: %q != %q", submitKey, submitKeyAgain)
	}
	causationKeyAgain, _ := campaign.CampaignPromotionTrustedRootCausationKey(testApprovalID, testCanonicalHash)
	if causationKey != causationKeyAgain {
		t.Fatalf("non-deterministic causationKey: %q != %q", causationKey, causationKeyAgain)
	}

	// Different approval IDs -> different keys:
	diffIDSubmit, _ := campaign.CampaignPromotionSubmitKey(43, testCanonicalHash)
	if submitKey == diffIDSubmit {
		t.Fatalf("collision across approval IDs on submit key: %q == %q", submitKey, diffIDSubmit)
	}
	diffIDCausation, _ := campaign.CampaignPromotionTrustedRootCausationKey(43, testCanonicalHash)
	if causationKey == diffIDCausation {
		t.Fatalf("collision across approval IDs on causation key: %q == %q", causationKey, diffIDCausation)
	}

	// Different hash prefixes -> different keys:
	diffHash := "fedcba98765432100123456789abcdef0123456789abcdef0123456789abcdef"
	diffHashSubmit, _ := campaign.CampaignPromotionSubmitKey(testApprovalID, diffHash)
	if submitKey == diffHashSubmit {
		t.Fatalf("collision across hashes on submit key: %q == %q", submitKey, diffHashSubmit)
	}
	diffHashCausation, _ := campaign.CampaignPromotionTrustedRootCausationKey(testApprovalID, diffHash)
	if causationKey == diffHashCausation {
		t.Fatalf("collision across hashes on causation key: %q == %q", causationKey, diffHashCausation)
	}

	// Validation constraints:
	t.Run("Rejects non-positive approval ID", func(t *testing.T) {
		for _, invalidID := range []int64{0, -1, -999} {
			if _, err := campaign.CampaignPromotionSubmitKey(invalidID, testCanonicalHash); !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("CampaignPromotionSubmitKey expected ErrInvalidInput for %d, got %v", invalidID, err)
			}
			if _, err := campaign.CampaignPromotionTrustedRootCausationKey(invalidID, testCanonicalHash); !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("CampaignPromotionTrustedRootCausationKey expected ErrInvalidInput for %d, got %v", invalidID, err)
			}
		}
	})

	t.Run("Rejects insufficient hash material", func(t *testing.T) {
		for _, shortHash := range []string{"", "0123", "0123456789abcde"} {
			if _, err := campaign.CampaignPromotionSubmitKey(testApprovalID, shortHash); !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("CampaignPromotionSubmitKey expected ErrInvalidInput for %q, got %v", shortHash, err)
			}
			if _, err := campaign.CampaignPromotionTrustedRootCausationKey(testApprovalID, shortHash); !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("CampaignPromotionTrustedRootCausationKey expected ErrInvalidInput for %q, got %v", shortHash, err)
			}
		}
	})

	t.Run("Rejects non-hex or non-lowercase characters", func(t *testing.T) {
		for _, badHash := range []string{
			"0123456789ABCDEF0123456789abcdef", // uppercase
			"0123456789:bcdef0123456789abcdef", // colon
			"0123456789-bcdef0123456789abcdef", // hyphen in hash
			" 123456789abcdef0123456789abcdef", // leading space
			"0123456789_cdef0123456789abcdef",  // underscore
		} {
			if _, err := campaign.CampaignPromotionSubmitKey(testApprovalID, badHash); !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("CampaignPromotionSubmitKey expected ErrInvalidInput for %q, got %v", badHash, err)
			}
			if _, err := campaign.CampaignPromotionTrustedRootCausationKey(testApprovalID, badHash); !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("CampaignPromotionTrustedRootCausationKey expected ErrInvalidInput for %q, got %v", badHash, err)
			}
		}
	})
}

// TestPromotionSubmitRequest_CapturesSafeIdempotencyKey verifies:
// - SubmitRequest.IdempotencyKey preserves pre-#226 identity: campaign-promotion:<id>:<hash16>
// - SubmitRequest.TrustedRootCausationKey is separated: campaign-promotion-<id>-<hash16>
// - Resulting Executive owner causation: "owner:" + TrustedRootCausationKey contains NO colons
// - CampaignPromotion record seals the durable ExecutiveSubmitIdempotencyKey
func TestPromotionSubmitRequest_CapturesSafeIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	_, submitter, svc, _, _, appr := setupPromotionFixture(t)

	res, err := svc.PromoteToExecutive(ctx, campaign.PromoteToExecutiveParams{
		OrganizationID:   "org-1",
		OwnerApprovalID:  appr.ID,
		PromotedByRoleID: "empresa/human",
		ConversationID:   1,
		ToolCallID:       "call-submit-test",
		IdempotencyKey:   "prom-submit-key-test",
	})
	if err != nil {
		t.Fatalf("PromoteToExecutive: %v", err)
	}

	wantSubmitKey := fmt.Sprintf("campaign-promotion:%d:%.16s", appr.ID, appr.CanonicalHash)
	capturedKey := submitter.lastRequest.IdempotencyKey
	if capturedKey != wantSubmitKey {
		t.Fatalf("captured submit key = %q, want %q", capturedKey, wantSubmitKey)
	}

	wantCausationKey := fmt.Sprintf("campaign-promotion-%d-%.16s", appr.ID, appr.CanonicalHash)
	capturedCausationKey := submitter.lastRequest.TrustedRootCausationKey
	if capturedCausationKey != wantCausationKey {
		t.Fatalf("captured causation key = %q, want %q", capturedCausationKey, wantCausationKey)
	}

	// Check executive root causation shape: "owner:" + capturedCausationKey
	rootCausation := "owner:" + capturedCausationKey
	if !strings.HasPrefix(rootCausation, "owner:") {
		t.Fatalf("root causation %q lacks literal owner: prefix", rootCausation)
	}

	suffix := strings.TrimPrefix(rootCausation, "owner:")
	if strings.Contains(suffix, ":") {
		t.Fatalf("root causation suffix %q contains forbidden colon \":\"", suffix)
	}

	if len(rootCausation) > 200 {
		t.Fatalf("root causation length %d exceeds 200", len(rootCausation))
	}

	// Verify the promotion record sealed the durable submit key.
	if res.Promotion.ExecutiveSubmitIdempotencyKey != wantSubmitKey {
		t.Fatalf("sealed ExecutiveSubmitIdempotencyKey = %q, want %q",
			res.Promotion.ExecutiveSubmitIdempotencyKey, wantSubmitKey)
	}
}

// TestPromotionIdempotencyAndShortCircuit implements Requirements 16 & 17:
// Promote the same NEW owner approval twice:
// - One CampaignPromotion
// - One Executive root
// - Second call: Reused = true, Executive Submit is not duplicated.
// Historical short-circuit: if CampaignPromotion already exists,
// PromoteToExecutive returns it before calling Submit.
func TestPromotionIdempotencyAndShortCircuit(t *testing.T) {
	ctx := context.Background()
	_, submitter, svc, _, _, appr := setupPromotionFixture(t)

	params := campaign.PromoteToExecutiveParams{
		OrganizationID:   "org-1",
		OwnerApprovalID:  appr.ID,
		PromotedByRoleID: "empresa/human",
		ConversationID:   1,
		ToolCallID:       "call-idemp-1",
		IdempotencyKey:   "prom-idemp-1",
	}

	res1, err := svc.PromoteToExecutive(ctx, params)
	if err != nil {
		t.Fatalf("first promotion: %v", err)
	}
	if res1.Reused {
		t.Errorf("expected res1.Reused = false")
	}
	if submitter.submitCalls != 1 {
		t.Fatalf("submitCalls = %d, want 1", submitter.submitCalls)
	}

	// Second call with same approval: must return Reused = true without calling Submit again.
	res2, err := svc.PromoteToExecutive(ctx, params)
	if err != nil {
		t.Fatalf("second promotion: %v", err)
	}
	if !res2.Reused {
		t.Errorf("expected res2.Reused = true")
	}
	if res2.Promotion.ID != res1.Promotion.ID {
		t.Errorf("promID2 = %d, want %d", res2.Promotion.ID, res1.Promotion.ID)
	}
	if res2.ExecutiveRootTaskID != res1.ExecutiveRootTaskID {
		t.Errorf("rootID2 = %d, want %d", res2.ExecutiveRootTaskID, res1.ExecutiveRootTaskID)
	}
	if submitter.submitCalls != 1 {
		t.Errorf("submitCalls after second call = %d, want 1 (not duplicated)", submitter.submitCalls)
	}
}
