package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool            *pgxpool.Pool
	rateLimitMax    int
	rateLimitWindow time.Duration
	registryReader  registry.Reader
}

// New wires a registry.Reader into the Store specifically so Send can
// enforce topology (see topology.go's TopologyValidator: CEO<->leader,
// leader<->own-workers, worker->own-leader only -- worker->worker and
// cross-department edges are never permitted). Before this, ValidateEdge
// existed as a fully-implemented, fully-tested validator that Send never
// called (a bare "TODO: implement topology check" comment stood in its
// place) -- the two DENY rules it enforces were not actually applied to
// any real Send call.
func New(store *platformpostgres.Store, registryReader registry.Reader, rateLimitMax int, rateLimitWindow time.Duration) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("agentmessaging store requires initialized PostgreSQL")
	}
	if registryReader == nil {
		return nil, errors.New("agentmessaging store requires a registry reader for topology validation")
	}
	if rateLimitMax <= 0 {
		return nil, errors.New("agentmessaging store requires a positive rate limit")
	}
	if rateLimitWindow <= 0 {
		return nil, errors.New("agentmessaging store requires a positive rate limit window")
	}
	return &Store{pool: store.Pool(), rateLimitMax: rateLimitMax, rateLimitWindow: rateLimitWindow, registryReader: registryReader}, nil
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

