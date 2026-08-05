package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultOutboxMaxAttempts = 10

type Store struct{ pool *pgxpool.Pool }

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("context store requires an initialized PostgreSQL store")
	}
	return &Store{pool: store.Pool()}, nil
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) AllocateID(ctx context.Context) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('context_snapshots','id'))`).Scan(&id)
	if err != nil {
		return 0, mapError(err)
	}
	return id, nil
}

func (s *Store) Create(ctx context.Context, command contextengine.CreateSnapshotCommand) (result contextengine.BuildResult, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, mapError(err)
	}
	defer func() {
		if err != nil {
			rollback(tx)
		}
	}()
	inserted, err := insertSnapshot(ctx, tx, command.Snapshot)
	if err != nil {
		return result, err
	}
	if !inserted {
		existing, getErr := getByIdempotency(ctx, tx, command.Snapshot.OrganizationID, command.Snapshot.IdempotencyKey, true, true)
		if getErr != nil {
			return result, getErr
		}
		if existing.RequestHash != command.Snapshot.RequestHash {
			return result, contextengine.ErrIdempotencyConflict
		}
		if existing.Status == contextengine.SnapshotInvalidated {
			return result, contextengine.ErrSnapshotInvalidated
		}
		if err = appendAudit(ctx, tx, existing, "context.snapshot_reused", "system", "orgd", "", nil); err != nil {
			return result, err
		}
		if err = tx.Commit(ctx); err != nil {
			return result, mapError(err)
		}
		return contextengine.BuildResult{Snapshot: existing, Reused: true}, nil
	}
	for _, segment := range command.Snapshot.Segments {
		if err = insertSegment(ctx, tx, command.Snapshot, segment); err != nil {
			return result, err
		}
	}
	if err = appendAuditAndOutbox(ctx, tx, command.Snapshot, "context.snapshot_created", "system", "orgd", ""); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, mapError(err)
	}
	return contextengine.BuildResult{Snapshot: command.Snapshot}, nil
}

func (s *Store) Get(ctx context.Context, id int64, includeSegments bool) (contextengine.Snapshot, error) {
	value, err := getSnapshot(ctx, s.pool, id, false)
	if err != nil {
		return contextengine.Snapshot{}, err
	}
	if includeSegments {
		value.Segments, err = listSegments(ctx, s.pool, id)
		if err != nil {
			return contextengine.Snapshot{}, err
		}
	}
	return value, nil
}

func (s *Store) GetByIdempotency(ctx context.Context, organizationID, key string, includeSegments bool) (contextengine.Snapshot, error) {
	return getByIdempotency(ctx, s.pool, organizationID, key, includeSegments, false)
}

func (s *Store) List(ctx context.Context, filter contextengine.ListFilter) ([]contextengine.Snapshot, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
SELECT `+snapshotColumns+` FROM context_snapshots
WHERE ($1='' OR organization_id=$1)
  AND ($2='' OR actor_role_id=$2)
  AND ($3='' OR status=$3)
ORDER BY created_at DESC,id DESC LIMIT $4`, filter.OrganizationID, filter.ActorRoleID, filter.Status, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := []contextengine.Snapshot{}
	for rows.Next() {
		value, scanErr := scanSnapshot(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func (s *Store) Invalidate(ctx context.Context, command contextengine.InvalidateCommand, now time.Time) (result contextengine.Snapshot, reused bool, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, false, mapError(err)
	}
	defer func() {
		if err != nil {
			rollback(tx)
		}
	}()
	current, err := getSnapshot(ctx, tx, command.SnapshotID, true)
	if err != nil {
		return result, false, err
	}
	if current.Status == contextengine.SnapshotInvalidated {
		if current.InvalidationReason != strings.TrimSpace(command.Reason) {
			return result, false, contextengine.ErrSnapshotInvalidated
		}
		var actor string
		queryErr := tx.QueryRow(ctx, `SELECT COALESCE(actor_id,'') FROM audit_events WHERE subject_type='context_snapshot' AND subject_id=$1 AND event_type='context.snapshot_invalidated' ORDER BY id DESC LIMIT 1`, strconv.FormatInt(current.ID, 10)).Scan(&actor)
		if queryErr != nil {
			return result, false, mapError(queryErr)
		}
		if actor != command.ActorRoleID {
			return result, false, contextengine.ErrSnapshotInvalidated
		}
		if err = tx.Commit(ctx); err != nil {
			return result, false, mapError(err)
		}
		return current, true, nil
	}
	if !contextengine.CanTransition(current.Status, contextengine.SnapshotInvalidated) {
		return result, false, contextengine.ErrSnapshotInvalidated
	}
	var updated contextengine.Snapshot
	updated, err = scanSnapshot(tx.QueryRow(ctx, `
UPDATE context_snapshots
SET status='invalidated',invalidated_at=$2,invalidation_reason=$3,version=version+1
WHERE id=$1 AND status='ready'
RETURNING `+snapshotColumns, command.SnapshotID, now, strings.TrimSpace(command.Reason)))
	if err != nil {
		return result, false, err
	}
	updated.Segments, err = listSegments(ctx, tx, updated.ID)
	if err != nil {
		return result, false, err
	}
	reason := contextengine.ReasonSnapshotInvalidated
	eventSnapshot := updated
	eventSnapshot.CreatedAt = now
	eventSnapshot.CorrelationID = command.CorrelationID
	eventSnapshot.CausationID = command.CausationID
	if err = appendAuditAndOutboxWithCorrelation(ctx, tx, eventSnapshot, "context.snapshot_invalidated", "role", command.ActorRoleID, reason, command.CorrelationID, command.CausationID); err != nil {
		return result, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, false, mapError(err)
	}
	return updated, false, nil
}

const snapshotColumns = `id,organization_id,organization_revision_id,actor_role_id,purpose,project_ref,task_ref,idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,correlation_id,causation_id,created_at,invalidated_at,invalidation_reason`

type scanner interface{ Scan(...any) error }
type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func scanSnapshot(row scanner) (contextengine.Snapshot, error) {
	var value contextengine.Snapshot
	var project, task, correlation, causation, invalidation *string
	err := row.Scan(&value.ID, &value.OrganizationID, &value.OrganizationRevisionID, &value.ActorRoleID, &value.Purpose, &project, &task, &value.IdempotencyKey, &value.RequestHash, &value.PrecedenceHash, &value.CanonicalBundleHash, &value.RenderedHash, &value.Status, &value.Version, &value.SegmentCount, &value.IncludedSegmentCount, &value.OmittedSegmentCount, &value.TotalBytes, &correlation, &causation, &value.CreatedAt, &value.InvalidatedAt, &invalidation)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, contextengine.ErrSnapshotNotFound
	}
	if err != nil {
		return value, mapError(err)
	}
	value.ProjectRef = deref(project)
	value.TaskRef = deref(task)
	value.CorrelationID = deref(correlation)
	value.CausationID = deref(causation)
	value.InvalidationReason = deref(invalidation)
	return value, nil
}

