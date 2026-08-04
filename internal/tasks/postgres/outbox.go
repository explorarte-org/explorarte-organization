package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ClaimOutbox(ctx context.Context, request tasks.OutboxClaimRequest) ([]tasks.ClaimedOutboxEvent, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) ([]tasks.ClaimedOutboxEvent, error) {
		rows, err := tx.Query(ctx, `
			SELECT id FROM outbox_events
			WHERE status='pending' AND available_at<=clock_timestamp()
			ORDER BY available_at,created_at,id
			FOR UPDATE SKIP LOCKED LIMIT $1
		`, request.BatchSize)
		if err != nil {
			return nil, mapError(err)
		}
		ids := make([]int64, 0, request.BatchSize)
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, mapError(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, mapError(err)
		}
		rows.Close()
		claimed := make([]tasks.ClaimedOutboxEvent, 0, len(ids))
		for _, id := range ids {
			plain, hash, err := newToken()
			if err != nil {
				return nil, err
			}
			event, err := scanOutbox(tx.QueryRow(ctx, `
				UPDATE outbox_events
				SET status='claimed',attempt_count=attempt_count+1,claim_token_hash=$2,claimed_by=$3,
				    claim_expires_at=clock_timestamp()+make_interval(secs=>$4),updated_at=clock_timestamp()
				WHERE id=$1 AND status='pending'
				RETURNING id,aggregate_type,aggregate_id,event_type,schema_version,payload,status,available_at,
				          attempt_count,max_attempts,claimed_by,claim_expires_at,last_error,created_at,updated_at,published_at
			`, id, hash, request.ConsumerID, request.ClaimDuration.Seconds()))
			if err != nil {
				return nil, err
			}
			claimed = append(claimed, tasks.ClaimedOutboxEvent{Event: event, ClaimToken: plain})
		}
		return claimed, nil
	})
}

