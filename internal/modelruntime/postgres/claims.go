package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

func newClaimToken() (string, string, error) {
	body := make([]byte, 32)
	if _, err := rand.Read(body); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

// ClaimInvocation remains only for expand-and-contract legacy rows. Any
// invocation pinned to an execution identity policy must use
// ClaimInvocationAuthenticated; this prevents an internal caller from bypassing
// the cryptographic boundary by invoking the older store method directly.
func (s *Store) ClaimInvocation(ctx context.Context, command modelruntime.ClaimCommand, config modelruntime.RuntimeConfig) (modelruntime.ClaimedInvocation, error) {
	rawToken, tokenHash, err := newClaimToken()
	if err != nil {
		return modelruntime.ClaimedInvocation{}, err
	}
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (modelruntime.ClaimedInvocation, error) {
		invocation, err := lockedClaimableInvocation(ctx, tx, command.InvocationID, command.ExecutionPrincipalID, config)
		if err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		if invocation.ExecutionIdentityPolicyVersionID != nil {
			return modelruntime.ClaimedInvocation{}, modelruntime.ErrExecutionIdentityDenied
		}
		return createClaimAttempt(ctx, tx, invocation, command.ClaimedBy, command.ExecutionPrincipalID, nil, nil, nil, tokenHash, rawToken, config)
	})
}

