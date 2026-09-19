package campaign

import (
	"fmt"
	"strings"
)

const (
	promotionSubmitKeyPrefix = "campaign-promotion-"
	canonicalHashMinLen      = 16
	canonicalHashPrefixLen   = 16
	maxSubmitKeyBytes        = 200
)

// CampaignPromotionSubmitKey produces a deterministic, trusted-root-safe Executive
// submit idempotency key from an owner approval ID and its canonical hash.
//
// Shape:
//
//	campaign-promotion-<approvalID>-<first16CanonicalHash>
//
// Example:
//
//	campaign-promotion-2-c94240ebc0c117c5
//
// The generated key strictly satisfies modeldispatch's trusted-root causation syntax
// when prefixed with "owner:":
//
//	^[a-zA-Z0-9]+(?:[._/-][a-zA-Z0-9]+)*$
//
// Colons are strictly forbidden. Input fields must be valid structured host facts:
// approvalID must be positive, and canonicalHash must contain at least 16 lowercase
// hexadecimal characters without any lossy replacement or arbitrary sanitization.
func CampaignPromotionSubmitKey(approvalID int64, canonicalHash string) (string, error) {
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

	key := fmt.Sprintf("%s%d-%s", promotionSubmitKeyPrefix, approvalID, prefix)
	if len(key) > maxSubmitKeyBytes {
		return "", fmt.Errorf("%w: campaign promotion submit key exceeds %d bytes (%d)", ErrInvalidInput, maxSubmitKeyBytes, len(key))
	}
	if strings.Contains(key, ":") {
		return "", fmt.Errorf("%w: campaign promotion submit key contains forbidden colon", ErrInvalidInput)
	}
	return key, nil
}

func campaignPromotionSubmitKey(approvalID int64, canonicalHash string) (string, error) {
	return CampaignPromotionSubmitKey(approvalID, canonicalHash)
}
