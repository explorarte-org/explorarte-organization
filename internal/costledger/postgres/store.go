package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
var _ costledger.CallReader = (*Store)(nil)
var _ costledger.ProgramScopedReserver = (*Store)(nil)

func (s *Store) ReserveWithinProgramCeiling(ctx context.Context, req costledger.ProgramReservation, now time.Time) error {
	if req.InvocationID <= 0 || req.CorrelationID == "" || len(req.FamilyModelIDs) == 0 || req.EstimatedUSD < 0 || req.MaxUSD <= 0 {
		return fmt.Errorf("%w: invalid program reservation", costledger.ErrInvalidRequest)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_usd_nanos,reserved_usd_nanos FROM provider_wallets WHERE provider_id=$1 FOR UPDATE`, req.ProviderID).Scan(&balance, &reserved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return costledger.ErrWalletNotFound
		}
		return err
	}
	var existing int64
	if err := tx.QueryRow(ctx, `SELECT amount_usd_nanos FROM provider_wallet_events WHERE provider_id=$1 AND invocation_id=$2 AND kind='reserved'`, req.ProviderID, req.InvocationID).Scan(&existing); err == nil {
		if existing != int64(req.EstimatedUSD) {
			return costledger.ErrAmountMismatch
		}
		return tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var used int64
	err = tx.QueryRow(ctx, familySpendSQL(`t.correlation_id=$1`), req.CorrelationID, req.ProviderID, req.FamilyModelIDs).Scan(&used)
	if err != nil {
		return err
	}
	if used+int64(req.EstimatedUSD) > int64(req.MaxUSD) {
		return costledger.ErrProgramBudgetExceeded
	}
	if balance-reserved < int64(req.EstimatedUSD) {
		return costledger.ErrInsufficientBalance
	}
	if _, err := tx.Exec(ctx, `INSERT INTO provider_wallet_events (provider_id,invocation_id,kind,amount_usd_nanos,created_at) VALUES ($1,$2,'reserved',$3,$4)`, req.ProviderID, req.InvocationID, int64(req.EstimatedUSD), now.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE provider_wallets SET reserved_usd_nanos=reserved_usd_nanos+$1,version=version+1,updated_at=$2 WHERE provider_id=$3`, int64(req.EstimatedUSD), now.UTC(), req.ProviderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListCallBreakdowns(ctx context.Context, organizationID, providerID string, limit int) ([]costledger.CallBreakdown, error) {
	organizationID = strings.TrimSpace(organizationID)
	providerID = strings.TrimSpace(providerID)
	if organizationID == "" || providerID == "" || limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("%w: organization, provider, and limit between 1 and 1000 are required", costledger.ErrInvalidRequest)
	}
	rows, err := s.pool.Query(ctx, `
WITH calls AS (
    SELECT
        e.provider_id AS wallet_provider_id,
        e.invocation_id,
        MAX(e.amount_usd_nanos) FILTER (WHERE e.kind='reserved') AS estimated_usd_nanos,
        MAX(e.amount_usd_nanos) FILTER (WHERE e.kind='committed') AS charged_usd_nanos,
        MAX(e.amount_usd_nanos) FILTER (WHERE e.kind='released') AS released_usd_nanos,
        MAX(e.created_at) AS last_ledger_at
    FROM provider_wallet_events e
    JOIN model_invocations scoped
      ON scoped.id=e.invocation_id
     AND scoped.organization_id=$1
    WHERE e.provider_id=$2
    GROUP BY e.provider_id, e.invocation_id
    ORDER BY MAX(e.created_at) DESC, e.invocation_id DESC
    LIMIT $3
)
SELECT
    calls.invocation_id,
    invocation.organization_id,
    invocation.task_id,
    invocation.attempt_id,
    invocation.dispatch_actor_role_id,
    invocation.subject_role_id,
    calls.wallet_provider_id,
    invocation.provider_id,
    invocation.provider_model_id,
    invocation.status,
    COALESCE(invocation.error_code,''),
    calls.estimated_usd_nanos,
    calls.charged_usd_nanos,
    calls.released_usd_nanos,
    COALESCE(usage.input_tokens,0),
    COALESCE(usage.output_tokens,0),
    COALESCE(usage.total_tokens,0),
    COALESCE(usage.provider_reported,FALSE),
    invocation.created_at,
    invocation.terminal_at,
    calls.last_ledger_at
FROM calls
JOIN model_invocations invocation ON invocation.id=calls.invocation_id
LEFT JOIN model_invocation_usage usage ON usage.invocation_id=calls.invocation_id
ORDER BY calls.last_ledger_at DESC, calls.invocation_id DESC`, organizationID, providerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	values := make([]costledger.CallBreakdown, 0, limit)
	for rows.Next() {
		var value costledger.CallBreakdown
		var estimated, charged, released *int64
		if err := rows.Scan(
			&value.InvocationID, &value.OrganizationID, &value.TaskID, &value.AttemptID,
			&value.DispatchActorRoleID, &value.SubjectRoleID,
			&value.WalletProviderID, &value.InvocationProviderID, &value.ProviderModelID,
			&value.InvocationStatus, &value.InvocationErrorCode,
			&estimated, &charged, &released,
			&value.InputTokens, &value.OutputTokens, &value.TotalTokens, &value.ProviderReported,
			&value.InvocationCreatedAt, &value.InvocationTerminalAt, &value.LastLedgerAt,
		); err != nil {
			return nil, err
		}
		if estimated != nil {
			value.EstimatedUSD = modelpricing.USDNanos(*estimated)
		}
		switch {
		case charged != nil:
			value.Settlement = costledger.SettlementCommitted
			value.ChargedUSD = modelpricing.USDNanos(*charged)
		case released != nil:
			value.Settlement = costledger.SettlementReleased
			value.ReleasedUSD = modelpricing.USDNanos(*released)
		default:
			value.Settlement = costledger.SettlementReserved
		}
		value.ProviderMismatch = value.WalletProviderID != value.InvocationProviderID
		value.InvocationCreatedAt = value.InvocationCreatedAt.UTC()
		value.LastLedgerAt = value.LastLedgerAt.UTC()
		if value.InvocationTerminalAt != nil {
			terminalAt := value.InvocationTerminalAt.UTC()
			value.InvocationTerminalAt = &terminalAt
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func (s *Store) ListEvents(ctx context.Context, providerID string, limit int) ([]costledger.WalletEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, provider_id, invocation_id, embedding_invocation_id, kind, amount_usd_nanos, created_at
FROM provider_wallet_events
WHERE provider_id=$1
ORDER BY created_at DESC, id DESC
LIMIT $2`, providerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWalletEvents(rows, limit)
}

func (s *Store) ListOrphanedReservations(ctx context.Context, olderThan time.Time, limit int) ([]costledger.WalletEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT r.id, r.provider_id, r.invocation_id, r.embedding_invocation_id, r.kind, r.amount_usd_nanos, r.created_at
FROM provider_wallet_events r
WHERE r.kind = 'reserved' AND r.created_at < $1
  AND NOT EXISTS (
      SELECT 1 FROM provider_wallet_events t
      WHERE t.provider_id = r.provider_id
        AND t.invocation_id IS NOT DISTINCT FROM r.invocation_id
        AND t.embedding_invocation_id IS NOT DISTINCT FROM r.embedding_invocation_id
        AND t.kind IN ('committed', 'released')
  )
ORDER BY r.created_at ASC, r.id ASC
LIMIT $2`, olderThan.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWalletEvents(rows, limit)
}

func scanWalletEvents(rows pgx.Rows, limit int) ([]costledger.WalletEvent, error) {
	events := make([]costledger.WalletEvent, 0, limit)
	for rows.Next() {
		var event costledger.WalletEvent
		var kind string
		if err := rows.Scan(&event.ID, &event.ProviderID, &event.InvocationID, &event.EmbeddingInvocationID, &kind, &event.AmountUSD, &event.CreatedAt); err != nil {
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
ON CONFLICT (provider_id, invocation_id, kind) WHERE invocation_id IS NOT NULL DO NOTHING`,
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

// ProvisionedProviderIDs implements
// modelruntime.ProviderWalletProvisionChecker (G2-001): it reports every
// provider_id that already has a provider_wallets row, so a caller can
// tell "funded, possibly at $0" apart from "never provisioned at all"
// without duplicating this table's own knowledge of which providers
// exist. Read-only -- never creates a row.
func (s *Store) ProvisionedProviderIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT provider_id FROM provider_wallets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	provisioned := make(map[string]bool)
	for rows.Next() {
		var providerID string
		if err := rows.Scan(&providerID); err != nil {
			return nil, err
		}
		provisioned[providerID] = true
	}
	return provisioned, rows.Err()
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
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at, cost_provenance, financial_outcome)
VALUES ($1,$2,'committed',$3,$4,'actual_provider_reported','actual')
ON CONFLICT (provider_id, invocation_id, kind) WHERE invocation_id IS NOT NULL DO NOTHING`,
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
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at, cost_provenance, financial_outcome)
VALUES ($1,$2,'released',0,$3,'unknown','released_not_sent')
ON CONFLICT (provider_id, invocation_id, kind) WHERE invocation_id IS NOT NULL DO NOTHING`,
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

var _ costledger.PendingReconciliationMarker = (*Store)(nil)

// MarkPendingReconciliation annotates an existing 'reserved' wallet event
// row in place — the only mutation provider_wallet_events ever permits (see
// migration 000037's relaxed reject_provider_wallet_event_mutation trigger,
// which allows exactly this one-time annotation and nothing else: kind,
// amount, provider_id, invocation_id and created_at all stay immutable).
// Idempotent: a row that is already annotated is left untouched and this
// still returns nil, matching Reconcile/Release's idempotency.
func (s *Store) MarkPendingReconciliation(ctx context.Context, providerID string, invocationID int64, now time.Time) error {
	now = now.UTC()
	tag, err := s.pool.Exec(ctx, `
UPDATE provider_wallet_events
SET cost_provenance='estimated_locally', financial_outcome='estimated_pending_reconciliation'
WHERE provider_id=$1 AND invocation_id=$2 AND kind='reserved' AND cost_provenance IS NULL AND financial_outcome IS NULL`,
		providerID, invocationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// RowsAffected()==0 here means either no 'reserved' row exists, or one
	// exists and is already annotated (idempotent retry) — disambiguate so
	// a genuinely missing reservation still surfaces as an error the same
	// way Reconcile/Release do.
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_wallet_events WHERE provider_id=$1 AND invocation_id=$2 AND kind='reserved')`, providerID, invocationID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return costledger.ErrReservationNotFound
	}
	return nil
}

var _ costledger.SubscriptionRecorder = (*Store)(nil)

// RecordSubscriptionConsumption inserts a single 'committed' wallet event
// for a subscription/token-plan-billed call (e.g. mimo), with
// amount_usd_nanos=0 and cost_provenance/financial_outcome set to their
// dedicated subscription-only values ('subscription_resource_consumed' /
// 'resource_consumed', added by migration 000039 alongside the existing
// PAYG values from 000037) -- never a bare, unexplained 0. Unlike Reserve/
// Reconcile/Release, this does NOT touch provider_wallets.balance_usd_nanos
// or reserved_usd_nanos at all: there was never a real-money reservation to
// begin with (Gate.Reserve skips PriceTier/ledger.Reserve entirely for a
// subscription provider -- see internal/modelruntime/costgate/gate.go), so
// there is nothing to debit or release. provider_wallets still needs a row
// for this provider_id to satisfy provider_wallet_events' foreign key --
// migration 000039 seeds one with balance_usd_nanos=0, documented as a pure
// FK anchor, never a real balance.
//
// Idempotent per (providerID, invocationID) via the same
// provider_wallet_events_unique_kind constraint (migration 000021) every
// other event-insert here relies on.
func (s *Store) RecordSubscriptionConsumption(ctx context.Context, providerID string, invocationID int64, now time.Time) error {
	now = now.UTC()
	_, err := s.pool.Exec(ctx, `
INSERT INTO provider_wallet_events (provider_id, invocation_id, kind, amount_usd_nanos, created_at, cost_provenance, financial_outcome)
VALUES ($1,$2,'committed',0,$3,'subscription_resource_consumed','resource_consumed')
ON CONFLICT (provider_id, invocation_id, kind) WHERE invocation_id IS NOT NULL DO NOTHING`,
		providerID, invocationID, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return err
	}
	return nil
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

// familySpendSQL builds the family-scoped spend query used both by the
// ceiling enforced at reservation time and by the read-only admission checks
// that decide whether new autonomous work may be opened at all.
//
// It is one function rather than two similar query literals on purpose. Two
// copies would eventually disagree about what "spent" means -- whether a
// released reservation counts, whether a still-open reservation counts at its
// estimate -- and the disagreement would appear as work admitted against a
// budget the reservation path then refuses, or worse, admitted against a
// budget that is already gone.
//
// scope is the row predicate selecting which invocations belong to the
// question being asked; $2 is always the provider and $3 the family's model
// IDs.
func familySpendSQL(scope string) string {
	return `WITH inv AS (
			SELECT mi.id FROM model_invocations mi JOIN tasks t ON t.id=mi.task_id
			WHERE ` + scope + ` AND mi.provider_id=$2 AND mi.provider_model_id = ANY($3::text[])
		), amounts AS (
			SELECT i.id,
				MAX(e.amount_usd_nanos) FILTER (WHERE e.kind='reserved') reserved,
				MAX(e.amount_usd_nanos) FILTER (WHERE e.kind='committed') committed,
				MAX(e.amount_usd_nanos) FILTER (WHERE e.kind='released') released
			FROM inv i JOIN provider_wallet_events e ON e.invocation_id=i.id GROUP BY i.id
		)
		SELECT COALESCE(SUM(CASE WHEN committed IS NOT NULL THEN committed WHEN released IS NOT NULL THEN 0 ELSE reserved END),0) FROM amounts`
}

// ProgramFamilySpend reports what a program has already spent in one budget
// family. It answers the same question, with the same arithmetic, that
// ReserveWithinProgramCeiling asks before admitting a reservation.
func (s *Store) ProgramFamilySpend(ctx context.Context, correlationID, providerID string, familyModelIDs []string) (modelpricing.USDNanos, error) {
	if strings.TrimSpace(correlationID) == "" || strings.TrimSpace(providerID) == "" || len(familyModelIDs) == 0 {
		return 0, fmt.Errorf("%w: invalid program spend query", costledger.ErrInvalidRequest)
	}
	var used int64
	if err := s.pool.QueryRow(ctx, familySpendSQL(`t.correlation_id=$1`), correlationID, providerID, familyModelIDs).Scan(&used); err != nil {
		return 0, err
	}
	return modelpricing.USDNanos(used), nil
}

// TaskFamilySpend reports what ONE task spent in a budget family.
//
// Recovery uses it as the estimate for a successor episode: the failed
// episode just performed the same work, so what it actually cost is the best
// available prediction of what repeating it will cost. That is a measurement,
// not a fixed per-episode allowance.
func (s *Store) TaskFamilySpend(ctx context.Context, taskID int64, providerID string, familyModelIDs []string) (modelpricing.USDNanos, error) {
	if taskID <= 0 || strings.TrimSpace(providerID) == "" || len(familyModelIDs) == 0 {
		return 0, fmt.Errorf("%w: invalid task spend query", costledger.ErrInvalidRequest)
	}
	var used int64
	if err := s.pool.QueryRow(ctx, familySpendSQL(`t.id=$1`), taskID, providerID, familyModelIDs).Scan(&used); err != nil {
		return 0, err
	}
	return modelpricing.USDNanos(used), nil
}

var _ costledger.SpendReader = (*Store)(nil)