// Send requires an authenticated execution principal whose dispatch_actor_role_id matches sender_role_id.
// Validates task ownership, topology constraints, and computes canonical request hash for idempotency integrity.
func (s *Store) Send(ctx context.Context, executionPrincipalID string, command agentmessaging.SendCommand, now time.Time) (agentmessaging.Message, bool, error) {
	if err := command.Validate(); err != nil {
		return agentmessaging.Message{}, false, err
	}
	now = now.UTC()

	// Validate execution principal is bound to sender role
	// This is a critical identity binding: principal.dispatch_actor_role_id must equal sender_role_id
	// If principal cannot be found or doesn't match, reject immediately - zero-trust authentication
	if _, err := s.validateExecutionPrincipalForSender(ctx, executionPrincipalID, command.OrganizationID, command.SenderRoleID); err != nil {
		return agentmessaging.Message{}, false, fmt.Errorf("principal validation failed: %w", err)
	}

	// Task ownership validation
	if err := s.validateTaskOwnership(ctx, command.OrganizationID, command.SenderTaskID, command.SenderRoleID); err != nil {
		return agentmessaging.Message{}, false, fmt.Errorf("sender task ownership invalid: %w", err)
	}

	// Recipient task validation if specified
	if command.RecipientTaskID != nil {
		if err := s.validateTaskOwnership(ctx, command.OrganizationID, *command.RecipientTaskID, command.RecipientRoleID); err != nil {
			return agentmessaging.Message{}, false, fmt.Errorf("recipient task ownership invalid: %w", err)
		}
	}

	// Topology validation (registry-derived edges): CEO<->leader,
	// leader<->own-workers, worker->own-leader only. Constructed fresh
	// per-call (cheap, stateless) rather than cached on Store, since
	// Store is not itself scoped to a single organization_id -- command.
	// OrganizationID is authoritative per Send call.
	if err := agentmessaging.NewTopologyValidator(s.registryReader, command.OrganizationID).ValidateEdge(ctx, command.SenderRoleID, command.RecipientRoleID); err != nil {
		return agentmessaging.Message{}, false, fmt.Errorf("topology validation failed: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return agentmessaging.Message{}, false, fmt.Errorf("begin send: %w", err)
	}
	defer tx.Rollback(ctx)

	var existingID int64
	err = tx.QueryRow(ctx, `SELECT id FROM agent_messages WHERE organization_id=$1 AND idempotency_key=$2`, command.OrganizationID, command.IdempotencyKey).Scan(&existingID)
	if err == nil {
		// Idempotency hit - verify hash matches
		existing, scanErr := scanMessage(tx.QueryRow(ctx, `SELECT `+messageColumns+` FROM agent_messages WHERE id=$1`, existingID))
		if scanErr != nil {
			return agentmessaging.Message{}, false, scanErr
		}
		// Compare against the hash this store derives from the command, which
		// is what the INSERT below persists. The previous comparison used
		// command.RequestHash -- a caller-supplied field that SendCommand.
		// Validate never checks and that no producer in this repository ever
		// populates. It was therefore always "", never equal to the stored
		// canonical hash, so every legitimate retry of an already-recorded
		// idempotency key returned ErrConflict instead of the existing
		// message: the deduplication this branch added was a hard failure
		// path rather than a replay guard.
		if existing.RequestHash != nil && *existing.RequestHash != computeCanonicalRequestHash(command) {
			// Same key but a genuinely different command: collision or attack.
			return agentmessaging.Message{}, false, agentmessaging.ErrConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return agentmessaging.Message{}, false, err
	}

	// Rate limiting check
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

	// Compute request hash for idempotency integrity
	requestHash := computeCanonicalRequestHash(command)

	message, err := scanMessage(tx.QueryRow(ctx, `
INSERT INTO agent_messages (
    organization_id, sender_role_id, sender_task_id, recipient_role_id, recipient_task_id,
    correlation_id, causation_id, message_type, payload, idempotency_key, max_attempts,
    request_hash, schema_version, payload_byte_size, available_at, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15,$15)
RETURNING `+messageColumns,
		command.OrganizationID, command.SenderRoleID, command.SenderTaskID, command.RecipientRoleID, command.RecipientTaskID,
		command.CorrelationID, command.CausationID, string(command.MessageType), []byte(command.Payload), command.IdempotencyKey, command.MaxAttempts,
		requestHash, command.SchemaVersion, len(command.Payload), now,
	))
	if err != nil {
		return agentmessaging.Message{}, false, err
	}
	return message, false, tx.Commit(ctx)
}

// validateExecutionPrincipalForSender verifies the execution principal exists, is active,
// and its dispatch_actor_role_id equals the command's sender_role_id.
func (s *Store) validateExecutionPrincipalForSender(ctx context.Context, principalID, expectedOrganizationID, expectedRoleID string) (string, error) {
	if strings.TrimSpace(principalID) == "" {
		return "", fmt.Errorf("execution principal ID required")
	}
	if strings.TrimSpace(expectedOrganizationID) == "" {
		return "", fmt.Errorf("organization ID required")
	}
	if strings.TrimSpace(expectedRoleID) == "" {
		return "", fmt.Errorf("sender role ID required")
	}

	// Cross-org DENY: model_execution_principals.organization_id must
	// match the SendCommand's own organization_id -- a role ID string
	// like "empresa/ceo" is not globally unique (organization_roles keys
	// on (organization_id, id)), so without this filter a principal
	// registered under one organization could authenticate a send for a
	// different organization whose role happens to share the same id.
	var actualRoleID string
	err := s.pool.QueryRow(ctx, `
SELECT dispatch_actor_role_id
FROM model_execution_principals
WHERE id = $1 AND organization_id = $2 AND status = 'active'`, principalID, expectedOrganizationID).Scan(&actualRoleID)

	if err == nil {
		if actualRoleID != expectedRoleID {
			return "", fmt.Errorf("principal %q dispatch_actor_role_id (%q) does not match sender_role_id (%q)", principalID, actualRoleID, expectedRoleID)
		}
		return actualRoleID, nil
	}
	return "", fmt.Errorf("execution principal not found, not active, or not in organization %q: %w", expectedOrganizationID, err)
}

// validateTaskOwnership verifies that a task exists in the given organization
// and its assigned_role_id matches the expected role.
func (s *Store) validateTaskOwnership(ctx context.Context, orgID string, taskID int64, expectedRoleID string) error {
	var taskOrgID, taskRoleID string
	err := s.pool.QueryRow(ctx, `
SELECT organization_id, assigned_role_id
FROM tasks
WHERE id = $1`, taskID).Scan(&taskOrgID, &taskRoleID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("task %d does not exist", taskID)
		}
		return fmt.Errorf("failed to query task: %w", err)
	}

	if taskOrgID != orgID {
		return fmt.Errorf("task %d belongs to organization %q, expected %q", taskID, taskOrgID, orgID)
	}
	if taskRoleID != expectedRoleID {
		return fmt.Errorf("task %d assigned to role %q, expected %q", taskID, taskRoleID, expectedRoleID)
	}
	return nil
}

// computeCanonicalRequestHash computes SHA-256 hash over semantically relevant fields
// in deterministic order for idempotency collision detection.
func computeCanonicalRequestHash(command agentmessaging.SendCommand) string {
	// Build canonical JSON with ALL semantic fields including max_attempts and schema_version
	// Order matters: consistent ordering ensures identical hashes for identical commands
	hashInput := map[string]interface{}{
		"schema_version":  command.SchemaVersion,
		"organization_id": command.OrganizationID,
		"sender_role_id":  command.SenderRoleID,
		"sender_task_id":  command.SenderTaskID,
		"max_attempts":    command.MaxAttempts,
	}

	// Recipient fields (may be null)
	if command.RecipientTaskID != nil {
		hashInput["recipient_task_id"] = *command.RecipientTaskID
	} else {
		hashInput["recipient_task_id"] = nil
	}
	hashInput["recipient_role_id"] = command.RecipientRoleID
	hashInput["correlation_id"] = command.CorrelationID
	hashInput["causation_id"] = command.CausationID
	hashInput["message_type"] = string(command.MessageType)
	hashInput["payload_canonical"] = string(command.Payload) // Raw bytes are already deterministic if marshaled properly
	hashInput["idempotency_key"] = command.IdempotencyKey

	canonicalBytes, _ := json.Marshal(hashInput)
	digest := sha256.Sum256(canonicalBytes)
	return hex.EncodeToString(digest[:])
}

// ClaimNext REQUIRES an authenticated execution principal. There is NO fallback to free-string consumerIDs.
// The principal's dispatch_actor_role_id MUST match recipientRoleID - this is the critical authentication boundary.
func (s *Store) ClaimNext(ctx context.Context, executionPrincipalID, organizationID, recipientRoleID string, batchSize int, claimDuration time.Duration, now time.Time) ([]agentmessaging.ClaimedMessage, error) {
	organizationID = strings.TrimSpace(organizationID)
	recipientRoleID = strings.TrimSpace(recipientRoleID)
	executionPrincipalID = strings.TrimSpace(executionPrincipalID)

	if organizationID == "" || recipientRoleID == "" || executionPrincipalID == "" || batchSize <= 0 || batchSize > 1000 || claimDuration <= 0 || now.IsZero() {
		return nil, fmt.Errorf("%w: organization, recipient, execution principal, batch size, claim duration, and now are required", agentmessaging.ErrInvalidRequest)
	}

	// CRITICAL AUTHENTICATION BOUNDARY: Principal must exist, be active, and have matching dispatch_actor_role_id
	principalDispatchRole, err := s.validateExecutionPrincipalForClaim(ctx, executionPrincipalID, organizationID, recipientRoleID)
	if err != nil {
		return nil, fmt.Errorf("principal validation failed: %w", err)
	}

	// Additional check: ensure principal role actually matches what we're claiming for
	// This double-check prevents any spoofing attempts
	if principalDispatchRole != recipientRoleID {
		return nil, fmt.Errorf("execution principal %q dispatch_actor_role_id (%q) does not match claimed recipient role (%q)", executionPrincipalID, principalDispatchRole, recipientRoleID)
	}

	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim next: %w", err)
	}
	defer tx.Rollback(ctx)

	// Recovery is part of claiming rather than a separate janitor contract:
	// any live consumer heals work abandoned by a crashed predecessor before
	// selecting new pending messages. FOR UPDATE SKIP LOCKED makes concurrent
	// consumers partition expired rows, while the surrounding transaction
	// prevents a recovered row from being claimed twice. attempt_count was
	// already incremented by the expired claim, so max-attempt messages become
	// dead instead of receiving one extra delivery.
	recoveryLimit := batchSize
	if recoveryLimit < 100 {
		recoveryLimit = 100
	}
	if _, err := tx.Exec(ctx, `
WITH expired AS (
    SELECT id
    FROM agent_messages
    WHERE organization_id=$1 AND recipient_role_id=$2 AND status='claimed'
      AND claim_expires_at<=$3
    ORDER BY claim_expires_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $4
)
UPDATE agent_messages AS m
SET status=CASE WHEN m.attempt_count>=m.max_attempts THEN 'dead' ELSE 'pending' END,
    claim_token_hash=NULL, claimed_by=NULL, claim_expires_at=NULL,
    last_error='claim lease expired',
    available_at=CASE WHEN m.attempt_count>=m.max_attempts THEN m.available_at ELSE $3 END,
    updated_at=$3
FROM expired
WHERE m.id=expired.id`, organizationID, recipientRoleID, now, recoveryLimit); err != nil {
		return nil, fmt.Errorf("recover expired claims: %w", err)
	}

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
		// Use execution principal ID as claimed_by instead of free-form consumerID
		message, err := scanMessage(tx.QueryRow(ctx, `
UPDATE agent_messages
SET status='claimed', attempt_count=attempt_count+1, claim_token_hash=$2, claimed_by=$3,
    claim_expires_at=$4, updated_at=$5
WHERE id=$1 AND status='pending'
RETURNING `+messageColumns, id, hash, executionPrincipalID, now.Add(claimDuration), now))
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, agentmessaging.ClaimedMessage{Message: message, ClaimToken: plain})
	}
	return claimed, tx.Commit(ctx)
}