func insertSnapshot(ctx context.Context, tx pgx.Tx, value contextengine.Snapshot) (bool, error) {
	var id int64
	err := tx.QueryRow(ctx, `
INSERT INTO context_snapshots(
 id,organization_id,organization_revision_id,actor_role_id,purpose,project_ref,task_ref,idempotency_key,
 request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,
 included_segment_count,omitted_segment_count,total_bytes,correlation_id,causation_id,created_at
) OVERRIDING SYSTEM VALUE
VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,NULLIF($19,''),NULLIF($20,''),$21)
ON CONFLICT(organization_id,idempotency_key) DO NOTHING RETURNING id`,
		value.ID, value.OrganizationID, value.OrganizationRevisionID, value.ActorRoleID, value.Purpose, value.ProjectRef, value.TaskRef, value.IdempotencyKey,
		value.RequestHash, value.PrecedenceHash, value.CanonicalBundleHash, value.RenderedHash, value.Status, value.Version, value.SegmentCount,
		value.IncludedSegmentCount, value.OmittedSegmentCount, value.TotalBytes, value.CorrelationID, value.CausationID, value.CreatedAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapError(err)
	}
	return true, nil
}

func insertSegment(ctx context.Context, tx pgx.Tx, snapshot contextengine.Snapshot, segment contextengine.Segment) error {
	_, err := tx.Exec(ctx, `
INSERT INTO context_segments(snapshot_id,organization_id,ordinal,render_ordinal,authority_priority,authority_tier,source_kind,source_reference,source_version,instruction_class,trust_class,data_class,may_grant_capabilities,included,omission_reason,content_hash,byte_count,content,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULLIF($15,''),$16,$17,$18,$19)`,
		snapshot.ID, snapshot.OrganizationID, segment.Ordinal, segment.RenderOrdinal, segment.AuthorityPriority, segment.AuthorityTier, segment.SourceKind, segment.SourceReference, segment.SourceVersion, segment.InstructionClass, segment.TrustClass, segment.DataClass, segment.MayGrantCapabilities, segment.Included, segment.OmissionReason, segment.ContentHash, segment.ByteCount, nullableContent(segment), snapshot.CreatedAt)
	return mapError(err)
}

