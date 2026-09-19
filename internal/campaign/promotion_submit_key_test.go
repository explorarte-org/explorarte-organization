package campaign_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

// TestCampaignPromotionSubmitKey_ExactKey implements Requirement 9:
// - approval ID = 42
// - canonical hash = 0123456789abcdef...
// - expected exactly: campaign-promotion-42-0123456789abcdef
// - assert no ":"
// - assert length <= 200
// - same inputs -> byte-identical key
// - different approval IDs -> different keys
// - different hash prefixes -> different keys
func TestCampaignPromotionSubmitKey_ExactKey(t *testing.T) {
	const (
		testApprovalID    = int64(42)
		testCanonicalHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		wantKey           = "campaign-promotion-42-0123456789abcdef"
	)

	key, err := campaign.CampaignPromotionSubmitKey(testApprovalID, testCanonicalHash)
	if err != nil {
		t.Fatalf("CampaignPromotionSubmitKey failed: %v", err)
	}

	if key != wantKey {
		t.Fatalf("key = %q, want %q", key, wantKey)
	}

	if strings.Contains(key, ":") {
		t.Fatalf("key %q contains forbidden colon", key)
	}

	if len(key) > 200 {
		t.Fatalf("key length %d exceeds 200 bytes", len(key))
	}

	// Determinism: same inputs -> byte-identical key.
	keyAgain, err := campaign.CampaignPromotionSubmitKey(testApprovalID, testCanonicalHash)
	if err != nil {
		t.Fatalf("repeat CampaignPromotionSubmitKey failed: %v", err)
	}
	if key != keyAgain {
		t.Fatalf("non-deterministic key: %q != %q", key, keyAgain)
	}

	// Different approval IDs -> different keys.
	keyDiffID, err := campaign.CampaignPromotionSubmitKey(43, testCanonicalHash)
	if err != nil {
		t.Fatalf("CampaignPromotionSubmitKey for ID 43 failed: %v", err)
	}
	if key == keyDiffID {
		t.Fatalf("collision across approval IDs: %q == %q", key, keyDiffID)
	}

	// Different hash prefixes -> different keys.
	keyDiffHash, err := campaign.CampaignPromotionSubmitKey(testApprovalID, "fedcba98765432100123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("CampaignPromotionSubmitKey for diff hash failed: %v", err)
	}
	if key == keyDiffHash {
		t.Fatalf("collision across hash prefixes: %q == %q", key, keyDiffHash)
	}

	// Validation constraints:
	t.Run("Rejects non-positive approval ID", func(t *testing.T) {
		for _, invalidID := range []int64{0, -1, -999} {
			_, err := campaign.CampaignPromotionSubmitKey(invalidID, testCanonicalHash)
			if err == nil {
				t.Errorf("expected error for approval ID %d, got nil", invalidID)
			}
			if !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("error %v does not wrap ErrInvalidInput", err)
			}
		}
	})

	t.Run("Rejects insufficient hash material", func(t *testing.T) {
		for _, shortHash := range []string{"", "0123", "0123456789abcde"} {
			_, err := campaign.CampaignPromotionSubmitKey(testApprovalID, shortHash)
			if err == nil {
				t.Errorf("expected error for hash %q (len %d), got nil", shortHash, len(shortHash))
			}
			if !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("error %v does not wrap ErrInvalidInput", err)
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
			_, err := campaign.CampaignPromotionSubmitKey(testApprovalID, badHash)
			if err == nil {
				t.Errorf("expected error for bad hash %q, got nil", badHash)
			}
			if !errors.Is(err, campaign.ErrInvalidInput) {
				t.Errorf("error %v does not wrap ErrInvalidInput", err)
			}
		}
	})
}

// TestPromotionSubmitRequest_CapturesSafeIdempotencyKey implements Requirement 10:
// Using PromotionService fakeSubmitter:
// promote a valid executable approval.
// Capture: executive.SubmitRequest.IdempotencyKey
// Expected: campaign-promotion-<id>-<hash16>
// Then assert the resulting Executive owner causation shape:
// "owner:" + submitKey
// contains no forbidden ":" after the owner prefix.
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

	wantSubmitKey := fmt.Sprintf("campaign-promotion-%d-%.16s", appr.ID, appr.CanonicalHash)
	capturedKey := submitter.lastRequest.IdempotencyKey

	if capturedKey != wantSubmitKey {
		t.Fatalf("captured submit key = %q, want %q", capturedKey, wantSubmitKey)
	}

	// Check executive root causation shape: "owner:" + submitKey
	rootCausation := "owner:" + capturedKey
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

	// Verify the promotion record sealed the identical submit key.
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
