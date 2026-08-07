package modelegress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func SHA256Bytes(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func InvocationActionDigest(invocationID int64, requestHash string, policyVersionID int64, policyHash string) (string, error) {
	if invocationID <= 0 || policyVersionID <= 0 || !lowerHex64Pattern.MatchString(requestHash) || !lowerHex64Pattern.MatchString(policyHash) {
		return "", fmt.Errorf("%w: invalid invocation action scope", ErrEvaluationConflict)
	}
	value := struct {
		InvocationID    int64  `json:"invocation_id"`
		RequestHash     string `json:"request_hash"`
		PolicyVersionID int64  `json:"model_egress_policy_version_id"`
		PolicyHash      string `json:"model_egress_policy_hash"`
	}{invocationID, requestHash, policyVersionID, policyHash}
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return SHA256Bytes(body), nil
}

func NormalizeClassifications(values []string) ([]string, string) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		switch value {
		case ScopeExecutiveCEO, ScopeDepartmentLeader, ScopeDepartmentWorker:
			// R24 internal scope markers are metadata, not data classes. Preserve
			// exactly these known values so the pre-send decision hash proves the
			// scope that was evaluated. They are removed before policy rule lookup.
		default:
			switch DataClassification(value) {
			case ClassificationPublic, ClassificationSanitized, ClassificationOrganizational, ClassificationSecret, ClassificationClinical, ClassificationUnknown:
			default:
				value = string(ClassificationUnknown)
			}
		}
		seen[value] = struct{}{}
	}
	normalized := make([]string, 0, len(seen))
	for value := range seen {
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	body, _ := json.Marshal(normalized)
	return normalized, SHA256Bytes(body)
}

func NormalizeReasonCodes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func DecisionHash(evaluation PreSendEvaluation) string {
	classifications, classificationsHash := NormalizeClassifications(evaluation.ContextClassifications)
	reasons := NormalizeReasonCodes(evaluation.EgressReasonCodes)
	value := struct {
		InvocationID            int64               `json:"invocation_id"`
		DispatchAttemptID       int64               `json:"dispatch_attempt_id"`
		OrganizationID          string              `json:"organization_id"`
		OrganizationRevisionID  int64               `json:"organization_revision_id"`
		ModelProfileVersionID   int64               `json:"model_profile_version_id"`
		ProviderID              string              `json:"provider_id"`
		ProviderTransport       string              `json:"provider_transport"`
		PolicyVersionID         int64               `json:"policy_version_id"`
		PolicyHash              string              `json:"policy_hash"`
		ActionDigest            string              `json:"action_digest"`
		CapabilityMatrixHash    string              `json:"capability_matrix_hash"`
		ContextClassifications  []string            `json:"context_classifications"`
		ClassificationsHash     string              `json:"classifications_hash"`
		AuthorizationEffect     AuthorizationEffect `json:"authorization_effect"`
		AuthorizationReasonCode string              `json:"authorization_reason_code"`
		EgressEffect            Effect              `json:"egress_effect"`
		EgressReasonCodes       []string            `json:"egress_reason_codes"`
	}{evaluation.InvocationID, evaluation.DispatchAttemptID, evaluation.OrganizationID, evaluation.OrganizationRevisionID,
		evaluation.ModelProfileVersionID, evaluation.ProviderID, evaluation.ProviderTransport,
		evaluation.PolicyVersionID, evaluation.PolicyHash, evaluation.ActionDigest, evaluation.CapabilityMatrixHash,
		classifications, classificationsHash, evaluation.AuthorizationEffect,
		evaluation.AuthorizationReasonCode, evaluation.EgressEffect, reasons}
	body, _ := json.Marshal(value)
	return SHA256Bytes(body)
}
