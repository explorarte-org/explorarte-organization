package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

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

func (s *Store) ClaimInvocation(ctx context.Context, command modelruntime.ClaimCommand, config modelruntime.RuntimeConfig) (modelruntime.ClaimedInvocation, error) {
	rawToken, tokenHash, err := newClaimToken()
	if err != nil {
		return modelruntime.ClaimedInvocation{}, err
	}
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (modelruntime.ClaimedInvocation, error) {
		if err := lockInvocation(ctx, tx, command.InvocationID); err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE id=$1 FOR UPDATE`, command.InvocationID))
		if err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		if invocation.Status != modelruntime.InvocationRequested {
			return modelruntime.ClaimedInvocation{}, fmt.Errorf("%w: invocation status is %s", modelruntime.ErrClaimUnavailable, invocation.Status)
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `model-runtime:`+invocation.OrganizationID); err != nil {
			return modelruntime.ClaimedInvocation{}, mapError(err)
		}
		var active int
		if err = tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM model_invocations i
WHERE i.organization_id=$1
  AND (
      i.status IN ('send_started','response_received')
      OR (i.status='claimed' AND EXISTS (
          SELECT 1 FROM model_dispatch_attempts a
          WHERE a.invocation_id=i.id
            AND a.status='claimed'
            AND a.claim_expires_at > clock_timestamp()
      ))
  )`, invocation.OrganizationID).Scan(&active); err != nil {
			return modelruntime.ClaimedInvocation{}, mapError(err)
		}
		if active >= config.GlobalConcurrency {
			return modelruntime.ClaimedInvocation{}, modelruntime.ErrConcurrencyLimit
		}
		var attemptNumber int
		if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(attempt_number),0)+1 FROM model_dispatch_attempts WHERE invocation_id=$1`, invocation.ID).Scan(&attemptNumber); err != nil {
			return modelruntime.ClaimedInvocation{}, mapError(err)
		}
		attempt, err := scanAttempt(tx.QueryRow(ctx, `
INSERT INTO model_dispatch_attempts(
    invocation_id,attempt_number,status,claim_token_hash,claimed_by,
    claim_expires_at,retry_safety
) VALUES(
    $1,$2,'claimed',$3,$4,
    clock_timestamp() + make_interval(secs => $5::double precision),
    'safe_before_send'
)
RETURNING `+attemptColumns,
			invocation.ID,
			attemptNumber,
			tokenHash,
			command.ClaimedBy,
			config.ClaimTTL.Seconds(),
		))
		if err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		invocation, err = scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='claimed',updated_at=clock_timestamp()
WHERE id=$1 AND status='requested'
RETURNING `+invocationColumns, invocation.ID))
		if err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationClaimed, "service", command.ClaimedBy, false, config.OutboxMaxAttempts, map[string]any{
			"dispatch_attempt_id": attempt.ID,
			"claim_expires_at":    attempt.ClaimExpiresAt,
		}); err != nil {
			return modelruntime.ClaimedInvocation{}, err
		}
		return modelruntime.ClaimedInvocation{
			Invocation:      invocation,
			DispatchAttempt: attempt,
			ClaimToken:      rawToken,
		}, nil
	})
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

func (s *Store) MarkSendStarted(ctx context.Context, invocationID, attemptID int64, token, providerKeyHash string) (modelruntime.Invocation, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.Invocation, error) {
		if err := lockInvocation(ctx, tx, invocationID); err != nil {
			return modelruntime.Invocation{}, err
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE id=$1 FOR UPDATE`, invocationID))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if invocation.Status != modelruntime.InvocationClaimed || invocation.CancelRequestedAt != nil {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		attempt, err := verifyToken(ctx, tx, attemptID, token)
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if attempt.InvocationID != invocationID || attempt.Status != modelruntime.DispatchClaimed {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		var claimActive bool
		if err = tx.QueryRow(ctx, `SELECT claim_expires_at > clock_timestamp() FROM model_dispatch_attempts WHERE id=$1`, attempt.ID).Scan(&claimActive); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		if !claimActive {
			return modelruntime.Invocation{}, modelruntime.ErrClaimUnavailable
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='send_started',
    send_started_at=clock_timestamp(),
    retry_safety='unsafe_after_send',
    provider_idempotency_key_hash=$2,
    claim_expires_at=GREATEST(claim_expires_at,$3)
WHERE id=$1`, attemptID, providerKeyHash, invocation.Deadline); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		invocation, err = scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='send_started',updated_at=clock_timestamp()
WHERE id=$1 AND status='claimed' AND cancel_requested_at IS NULL
RETURNING `+invocationColumns, invocationID))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationDispatched, "service", attempt.ClaimedBy, false, 1, map[string]any{
			"dispatch_attempt_id": attemptID,
		}); err != nil {
			return modelruntime.Invocation{}, err
		}
		return invocation, nil
	})
}
