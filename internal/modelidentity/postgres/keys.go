package postgres

import (
	"context"
	"errors"

	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	"github.com/jackc/pgx/v5"
)

func insertKeyAudit(ctx context.Context, tx pgx.Tx, event string, key modelidentity.ExecutionIdentityKey, actor string, extra map[string]any) error {
	payload := map[string]any{"key_id": key.ID, "execution_principal_id": key.ExecutionPrincipalID, "key_version": key.KeyVersion, "algorithm": key.Algorithm, "public_key_fingerprint": key.PublicKeyFingerprint, "status": key.Status}
	for k, v := range extra {
		payload[k] = v
	}
	return insertAudit(ctx, tx, event, "role", actor, "model_execution_identity_key", subjectID(key.ID), payload)
}

func (s *Store) RegisterKey(ctx context.Context, p modelidentity.PreparedKey) (modelidentity.RegisterKeyResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (modelidentity.RegisterKeyResult, error) {
		row := tx.QueryRow(ctx, `INSERT INTO model_execution_identity_keys(organization_id,execution_principal_id,key_version,algorithm,public_key,public_key_fingerprint,secret_ref,status,idempotency_key,request_hash,created_by_role_id,valid_until) SELECT $1,$2,1,'ed25519',$3,$4,$5,'active',$6,$7,$8,$9 WHERE NOT EXISTS (SELECT 1 FROM model_execution_identity_keys WHERE organization_id=$1 AND execution_principal_id=$2 AND status='active') ON CONFLICT(organization_id,idempotency_key) DO NOTHING RETURNING `+keyColumns, p.OrganizationID, p.ExecutionPrincipalID, p.PublicKey, p.PublicKeyFingerprint, p.SecretRef, p.IdempotencyKey, p.RequestHash, p.CreatedByRoleID, p.ValidUntil)
		key, err := scanKey(row)
		if err == nil {
			if err = insertKeyAudit(ctx, tx, modelidentity.AuditKeyRegistered, key, p.CreatedByRoleID, nil); err != nil {
				return modelidentity.RegisterKeyResult{}, err
			}
			return modelidentity.RegisterKeyResult{Key: key}, nil
		}
		if !errors.Is(err, modelidentity.ErrKeyNotFound) {
			return modelidentity.RegisterKeyResult{}, err
		}
		key, err = scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM model_execution_identity_keys WHERE organization_id=$1 AND idempotency_key=$2 FOR UPDATE`, p.OrganizationID, p.IdempotencyKey))
		if errors.Is(err, modelidentity.ErrKeyNotFound) {
			return modelidentity.RegisterKeyResult{}, modelidentity.ErrKeyConflict
		}
		if err != nil {
			return modelidentity.RegisterKeyResult{}, err
		}
		if key.RequestHash != p.RequestHash {
			return modelidentity.RegisterKeyResult{}, modelidentity.ErrKeyConflict
		}
		if err = insertKeyAudit(ctx, tx, modelidentity.AuditKeyReused, key, p.CreatedByRoleID, nil); err != nil {
			return modelidentity.RegisterKeyResult{}, err
		}
		return modelidentity.RegisterKeyResult{Key: key, Reused: true}, nil
	})
}

func (s *Store) RotateKey(ctx context.Context, p modelidentity.PreparedKey) (modelidentity.RegisterKeyResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (modelidentity.RegisterKeyResult, error) {
		// Resolve idempotency before requiring a currently active key. An exact
		// retry must remain reusable even if the previously rotated key was
		// subsequently revoked or retired.
		reused, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM model_execution_identity_keys WHERE organization_id=$1 AND idempotency_key=$2 FOR UPDATE`, p.OrganizationID, p.IdempotencyKey))
		if err == nil {
			if reused.RequestHash != p.RequestHash {
				return modelidentity.RegisterKeyResult{}, modelidentity.ErrKeyConflict
			}
			if err = insertKeyAudit(ctx, tx, modelidentity.AuditKeyReused, reused, p.CreatedByRoleID, nil); err != nil {
				return modelidentity.RegisterKeyResult{}, err
			}
			return modelidentity.RegisterKeyResult{Key: reused, Reused: true}, nil
		}
		if !errors.Is(err, modelidentity.ErrKeyNotFound) {
			return modelidentity.RegisterKeyResult{}, err
		}
		existing, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM model_execution_identity_keys WHERE organization_id=$1 AND execution_principal_id=$2 AND status='active' FOR UPDATE`, p.OrganizationID, p.ExecutionPrincipalID))
		if err != nil {
			return modelidentity.RegisterKeyResult{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE model_execution_identity_keys SET status='retiring',updated_at=clock_timestamp() WHERE id=$1 AND status='active'`, existing.ID); err != nil {
			return modelidentity.RegisterKeyResult{}, mapError(err)
		}
		key, err := scanKey(tx.QueryRow(ctx, `INSERT INTO model_execution_identity_keys(organization_id,execution_principal_id,key_version,algorithm,public_key,public_key_fingerprint,secret_ref,status,idempotency_key,request_hash,created_by_role_id,valid_until) VALUES($1,$2,$3,'ed25519',$4,$5,$6,'active',$7,$8,$9,$10) RETURNING `+keyColumns, p.OrganizationID, p.ExecutionPrincipalID, existing.KeyVersion+1, p.PublicKey, p.PublicKeyFingerprint, p.SecretRef, p.IdempotencyKey, p.RequestHash, p.CreatedByRoleID, p.ValidUntil))
		if err != nil {
			return modelidentity.RegisterKeyResult{}, err
		}
		if err = insertKeyAudit(ctx, tx, modelidentity.AuditKeyRotated, key, p.CreatedByRoleID, map[string]any{"previous_key_id": existing.ID}); err != nil {
			return modelidentity.RegisterKeyResult{}, err
		}
		return modelidentity.RegisterKeyResult{Key: key}, nil
	})
}

