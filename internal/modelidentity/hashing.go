package modelidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

func SHA256Bytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func PublicKeyFingerprint(publicKey []byte) string { return SHA256Bytes(publicKey) }

func KeyRequestHash(p PreparedKey) (string, error) {
	body, err := json.Marshal(struct {
		OrganizationID       string `json:"organization_id"`
		ExecutionPrincipalID int64  `json:"execution_principal_id"`
		PublicKeyFingerprint string `json:"public_key_fingerprint"`
		SecretRef            string `json:"secret_ref"`
		ValidUntil           string `json:"valid_until,omitempty"`
		CreatedByRoleID      string `json:"created_by_role_id"`
	}{p.OrganizationID, p.ExecutionPrincipalID, p.PublicKeyFingerprint, p.SecretRef, formatOptionalTime(p.ValidUntil), p.CreatedByRoleID})
	if err != nil {
		return "", err
	}
	return SHA256Bytes(body), nil
}

func BuildAssertionPayload(scope ChallengeScope, challengeID int64, nonce string, issuedAt, expiresAt time.Time) (AssertionPayload, []byte, error) {
	payload := AssertionPayload{
		SchemaVersion:                    1,
		OrganizationID:                   scope.OrganizationID,
		OrganizationRevisionID:           scope.OrganizationRevisionID,
		InvocationID:                     scope.InvocationID,
		DispatcherAssignmentID:           scope.DispatcherAssignmentID,
		ExecutionPrincipalID:             scope.ExecutionPrincipalID,
		ExecutionIdentityPolicyVersionID: scope.ExecutionIdentityPolicyVersionID,
		ExecutionIdentityPolicyHash:      scope.ExecutionIdentityPolicyHash,
		ExecutionIdentityKeyID:           scope.ExecutionIdentityKeyID,
		ChallengeID:                      challengeID,
		ChallengeNonce:                   nonce,
		ActionDigest:                     scope.ActionDigest,
		RequestHash:                      scope.RequestHash,
		IssuedAt:                         canonicalAssertionTime(issuedAt).Format(time.RFC3339Nano),
		ExpiresAt:                        canonicalAssertionTime(expiresAt).Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(payload)
	return payload, body, err
}

func canonicalAssertionTime(value time.Time) time.Time {
	// PostgreSQL timestamptz stores microsecond precision. Canonical signed
	// payloads must use the same precision before persistence so a challenge
	// reconstructed from durable state produces byte-identical JSON.
	return value.UTC().Truncate(time.Microsecond)
}

func AssertionHash(payloadHash, signatureHash string) string {
	body, _ := json.Marshal(struct {
		PayloadHash   string `json:"payload_hash"`
		SignatureHash string `json:"signature_hash"`
	}{payloadHash, signatureHash})
	return SHA256Bytes(body)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
