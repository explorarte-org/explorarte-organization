package postgres

import (
	"encoding/json"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

const invocationColumns = `id,organization_id,organization_revision_id,task_id,attempt_id,dispatch_actor_role_id,subject_role_id,context_snapshot_id,purpose,model_profile_id,model_profile_version_id,provider_id,provider_model_id,model_egress_policy_version_id,model_egress_policy_hash,required_capabilities,output_mode,output_schema,max_output_tokens,temperature,thinking_mode,idempotency_key,request_hash,status,error_code,cancel_requested_at,deadline,correlation_id,causation_id,created_at,updated_at,terminal_at`
const attemptColumns = `id,invocation_id,attempt_number,status,claimed_by,claimed_at,claim_expires_at,send_started_at,response_received_at,provider_request_id,retry_safety,outcome_classification,error_code,created_at,finished_at`

func scanInvocation(row scanner) (modelruntime.Invocation, error) {
	var v modelruntime.Invocation
	var caps, schema []byte
	var errorCode, correlationID, causationID, egressPolicyHash *string
	err := row.Scan(&v.ID, &v.OrganizationID, &v.OrganizationRevisionID, &v.TaskID, &v.AttemptID, &v.DispatchActorRoleID, &v.SubjectRoleID, &v.ContextSnapshotID, &v.Purpose, &v.ModelProfileID, &v.ModelProfileVersionID, &v.ProviderID, &v.ProviderModelID, &v.ModelEgressPolicyVersionID, &egressPolicyHash, &caps, &v.OutputMode, &schema, &v.MaxOutputTokens, &v.Temperature, &v.ThinkingMode, &v.IdempotencyKey, &v.RequestHash, &v.Status, &errorCode, &v.CancelRequestedAt, &v.Deadline, &correlationID, &causationID, &v.CreatedAt, &v.UpdatedAt, &v.TerminalAt)
	if err != nil {
		return v, mapError(err)
	}
	v.ErrorCode = nullableString(errorCode)
	v.CorrelationID = nullableString(correlationID)
	v.CausationID = nullableString(causationID)
	v.ModelEgressPolicyHash = nullableString(egressPolicyHash)
	if err = json.Unmarshal(caps, &v.RequiredCapabilities); err != nil {
		return v, fmt.Errorf("decode invocation capabilities: %w", err)
	}
	if len(schema) > 0 && string(schema) != "null" {
		v.OutputSchema = append([]byte(nil), schema...)
	}
	return v, nil
}

func scanAttempt(row scanner) (modelruntime.DispatchAttempt, error) {
	var v modelruntime.DispatchAttempt
	var providerRequestID, outcomeClassification, errorCode *string
	err := row.Scan(&v.ID, &v.InvocationID, &v.AttemptNumber, &v.Status, &v.ClaimedBy, &v.ClaimedAt, &v.ClaimExpiresAt, &v.SendStartedAt, &v.ResponseReceivedAt, &providerRequestID, &v.RetrySafety, &outcomeClassification, &errorCode, &v.CreatedAt, &v.FinishedAt)
	if err != nil {
		return v, mapError(err)
	}
	v.ProviderRequestID = nullableString(providerRequestID)
	v.OutcomeClassification = nullableString(outcomeClassification)
	v.ErrorCode = nullableString(errorCode)
	return v, nil
}

func nullableString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
