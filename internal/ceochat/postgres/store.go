// Package postgres is the durable PostgreSQL implementation of
// ceochat.Store (conversations and messages only -- see migration 000074).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("ceochat store requires an initialized PostgreSQL store")
	}
	if strings.TrimSpace(organizationID) == "" {
		return nil, errors.New("ceochat store requires an organization scope")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

func (s *Store) CreateConversation(ctx context.Context, conversation ceochat.Conversation) (ceochat.Conversation, error) {
	if strings.TrimSpace(conversation.OwnerRoleID) == "" {
		return ceochat.Conversation{}, fmt.Errorf("%w: owner role is required", ceochat.ErrInvalidInput)
	}
	status := conversation.Status
	if status == "" {
		status = ceochat.ConversationActive
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO ceo_chat_conversations (organization_id, owner_role_id, status)
		VALUES ($1, $2, $3)
		RETURNING id, organization_id, owner_role_id, status, created_at, updated_at`,
		s.organizationID, conversation.OwnerRoleID, string(status))
	return scanConversation(row)
}

func (s *Store) GetConversation(ctx context.Context, organizationID string, id int64) (ceochat.Conversation, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, owner_role_id, status, created_at, updated_at
		FROM ceo_chat_conversations WHERE id = $1 AND organization_id = $2`, id, organizationID)
	conversation, err := scanConversation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ceochat.Conversation{}, fmt.Errorf("%w: conversation %d", ceochat.ErrConversationNotFound, id)
	}
	return conversation, err
}

func scanConversation(row pgx.Row) (ceochat.Conversation, error) {
	var c ceochat.Conversation
	var status string
	if err := row.Scan(&c.ID, &c.OrganizationID, &c.OwnerRoleID, &status, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return ceochat.Conversation{}, err
	}
	c.Status = ceochat.ConversationStatus(status)
	return c, nil
}

// AppendMessage inserts one message and assigns its Sequence from
// max(sequence)+1 under the conversation row's own lock, so two concurrent
// Sends on the same conversation never race to the same sequence number.
// A unique-key collision on (conversation_id, idempotency_key) is reported
// as ceochat.ErrIdempotencyConflict; the caller decides reuse vs conflict by
// reading the row that won (see Service.recordOwnerMessage).
func (s *Store) AppendMessage(ctx context.Context, message ceochat.Message) (ceochat.Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ceochat.Message{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var locked int64
	if err = tx.QueryRow(ctx, `SELECT id FROM ceo_chat_conversations WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		message.ConversationID, s.organizationID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ceochat.Message{}, fmt.Errorf("%w: conversation %d", ceochat.ErrConversationNotFound, message.ConversationID)
		}
		return ceochat.Message{}, err
	}

	var nextSequence int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM ceo_chat_messages WHERE conversation_id = $1`,
		message.ConversationID).Scan(&nextSequence); err != nil {
		return ceochat.Message{}, err
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO ceo_chat_messages (
			conversation_id, organization_id, sequence, role, content, idempotency_key,
			task_id, attempt_id, run_id, correlation_id, causation_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, 0), NULLIF($9, ''), $10, NULLIF($11, ''))
		RETURNING id, conversation_id, organization_id, sequence, role, content,
			COALESCE(idempotency_key, ''), COALESCE(task_id, 0), COALESCE(attempt_id, 0),
			COALESCE(run_id, ''), correlation_id, COALESCE(causation_id, ''), created_at`,
		message.ConversationID, s.organizationID, nextSequence, string(message.Role), message.Content,
		nullIfEmpty(message.IdempotencyKey), message.TaskID, message.AttemptID, message.RunID,
		message.CorrelationID, message.CausationID)
	appended, scanErr := scanMessage(row)
	if scanErr != nil {
		if isUniqueViolation(scanErr) {
			return ceochat.Message{}, fmt.Errorf("%w: conversation %d", ceochat.ErrIdempotencyConflict, message.ConversationID)
		}
		return ceochat.Message{}, scanErr
	}
	if err = tx.Commit(ctx); err != nil {
		return ceochat.Message{}, err
	}
	return appended, nil
}

func (s *Store) FindOwnerMessage(ctx context.Context, conversationID int64, idempotencyKey string) (ceochat.Message, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, conversation_id, organization_id, sequence, role, content,
			COALESCE(idempotency_key, ''), COALESCE(task_id, 0), COALESCE(attempt_id, 0),
			COALESCE(run_id, ''), correlation_id, COALESCE(causation_id, ''), created_at
		FROM ceo_chat_messages
		WHERE conversation_id = $1 AND idempotency_key = $2 AND role = 'owner'`, conversationID, idempotencyKey)
	message, err := scanMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ceochat.Message{}, false, nil
	}
	if err != nil {
		return ceochat.Message{}, false, err
	}
	return message, true, nil
}

func (s *Store) FindAssistantReply(ctx context.Context, ownerMessageID int64) (ceochat.Message, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT a.id, a.conversation_id, a.organization_id, a.sequence, a.role, a.content,
			COALESCE(a.idempotency_key, ''), COALESCE(a.task_id, 0), COALESCE(a.attempt_id, 0),
			COALESCE(a.run_id, ''), a.correlation_id, COALESCE(a.causation_id, ''), a.created_at
		FROM ceo_chat_messages a
		JOIN ceo_chat_messages o ON o.id = $1
		WHERE a.role = 'assistant' AND a.task_id = o.task_id AND a.conversation_id = o.conversation_id
		ORDER BY a.sequence ASC
		LIMIT 1`, ownerMessageID)
	message, err := scanMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ceochat.Message{}, false, nil
	}
	if err != nil {
		return ceochat.Message{}, false, err
	}
	return message, true, nil
}

func (s *Store) ListMessages(ctx context.Context, conversationID int64, limit int) ([]ceochat.Message, error) {
	if limit <= 0 {
		limit = ceochat.DefaultHistoryMessageLimit
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, conversation_id, organization_id, sequence, role, content,
			COALESCE(idempotency_key, ''), COALESCE(task_id, 0), COALESCE(attempt_id, 0),
			COALESCE(run_id, ''), correlation_id, COALESCE(causation_id, ''), created_at
		FROM (
			SELECT * FROM ceo_chat_messages WHERE conversation_id = $1 ORDER BY sequence DESC LIMIT $2
		) recent
		ORDER BY sequence ASC`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []ceochat.Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func scanMessage(row pgx.Row) (ceochat.Message, error) {
	var m ceochat.Message
	var role string
	if err := row.Scan(&m.ID, &m.ConversationID, &m.OrganizationID, &m.Sequence, &role, &m.Content,
		&m.IdempotencyKey, &m.TaskID, &m.AttemptID, &m.RunID, &m.CorrelationID, &m.CausationID, &m.CreatedAt); err != nil {
		return ceochat.Message{}, err
	}
	m.Role = ceochat.MessageRole(role)
	return m, nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var _ ceochat.Store = (*Store)(nil)
