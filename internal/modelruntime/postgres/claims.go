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
		// A pinned invocation may only be claimed by the principal it was pinned
		// to at creation time. A legacy invocation (both nil) is claimable by any
		// active principal; the dispatcher fails it before send immediately after.
		if invocation.ExecutionPrincipalID != nil && *invocation.ExecutionPrincipalID != command.ExecutionPrincipalID {
			return modelruntime.ClaimedInvocation{}, modelruntime.ErrExecutionPrincipalMismatch
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
		// Legacy (unpinned) invocations leave the attempt's principal NULL too: the
		// FK to model_invocations(id, execution_principal_id) would otherwise
		// never match a row whose own execution_principal_id is NULL.
		var attemptPrincipalID *int64
		if invocation.ExecutionPrincipalID != nil {
			attemptPrincipalID = &command.ExecutionPrincipalID
		}
		attempt, err := scanAttempt(tx.QueryRow(ctx, `
INSERT INTO model_dispatch_attempts(
    invocation_id,attempt_number,status,claim_token_hash,claimed_by,
    execution_principal_id,claim_expires_at,retry_safety
) VALUES(
    $1,$2,'claimed',$3,$4,
    $5,
    clock_timestamp() + make_interval(secs => $6::double precision),
    'safe_before_send'
)
RETURNING `+attemptColumns,
			invocation.ID,
			attemptNumber,
			tokenHash,
			command.ClaimedBy,
			attemptPrincipalID,
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