func getSnapshot(ctx context.Context, db queryer, id int64, lock bool) (contextengine.Snapshot, error) {
	query := `SELECT ` + snapshotColumns + ` FROM context_snapshots WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanSnapshot(db.QueryRow(ctx, query, id))
}

func getByIdempotency(ctx context.Context, db queryer, organizationID, key string, includeSegments, lock bool) (contextengine.Snapshot, error) {
	query := `SELECT ` + snapshotColumns + ` FROM context_snapshots WHERE organization_id=$1 AND idempotency_key=$2`
	if lock {
		query += ` FOR SHARE`
	}
	value, err := scanSnapshot(db.QueryRow(ctx, query, organizationID, key))
	if err != nil {
		return value, err
	}
	if includeSegments {
		value.Segments, err = listSegments(ctx, db, value.ID)
	}
	return value, err
}

func listSegments(ctx context.Context, db queryer, snapshotID int64) ([]contextengine.Segment, error) {
	rows, err := db.Query(ctx, `SELECT ordinal,render_ordinal,authority_priority,authority_tier,source_kind,source_reference,source_version,instruction_class,trust_class,data_class,may_grant_capabilities,included,omission_reason,content_hash,byte_count,content FROM context_segments WHERE snapshot_id=$1 ORDER BY render_ordinal`, snapshotID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := []contextengine.Segment{}
	for rows.Next() {
		var value contextengine.Segment
		var omission *string
		if err = rows.Scan(&value.Ordinal, &value.RenderOrdinal, &value.AuthorityPriority, &value.AuthorityTier, &value.SourceKind, &value.SourceReference, &value.SourceVersion, &value.InstructionClass, &value.TrustClass, &value.DataClass, &value.MayGrantCapabilities, &value.Included, &omission, &value.ContentHash, &value.ByteCount, &value.Content); err != nil {
			return nil, mapError(err)
		}
		value.OmissionReason = deref(omission)
		values = append(values, value)
	}
	return values, mapError(rows.Err())
}

func appendAuditAndOutbox(ctx context.Context, tx pgx.Tx, snapshot contextengine.Snapshot, eventType, actorType, actorID string, reason contextengine.ReasonCode) error {
	return appendAuditAndOutboxWithCorrelation(ctx, tx, snapshot, eventType, actorType, actorID, reason, snapshot.CorrelationID, snapshot.CausationID)
}

func appendAuditAndOutboxWithCorrelation(ctx context.Context, tx pgx.Tx, snapshot contextengine.Snapshot, eventType, actorType, actorID string, reason contextengine.ReasonCode, correlation, causation string) error {
	payload := eventPayload(snapshot, reason)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode context outbox payload: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,schema_version,payload,max_attempts) VALUES('context_snapshot',$1,$2,1,$3::jsonb,$4)`, strconv.FormatInt(snapshot.ID, 10), eventType, encoded, defaultOutboxMaxAttempts); err != nil {
		return mapError(err)
	}
	return appendAudit(ctx, tx, snapshot, eventType, actorType, actorID, reason, encoded)
}

func appendAudit(ctx context.Context, tx pgx.Tx, snapshot contextengine.Snapshot, eventType, actorType, actorID string, reason contextengine.ReasonCode, payload []byte) error {
	if payload == nil {
		var err error
		payload, err = json.Marshal(eventPayload(snapshot, reason))
		if err != nil {
			return fmt.Errorf("encode context audit payload: %w", err)
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(occurred_at,event_type,actor_type,actor_id,subject_type,subject_id,correlation_id,causation_id,payload) VALUES($1,$2,$3,$4,'context_snapshot',$5,NULLIF($6,''),NULLIF($7,''),$8::jsonb)`, snapshot.CreatedAt, eventType, actorType, actorID, strconv.FormatInt(snapshot.ID, 10), snapshot.CorrelationID, snapshot.CausationID, payload)
	return mapError(err)
}

func eventPayload(snapshot contextengine.Snapshot, reason contextengine.ReasonCode) map[string]any {
	value := map[string]any{
		"schema_version":           1,
		"context_snapshot_id":      snapshot.ID,
		"organization_id":          snapshot.OrganizationID,
		"organization_revision_id": snapshot.OrganizationRevisionID,
		"actor_role_id":            snapshot.ActorRoleID,
		"status":                   snapshot.Status,
		"purpose":                  snapshot.Purpose,
		"precedence_hash":          snapshot.PrecedenceHash,
		"canonical_bundle_hash":    snapshot.CanonicalBundleHash,
		"rendered_hash":            snapshot.RenderedHash,
		"segment_count":            snapshot.SegmentCount,
		"included_segment_count":   snapshot.IncludedSegmentCount,
		"omitted_segment_count":    snapshot.OmittedSegmentCount,
	}
	if reason != "" {
		value["reason_code"] = reason
	}
	return value
}

func nullableContent(segment contextengine.Segment) any {
	if !segment.Included {
		return nil
	}
	return segment.Content
}
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return contextengine.ErrSnapshotNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("context PostgreSQL uniqueness conflict: %w", err)
		case "23503", "23514", "22P02":
			return fmt.Errorf("context PostgreSQL constraint violation: %w", err)
		case "57P01", "57P02", "57P03", "08000", "08001", "08003", "08004", "08006", "08007", "08P01":
			return fmt.Errorf("%w: PostgreSQL %s", contextengine.ErrDatabaseUnavailable, pgErr.Code)
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) || pgconn.SafeToRetry(err) {
		return fmt.Errorf("%w: %v", contextengine.ErrDatabaseUnavailable, err)
	}
	return err
}