func (s *Store) GetKey(ctx context.Context, id int64) (modelidentity.ExecutionIdentityKey, error) {
	return scanKey(s.pool.QueryRow(ctx, `SELECT `+keyColumns+` FROM model_execution_identity_keys WHERE id=$1`, id))
}

func (s *Store) ListKeys(ctx context.Context, organizationID string, principalID int64, limit int) ([]modelidentity.ExecutionIdentityKey, error) {
	query := `SELECT ` + keyColumns + ` FROM model_execution_identity_keys WHERE organization_id=$1`
	args := []any{organizationID}
	if principalID > 0 {
		query += ` AND execution_principal_id=$2 ORDER BY id DESC LIMIT $3`
		args = append(args, principalID, limit)
	} else {
		query += ` ORDER BY id DESC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var result []modelidentity.ExecutionIdentityKey
	for rows.Next() {
		v, e := scanKey(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, v)
	}
	return result, mapError(rows.Err())
}

func (s *Store) RetireKey(ctx context.Context, id int64, actor string) (modelidentity.ExecutionIdentityKey, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelidentity.ExecutionIdentityKey, error) {
		key, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM model_execution_identity_keys WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return key, err
		}
		if key.Status == modelidentity.KeyRetired {
			return key, nil
		}
		if key.Status != modelidentity.KeyRetiring {
			return key, modelidentity.ErrKeyInactive
		}
		key, err = scanKey(tx.QueryRow(ctx, `UPDATE model_execution_identity_keys SET status='retired',retired_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status='retiring' RETURNING `+keyColumns, id))
		if err != nil {
			return key, err
		}
		if err = insertKeyAudit(ctx, tx, modelidentity.AuditKeyRetired, key, actor, nil); err != nil {
			return key, err
		}
		return key, nil
	})
}

func (s *Store) RevokeKey(ctx context.Context, id int64, actor, reason string) (modelidentity.ExecutionIdentityKey, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelidentity.ExecutionIdentityKey, error) {
		key, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyColumns+` FROM model_execution_identity_keys WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return key, err
		}
		if key.Status == modelidentity.KeyRevoked {
			return key, nil
		}
		if key.Status == modelidentity.KeyRetired {
			return key, modelidentity.ErrKeyInactive
		}
		key, err = scanKey(tx.QueryRow(ctx, `UPDATE model_execution_identity_keys SET status='revoked',revoked_at=clock_timestamp(),revoked_by_role_id=$2,revocation_reason_code=$3,updated_at=clock_timestamp() WHERE id=$1 AND status IN ('active','retiring') RETURNING `+keyColumns, id, actor, reason))
		if err != nil {
			return key, err
		}
		if err = insertKeyAudit(ctx, tx, modelidentity.AuditKeyRevoked, key, actor, map[string]any{"reason_code": reason}); err != nil {
			return key, err
		}
		return key, nil
	})
}

func (s *Store) ResolveActiveKeyByFingerprint(ctx context.Context, organizationID string, principalID int64, fingerprint string) (modelidentity.ExecutionIdentityKey, error) {
	key, err := scanKey(s.pool.QueryRow(ctx, `SELECT `+keyColumns+` FROM model_execution_identity_keys WHERE organization_id=$1 AND execution_principal_id=$2 AND public_key_fingerprint=$3 AND status='active' AND valid_from<=clock_timestamp() AND (valid_until IS NULL OR valid_until>clock_timestamp())`, organizationID, principalID, fingerprint))
	if err != nil {
		return key, err
	}
	return key, nil
}
