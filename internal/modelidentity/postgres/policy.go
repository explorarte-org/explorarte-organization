package postgres

import (
	"context"
	"errors"

	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Status(ctx context.Context, organizationID string, policy modelidentity.CanonicalPolicy) (modelidentity.RegistryStatus, error) {
	status := modelidentity.RegistryStatus{OrganizationID: organizationID, PolicyID: policy.PolicyID, PolicyVersion: policy.PolicyVersion, CanonicalHash: policy.CanonicalHash}
	version, err := scanPolicy(s.pool.QueryRow(ctx, `SELECT `+policyColumns+` FROM model_execution_identity_policy_versions WHERE organization_id=$1 AND status='active'`, organizationID))
	if errors.Is(err, modelidentity.ErrPolicyNotFound) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.MaterializedHash = version.CanonicalHash
	status.PolicyVersionID = version.ID
	status.Synchronized = version.PolicyID == policy.PolicyID && version.PolicyVersion == policy.PolicyVersion && version.CanonicalHash == policy.CanonicalHash && version.Algorithm == policy.Algorithm && version.ChallengeTTLSeconds == policy.ChallengeTTLSeconds && version.ClockSkewSeconds == policy.ClockSkewSeconds
	return status, nil
}

func (s *Store) Apply(ctx context.Context, organizationID string, policy modelidentity.CanonicalPolicy) (modelidentity.RegistrySyncResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (modelidentity.RegistrySyncResult, error) {
		var currentID int64
		var currentHash, currentPolicyID string
		var currentPolicyVersion int
		err := tx.QueryRow(ctx, `SELECT id,canonical_hash,policy_id,policy_version FROM model_execution_identity_policy_versions WHERE organization_id=$1 AND status='active' FOR UPDATE`, organizationID).Scan(&currentID, &currentHash, &currentPolicyID, &currentPolicyVersion)
		if err == nil && currentHash == policy.CanonicalHash {
			return modelidentity.RegistrySyncResult{NoOp: true, PolicyVersionID: currentID, PolicyID: policy.PolicyID, PolicyVersion: policy.PolicyVersion, CanonicalHash: policy.CanonicalHash}, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return modelidentity.RegistrySyncResult{}, mapError(err)
		}
		if err == nil && (currentPolicyID != policy.PolicyID || policy.PolicyVersion <= currentPolicyVersion) {
			return modelidentity.RegistrySyncResult{}, modelidentity.ErrPolicyConflict
		}
		var duplicate int
		if queryErr := tx.QueryRow(ctx, `SELECT COUNT(*) FROM model_execution_identity_policy_versions WHERE organization_id=$1 AND (canonical_hash=$2 OR (policy_id=$3 AND policy_version=$4))`, organizationID, policy.CanonicalHash, policy.PolicyID, policy.PolicyVersion).Scan(&duplicate); queryErr != nil {
			return modelidentity.RegistrySyncResult{}, mapError(queryErr)
		}
		if duplicate > 0 {
			return modelidentity.RegistrySyncResult{}, modelidentity.ErrPolicyConflict
		}
		if err == nil {
			if _, err = tx.Exec(ctx, `UPDATE model_execution_identity_policy_versions SET status='superseded',superseded_at=clock_timestamp() WHERE id=$1 AND status='active'`, currentID); err != nil {
				return modelidentity.RegistrySyncResult{}, mapError(err)
			}
		}
		version, err := scanPolicy(tx.QueryRow(ctx, `INSERT INTO model_execution_identity_policy_versions(organization_id,policy_id,policy_version,canonical_hash,algorithm,challenge_ttl_seconds,clock_skew_seconds,status) VALUES($1,$2,$3,$4,$5,$6,$7,'active') RETURNING `+policyColumns, organizationID, policy.PolicyID, policy.PolicyVersion, policy.CanonicalHash, policy.Algorithm, policy.ChallengeTTLSeconds, policy.ClockSkewSeconds))
		if err != nil {
			if errors.Is(err, modelidentity.ErrKeyConflict) {
				return modelidentity.RegistrySyncResult{}, modelidentity.ErrPolicyConflict
			}
			return modelidentity.RegistrySyncResult{}, err
		}
		if err = insertAudit(ctx, tx, modelidentity.AuditPolicySynced, "system", "orgctl", "model_execution_identity_policy", organizationID, map[string]any{"policy_version_id": version.ID, "policy_id": version.PolicyID, "policy_version": version.PolicyVersion, "canonical_hash": version.CanonicalHash}); err != nil {
			return modelidentity.RegistrySyncResult{}, err
		}
		return modelidentity.RegistrySyncResult{Applied: true, PolicyVersionID: version.ID, PolicyID: version.PolicyID, PolicyVersion: version.PolicyVersion, CanonicalHash: version.CanonicalHash}, nil
	})
}

func (s *Store) ResolveActive(ctx context.Context, organizationID string) (modelidentity.ResolvedPolicy, error) {
	version, err := scanPolicy(s.pool.QueryRow(ctx, `SELECT `+policyColumns+` FROM model_execution_identity_policy_versions WHERE organization_id=$1 AND status='active'`, organizationID))
	if err != nil {
		return modelidentity.ResolvedPolicy{}, err
	}
	return modelidentity.ResolvedPolicy{Version: version}, nil
}

func (s *Store) ResolveByID(ctx context.Context, organizationID string, id int64) (modelidentity.ResolvedPolicy, error) {
	version, err := scanPolicy(s.pool.QueryRow(ctx, `SELECT `+policyColumns+` FROM model_execution_identity_policy_versions WHERE id=$1 AND organization_id=$2`, id, organizationID))
	if err != nil {
		return modelidentity.ResolvedPolicy{}, err
	}
	return modelidentity.ResolvedPolicy{Version: version}, nil
}
