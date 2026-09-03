package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

// insertProviderOutcome persists a ProviderOutcome, including its Gate F
// (Provider Failure Telemetry) fields. phase records which of the three
// modelruntime.AdapterFailurePhase stages this outcome was observed at --
// it is the caller's responsibility (see each Store method below) since it
// follows directly from which state transition is being persisted, not from
// anything decodable off the outcome value itself. An empty phase is stored
// as NULL; model_provider_outcomes.provider_reached is then a generated
// column derived 1:1 from phase <> 'before_request' (NULL phase, i.e. the
// ordinary success path via MarkResponseReceived, is treated as reached).
func insertProviderOutcome(ctx context.Context, tx pgx.Tx, invocationID, attemptID int64, outcome modelruntime.ProviderOutcome, phase modelruntime.AdapterFailurePhase) error {
	if err := outcome.Validate(); err != nil {
		return err
	}
	var httpStatus any
	if outcome.HTTPStatus > 0 {
		httpStatus = outcome.HTTPStatus
	}
	var requestDurationMS any
	if outcome.RequestDuration != nil {
		requestDurationMS = outcome.RequestDuration.Milliseconds()
	}
	tag, err := tx.Exec(ctx, `
INSERT INTO model_provider_outcomes(
    provider_request_record_id,organization_id,invocation_id,dispatch_attempt_id,
    outcome_classification,provider_request_id,http_status,error_class,error_code,
    retryable,response_hash,response_schema_version,cancellation_confirmed,
    adapter_failure_phase,finish_reason,response_content_bytes,usage_available,
    input_tokens,output_tokens,cache_hit_tokens,cache_miss_tokens,
    response_format,max_output_tokens,request_duration_ms,
    json_error_class,json_error_offset,starts_with_json_object,ends_with_json_object
)
SELECT r.id,r.organization_id,r.invocation_id,r.dispatch_attempt_id,
       $3,NULLIF($4,''),$5,NULLIF($6,''),NULLIF($7,''),$8,NULLIF($9,''),$10,$11,
       NULLIF($12,''),NULLIF($13,''),$14,$15,
       $16,$17,$18,$19,
       NULLIF($20,''),$21,$22,
       NULLIF($23,''),$24,$25,$26
FROM model_provider_requests r
WHERE r.invocation_id=$1 AND r.dispatch_attempt_id=$2`,
		invocationID, attemptID, outcome.OutcomeClassification, outcome.ProviderRequestID,
		httpStatus, outcome.ErrorClass, outcome.ErrorCode, outcome.Retryable,
		outcome.ResponseHash, outcome.ResponseSchemaVersion, outcome.CancellationConfirmed,
		string(phase), outcome.FinishReason, outcome.ResponseContentBytes, outcome.UsageAvailable,
		outcome.InputTokens, outcome.OutputTokens, outcome.CacheHitTokens, outcome.CacheMissTokens,
		outcome.ResponseFormat, outcome.MaxOutputTokens, requestDurationMS,
		outcome.JSONErrorClass, outcome.JSONErrorOffset, outcome.StartsWithJSONObject, outcome.EndsWithJSONObject)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() != 1 {
		return modelruntime.ErrConflict
	}
	return nil
}