// validateExecutionPrincipalForClaim verifies the execution principal exists, is active,
// and its dispatch_actor_role_id matches recipientRoleID.
func (s *Store) validateExecutionPrincipalForClaim(ctx context.Context, principalID, expectedOrganizationID, expectedRoleID string) (string, error) {
	if strings.TrimSpace(principalID) == "" {
		return "", fmt.Errorf("execution principal ID required")
	}
	if strings.TrimSpace(expectedOrganizationID) == "" {
		return "", fmt.Errorf("organization ID required")
	}
	if strings.TrimSpace(expectedRoleID) == "" {
		return "", fmt.Errorf("recipient role ID required")
	}

	// Cross-org DENY: same reasoning as validateExecutionPrincipalForSender.
	var actualRoleID string
	err := s.pool.QueryRow(ctx, `
SELECT dispatch_actor_role_id
FROM model_execution_principals
WHERE id = $1 AND organization_id = $2 AND status = 'active'`, principalID, expectedOrganizationID).Scan(&actualRoleID)

	if err == nil {
		if actualRoleID != expectedRoleID {
			return "", fmt.Errorf("principal %q dispatch_actor_role_id (%q) does not match recipient role (%q)", principalID, actualRoleID, expectedRoleID)
		}
		return actualRoleID, nil
	}
	return "", fmt.Errorf("execution principal not found, not active, or not in organization %q: %w", expectedOrganizationID, err)
}

