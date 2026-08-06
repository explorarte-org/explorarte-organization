package modelidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func identityFixture(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, ExecutionIdentityKey, IssuedChallenge, time.Time) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	scope := ChallengeScope{
		OrganizationID: "explorarte", OrganizationRevisionID: 7, InvocationID: 11,
		DispatcherAssignmentID: 31, ExecutionPrincipalID: 21,
		ExecutionIdentityPolicyVersionID: 41, ExecutionIdentityPolicyHash: SHA256Bytes([]byte("policy")),
		ExecutionIdentityKeyID: 51, ActionDigest: SHA256Bytes([]byte("action")), RequestHash: SHA256Bytes([]byte("request")),
	}
	nonce := "fixed-nonce-for-test"
	_, payload, err := BuildAssertionPayload(scope, 61, nonce, now, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	challenge := Challenge{
		ID: 61, OrganizationID: scope.OrganizationID, OrganizationRevisionID: scope.OrganizationRevisionID,
		InvocationID: scope.InvocationID, DispatcherAssignmentID: scope.DispatcherAssignmentID,
		ExecutionPrincipalID: scope.ExecutionPrincipalID, ExecutionIdentityPolicyVersionID: scope.ExecutionIdentityPolicyVersionID,
		ExecutionIdentityPolicyHash: scope.ExecutionIdentityPolicyHash, ExecutionIdentityKeyID: scope.ExecutionIdentityKeyID,
		NonceHash: SHA256Bytes([]byte(nonce)), PayloadHash: SHA256Bytes(payload), ActionDigest: scope.ActionDigest,
		RequestHash: scope.RequestHash, IssuedAt: now, ExpiresAt: now.Add(2 * time.Minute), CreatedAt: now,
	}
	key := ExecutionIdentityKey{ID: 51, OrganizationID: "explorarte", ExecutionPrincipalID: 21, Algorithm: AlgorithmEd25519, PublicKey: publicKey, PublicKeyFingerprint: PublicKeyFingerprint(publicKey), Status: KeyActive, ValidFrom: now.Add(-time.Minute)}
	return publicKey, privateKey, key, IssuedChallenge{Challenge: challenge, Nonce: nonce, Payload: payload}, now
}

func TestAssertionPayloadCanonicalizesPostgresTimestampPrecision(t *testing.T) {
	scope := ChallengeScope{
		OrganizationID: "explorarte", OrganizationRevisionID: 7, InvocationID: 11,
		DispatcherAssignmentID: 31, ExecutionPrincipalID: 21,
		ExecutionIdentityPolicyVersionID: 41, ExecutionIdentityPolicyHash: SHA256Bytes([]byte("policy")),
		ExecutionIdentityKeyID: 51, ActionDigest: SHA256Bytes([]byte("action")), RequestHash: SHA256Bytes([]byte("request")),
	}
	issuedAt := time.Date(2026, 8, 6, 0, 0, 0, 123456789, time.FixedZone("offset", -4*60*60))
	expiresAt := issuedAt.Add(2 * time.Minute)
	_, beforePersistence, err := BuildAssertionPayload(scope, 61, "fixed-nonce-for-test", issuedAt, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	_, afterPersistence, err := BuildAssertionPayload(scope, 61, "fixed-nonce-for-test", issuedAt.UTC().Truncate(time.Microsecond), expiresAt.UTC().Truncate(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	if string(beforePersistence) != string(afterPersistence) {
		t.Fatalf("payload changed after PostgreSQL timestamp normalization:\nbefore=%s\nafter=%s", beforePersistence, afterPersistence)
	}
}

func TestVerifyAssertionValidAndTamperResistant(t *testing.T) {
	_, privateKey, key, issued, now := identityFixture(t)
	signature := ed25519.Sign(privateKey, issued.Payload)
	verified, err := VerifyAssertion(key, issued, signature, now.Add(time.Second), 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if verified.ChallengeID != issued.Challenge.ID || verified.KeyID != key.ID || verified.AssertionHash == "" {
		t.Fatalf("unexpected assertion: %#v", verified)
	}
	issued.Payload = append([]byte(nil), issued.Payload...)
	issued.Payload[0] ^= 1
	if _, err = VerifyAssertion(key, issued, signature, now.Add(time.Second), 15*time.Second); !errors.Is(err, ErrAssertionInvalid) {
		t.Fatalf("tampered payload error=%v", err)
	}
}

func TestVerifyAssertionRejectsExpiredOrRevokedKey(t *testing.T) {
	_, privateKey, key, issued, now := identityFixture(t)
	signature := ed25519.Sign(privateKey, issued.Payload)
	if _, err := VerifyAssertion(key, issued, signature, now.Add(3*time.Minute), 0); !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("expired error=%v", err)
	}
	key.Status = KeyRevoked
	if _, err := VerifyAssertion(key, issued, signature, now.Add(time.Second), 0); !errors.Is(err, ErrKeyInactive) {
		t.Fatalf("revoked error=%v", err)
	}
}

func TestAssertionPayloadBindsAllExecutionScope(t *testing.T) {
	_, _, _, issued, _ := identityFixture(t)
	base := SHA256Bytes(issued.Payload)
	mutations := []func(*ChallengeScope){
		func(v *ChallengeScope) { v.OrganizationRevisionID++ },
		func(v *ChallengeScope) { v.InvocationID++ },
		func(v *ChallengeScope) { v.DispatcherAssignmentID++ },
		func(v *ChallengeScope) { v.ExecutionPrincipalID++ },
		func(v *ChallengeScope) { v.ExecutionIdentityPolicyVersionID++ },
		func(v *ChallengeScope) { v.ExecutionIdentityKeyID++ },
		func(v *ChallengeScope) { v.ActionDigest = SHA256Bytes([]byte("other-action")) },
		func(v *ChallengeScope) { v.RequestHash = SHA256Bytes([]byte("other-request")) },
	}
	original := ChallengeScope{OrganizationID: "explorarte", OrganizationRevisionID: 7, InvocationID: 11, DispatcherAssignmentID: 31, ExecutionPrincipalID: 21, ExecutionIdentityPolicyVersionID: 41, ExecutionIdentityPolicyHash: SHA256Bytes([]byte("policy")), ExecutionIdentityKeyID: 51, ActionDigest: SHA256Bytes([]byte("action")), RequestHash: SHA256Bytes([]byte("request"))}
	for i, mutate := range mutations {
		candidate := original
		mutate(&candidate)
		_, body, err := BuildAssertionPayload(candidate, 61, "fixed-nonce-for-test", issued.Challenge.IssuedAt, issued.Challenge.ExpiresAt)
		if err != nil {
			t.Fatal(err)
		}
		if SHA256Bytes(body) == base {
			t.Fatalf("mutation %d did not change payload hash", i)
		}
	}
}