// insertRecoveredUsage persists a Usage row for an invocation that is being
// recorded as a business failure (RejectProviderResponse/FailAfterResponse)
// but whose provider-reported token counts were nonetheless recovered — see
// modelruntime.FailureCommand.Usage's doc comment. Mirrors the usage insert
// CompleteInvocation already does on the success path, including the
// nullable cache-hit/miss columns.
func insertRecoveredUsage(ctx context.Context, tx pgx.Tx, usage *modelruntime.Usage) error {
	if usage == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO model_invocation_usage(
    invocation_id,dispatch_attempt_id,input_tokens,output_tokens,total_tokens,provider_reported,
    prompt_cache_hit_tokens,prompt_cache_miss_tokens
) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (invocation_id) DO NOTHING`,
		usage.InvocationID, usage.DispatchAttemptID, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, usage.ProviderReported,
		usage.PromptCacheHitTokens, usage.PromptCacheMissTokens,
	); err != nil {
		return mapError(err)
	}
	return nil
}

func (s *Store) MarkResponseReceived(ctx context.Context, invocationID, attemptID int64, token string, outcome modelruntime.ProviderOutcome) (modelruntime.Invocation, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.Invocation, error) {
		if err := lockInvocation(ctx, tx, invocationID); err != nil {
			return modelruntime.Invocation{}, err
		}
		attempt, err := verifyToken(ctx, tx, attemptID, token)
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if attempt.InvocationID != invocationID || attempt.Status != modelruntime.DispatchSendStarted {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		if outcome.OutcomeClassification != modelruntime.ProviderOutcomeResponseReceived {
			return modelruntime.Invocation{}, modelruntime.ErrInvalidRequest
		}
		if err = insertProviderOutcome(ctx, tx, invocationID, attemptID, outcome, modelruntime.AdapterFailureResponseReceived); err != nil {
			return modelruntime.Invocation{}, err
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='response_received',
    response_received_at=clock_timestamp(),
    provider_request_id=NULLIF($2,''),
    retry_safety='not_retryable'
WHERE id=$1`, attemptID, outcome.ProviderRequestID); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='response_received',updated_at=clock_timestamp()
WHERE id=$1 AND status='send_started'
RETURNING `+invocationColumns, invocationID))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		return invocation, nil
	})
}

func (s *Store) RejectProviderResponse(ctx context.Context, command modelruntime.FailureCommand, outcome modelruntime.ProviderOutcome, outboxMax int) (modelruntime.Invocation, error) {
	if outcome.OutcomeClassification != modelruntime.ProviderOutcomeRejected {
		return modelruntime.Invocation{}, modelruntime.ErrInvalidRequest
	}
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.Invocation, error) {
		if err := lockInvocation(ctx, tx, command.InvocationID); err != nil {
			return modelruntime.Invocation{}, err
		}
		attempt, err := verifyToken(ctx, tx, command.DispatchAttemptID, command.ClaimToken)
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if attempt.InvocationID != command.InvocationID || attempt.Status != modelruntime.DispatchSendStarted {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		if err = insertProviderOutcome(ctx, tx, command.InvocationID, command.DispatchAttemptID, outcome, modelruntime.AdapterFailureResponseReceived); err != nil {
			return modelruntime.Invocation{}, err
		}
		if err = insertRecoveredUsage(ctx, tx, command.Usage); err != nil {
			return modelruntime.Invocation{}, err
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='completed',response_received_at=clock_timestamp(),provider_request_id=NULLIF($2,''),
    retry_safety='not_retryable',outcome_classification=NULLIF($3,''),error_code=NULLIF($4,''),
    finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID, outcome.ProviderRequestID, command.OutcomeClassification, command.ErrorCode); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='failed',error_code=NULLIF($2,''),updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1 AND status='send_started'
RETURNING `+invocationColumns, command.InvocationID, command.ErrorCode))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationFailed, "service", attempt.ClaimedBy, true, outboxMax, map[string]any{
			"dispatch_attempt_id":    attempt.ID,
			"outcome_classification": command.OutcomeClassification,
			"provider_http_status":   outcome.HTTPStatus,
			"provider_error_class":   outcome.ErrorClass,
			"provider_error_code":    outcome.ErrorCode,
			"provider_retryable":     outcome.Retryable,
		}); err != nil {
			return modelruntime.Invocation{}, err
		}
		return invocation, nil
	})
}

