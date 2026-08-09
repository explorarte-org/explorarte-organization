package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool            *pgxpool.Pool
	rateLimitMax    int
	rateLimitWindow time.Duration
}

func New(store *platformpostgres.Store, rateLimitMax int, rateLimitWindow time.Duration) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("agentmessaging store requires initialized PostgreSQL")
	}
	if rateLimitMax <= 0 {
		return nil, errors.New("agentmessaging store requires a positive rate limit")
	}
	if rateLimitWindow <= 0 {
		return nil, errors.New("agentmessaging store requires a positive rate limit window")
	}
	return &Store{pool: store.Pool(), rateLimitMax: rateLimitMax, rateLimitWindow: rateLimitWindow}, nil
}

var _ agentmessaging.Ledger = (*Store)(nil)

const messageColumns = `id, organization_id, sender_role_id, sender_task_id, recipient_role_id, recipient_task_id,
correlation_id, causation_id, message_type, payload, idempotency_key, status, attempt_count, max_attempts,
claimed_by, claim_expires_at, last_error, available_at, created_at, updated_at, delivered_at`

func scanMessage(row pgx.Row) (agentmessaging.Message, error) {
	var m agentmessaging.Message
	if err := row.Scan(
		&m.ID, &m.OrganizationID, &m.SenderRoleID, &m.SenderTaskID, &m.RecipientRoleID, &m.RecipientTaskID,
		&m.CorrelationID, &m.CausationID, &m.MessageType, &m.Payload, &m.IdempotencyKey, &m.Status, &m.AttemptCount, &m.MaxAttempts,
		&m.ClaimedBy, &m.ClaimExpiresAt, &m.LastError, &m.AvailableAt, &m.CreatedAt, &m.UpdatedAt, &m.DeliveredAt,
	); err != nil {
		return agentmessaging.Message{}, err
	}
	return m, nil
}