func (s *Store) ClaimInvocationAuthenticated(ctx context.Context, command modelruntime.AuthenticatedClaimCommand, config modelruntime.RuntimeConfig) (modelruntime.ClaimedInvocation, error) {
	if command.InvocationID <= 0 || command.ExecutionPrincipalID <= 0 || command.IdentityKeyID <= 0 || command.ChallengeID <= 0 || command.ClaimedBy == "" || command.ChallengeNonce == "" || len(command.Signature) != ed25519.SignatureSize {
		return modelruntime.ClaimedInvocation{}, modelidentity.ErrAssertionInvalid
	}
	rawToken, tokenHash, err := newClaimToken()
	if err != nil {
		return modelruntime.ClaimedInvocation{}, err
	}
	claimed, claimErr := withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (modelruntime.ClaimedInvocation, error) {
		invocation, err := lockedClaimableInvocation(ctx, tx, command.InvocationID, command.ExecutionPrincipalID, config)
		if err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		if invocation.ExecutionIdentityPolicyVersionID == nil || invocation.ExecutionIdentityPolicyHash == "" {
			return modelruntime.ClaimedInvocation{}, modelruntime.ErrExecutionIdentityUnpinned
		}

		var challenge modelidentity.Challenge
		var keyStatus, keyAlgorithm, policyStatus string
		var keyValidFrom, databaseNow time.Time
		var keyValidUntil *time.Time
		var publicKey []byte
		var clockSkewSeconds int
		err = tx.QueryRow(ctx, `
SELECT c.id,c.organization_id,c.organization_revision_id,c.invocation_id,c.dispatcher_assignment_id,
       c.execution_principal_id,c.execution_identity_policy_version_id,c.execution_identity_policy_hash,
       c.execution_identity_key_id,c.nonce_hash,c.payload_hash,c.action_digest,c.request_hash,c.issued_at,
       c.expires_at,c.consumed_at,c.invalidated_at,c.created_at,
       k.status,k.valid_from,k.valid_until,k.algorithm,k.public_key,
       p.status,p.clock_skew_seconds,clock_timestamp()
FROM model_execution_identity_challenges c
JOIN model_execution_identity_keys k
  ON k.id=c.execution_identity_key_id AND k.organization_id=c.organization_id
 AND k.execution_principal_id=c.execution_principal_id
JOIN model_execution_identity_policy_versions p
  ON p.id=c.execution_identity_policy_version_id AND p.organization_id=c.organization_id
 AND p.canonical_hash=c.execution_identity_policy_hash
WHERE c.id=$1
FOR UPDATE OF c,k`, command.ChallengeID).Scan(
			&challenge.ID, &challenge.OrganizationID, &challenge.OrganizationRevisionID, &challenge.InvocationID,
			&challenge.DispatcherAssignmentID, &challenge.ExecutionPrincipalID, &challenge.ExecutionIdentityPolicyVersionID,
			&challenge.ExecutionIdentityPolicyHash, &challenge.ExecutionIdentityKeyID, &challenge.NonceHash, &challenge.PayloadHash,
			&challenge.ActionDigest, &challenge.RequestHash, &challenge.IssuedAt, &challenge.ExpiresAt, &challenge.ConsumedAt,
			&challenge.InvalidatedAt, &challenge.CreatedAt,
			&keyStatus, &keyValidFrom, &keyValidUntil, &keyAlgorithm, &publicKey,
			&policyStatus, &clockSkewSeconds, &databaseNow)
		if err != nil {
			return modelruntime.ClaimedInvocation{}, mapError(err)
		}
		now := databaseNow.UTC()
		if challenge.ConsumedAt != nil || challenge.InvalidatedAt != nil {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrReplayDenied
		}
		if !now.Before(challenge.ExpiresAt) {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrChallengeExpired
		}
		if policyStatus != string(modelidentity.PolicyActive) && policyStatus != string(modelidentity.PolicySuperseded) {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrPolicyStale
		}
		if keyAlgorithm != string(modelidentity.AlgorithmEd25519) || len(publicKey) != ed25519.PublicKeySize {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrInvalidKey
		}
		if keyStatus != string(modelidentity.KeyActive) && keyStatus != string(modelidentity.KeyRetiring) {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrKeyInactive
		}
		if now.Before(keyValidFrom) || (keyValidUntil != nil && !now.Before(*keyValidUntil)) {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrKeyInactive
		}
		if challenge.InvocationID != invocation.ID || challenge.OrganizationID != invocation.OrganizationID ||
			challenge.OrganizationRevisionID != invocation.OrganizationRevisionID ||
			invocation.DispatcherAssignmentID == nil || challenge.DispatcherAssignmentID != *invocation.DispatcherAssignmentID ||
			challenge.ExecutionPrincipalID != command.ExecutionPrincipalID || challenge.ExecutionIdentityKeyID != command.IdentityKeyID ||
			challenge.ExecutionIdentityPolicyVersionID != *invocation.ExecutionIdentityPolicyVersionID ||
			challenge.ExecutionIdentityPolicyHash != invocation.ExecutionIdentityPolicyHash || challenge.RequestHash != invocation.RequestHash {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrAssertionInvalid
		}
		expectedActionDigest, digestErr := modelruntime.ActionDigest(invocation)
		if digestErr != nil || challenge.ActionDigest != expectedActionDigest {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrAssertionInvalid
		}
		if modelidentity.SHA256Bytes([]byte(command.ChallengeNonce)) != challenge.NonceHash {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrAssertionInvalid
		}
		scope := modelidentity.ChallengeScope{
			OrganizationID: challenge.OrganizationID, OrganizationRevisionID: challenge.OrganizationRevisionID,
			InvocationID: challenge.InvocationID, DispatcherAssignmentID: challenge.DispatcherAssignmentID,
			ExecutionPrincipalID:             challenge.ExecutionPrincipalID,
			ExecutionIdentityPolicyVersionID: challenge.ExecutionIdentityPolicyVersionID,
			ExecutionIdentityPolicyHash:      challenge.ExecutionIdentityPolicyHash,
			ExecutionIdentityKeyID:           challenge.ExecutionIdentityKeyID,
			ActionDigest:                     challenge.ActionDigest, RequestHash: challenge.RequestHash,
		}
		_, payload, payloadErr := modelidentity.BuildAssertionPayload(scope, challenge.ID, command.ChallengeNonce, challenge.IssuedAt, challenge.ExpiresAt)
		if payloadErr != nil || modelidentity.SHA256Bytes(payload) != challenge.PayloadHash ||
			!ed25519.Verify(ed25519.PublicKey(publicKey), payload, command.Signature) {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrAssertionInvalid
		}
		// Reject assertions whose verification instant falls outside the policy's
		// bounded clock-skew window even when the database clock is authoritative.
		clockSkew := time.Duration(clockSkewSeconds) * time.Second
		if now.Add(clockSkew).Before(challenge.IssuedAt) || !now.Add(-clockSkew).Before(challenge.ExpiresAt) {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrChallengeExpired
		}
		signatureHash := modelidentity.SHA256Bytes(command.Signature)
		assertionHash := modelidentity.AssertionHash(challenge.PayloadHash, signatureHash)

		claimed, err := createClaimAttempt(ctx, tx, invocation, command.ClaimedBy, command.ExecutionPrincipalID, nil, nil, nil, tokenHash, rawToken, config)
		if err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		var assertionID int64
		err = tx.QueryRow(ctx, `INSERT INTO model_execution_identity_assertions(organization_id,challenge_id,invocation_id,dispatch_attempt_id,execution_principal_id,execution_identity_key_id,payload_hash,signature_hash,assertion_hash,verification_effect,verification_reason_code,verified_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'allow','signature_valid',$10) RETURNING id`, invocation.OrganizationID, challenge.ID, invocation.ID, claimed.DispatchAttempt.ID, command.ExecutionPrincipalID, command.IdentityKeyID, challenge.PayloadHash, signatureHash, assertionHash, now).Scan(&assertionID)
		if err != nil {
			return modelruntime.ClaimedInvocation{}, mapError(err)
		}
		tag, err := tx.Exec(ctx, `UPDATE model_execution_identity_challenges SET consumed_at=clock_timestamp() WHERE id=$1 AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at>clock_timestamp()`, challenge.ID)
		if err != nil {
			return modelruntime.ClaimedInvocation{}, mapError(err)
		}
		if tag.RowsAffected() != 1 {
			return modelruntime.ClaimedInvocation{}, modelidentity.ErrReplayDenied
		}
		claimed.DispatchAttempt, err = scanAttempt(tx.QueryRow(ctx, `UPDATE model_dispatch_attempts SET execution_identity_key_id=$2,identity_assertion_id=$3,identity_verified_at=$4 WHERE id=$1 RETURNING `+attemptColumns, claimed.DispatchAttempt.ID, command.IdentityKeyID, assertionID, now))
		if err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		body, _ := json.Marshal(map[string]any{"challenge_id": challenge.ID, "invocation_id": invocation.ID, "dispatch_attempt_id": claimed.DispatchAttempt.ID, "execution_principal_id": command.ExecutionPrincipalID, "execution_identity_key_id": command.IdentityKeyID, "assertion_hash": assertionHash})
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload) VALUES($1,'service',$2,'model_execution_identity_assertion',$3,$4::jsonb)`, modelidentity.AuditIdentityVerified, command.ClaimedBy, fmt.Sprint(assertionID), body); err != nil {
			return modelruntime.ClaimedInvocation{}, mapError(err)
		}
		return claimed, nil
	})
	if claimErr != nil && isIdentityClaimDenial(claimErr) {
		eventType := modelidentity.AuditIdentityDenied
		if errors.Is(claimErr, modelidentity.ErrReplayDenied) || errors.Is(claimErr, modelidentity.ErrChallengeConsumed) {
			eventType = modelidentity.AuditIdentityReplayDeny
		}
		payload, _ := json.Marshal(map[string]any{
			"invocation_id": command.InvocationID, "challenge_id": command.ChallengeID,
			"execution_principal_id":    command.ExecutionPrincipalID,
			"execution_identity_key_id": command.IdentityKeyID,
			"reason_code":               identityClaimReasonCode(claimErr),
		})
		if _, auditErr := s.pool.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload) VALUES($1,'service',$2,'model_invocation',$3,$4::jsonb)`, eventType, command.ClaimedBy, fmt.Sprint(command.InvocationID), payload); auditErr != nil {
			return claimed, errors.Join(claimErr, mapError(auditErr))
		}
	}
	return claimed, claimErr
}

