package campaign

import (
	"fmt"
	"strings"
)

const (
	promotionSubmitKeyPrefix            = "campaign-promotion:"
	promotionTrustedRootCausationPrefix = "campaign-promotion-"
	canonicalHashMinLen                 = 16
	canonicalHashPrefixLen              = 16
	maxSubmitKeyBytes                   = 200
)

// CampaignPromotionSubmitKey produces a deterministic, pre-#226 compatible Executive
// submit idempotency key from an owner approval ID and its canonical hash.
//
// Shape:
//
//	campaign-promotion:<approvalID>:<first16CanonicalHash>
//
// Example:
//
//	campaign-promotion:42:0123456789abcdef
//
// This preserves Campaign's durable submission identity across versions so that
// retries find the existing durable Executive root without opening a duplicate-root
// crash window.
func CampaignPromotionSubmitKey(approvalID int64, canonicalHash string) (string, error) {
	prefix, err := validateApprovalAndCanonicalHash(approvalID, canonicalHash)
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("%s%d:%s", promotionSubmitKeyPrefix, approvalID, prefix)
	if len(key) > maxSubmitKeyBytes {
		return "", fmt.Errorf("%w: campaign promotion submit key exceeds %d bytes (%d)", ErrInvalidInput, maxSubmitKeyBytes, len(key))
	}
	return key, nil
}

// CampaignPromotionTrustedRootCausationKey produces a deterministic, trusted-root-safe
// causation key from an owner approval ID and its canonical hash.
//
// Shape:
//
//	campaign-promotion-<approvalID>-<first16CanonicalHash>
//
// Example:
//
//	campaign-promotion-42-0123456789abcdef
//
// When prefixed with "owner:" in Executive root causation ("owner:" + key), the resulting
// token strictly satisfies modeldispatch's trusted-root causation syntax:
//
//	^[a-zA-Z0-9]+(?:[._/-][a-zA-Z0-9]+)*$
//
// Colons are strictly forbidden in this causation key.
func CampaignPromotionTrustedRootCausationKey(approvalID int64, canonicalHash string) (string, error) {
	prefix, err := validateApprovalAndCanonicalHash(approvalID, canonicalHash)
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("%s%d-%s", promotionTrustedRootCausationPrefix, approvalID, prefix)
	if len(key) > maxSubmitKeyBytes {
		return "", fmt.Errorf("%w: campaign promotion trusted root causation key exceeds %d bytes (%d)", ErrInvalidInput, maxSubmitKeyBytes, len(key))
	}
	if strings.Contains(key, ":") {
		return "", fmt.Errorf("%w: campaign promotion trusted root causation key contains forbidden colon", ErrInvalidInput)
	}
	return key, nil
}

func validateApprovalAndCanonicalHash(approvalID int64, canonicalHash string) (string, error) {
	if approvalID <= 0 {
		return "", fmt.Errorf("%w: owner approval ID must be positive, got %d", ErrInvalidInput, approvalID)
	}
	if len(canonicalHash) < canonicalHashMinLen {
		return "", fmt.Errorf("%w: canonical hash must have at least %d characters, got %d", ErrInvalidInput, canonicalHashMinLen, len(canonicalHash))
	}
	prefix := canonicalHash[:canonicalHashPrefixLen]
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", fmt.Errorf("%w: canonical hash prefix must be lowercase hexadecimal characters, got %q", ErrInvalidInput, prefix)
		}
	}
	return prefix, nil
}

func campaignPromotionSubmitKey(approvalID int64, canonicalHash string) (string, error) {
	return CampaignPromotionSubmitKey(approvalID, canonicalHash)
}