func (s *Store) Send(ctx context.Context, command agentmessaging.SendCommand, now time.Time) (agentmessaging.Message, bool, error) {
	if err := command.Validate(); err != nil {
		return agentmessaging.Message{}, false, err
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return agentmessaging.Message{}, false, fmt.Errorf("begin send: %w", err)
	}
	defer tx.Rollback(ctx)

	var existingID int64
	err = tx.QueryRow(ctx, `SELECT id FROM agent_messages WHERE organization_id=$1 AND idempotency_key=$2`, command.OrganizationID, command.IdempotencyKey).Scan(&existingID)
	if err == nil {
		existing, scanErr := scanMessage(tx.QueryRow(ctx, `SELECT `+messageColumns+` FROM agent_messages WHERE id=$1`, existingID))
		if scanErr != nil {
			return agentmessaging.Message{}, false, scanErr
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return agentmessaging.Message{}, false, err
	}

	var sent int
	windowStart := now.Add(-s.rateLimitWindow)
	if err := tx.QueryRow(ctx, `
SELECT count(*) FROM agent_messages
WHERE organization_id=$1 AND sender_role_id=$2 AND recipient_role_id=$3 AND created_at >= $4`,
		command.OrganizationID, command.SenderRoleID, command.RecipientRoleID, windowStart).Scan(&sent); err != nil {
		return agentmessaging.Message{}, false, err
	}
	if sent >= s.rateLimitMax {
		return agentmessaging.Message{}, false, fmt.Errorf("%w: %s->%s exceeded %d messages per %s", agentmessaging.ErrRateLimited, command.SenderRoleID, command.RecipientRoleID, s.rateLimitMax, s.rateLimitWindow)
	}

	message, err := scanMessage(tx.QueryRow(ctx, `
INSERT INTO agent_messages (
    organization_id, sender_role_id, sender_task_id, recipient_role_id, recipient_task_id,
    correlation_id, causation_id, message_type, payload, idempotency_key, max_attempts, available_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,$12)
RETURNING `+messageColumns,
		command.OrganizationID, command.SenderRoleID, command.SenderTaskID, command.RecipientRoleID, command.RecipientTaskID,
		command.CorrelationID, command.CausationID, string(command.MessageType), []byte(command.Payload), command.IdempotencyKey, command.MaxAttempts, now,
	))
	if err != nil {
		return agentmessaging.Message{}, false, err
	}
	return message, false, tx.Commit(ctx)
}

func (s *Store) ClaimNext(ctx context.Context, organizationID, recipientRoleID, consumerID string, batchSize int, claimDuration time.Duration, now time.Time) ([]agentmessaging.ClaimedMessage, error) {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim next: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT id FROM agent_messages
WHERE organization_id=$1 AND recipient_role_id=$2 AND status='pending' AND available_at<=$3
ORDER BY available_at, created_at, id
FOR UPDATE SKIP LOCKED LIMIT $4`, organizationID, recipientRoleID, now, batchSize)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, batchSize)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	claimed := make([]agentmessaging.ClaimedMessage, 0, len(ids))
	for _, id := range ids {
		plain, hash, err := newToken()
		if err != nil {
			return nil, err
		}
		message, err := scanMessage(tx.QueryRow(ctx, `
UPDATE agent_messages
SET status='claimed', attempt_count=attempt_count+1, claim_token_hash=$2, claimed_by=$3,
    claim_expires_at=$4, updated_at=$5
WHERE id=$1 AND status='pending'
RETURNING `+messageColumns, id, hash, consumerID, now.Add(claimDuration), now))
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, agentmessaging.ClaimedMessage{Message: message, ClaimToken: plain})
	}
	return claimed, tx.Commit(ctx)
}

func (s *Store) Ack(ctx context.Context, disposition agentmessaging.Disposition, now time.Time) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ack: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := verifyClaim(ctx, tx, disposition); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
UPDATE agent_messages
SET status='delivered', claim_token_hash=NULL, claimed_by=NULL, claim_expires_at=NULL,
    delivered_at=$2, updated_at=$2, last_error=NULL
WHERE id=$1 AND status='claimed'`, disposition.MessageID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return agentmessaging.ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *Store) Nack(ctx context.Context, disposition agentmessaging.Disposition, now time.Time) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin nack: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := verifyClaim(ctx, tx, disposition); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
UPDATE agent_messages
SET status=CASE WHEN attempt_count>=max_attempts THEN 'dead' ELSE 'pending' END,
    claim_token_hash=NULL, claimed_by=NULL, claim_expires_at=NULL, last_error=$3,
    available_at=CASE WHEN attempt_count>=max_attempts THEN available_at ELSE $2 END,
    updated_at=$2
WHERE id=$1 AND status='claimed'`, disposition.MessageID, now, nullableString(disposition.Error))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return agentmessaging.ErrConflict
	}
	return tx.Commit(ctx)
}

func verifyClaim(ctx context.Context, tx pgx.Tx, disposition agentmessaging.Disposition) error {
	var status string
	var claimedBy *string
	var claimExpiresAt *time.Time
	var tokenHash string
	if err := tx.QueryRow(ctx, `SELECT status, claimed_by, claim_expires_at, COALESCE(claim_token_hash,'') FROM agent_messages WHERE id=$1 FOR UPDATE`, disposition.MessageID).
		Scan(&status, &claimedBy, &claimExpiresAt, &tokenHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentmessaging.ErrNotFound
		}
		return err
	}
	if status != string(agentmessaging.StatusClaimed) || claimedBy == nil || *claimedBy != disposition.ConsumerID || claimExpiresAt == nil {
		return agentmessaging.ErrConflict
	}
	provided := hashToken(disposition.ClaimToken)
	if subtle.ConstantTimeCompare([]byte(tokenHash), []byte(provided)) != 1 {
		return agentmessaging.ErrConflict
	}
	return nil
}

func newToken() (plain, hash string, err error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", fmt.Errorf("generate cryptographic token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buffer)
	return plain, hashToken(plain), nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
