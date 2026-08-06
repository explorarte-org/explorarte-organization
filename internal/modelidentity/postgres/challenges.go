package postgres

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateChallenge(ctx context.Context, p modelidentity.PreparedChallenge) (modelidentity.Challenge, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (modelidentity.Challenge, error) {
		// Invalidate prior unconsumed challenges for this invocation. This keeps the
		// active proof surface single-use even when a caller retries before expiry.
		if _, err := tx.Exec(ctx, `UPDATE model_execution_identity_challenges SET invalidated_at=clock_timestamp() WHERE invocation_id=$1 AND consumed_at IS NULL AND invalidated_at IS NULL`, p.Scope.InvocationID); err != nil {
			return modelidentity.Challenge{}, mapError(err)
		}
		var id int64
		if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('model_execution_identity_challenges','id'))`).Scan(&id); err != nil {
			return modelidentity.Challenge{}, mapError(err)
		}
		_, payload, err := modelidentity.BuildAssertionPayload(p.Scope, id, p.Nonce, p.IssuedAt, p.ExpiresAt)
		if err != nil {
			return modelidentity.Challenge{}, err
		}
		payloadHash := modelidentity.SHA256Bytes(payload)
		challenge, err := scanChallenge(tx.QueryRow(ctx, `INSERT INTO model_execution_identity_challenges(id,organization_id,organization_revision_id,invocation_id,dispatcher_assignment_id,execution_principal_id,execution_identity_policy_version_id,execution_identity_policy_hash,execution_identity_key_id,nonce_hash,payload_hash,action_digest,request_hash,issued_at,expires_at) OVERRIDING SYSTEM VALUE VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING `+challengeColumns, id, p.Scope.OrganizationID, p.Scope.OrganizationRevisionID, p.Scope.InvocationID, p.Scope.DispatcherAssignmentID, p.Scope.ExecutionPrincipalID, p.Scope.ExecutionIdentityPolicyVersionID, p.Scope.ExecutionIdentityPolicyHash, p.Scope.ExecutionIdentityKeyID, p.NonceHash, payloadHash, p.Scope.ActionDigest, p.Scope.RequestHash, p.IssuedAt, p.ExpiresAt))
		if err != nil {
			return modelidentity.Challenge{}, err
		}
		if err = insertAudit(ctx, tx, modelidentity.AuditChallengeCreated, "service", "model-runtime", "model_execution_identity_challenge", subjectID(challenge.ID), map[string]any{"challenge_id": challenge.ID, "invocation_id": challenge.InvocationID, "dispatcher_assignment_id": challenge.DispatcherAssignmentID, "execution_principal_id": challenge.ExecutionPrincipalID, "execution_identity_key_id": challenge.ExecutionIdentityKeyID, "payload_hash": challenge.PayloadHash, "expires_at": challenge.ExpiresAt}); err != nil {
			return modelidentity.Challenge{}, err
		}
		return challenge, nil
	})
}

func (s *Store) GetChallenge(ctx context.Context, id int64) (modelidentity.Challenge, error) {
	return scanChallenge(s.pool.QueryRow(ctx, `SELECT `+challengeColumns+` FROM model_execution_identity_challenges WHERE id=$1`, id))
}