func (s *Store) FailCommittedBeforeRequest(ctx context.Context, command modelruntime.FailureCommand, outcome modelruntime.ProviderOutcome, outboxMax int) (modelruntime.Invocation, error) {
	if outcome.OutcomeClassification != modelruntime.ProviderOutcomeNotSent {
		return modelruntime.Invocation{}, modelruntime.ErrInvalidRequest
	}
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.Invocation, error) {
		if err := lockInvocation(ctx, tx, command.InvocationID); err != nil {
			return modelruntime.Invocation{}, err
		}
		attempt, err := verifyToken(ctx, tx, command.DispatchAttemptID, command.ClaimToken)
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if attempt.InvocationID != command.InvocationID || attempt.Status != modelruntime.DispatchSendStarted {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		if err = insertProviderOutcome(ctx, tx, command.InvocationID, command.DispatchAttemptID, outcome, modelruntime.AdapterFailureBeforeRequest); err != nil {
			return modelruntime.Invocation{}, err
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='failed_before_send',retry_safety='safe_before_send',
    outcome_classification=NULLIF($2,''),error_code=NULLIF($3,''),finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID, command.OutcomeClassification, command.ErrorCode); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='failed',error_code=NULLIF($2,''),updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1 AND status='send_started'
RETURNING `+invocationColumns, command.InvocationID, command.ErrorCode))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationFailed, "service", attempt.ClaimedBy, true, outboxMax, map[string]any{
			"dispatch_attempt_id":    attempt.ID,
			"outcome_classification": command.OutcomeClassification,
			"provider_error_class":   outcome.ErrorClass,
			"provider_error_code":    outcome.ErrorCode,
		}); err != nil {
			return modelruntime.Invocation{}, err
		}
		return invocation, nil
	})
}

func (s *Store) CompleteInvocation(ctx context.Context, command modelruntime.CompletionCommand, outboxMax int) (modelruntime.DispatchResult, error) {
	if command.Response.Result.InvocationID != command.InvocationID ||
		command.Response.Result.DispatchAttemptID != command.DispatchAttemptID ||
		command.Response.Usage.InvocationID != command.InvocationID ||
		command.Response.Usage.DispatchAttemptID != command.DispatchAttemptID {
		return modelruntime.DispatchResult{}, fmt.Errorf("%w: normalized result scope mismatch", modelruntime.ErrInvalidRequest)
	}
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.DispatchResult, error) {
		if err := lockInvocation(ctx, tx, command.InvocationID); err != nil {
			return modelruntime.DispatchResult{}, err
		}
		attempt, err := verifyToken(ctx, tx, command.DispatchAttemptID, command.ClaimToken)
		if err != nil {
			return modelruntime.DispatchResult{}, err
		}
		if attempt.InvocationID != command.InvocationID || attempt.Status != modelruntime.DispatchResponseReceived {
			return modelruntime.DispatchResult{}, modelruntime.ErrConflict
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE id=$1 FOR UPDATE`, command.InvocationID))
		if err != nil {
			return modelruntime.DispatchResult{}, err
		}
		if invocation.Status != modelruntime.InvocationResponseReceived {
			return modelruntime.DispatchResult{}, modelruntime.ErrConflict
		}
		if command.Response.Result.OutputMode != invocation.OutputMode {
			return modelruntime.DispatchResult{}, fmt.Errorf("%w: normalized output mode mismatch", modelruntime.ErrInvalidRequest)
		}

		toolBody, err := json.Marshal(command.Response.Result.ToolIntents)
		if err != nil {
			return modelruntime.DispatchResult{}, err
		}
		var textOutput any
		var jsonOutput any
		if command.Response.Result.OutputMode == modelruntime.OutputText {
			textOutput = command.Response.Result.TextOutput
		} else {
			jsonOutput = string(command.Response.Result.JSONOutput)
		}
		var resultID int64
		var createdAt time.Time
		if err = tx.QueryRow(ctx, `
INSERT INTO model_invocation_results(
    invocation_id,dispatch_attempt_id,output_mode,text_output,json_output,
    tool_intents,response_hash,response_bytes
) VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7,$8)
RETURNING id,created_at`,
			invocation.ID,
			attempt.ID,
			command.Response.Result.OutputMode,
			textOutput,
			jsonOutput,
			toolBody,
			command.Response.Result.ResponseHash,
			command.Response.Result.ResponseBytes,
		).Scan(&resultID, &createdAt); err != nil {
			return modelruntime.DispatchResult{}, mapError(err)
		}
		// Reasoning is written here, in the same transaction as the result it
		// explains, so a result can never exist without its justification or
		// a justification without its result.
		//
		// It goes to its own table and nowhere else. It is not part of the
		// result row, so it cannot reach response hashing; it is not in the
		// outbox payload, so it cannot leave the system; and no context
		// projection reads this table, so it cannot travel back into a model
		// input. Those three exclusions are what let ORGANIZATIONAL data be
		// kept at all.
		if reasoning := command.Response.RoleReasoning; len(reasoning) > 0 {
			if _, err = tx.Exec(ctx, `
INSERT INTO model_invocation_reasoning(
    invocation_id,dispatch_attempt_id,content,content_hash,content_bytes
) VALUES($1,$2,$3,$4,$5)
ON CONFLICT (invocation_id) DO NOTHING`,
				invocation.ID, attempt.ID, reasoning,
				modelruntime.SHA256Bytes(reasoning), len(reasoning),
			); err != nil {
				return modelruntime.DispatchResult{}, mapError(err)
			}
		}
		// prompt_cache_hit_tokens/prompt_cache_miss_tokens: fixed R9.1 --
		// normalizer.go's Normalize now copies
		// RawResponse.PromptCacheHitTokens/PromptCacheMissTokens onto the
		// Usage it returns, so this success path is populated the same as
		// the business-failure paths (insertRecoveredUsage) already were.
		if _, err = tx.Exec(ctx, `
INSERT INTO model_invocation_usage(
    invocation_id,dispatch_attempt_id,input_tokens,output_tokens,total_tokens,provider_reported,
    prompt_cache_hit_tokens,prompt_cache_miss_tokens
) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			invocation.ID,
			attempt.ID,
			command.Response.Usage.InputTokens,
			command.Response.Usage.OutputTokens,
			command.Response.Usage.TotalTokens,
			command.Response.Usage.ProviderReported,
			command.Response.Usage.PromptCacheHitTokens,
			command.Response.Usage.PromptCacheMissTokens,
		); err != nil {
			return modelruntime.DispatchResult{}, mapError(err)
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='completed',retry_safety='not_retryable',
    outcome_classification='succeeded',finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID); err != nil {
			return modelruntime.DispatchResult{}, mapError(err)
		}
		invocation, err = scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='succeeded',error_code=NULL,updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1
RETURNING `+invocationColumns, invocation.ID))
		if err != nil {
			return modelruntime.DispatchResult{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationSucceeded, "service", attempt.ClaimedBy, true, outboxMax, map[string]any{
			"dispatch_attempt_id": attempt.ID,
			"response_hash":       command.Response.Result.ResponseHash,
			"response_bytes":      command.Response.Result.ResponseBytes,
			"tool_intent_count":   len(command.Response.Result.ToolIntents),
		}); err != nil {
			return modelruntime.DispatchResult{}, err
		}
		result := command.Response.Result
		result.ID = resultID
		result.CreatedAt = createdAt
		usage := command.Response.Usage
		return modelruntime.DispatchResult{Invocation: invocation, Result: &result, Usage: &usage}, nil
	})
}

