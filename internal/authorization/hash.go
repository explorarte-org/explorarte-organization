package authorization

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

func RequestHash(organizationID string, revisionID int64, matrixHash, requesterRoleID, capabilityID, approvalMode, resourceType, resourceID, actionDigest string, ttl time.Duration, reason string) string {
	parts := []string{
		organizationID,
		strconv.FormatInt(revisionID, 10),
		matrixHash,
		requesterRoleID,
		capabilityID,
		approvalMode,
		resourceType,
		resourceID,
		actionDigest,
		ttl.String(),
		strings.TrimSpace(reason),
	}
	var canonical strings.Builder
	for _, part := range parts {
		canonical.WriteString(strconv.Itoa(len(part)))
		canonical.WriteByte(':')
		canonical.WriteString(part)
		canonical.WriteByte('|')
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:])
}
