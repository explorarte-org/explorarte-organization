package postgres

import (
	"errors"

	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	"github.com/jackc/pgx/v5"
)

type scanner interface{ Scan(...any) error }

const policyColumns = `id,organization_id,policy_id,policy_version,canonical_hash,algorithm,challenge_ttl_seconds,clock_skew_seconds,status,created_at,superseded_at`
const keyColumns = `id,organization_id,execution_principal_id,key_version,algorithm,public_key,public_key_fingerprint,secret_ref,status,idempotency_key,request_hash,created_by_role_id,valid_from,valid_until,created_at,updated_at,retired_at,revoked_at,revoked_by_role_id,revocation_reason_code`
const challengeColumns = `id,organization_id,organization_revision_id,invocation_id,dispatcher_assignment_id,execution_principal_id,execution_identity_policy_version_id,execution_identity_policy_hash,execution_identity_key_id,nonce_hash,payload_hash,action_digest,request_hash,issued_at,expires_at,consumed_at,invalidated_at,created_at`

func scanPolicy(row scanner) (modelidentity.PolicyVersion, error) {
	var v modelidentity.PolicyVersion
	err := row.Scan(&v.ID, &v.OrganizationID, &v.PolicyID, &v.PolicyVersion, &v.CanonicalHash, &v.Algorithm, &v.ChallengeTTLSeconds, &v.ClockSkewSeconds, &v.Status, &v.CreatedAt, &v.SupersededAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v, modelidentity.ErrPolicyNotFound
		}
		return v, mapError(err)
	}
	return v, nil
}

func scanKey(row scanner) (modelidentity.ExecutionIdentityKey, error) {
	var v modelidentity.ExecutionIdentityKey
	var revokedBy, reason *string
	err := row.Scan(&v.ID, &v.OrganizationID, &v.ExecutionPrincipalID, &v.KeyVersion, &v.Algorithm, &v.PublicKey, &v.PublicKeyFingerprint, &v.SecretRef, &v.Status, &v.IdempotencyKey, &v.RequestHash, &v.CreatedByRoleID, &v.ValidFrom, &v.ValidUntil, &v.CreatedAt, &v.UpdatedAt, &v.RetiredAt, &v.RevokedAt, &revokedBy, &reason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v, modelidentity.ErrKeyNotFound
		}
		return v, mapError(err)
	}
	if revokedBy != nil {
		v.RevokedByRoleID = *revokedBy
	}
	if reason != nil {
		v.RevocationReasonCode = *reason
	}
	return v, nil
}

func scanChallenge(row scanner) (modelidentity.Challenge, error) {
	var v modelidentity.Challenge
	err := row.Scan(&v.ID, &v.OrganizationID, &v.OrganizationRevisionID, &v.InvocationID, &v.DispatcherAssignmentID, &v.ExecutionPrincipalID, &v.ExecutionIdentityPolicyVersionID, &v.ExecutionIdentityPolicyHash, &v.ExecutionIdentityKeyID, &v.NonceHash, &v.PayloadHash, &v.ActionDigest, &v.RequestHash, &v.IssuedAt, &v.ExpiresAt, &v.ConsumedAt, &v.InvalidatedAt, &v.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v, modelidentity.ErrChallengeNotFound
		}
		return v, mapError(err)
	}
	return v, nil
}
