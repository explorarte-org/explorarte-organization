package postgres

import (
	"context"
	"encoding/json"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

func (s *Store) GetInvocationResult(ctx context.Context, invocationID int64) (modelruntime.InvocationResult, error) {
	var result modelruntime.InvocationResult
	var textOutput *string
	var jsonOutput []byte
	var toolBody []byte
	err := s.pool.QueryRow(ctx, `
SELECT id,invocation_id,dispatch_attempt_id,output_mode,text_output,json_output,tool_intents,response_hash,response_bytes,created_at
FROM model_invocation_results WHERE invocation_id=$1`, invocationID).Scan(
		&result.ID, &result.InvocationID, &result.DispatchAttemptID, &result.OutputMode,
		&textOutput, &jsonOutput, &toolBody, &result.ResponseHash, &result.ResponseBytes, &result.CreatedAt,
	)
	if err != nil {
		return modelruntime.InvocationResult{}, mapError(err)
	}
	if textOutput != nil {
		result.TextOutput = *textOutput
	}
	if len(jsonOutput) > 0 && string(jsonOutput) != "null" {
		result.JSONOutput = append([]byte(nil), jsonOutput...)
	}
	if len(toolBody) == 0 || string(toolBody) == "null" {
		result.ToolIntents = []modelruntime.ToolIntent{}
	} else if err = json.Unmarshal(toolBody, &result.ToolIntents); err != nil {
		return modelruntime.InvocationResult{}, err
	}
	return result, nil
}

func (s *Store) GetInvocationOutcome(ctx context.Context, invocationID int64) (modelruntime.DispatchResult, error) {
	invocation, err := s.GetInvocation(ctx, invocationID)
	if err != nil {
		return modelruntime.DispatchResult{}, err
	}
	if invocation.Status != modelruntime.InvocationSucceeded {
		return modelruntime.DispatchResult{}, modelruntime.ErrConflict
	}
	result, err := s.GetInvocationResult(ctx, invocationID)
	if err != nil {
		return modelruntime.DispatchResult{}, err
	}
	var usage modelruntime.Usage
	err = s.pool.QueryRow(ctx, `
SELECT invocation_id,dispatch_attempt_id,input_tokens,output_tokens,total_tokens,provider_reported,
       prompt_cache_hit_tokens,prompt_cache_miss_tokens
FROM model_invocation_usage WHERE invocation_id=$1`, invocationID).Scan(
		&usage.InvocationID, &usage.DispatchAttemptID, &usage.InputTokens, &usage.OutputTokens,
		&usage.TotalTokens, &usage.ProviderReported, &usage.PromptCacheHitTokens, &usage.PromptCacheMissTokens,
	)
	if err != nil {
		return modelruntime.DispatchResult{}, mapError(err)
	}
	if result.InvocationID != invocation.ID || usage.InvocationID != invocation.ID || result.DispatchAttemptID != usage.DispatchAttemptID {
		return modelruntime.DispatchResult{}, modelruntime.ErrConflict
	}
	return modelruntime.DispatchResult{Invocation: invocation, Result: &result, Usage: &usage}, nil
}

func (s *Store) FindInvocationsByTaskAttempt(ctx context.Context, organizationID string, taskID, attemptID int64) ([]modelruntime.Invocation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE organization_id=$1 AND task_id=$2 AND attempt_id=$3 ORDER BY id`, organizationID, taskID, attemptID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []modelruntime.Invocation{}
	for rows.Next() {
		value, scanErr := scanInvocation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return out, nil
}

var _ modelruntime.InvocationResultReader = (*Store)(nil)
var _ modelruntime.InvocationOutcomeReader = (*Store)(nil)