func (s *Store) FailBeforeSend(ctx context.Context, command modelruntime.FailureCommand, outboxMax int) (modelruntime.Invocation, error) {
	eventType := command.EventType
	if eventType == "" {
		eventType = modelruntime.AuditInvocationFailed
	}
	if eventType != modelruntime.AuditInvocationFailed && eventType != modelruntime.AuditInvocationTimedOut {
		return modelruntime.Invocation{}, fmt.Errorf("%w: invalid pre-send failure event", modelruntime.ErrInvalidRequest)
	}
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.Invocation, error) {
		if err := lockInvocation(ctx, tx, command.InvocationID); err != nil {
			return modelruntime.Invocation{}, err
		}
		attempt, err := verifyToken(ctx, tx, command.DispatchAttemptID, command.ClaimToken)
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if attempt.InvocationID != command.InvocationID || attempt.Status != modelruntime.DispatchClaimed {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='failed_before_send',retry_safety='safe_before_send',
    outcome_classification=NULLIF($2,''),error_code=NULLIF($3,''),finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID, command.OutcomeClassification, command.ErrorCode); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='failed',error_code=NULLIF($2,''),updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1
RETURNING `+invocationColumns, command.InvocationID, command.ErrorCode))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, eventType, "service", attempt.ClaimedBy, true, outboxMax, map[string]any{
			"dispatch_attempt_id":    attempt.ID,
			"outcome_classification": command.OutcomeClassification,
		}); err != nil {
			return modelruntime.Invocation{}, err
		}
		return invocation, nil
	})
}

func (s *Store) FailAfterResponse(ctx context.Context, command modelruntime.FailureCommand, outboxMax int) (modelruntime.Invocation, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.Invocation, error) {
		if err := lockInvocation(ctx, tx, command.InvocationID); err != nil {
			return modelruntime.Invocation{}, err
		}
		attempt, err := verifyToken(ctx, tx, command.DispatchAttemptID, command.ClaimToken)
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if attempt.InvocationID != command.InvocationID || attempt.Status != modelruntime.DispatchResponseReceived {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		if err = insertRecoveredUsage(ctx, tx, command.Usage); err != nil {
			return modelruntime.Invocation{}, err
		}
		// G3-004: the outcome row for this attempt was already inserted by
		// MarkResponseReceived (the provider answered fine; normalization is
		// what just failed) -- this UPDATEs that existing row rather than
		// inserting a second one, and is a no-op WHERE clause match (0 rows,
		// no error) on every call site that never sets this field.
		if command.NormalizationFailureRawContent != nil {
			if _, err = tx.Exec(ctx, `
UPDATE model_provider_outcomes
SET normalization_failure_raw_content=$2
WHERE dispatch_attempt_id=$1`, command.DispatchAttemptID, command.NormalizationFailureRawContent); err != nil {
				return modelruntime.Invocation{}, mapError(err)
			}
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='completed',retry_safety='not_retryable',
    outcome_classification=NULLIF($2,''),error_code=NULLIF($3,''),finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID, command.OutcomeClassification, command.ErrorCode); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='failed',error_code=NULLIF($2,''),updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1 AND status='response_received'
RETURNING `+invocationColumns, command.InvocationID, command.ErrorCode))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationFailed, "service", attempt.ClaimedBy, true, outboxMax, map[string]any{
			"dispatch_attempt_id":    attempt.ID,
			"outcome_classification": command.OutcomeClassification,
		}); err != nil {
			return modelruntime.Invocation{}, err
		}
		return invocation, nil
	})
}

