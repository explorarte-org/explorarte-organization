package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateEmbeddingInvocation(ctx context.Context, invocation costledger.EmbeddingInvocation) (costledger.EmbeddingInvocation, error) {
	if strings.TrimSpace(invocation.OrganizationID) == "" || strings.TrimSpace(invocation.ActorRoleID) == "" ||
		strings.TrimSpace(invocation.ProviderID) == "" || strings.TrimSpace(invocation.ProviderModelID) == "" ||
		!invocation.BillingMode.Valid() || !invocation.Operation.Valid() {
		return costledger.EmbeddingInvocation{}, fmt.Errorf("%w: invalid embedding invocation", costledger.ErrInvalidRequest)
	}
	now := invocation.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var taskID *int64
	if invocation.TaskID != nil {
		taskID = invocation.TaskID
	}
	err := s.pool.QueryRow(ctx, `
INSERT INTO embedding_invocations (organization_id, actor_role_id, task_id, provider_id, provider_model_id, billing_mode, operation, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING id`,
		invocation.OrganizationID, invocation.ActorRoleID, taskID, invocation.ProviderID, invocation.ProviderModelID,
		string(invocation.BillingMode), string(invocation.Operation), now).Scan(&invocation.ID)
	if err != nil {
		return costledger.EmbeddingInvocation{}, err
	}
	invocation.CreatedAt = now
	return invocation, nil
}

// ReserveEmbedding mirrors Store.Reserve exactly (same idempotency,
// insufficient-balance semantics) but writes embedding_invocation_id
// instead of invocation_id — see migration 000030 for why the two are
// separate, mutually exclusive columns on the same provider_wallet_events
// table rather than a shared column.
func (s *Store) ReserveEmbedding(ctx context.Context, providerID string, embeddingInvocationID int64, estimatedUSD modelpricing.USDNanos, now time.Time) error {
	if estimatedUSD < 0 {
		return fmt.Errorf("%w: estimated cost must be non-negative", costledger.ErrInvalidRequest)
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reserve embedding: %w", err)
	}
	defer tx.Rollback(ctx)

	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_usd_nanos, reserved_usd_nanos FROM provider_wallets WHERE provider_id=$1 FOR UPDATE`, providerID).Scan(&balance, &reserved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrWalletNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, embedding_invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'reserved',$3,$4)
ON CONFLICT (provider_id, embedding_invocation_id, kind) WHERE embedding_invocation_id IS NOT NULL DO NOTHING`,
		providerID, embeddingInvocationID, int64(estimatedUSD), now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var existingAmount int64
		if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND embedding_invocation_id=$2 AND kind='reserved'`, providerID, embeddingInvocationID).Scan(&existingAmount); err != nil {
			return err
		}
		if existingAmount != int64(estimatedUSD) {
			return fmt.Errorf("%w: reserved %d, retried with %d", costledger.ErrAmountMismatch, existingAmount, int64(estimatedUSD))
		}
		return tx.Commit(ctx)
	}

	if balance-reserved < int64(estimatedUSD) {
		return costledger.ErrInsufficientBalance
	}
	if _, err := tx.Exec(ctx, `UPDATE provider_wallets SET reserved_usd_nanos = reserved_usd_nanos + $1, version = version + 1, updated_at=$2 WHERE provider_id=$3`, int64(estimatedUSD), now, providerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ReconcileEmbedding(ctx context.Context, providerID string, embeddingInvocationID int64, actualUSD modelpricing.USDNanos, now time.Time) error {
	if actualUSD < 0 {
		return fmt.Errorf("%w: actual cost must be non-negative", costledger.ErrInvalidRequest)
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reconcile embedding: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, _, err := lockWalletAndReservation(ctx, tx, providerID); err != nil {
		return err
	}
	var reservedAmount int64
	if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND embedding_invocation_id=$2 AND kind='reserved'`, providerID, embeddingInvocationID).Scan(&reservedAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrReservationNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, embedding_invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'committed',$3,$4)
ON CONFLICT (provider_id, embedding_invocation_id, kind) WHERE embedding_invocation_id IS NOT NULL DO NOTHING`,
		providerID, embeddingInvocationID, int64(actualUSD), now)
	if err != nil {
		if isUniqueViolation(err) {
			return costledger.ErrAlreadyTerminal
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
UPDATE provider_wallets SET
    balance_usd_nanos = balance_usd_nanos - $1,
    reserved_usd_nanos = reserved_usd_nanos - $2,
    version = version + 1, updated_at=$3
WHERE provider_id=$4`, int64(actualUSD), reservedAmount, now, providerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ReleaseEmbedding(ctx context.Context, providerID string, embeddingInvocationID int64, now time.Time) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin release embedding: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, _, err := lockWalletAndReservation(ctx, tx, providerID); err != nil {
		return err
	}
	var reservedAmount int64
	if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND embedding_invocation_id=$2 AND kind='reserved'`, providerID, embeddingInvocationID).Scan(&reservedAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrReservationNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, embedding_invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'released',0,$3)
ON CONFLICT (provider_id, embedding_invocation_id, kind) WHERE embedding_invocation_id IS NOT NULL DO NOTHING`,
		providerID, embeddingInvocationID, now)
	if err != nil {
		if isUniqueViolation(err) {
			return costledger.ErrAlreadyTerminal
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `UPDATE provider_wallets SET reserved_usd_nanos = reserved_usd_nanos - $1, version = version + 1, updated_at=$2 WHERE provider_id=$3`, reservedAmount, now, providerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