func isIdentityClaimDenial(err error) bool {
	return errors.Is(err, modelidentity.ErrAssertionInvalid) || errors.Is(err, modelidentity.ErrReplayDenied) ||
		errors.Is(err, modelidentity.ErrChallengeConsumed) || errors.Is(err, modelidentity.ErrChallengeExpired) ||
		errors.Is(err, modelidentity.ErrKeyInactive) || errors.Is(err, modelidentity.ErrInvalidKey) ||
		errors.Is(err, modelidentity.ErrPolicyStale) || errors.Is(err, modelruntime.ErrExecutionIdentityUnpinned) ||
		errors.Is(err, modelruntime.ErrExecutionIdentityDenied)
}

func identityClaimReasonCode(err error) string {
	switch {
	case errors.Is(err, modelidentity.ErrReplayDenied), errors.Is(err, modelidentity.ErrChallengeConsumed):
		return "replay_denied"
	case errors.Is(err, modelidentity.ErrChallengeExpired):
		return "challenge_expired"
	case errors.Is(err, modelidentity.ErrKeyInactive):
		return "key_inactive"
	case errors.Is(err, modelidentity.ErrPolicyStale):
		return "policy_stale"
	case errors.Is(err, modelruntime.ErrExecutionIdentityUnpinned):
		return "identity_unpinned"
	default:
		return "assertion_invalid"
	}
}