func (s *Store) MarkAmbiguous(ctx context.Context, command modelruntime.FailureCommand, eventType string, outboxMax int) (modelruntime.Invocation, error) {
	if eventType != modelruntime.AuditInvocationAmbiguous && eventType != modelruntime.AuditInvocationTimedOut {
		return modelruntime.Invocation{}, fmt.Errorf("%w: invalid ambiguity event", modelruntime.ErrInvalidRequest)
	}
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.Invocation, error) {
		if err := lockInvocation(ctx, tx, command.InvocationID); err != nil {
			return modelruntime.Invocation{}, err
		}
		attempt, err := verifyToken(ctx, tx, command.DispatchAttemptID, command.ClaimToken)
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if attempt.InvocationID != command.InvocationID || attempt.Status != modelruntime.DispatchSendStarted {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		if command.ProviderOutcome != nil {
			if err = insertProviderOutcome(ctx, tx, command.InvocationID, command.DispatchAttemptID, *command.ProviderOutcome, modelruntime.AdapterFailureAmbiguous); err != nil {
				return modelruntime.Invocation{}, err
			}
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='ambiguous',retry_safety='not_retryable',
    outcome_classification=NULLIF($2,''),error_code=NULLIF($3,''),finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID, command.OutcomeClassification, command.ErrorCode); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='ambiguous',error_code=NULLIF($2,''),updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1 AND status='send_started'
RETURNING `+invocationColumns, command.InvocationID, command.ErrorCode))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, eventType, "service", attempt.ClaimedBy, true, outboxMax, map[string]any{
			"dispatch_attempt_id":    attempt.ID,
			"outcome_classification": command.OutcomeClassification,
		}); err != nil {
			return modelruntime.Invocation{}, err
		}
		return invocation, nil
	})
}

func (s *Store) MarkCancelled(ctx context.Context, command modelruntime.FailureCommand, outboxMax int) (modelruntime.Invocation, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.Invocation, error) {
		if err := lockInvocation(ctx, tx, command.InvocationID); err != nil {
			return modelruntime.Invocation{}, err
		}
		attempt, err := verifyToken(ctx, tx, command.DispatchAttemptID, command.ClaimToken)
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if attempt.InvocationID != command.InvocationID || (attempt.Status != modelruntime.DispatchSendStarted && attempt.Status != modelruntime.DispatchResponseReceived) {
			return modelruntime.Invocation{}, modelruntime.ErrConflict
		}
		var cancellationRequested bool
		if err = tx.QueryRow(ctx, `SELECT cancel_requested_at IS NOT NULL FROM model_invocations WHERE id=$1 FOR UPDATE`, command.InvocationID).Scan(&cancellationRequested); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		if !cancellationRequested {
			return modelruntime.Invocation{}, fmt.Errorf("%w: no durable cancellation request", modelruntime.ErrConflict)
		}
		if attempt.Status == modelruntime.DispatchResponseReceived && command.ProviderOutcome != nil {
			return modelruntime.Invocation{}, fmt.Errorf("%w: provider outcome already persisted", modelruntime.ErrConflict)
		}
		if attempt.Status == modelruntime.DispatchSendStarted {
			if command.ProviderOutcome == nil || command.ProviderOutcome.OutcomeClassification != modelruntime.ProviderOutcomeCancelled {
				return modelruntime.Invocation{}, fmt.Errorf("%w: confirmed cancellation outcome is required", modelruntime.ErrInvalidRequest)
			}
			if err = insertProviderOutcome(ctx, tx, command.InvocationID, command.DispatchAttemptID, *command.ProviderOutcome, modelruntime.AdapterFailureAmbiguous); err != nil {
				return modelruntime.Invocation{}, err
			}
		}
		if _, err = tx.Exec(ctx, `
UPDATE model_dispatch_attempts
SET status='completed',retry_safety='not_retryable',
    outcome_classification='cancelled_confirmed',error_code=NULLIF($2,''),finished_at=clock_timestamp()
WHERE id=$1`, attempt.ID, command.ErrorCode); err != nil {
			return modelruntime.Invocation{}, mapError(err)
		}
		invocation, err := scanInvocation(tx.QueryRow(ctx, `
UPDATE model_invocations
SET status='cancelled',error_code=NULLIF($2,''),updated_at=clock_timestamp(),terminal_at=clock_timestamp()
WHERE id=$1
RETURNING `+invocationColumns, command.InvocationID, command.ErrorCode))
		if err != nil {
			return modelruntime.Invocation{}, err
		}
		if err = appendInvocationEvent(ctx, tx, invocation, modelruntime.AuditInvocationCancelled, "service", attempt.ClaimedBy, true, outboxMax, map[string]any{
			"dispatch_attempt_id":    attempt.ID,
			"outcome_classification": "cancelled_confirmed",
		}); err != nil {
			return modelruntime.Invocation{}, err
		}
		return invocation, nil
	})
}

// ProviderFailureRetryable answers whether the durable provider outcome for an
// invocation described a TRANSIENT failure.
//
// It reads the answer Model Runtime already recorded rather than re-deriving
// it from an error code. Deriving it elsewhere would put a second copy of
// "which failures are worth repeating" outside the package that decides it,
// and the two would drift the first time a provider added a code.
//
// Absent means false: an invocation with no recorded outcome has not failed in
// a way anyone can call transient, and guessing otherwise would repeat calls
// that may already have been billed.
func (s *Store) ProviderFailureRetryable(ctx context.Context, invocationID int64) (bool, error) {
	var retryable bool
	err := s.pool.QueryRow(ctx, `
SELECT o.retryable
FROM model_provider_outcomes o
JOIN model_provider_requests r ON r.id = o.provider_request_record_id
WHERE r.invocation_id = $1
ORDER BY o.id DESC
LIMIT 1`, invocationID).Scan(&retryable)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not "false". Nothing was recorded, and reporting that as a
			// definite no makes an unasked question indistinguishable from
			// an answered one.
			return false, modelruntime.ErrProviderOutcomeUnknown
		}
		return false, mapError(err)
	}
	return retryable, nil
}
