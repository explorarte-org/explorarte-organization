package modelidentity

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"
)

type ChallengeService struct {
	store Store
	clock Clock
}

func NewChallengeService(store Store, clock Clock) (*ChallengeService, error) {
	if store == nil {
		return nil, fmt.Errorf("model identity challenge store is required")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &ChallengeService{store: store, clock: clock}, nil
}

func (s *ChallengeService) ResolvePolicy(ctx context.Context, organizationID string) (ResolvedPolicy, error) {
	return s.store.ResolveActive(ctx, organizationID)
}

func (s *ChallengeService) ResolvePolicyByID(ctx context.Context, organizationID string, id int64) (ResolvedPolicy, error) {
	return s.store.ResolveByID(ctx, organizationID, id)
}

func (s *ChallengeService) ResolveActiveKeyByFingerprint(ctx context.Context, organizationID string, principalID int64, fingerprint string) (ExecutionIdentityKey, error) {
	return s.store.ResolveActiveKeyByFingerprint(ctx, organizationID, principalID, fingerprint)
}

func (s *ChallengeService) Issue(ctx context.Context, scope ChallengeScope, policy ResolvedPolicy) (IssuedChallenge, error) {
	if scope.OrganizationID == "" || scope.InvocationID <= 0 || scope.ExecutionPrincipalID <= 0 || scope.ExecutionIdentityKeyID <= 0 || scope.DispatcherAssignmentID <= 0 || scope.ActionDigest == "" || scope.RequestHash == "" {
		return IssuedChallenge{}, ErrAssertionInvalid
	}
	if policy.Version.ID != scope.ExecutionIdentityPolicyVersionID || policy.Version.CanonicalHash != scope.ExecutionIdentityPolicyHash ||
		(policy.Version.Status != PolicyActive && policy.Version.Status != PolicySuperseded) {
		return IssuedChallenge{}, ErrPolicyStale
	}
	nonce, err := NewNonce()
	if err != nil {
		return IssuedChallenge{}, err
	}
	issuedAt := s.clock.Now().UTC()
	expiresAt := issuedAt.Add(time.Duration(policy.Version.ChallengeTTLSeconds) * time.Second)
	prepared := PreparedChallenge{Scope: scope, Nonce: nonce, NonceHash: SHA256Bytes([]byte(nonce)), IssuedAt: issuedAt, ExpiresAt: expiresAt}
	challenge, err := s.store.CreateChallenge(ctx, prepared)
	if err != nil {
		return IssuedChallenge{}, err
	}
	_, payload, err := BuildAssertionPayload(scope, challenge.ID, nonce, issuedAt, expiresAt)
	if err != nil {
		return IssuedChallenge{}, err
	}
	if challenge.PayloadHash != SHA256Bytes(payload) {
		return IssuedChallenge{}, ErrAssertionInvalid
	}
	return IssuedChallenge{Challenge: challenge, Nonce: nonce, Payload: payload}, nil
}

func (s *ChallengeService) Verify(key ExecutionIdentityKey, issued IssuedChallenge, signature []byte, policy ResolvedPolicy) (VerifiedAssertion, error) {
	if len(signature) != ed25519.SignatureSize || key.ID != issued.Challenge.ExecutionIdentityKeyID ||
		key.ExecutionPrincipalID != issued.Challenge.ExecutionPrincipalID ||
		policy.Version.ID != issued.Challenge.ExecutionIdentityPolicyVersionID ||
		policy.Version.CanonicalHash != issued.Challenge.ExecutionIdentityPolicyHash ||
		(policy.Version.Status != PolicyActive && policy.Version.Status != PolicySuperseded) {
		return VerifiedAssertion{}, ErrAssertionInvalid
	}
	return VerifyAssertion(key, issued, signature, s.clock.Now(), time.Duration(policy.Version.ClockSkewSeconds)*time.Second)
}
