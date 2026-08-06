package modelidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

func DecodePublicKey(raw string) (ed25519.PublicKey, error) {
	body, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		body, err = base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	}
	if err != nil || len(body) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: Ed25519 public key must be base64-encoded 32 bytes", ErrInvalidKey)
	}
	return ed25519.PublicKey(append([]byte(nil), body...)), nil
}

func NewNonce() (string, error) {
	body := make([]byte, 32)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func VerifyAssertion(key ExecutionIdentityKey, issued IssuedChallenge, signature []byte, now time.Time, clockSkew time.Duration) (VerifiedAssertion, error) {
	if key.Algorithm != AlgorithmEd25519 || len(key.PublicKey) != ed25519.PublicKeySize {
		return VerifiedAssertion{}, ErrInvalidKey
	}
	if key.Status != KeyActive && key.Status != KeyRetiring {
		return VerifiedAssertion{}, ErrKeyInactive
	}
	if now.Before(key.ValidFrom) || (key.ValidUntil != nil && !now.Before(*key.ValidUntil)) {
		return VerifiedAssertion{}, ErrKeyInactive
	}
	if now.Add(clockSkew).Before(issued.Challenge.IssuedAt) || !now.Add(-clockSkew).Before(issued.Challenge.ExpiresAt) {
		return VerifiedAssertion{}, ErrChallengeExpired
	}
	if SHA256Bytes(issued.Payload) != issued.Challenge.PayloadHash {
		return VerifiedAssertion{}, ErrAssertionInvalid
	}
	if !ed25519.Verify(ed25519.PublicKey(key.PublicKey), issued.Payload, signature) {
		return VerifiedAssertion{}, ErrAssertionInvalid
	}
	sigHash := SHA256Bytes(signature)
	return VerifiedAssertion{
		ChallengeID:   issued.Challenge.ID,
		KeyID:         key.ID,
		PayloadHash:   issued.Challenge.PayloadHash,
		SignatureHash: sigHash,
		AssertionHash: AssertionHash(issued.Challenge.PayloadHash, sigHash),
		VerifiedAt:    now.UTC(),
	}, nil
}