func (s *Store) AckOutbox(ctx context.Context, disposition tasks.OutboxDisposition) error {
	_, err := withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (struct{}, error) {
		record, hash, now, err := lockOutbox(ctx, tx, disposition.EventID)
		if err != nil {
			return struct{}{}, err
		}
		if err := verifyOutboxClaim(record, hash, now, disposition); err != nil {
			return struct{}{}, err
		}
		result, err := tx.Exec(ctx, `
			UPDATE outbox_events
			SET status='published',claim_token_hash=NULL,claimed_by=NULL,claim_expires_at=NULL,
			    published_at=clock_timestamp(),updated_at=clock_timestamp(),last_error=NULL
			WHERE id=$1 AND status='claimed'
		`, disposition.EventID)
		if err != nil {
			return struct{}{}, mapError(err)
		}
		if result.RowsAffected() != 1 {
			return struct{}{}, tasks.ErrConflict
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) NackOutbox(ctx context.Context, disposition tasks.OutboxDisposition) error {
	_, err := withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (struct{}, error) {
		record, hash, now, err := lockOutbox(ctx, tx, disposition.EventID)
		if err != nil {
			return struct{}{}, err
		}
		if err := verifyOutboxClaim(record, hash, now, disposition); err != nil {
			return struct{}{}, err
		}
		status := tasks.OutboxPending
		if record.AttemptCount >= record.MaxAttempts {
			status = tasks.OutboxDead
		}
		result, err := tx.Exec(ctx, `
			UPDATE outbox_events
			SET status=$2,claim_token_hash=NULL,claimed_by=NULL,claim_expires_at=NULL,last_error=$3,
			    available_at=CASE WHEN $2='pending' THEN clock_timestamp()+make_interval(secs=>LEAST(600,5*power(2,GREATEST(attempt_count-1,0)))::double precision) ELSE available_at END,
			    updated_at=clock_timestamp()
			WHERE id=$1 AND status='claimed'
		`, disposition.EventID, status, disposition.Error)
		if err != nil {
			return struct{}{}, mapError(err)
		}
		if result.RowsAffected() != 1 {
			return struct{}{}, tasks.ErrConflict
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) RecoverOutbox(ctx context.Context, batch int) (int, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (int, error) {
		return recoverOutboxTx(ctx, tx, batch)
	})
}

func recoverOutboxTx(ctx context.Context, tx pgx.Tx, batch int) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,attempt_count,max_attempts FROM outbox_events
		WHERE status='claimed' AND claim_expires_at<=clock_timestamp()
		ORDER BY claim_expires_at,id FOR UPDATE SKIP LOCKED LIMIT $1
	`, batch)
	if err != nil {
		return 0, mapError(err)
	}
	type expired struct {
		id            int64
		attempts, max int
	}
	items := make([]expired, 0, batch)
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.attempts, &item.max); err != nil {
			rows.Close()
			return 0, mapError(err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, mapError(err)
	}
	rows.Close()
	for _, item := range items {
		status := tasks.OutboxPending
		if item.attempts >= item.max {
			status = tasks.OutboxDead
		}
		if _, err := tx.Exec(ctx, `
			UPDATE outbox_events
			SET status=$2,claim_token_hash=NULL,claimed_by=NULL,claim_expires_at=NULL,
			    last_error='claim_expired',available_at=clock_timestamp(),updated_at=clock_timestamp()
			WHERE id=$1 AND status='claimed'
		`, item.id, status); err != nil {
			return 0, mapError(err)
		}
	}
	return len(items), nil
}

func (s *Store) OutboxStats(ctx context.Context) (tasks.OutboxStats, error) {
	var stats tasks.OutboxStats
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE status='pending'),
		       COUNT(*) FILTER (WHERE status='claimed'),
		       COUNT(*) FILTER (WHERE status='published'),
		       COUNT(*) FILTER (WHERE status='dead')
		FROM outbox_events
	`).Scan(&stats.Pending, &stats.Claimed, &stats.Published, &stats.Dead)
	return stats, mapError(err)
}

func scanOutbox(row scanner) (tasks.OutboxEvent, error) {
	var value tasks.OutboxEvent
	var payload []byte
	err := row.Scan(&value.ID, &value.AggregateType, &value.AggregateID, &value.EventType, &value.SchemaVersion,
		&payload, &value.Status, &value.AvailableAt, &value.AttemptCount, &value.MaxAttempts,
		&value.ClaimedBy, &value.ClaimExpiresAt, &value.LastError, &value.CreatedAt, &value.UpdatedAt, &value.PublishedAt)
	if err != nil {
		return tasks.OutboxEvent{}, mapError(err)
	}
	if err := json.Unmarshal(payload, &value.Payload); err != nil {
		return tasks.OutboxEvent{}, fmt.Errorf("decode outbox payload: %w", err)
	}
	return value, nil
}

func lockOutbox(ctx context.Context, tx pgx.Tx, id int64) (tasks.OutboxEvent, string, time.Time, error) {
	var value tasks.OutboxEvent
	var payload []byte
	var hash string
	var now time.Time
	err := tx.QueryRow(ctx, `
		SELECT id,aggregate_type,aggregate_id,event_type,schema_version,payload,status,available_at,
		       attempt_count,max_attempts,claimed_by,claim_expires_at,last_error,created_at,updated_at,published_at,
		       COALESCE(claim_token_hash,''),clock_timestamp()
		FROM outbox_events WHERE id=$1 FOR UPDATE
	`, id).Scan(&value.ID, &value.AggregateType, &value.AggregateID, &value.EventType, &value.SchemaVersion,
		&payload, &value.Status, &value.AvailableAt, &value.AttemptCount, &value.MaxAttempts,
		&value.ClaimedBy, &value.ClaimExpiresAt, &value.LastError, &value.CreatedAt, &value.UpdatedAt, &value.PublishedAt,
		&hash, &now)
	if err != nil {
		return tasks.OutboxEvent{}, "", time.Time{}, mapError(err)
	}
	if err := json.Unmarshal(payload, &value.Payload); err != nil {
		return tasks.OutboxEvent{}, "", time.Time{}, fmt.Errorf("decode outbox payload: %w", err)
	}
	return value, hash, now, nil
}

func verifyOutboxClaim(record tasks.OutboxEvent, tokenHash string, now time.Time, disposition tasks.OutboxDisposition) error {
	if record.Status != tasks.OutboxClaimed || record.ClaimedBy == nil || *record.ClaimedBy != disposition.ConsumerID || record.ClaimExpiresAt == nil {
		return tasks.ErrLeaseMismatch
	}
	provided := hashToken(disposition.ClaimToken)
	if subtle.ConstantTimeCompare([]byte(tokenHash), []byte(provided)) != 1 {
		return tasks.ErrLeaseMismatch
	}
	if !record.ClaimExpiresAt.After(now) {
		return tasks.ErrLeaseExpired
	}
	return nil
}