func lockedClaimableInvocation(ctx context.Context, tx pgx.Tx, invocationID, principalID int64, config modelruntime.RuntimeConfig) (modelruntime.Invocation, error) {
	if err := lockInvocation(ctx, tx, invocationID); err != nil {
		return modelruntime.Invocation{}, err
	}
	invocation, err := scanInvocation(tx.QueryRow(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE id=$1 FOR UPDATE`, invocationID))
	if err != nil {
		return invocation, err
	}
	if invocation.Status != modelruntime.InvocationRequested {
		return invocation, fmt.Errorf("%w: invocation status is %s", modelruntime.ErrClaimUnavailable, invocation.Status)
	}
	if invocation.ExecutionPrincipalID != nil && *invocation.ExecutionPrincipalID != principalID {
		return invocation, modelruntime.ErrExecutionPrincipalMismatch
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `model-runtime:`+invocation.OrganizationID); err != nil {
		return invocation, mapError(err)
	}
	var active int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM model_invocations i WHERE i.organization_id=$1 AND (i.status IN ('send_started','response_received') OR (i.status='claimed' AND EXISTS (SELECT 1 FROM model_dispatch_attempts a WHERE a.invocation_id=i.id AND a.status='claimed' AND a.claim_expires_at>clock_timestamp())))`, invocation.OrganizationID).Scan(&active); err != nil {
		return invocation, mapError(err)
	}
	if active >= config.GlobalConcurrency {
		return invocation, modelruntime.ErrConcurrencyLimit
	}
	return invocation, nil
}

func createClaimAttempt(ctx context.Context, tx pgx.Tx, invocation modelruntime.Invocation, claimedBy string, principalID int64, keyID, assertionID *int64, verifiedAt *time.Time, tokenHash, rawToken string, config modelruntime.RuntimeConfig) (modelruntime.ClaimedInvocation, error) {
	var attemptNumber int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(attempt_number),0)+1 FROM model_dispatch_attempts WHERE invocation_id=$1`, invocation.ID).Scan(&attemptNumber); err != nil {
		return modelruntime.ClaimedInvocation{}, mapError(err)
	}
	var attemptPrincipalID *int64
	if invocation.ExecutionPrincipalID != nil {
		attemptPrincipalID = &principalID
	}
	attempt, err := scanAttempt(tx.QueryRow(ctx, `INSERT INTO model_dispatch_attempts(invocation_id,attempt_number,status,claim_token_hash,claimed_by,execution_principal_id,execution_identity_key_id,identity_assertion_id,identity_verified_at,claim_expires_at,retry_safety) VALUES($1,$2,'claimed',$3,$4,$5,$6,$7,$8,clock_timestamp()+make_interval(secs=>$9::double precision),'safe_before_send') RETURNING `+attemptColumns, invocation.ID, attemptNumber, tokenHash, claimedBy, attemptPrincipalID, keyID, assertionID, verifiedAt, config.ClaimTTL.Seconds()))
	if err != nil {
		return modelruntime.ClaimedInvocation{}, err
	}
	invocation, err = scanInvocation(tx.QueryRow(ctx, `UPDATE model_invocations SET status='claimed',updated_at=clock_timestamp() WHERE id=$1 AND status='requested' RETURNING `+invocationColumns, invocation.ID))
	if err != nil {
		return modelruntime.ClaimedInvocation{}, err
	}
	if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationClaimed, "service", claimedBy, false, config.OutboxMaxAttempts, map[string]any{"dispatch_attempt_id": attempt.ID, "claim_expires_at": attempt.ClaimExpiresAt, "execution_identity_key_id": keyID}); err != nil {
		return modelruntime.ClaimedInvocation{}, err
	}
	return modelruntime.ClaimedInvocation{Invocation: invocation, DispatchAttempt: attempt, ClaimToken: rawToken}, nil
}

func verifyToken(ctx context.Context, tx pgx.Tx, attemptID int64, token string) (modelruntime.DispatchAttempt, error) {
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	attempt, err := scanAttempt(tx.QueryRow(ctx, `SELECT `+attemptColumns+` FROM model_dispatch_attempts WHERE id=$1 AND claim_token_hash=$2 FOR UPDATE`, attemptID, hash))
	if err != nil {
		if err == modelruntime.ErrNotFound {
			return attempt, modelruntime.ErrClaimTokenMismatch
		}
		return attempt, err
	}
	return attempt, nil
}
