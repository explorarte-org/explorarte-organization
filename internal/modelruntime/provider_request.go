package modelruntime

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

func BuildProviderRequestEvidence(request CanonicalRequest, descriptor AdapterDescriptor) (modelegress.ProviderRequestEvidence, error) {
	if err := descriptor.Validate(); err != nil {
		return modelegress.ProviderRequestEvidence{}, err
	}
	if request.InvocationID <= 0 || request.DispatchAttemptID <= 0 || request.OrganizationRevisionID <= 0 ||
		request.ModelProfileVersionID <= 0 || request.ProviderID != descriptor.ProviderID ||
		strings.TrimSpace(request.ProviderModelID) == "" || len(request.ContextRenderedHash) != 64 ||
		len(request.ProviderIdempotencyKey) != 64 || request.Deadline.IsZero() {
		return modelegress.ProviderRequestEvidence{}, fmt.Errorf("%w: provider request evidence scope is invalid", ErrInvalidRequest)
	}
	capabilities := make([]string, 0, len(request.RequiredCapabilities))
	for _, capability := range request.RequiredCapabilities {
		value := strings.TrimSpace(string(capability))
		if value != "" {
			capabilities = append(capabilities, value)
		}
	}
	sort.Strings(capabilities)
	outputSchemaHash := ""
	if len(request.OutputSchema) > 0 {
		outputSchemaHash = SHA256Bytes(request.OutputSchema)
	}
	body, err := CanonicalJSON(map[string]any{
		"schema_version":            descriptor.RequestSchemaVersion,
		"adapter_id":                descriptor.AdapterID,
		"adapter_version":           descriptor.AdapterVersion,
		"invocation_id":             request.InvocationID,
		"dispatch_attempt_id":       request.DispatchAttemptID,
		"organization_id":           request.OrganizationID,
		"organization_revision_id":  request.OrganizationRevisionID,
		"task_id":                   request.TaskID,
		"attempt_id":                request.AttemptID,
		"dispatch_actor_role_id":    request.DispatchActorRoleID,
		"subject_role_id":           request.SubjectRoleID,
		"model_profile_id":          request.ModelProfileID,
		"model_profile_version_id":  request.ModelProfileVersionID,
		"provider_id":               request.ProviderID,
		"provider_model_id":         request.ProviderModelID,
		"provider_idempotency_key":  request.ProviderIdempotencyKey,
		"context_snapshot_id":       request.ContextSnapshotID,
		"context_rendered_hash":     request.ContextRenderedHash,
		"required_capabilities":     capabilities,
		"output_mode":               request.OutputMode,
		"output_schema_hash":        outputSchemaHash,
		"max_output_tokens":         request.MaxOutputTokens,
		"temperature":               request.Temperature,
		"thinking_mode":             request.ThinkingMode,
		"reasoning_effort":          request.ReasoningEffort,
		"deadline":                  request.Deadline.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano),
		"endpoint_fingerprint":      descriptor.EndpointFingerprint,
		"credential_reference_hash": descriptor.CredentialRefHash,
	})
	if err != nil {
		return modelegress.ProviderRequestEvidence{}, err
	}
	return modelegress.ProviderRequestEvidence{
		ModelProfileID: request.ModelProfileID, ProviderModelID: request.ProviderModelID, AdapterID: descriptor.AdapterID,
		AdapterVersion: descriptor.AdapterVersion, RequestSchemaVersion: descriptor.RequestSchemaVersion,
		ResponseSchemaVersion: descriptor.ResponseSchemaVersion, RequestHash: SHA256Bytes(body),
		EndpointFingerprint: descriptor.EndpointFingerprint, CredentialRefHash: descriptor.CredentialRefHash,
	}, nil
}