// Ack requires executionPrincipalID matching the principal that performed ClaimNext.
// The claimed_by field must equal this principal, and the principal must still be active.
func (s *Store) Ack(ctx context.Context, executionPrincipalID string, disposition agentmessaging.Disposition, now time.Time) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ack: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := verifyClaimWithPrincipal(ctx, tx, executionPrincipalID, fmt.Sprint(disposition.MessageID), disposition.ClaimToken, now); err != nil {
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

// Nack requires executionPrincipalID matching the principal that performed ClaimNext.
func (s *Store) Nack(ctx context.Context, executionPrincipalID string, disposition agentmessaging.Disposition, now time.Time) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin nack: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := verifyClaimWithPrincipal(ctx, tx, executionPrincipalID, fmt.Sprint(disposition.MessageID), disposition.ClaimToken, now); err != nil {
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

// verifyClaimWithPrincipal verifies both the token AND the execution principal.
// This is a stricter version of verifyClaim - it requires the principal to match claimed_by
// AND the principal to still be active at settlement time.
func verifyClaimWithPrincipal(ctx context.Context, tx pgx.Tx, executionPrincipalID, messageID, claimToken string, now time.Time) error {
	var status string
	var claimedBy *string
	var claimExpiresAt *time.Time
	var tokenHash string

	// First, lock and read the message row
	if err := tx.QueryRow(ctx, `SELECT status, claimed_by, claim_expires_at, COALESCE(claim_token_hash,'') FROM agent_messages WHERE id=$1 FOR UPDATE`, messageID).
		Scan(&status, &claimedBy, &claimExpiresAt, &tokenHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentmessaging.ErrNotFound
		}
		return err
	}

	// Check basic state conditions
	if status != string(agentmessaging.StatusClaimed) {
		return agentmessaging.ErrConflict
	}
	if claimedBy == nil {
		return agentmessaging.ErrConflict
	}
	if claimExpiresAt == nil {
		return agentmessaging.ErrConflict
	}

	// CRITICAL: claimed_by must match executionPrincipalID (the principal that did ClaimNext)
	if *claimedBy != executionPrincipalID {
		return agentmessaging.ErrConflict
	}

	// Token verification
	provided := hashToken(claimToken)
	if subtle.ConstantTimeCompare([]byte(tokenHash), []byte(provided)) != 1 {
		return agentmessaging.ErrConflict
	}

	// Lease expiration check
	if !claimExpiresAt.After(now) {
		return agentmessaging.ErrClaimExpired
	}

	// Additional security: verify the principal still exists and is active
	// If principal was revoked/disabled mid-lease, settlement should fail
	var isActive bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_execution_principals WHERE id=$1 AND status='active')`, executionPrincipalID).Scan(&isActive)
	if err != nil {
		return fmt.Errorf("failed to verify principal status: %w", err)
	}
	if !isActive {
		return agentmessaging.ErrConflict // Principal no longer active
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
