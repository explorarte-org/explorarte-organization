package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// isUniqueViolation reports whether err is a Postgres unique_violation
// (23505) — used to detect a hit against
// provider_wallet_events_one_terminal_idx (migration 000025), which
// enforces at the database level that a reservation may become committed
// or released, never both.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type Store struct {
	pool *pgxpool.Pool
}

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("costledger store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
}

var _ costledger.Ledger = (*Store)(nil)

func (s *Store) ListEvents(ctx context.Context, providerID string, limit int) ([]costledger.WalletEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, provider_id, invocation_id, kind, amount_usd_nanos, created_at
FROM provider_wallet_events
WHERE provider_id=$1
ORDER BY created_at DESC, id DESC
LIMIT $2`, providerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]costledger.WalletEvent, 0, limit)
	for rows.Next() {
		var event costledger.WalletEvent
		var kind string
		if err := rows.Scan(&event.ID, &event.ProviderID, &event.InvocationID, &kind, &event.AmountUSD, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Kind = costledger.EventKind(kind)
		event.CreatedAt = event.CreatedAt.UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) ListOrphanedReservations(ctx context.Context, olderThan time.Time, limit int) ([]costledger.WalletEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT r.id, r.provider_id, r.invocation_id, r.kind, r.amount_usd_nanos, r.created_at
FROM provider_wallet_events r
WHERE r.kind = 'reserved' AND r.created_at < $1
  AND NOT EXISTS (
      SELECT 1 FROM provider_wallet_events t
      WHERE t.provider_id = r.provider_id AND t.invocation_id = r.invocation_id
        AND t.kind IN ('committed', 'released')
  )
ORDER BY r.created_at ASC, r.id ASC
LIMIT $2`, olderThan.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]costledger.WalletEvent, 0, limit)
	for rows.Next() {
		var event costledger.WalletEvent
		var kind string
		if err := rows.Scan(&event.ID, &event.ProviderID, &event.InvocationID, &kind, &event.AmountUSD, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Kind = costledger.EventKind(kind)
		event.CreatedAt = event.CreatedAt.UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) GetWallet(ctx context.Context, providerID string) (costledger.ProviderWallet, error) {
	var wallet costledger.ProviderWallet
	wallet.ProviderID = providerID
	err := s.pool.QueryRow(ctx, `
SELECT balance_usd_nanos, reserved_usd_nanos, version, updated_at
FROM provider_wallets WHERE provider_id=$1`, providerID).
		Scan(&wallet.BalanceUSD, &wallet.ReservedUSD, &wallet.Version, &wallet.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ProviderWallet{}, costledger.ErrWalletNotFound
		}
		return costledger.ProviderWallet{}, err
	}
	wallet.UpdatedAt = wallet.UpdatedAt.UTC()
	return wallet, nil
}

func (s *Store) SetBalance(ctx context.Context, providerID string, balanceUSD modelpricing.USDNanos, now time.Time) (costledger.ProviderWallet, error) {
	now = now.UTC()
	var wallet costledger.ProviderWallet
	wallet.ProviderID = providerID
	err := s.pool.QueryRow(ctx, `
INSERT INTO provider_wallets (provider_id, balance_usd_nanos, reserved_usd_nanos, version, updated_at)
VALUES ($1,$2,0,1,$3)
ON CONFLICT (provider_id) DO UPDATE SET
    balance_usd_nanos = EXCLUDED.balance_usd_nanos,
    version = provider_wallets.version + 1,
    updated_at = EXCLUDED.updated_at
WHERE provider_wallets.reserved_usd_nanos <= EXCLUDED.balance_usd_nanos
RETURNING balance_usd_nanos, reserved_usd_nanos, version, updated_at`,
		providerID, int64(balanceUSD), now).
		Scan(&wallet.BalanceUSD, &wallet.ReservedUSD, &wallet.Version, &wallet.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ProviderWallet{}, fmt.Errorf("%w: new balance is below the amount already reserved", costledger.ErrInvalidRequest)
		}
		return costledger.ProviderWallet{}, err
	}
	wallet.UpdatedAt = wallet.UpdatedAt.UTC()
	return wallet, nil
}

func (s *Store) Reserve(ctx context.Context, providerID string, invocationID int64, estimatedUSD modelpricing.USDNanos, now time.Time) error {
	if estimatedUSD < 0 {
		return fmt.Errorf("%w: estimated cost must be non-negative", costledger.ErrInvalidRequest)
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reserve: %w", err)
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
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'reserved',$3,$4)
ON CONFLICT (provider_id, invocation_id, kind) DO NOTHING`,
		providerID, invocationID, int64(estimatedUSD), now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Already reserved by an earlier attempt of this same invocation.
		// Idempotent only if the amount matches — a retry that asks for a
		// different amount than what's actually on the ledger must fail
		// loudly rather than silently keep the stale reservation.
		var existingAmount int64
		if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND invocation_id=$2 AND kind='reserved'`, providerID, invocationID).Scan(&existingAmount); err != nil {
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

func (s *Store) Reconcile(ctx context.Context, providerID string, invocationID int64, actualUSD modelpricing.USDNanos, now time.Time) error {
	if actualUSD < 0 {
		return fmt.Errorf("%w: actual cost must be non-negative", costledger.ErrInvalidRequest)
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reconcile: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, _, err := lockWalletAndReservation(ctx, tx, providerID); err != nil {
		return err
	}
	var reservedAmount int64
	if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND invocation_id=$2 AND kind='reserved'`, providerID, invocationID).Scan(&reservedAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrReservationNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'committed',$3,$4)
ON CONFLICT (provider_id, invocation_id, kind) DO NOTHING`,
		providerID, invocationID, int64(actualUSD), now)
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

func (s *Store) Release(ctx context.Context, providerID string, invocationID int64, now time.Time) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin release: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, _, err := lockWalletAndReservation(ctx, tx, providerID); err != nil {
		return err
	}
	var reservedAmount int64
	if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND invocation_id=$2 AND kind='reserved'`, providerID, invocationID).Scan(&reservedAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrReservationNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at)
VALUES ($1,$2,'released',0,$3)
ON CONFLICT (provider_id, invocation_id, kind) DO NOTHING`,
		providerID, invocationID, now)
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

func lockWalletAndReservation(ctx context.Context, tx pgx.Tx, providerID string) (int64, int64, error) {
	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_usd_nanos, reserved_usd_nanos FROM provider_wallets WHERE provider_id=$1 FOR UPDATE`, providerID).Scan(&balance, &reserved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, costledger.ErrWalletNotFound
		}
		return 0, 0, err
	}
	return balance, reserved, nil
}
