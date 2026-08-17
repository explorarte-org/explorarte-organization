// Package postgres implements the durable ExecutionContextView store
// (internal/contextcompiler.ExecutionContextViewStore) against PostgreSQL.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("execution context view store requires an initialized PostgreSQL store")
	}
	return &Store{pool: store.Pool()}, nil
}

// Persist is idempotent per view.ContextSnapshotID: INSERT ... ON CONFLICT
// DO NOTHING relies on the execution_context_views_context_snapshot_id_key
// UNIQUE constraint (migration 000051). If the row already existed, Persist
// re-fetches it and compares the content that WOULD have been written
// against what IS durably recorded; any mismatch is
// contextcompiler.ErrExecutionContextViewDrift, never a silent overwrite or
// a silent acceptance of the caller's possibly-wrong attempt.
func (s *Store) Persist(ctx context.Context, view contextcompiler.ExecutionContextView) (contextcompiler.ExecutionContextView, error) {
	if view.OrganizationID == "" || view.ContextSnapshotID <= 0 {
		return contextcompiler.ExecutionContextView{}, errors.New("execution context view requires organization_id and context_snapshot_id")
	}
	if view.ProviderVisibleDigest != sha256Hex(view.ProviderVisibleBytes) {
		return contextcompiler.ExecutionContextView{}, fmt.Errorf("%w: digest does not match bytes at persist time", contextcompiler.ErrExecutionContextViewIntegrity)
	}
	// CompileForTaskClass's fallback branch (no registered ContextProfile
	// for this task class) leaves SegmentDiffs as its Go zero value, a nil
	// slice, which json.Marshal serializes as JSON null -- not an empty
	// array. Normalize before marshaling so the segment_diffs CHECK
	// (jsonb_typeof = 'array') always holds, regardless of which path
	// produced the view.
	segmentDiffs := view.SegmentDiffs
	if segmentDiffs == nil {
		segmentDiffs = []contextcompiler.SegmentDiff{}
	}
	diffsJSON, err := json.Marshal(segmentDiffs)
	if err != nil {
		return contextcompiler.ExecutionContextView{}, fmt.Errorf("marshal segment diffs: %w", err)
	}

	var id int64
	err = s.pool.QueryRow(ctx, `
		INSERT INTO execution_context_views (
			organization_id, context_snapshot_id, context_profile_id, context_profile_version,
			fell_back_to_canonical, fallback_reason, provider_render_version,
			stable_prefix_hash, stable_prefix_bytes, dynamic_suffix_hash, dynamic_suffix_bytes,
			authority_order_hash, compiled_content_hash, segment_diffs,
			provider_visible_bytes, provider_visible_digest, provider_visible_byte_count
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (context_snapshot_id) DO NOTHING
		RETURNING id
	`, view.OrganizationID, view.ContextSnapshotID, view.ContextProfileID, view.ContextProfileVersion,
		view.FellBackToCanonical, view.FallbackReason, view.ProviderRenderVersion,
		view.StablePrefixHash, view.StablePrefixBytes, view.DynamicSuffixHash, view.DynamicSuffixBytes,
		view.AuthorityOrderHash, view.CompiledContentHash, diffsJSON,
		view.ProviderVisibleBytes, view.ProviderVisibleDigest, view.ProviderVisibleByteCount,
	).Scan(&id)

	if err == nil {
		return s.Get(ctx, id)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return contextcompiler.ExecutionContextView{}, mapError(err)
	}

	// No row was inserted: a durable view for this snapshot already
	// exists. Load it and prove the caller's attempt agrees with it --
	// via SameLogicalView, which compares every durably meaningful field,
	// not only bytes/digest -- before ever returning it as "the" persisted
	// view.
	existing, getErr := s.GetByContextSnapshot(ctx, view.OrganizationID, view.ContextSnapshotID)
	if getErr != nil {
		return contextcompiler.ExecutionContextView{}, getErr
	}
	if !contextcompiler.SameLogicalView(existing, view) {
		return contextcompiler.ExecutionContextView{}, fmt.Errorf("%w: snapshot=%d", contextcompiler.ErrExecutionContextViewDrift, view.ContextSnapshotID)
	}
	return existing, nil
}

func (s *Store) Get(ctx context.Context, id int64) (contextcompiler.ExecutionContextView, error) {
	row := s.pool.QueryRow(ctx, selectColumns+` WHERE id = $1`, id)
	return scanView(row)
}

func (s *Store) GetByContextSnapshot(ctx context.Context, organizationID string, contextSnapshotID int64) (contextcompiler.ExecutionContextView, error) {
	row := s.pool.QueryRow(ctx, selectColumns+` WHERE organization_id = $1 AND context_snapshot_id = $2`, organizationID, contextSnapshotID)
	return scanView(row)
}

const selectColumns = `
	SELECT id, organization_id, context_snapshot_id, context_profile_id, context_profile_version,
	       fell_back_to_canonical, fallback_reason, provider_render_version,
	       stable_prefix_hash, stable_prefix_bytes, dynamic_suffix_hash, dynamic_suffix_bytes,
	       authority_order_hash, compiled_content_hash, segment_diffs,
	       provider_visible_bytes, provider_visible_digest, provider_visible_byte_count, created_at
	FROM execution_context_views`

func scanView(row pgx.Row) (contextcompiler.ExecutionContextView, error) {
	var v contextcompiler.ExecutionContextView
	var diffsJSON []byte
	err := row.Scan(&v.ID, &v.OrganizationID, &v.ContextSnapshotID, &v.ContextProfileID, &v.ContextProfileVersion,
		&v.FellBackToCanonical, &v.FallbackReason, &v.ProviderRenderVersion,
		&v.StablePrefixHash, &v.StablePrefixBytes, &v.DynamicSuffixHash, &v.DynamicSuffixBytes,
		&v.AuthorityOrderHash, &v.CompiledContentHash, &diffsJSON,
		&v.ProviderVisibleBytes, &v.ProviderVisibleDigest, &v.ProviderVisibleByteCount, &v.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contextcompiler.ExecutionContextView{}, contextcompiler.ErrExecutionContextViewNotFound
		}
		return contextcompiler.ExecutionContextView{}, mapError(err)
	}
	if err := json.Unmarshal(diffsJSON, &v.SegmentDiffs); err != nil {
		return contextcompiler.ExecutionContextView{}, fmt.Errorf("unmarshal segment diffs: %w", err)
	}
	// Integrity check on every read: a loaded view whose persisted digest
	// does not match SHA-256 of its persisted bytes is never returned as
	// valid, regardless of how the mismatch happened (corruption, direct
	// DB tampering, a future bug elsewhere).
	if v.ProviderVisibleDigest != sha256Hex(v.ProviderVisibleBytes) {
		return contextcompiler.ExecutionContextView{}, fmt.Errorf("%w: view id=%d", contextcompiler.ErrExecutionContextViewIntegrity, v.ID)
	}
	return v, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return contextcompiler.ErrExecutionContextViewNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("execution context view PostgreSQL uniqueness conflict: %w", err)
		case "23503", "23514", "22P02":
			return fmt.Errorf("execution context view PostgreSQL constraint violation: %w", err)
		case "57P01", "57P02", "57P03", "08000", "08001", "08003", "08004", "08006", "08007", "08P01":
			return fmt.Errorf("execution context view database unavailable: PostgreSQL %s: %w", pgErr.Code, err)
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) || pgconn.SafeToRetry(err) {
		return fmt.Errorf("execution context view database unavailable: %w", err)
	}
	return err
}

var _ contextcompiler.ExecutionContextViewStore = (*Store)(nil)
