package modeldispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func PrincipalRequestHash(organizationID, principalKey, dispatchActorRoleID string, kind PrincipalKind, registeredByRoleID string) (string, error) {
	body, err := json.Marshal(struct {
		OrganizationID      string        `json:"organization_id"`
		PrincipalKey        string        `json:"principal_key"`
		DispatchActorRoleID string        `json:"dispatch_actor_role_id"`
		PrincipalKind       PrincipalKind `json:"principal_kind"`
		RegisteredByRoleID  string        `json:"registered_by_role_id"`
	}{organizationID, principalKey, dispatchActorRoleID, kind, registeredByRoleID})
	if err != nil {
		return "", err
	}
	return sha256Hex(body), nil
}

func AssignmentRequestHash(organizationID string, organizationRevisionID, taskID, attemptID int64, subjectRoleID string, executionPrincipalID int64, principalKey, dispatchActorRoleID string, validFrom, validUntil time.Time, maxInvocations int, createdByRoleID string) (string, error) {
	body, err := json.Marshal(struct {
		OrganizationID         string `json:"organization_id"`
		OrganizationRevisionID int64  `json:"organization_revision_id"`
		TaskID                 int64  `json:"task_id"`
		AttemptID              int64  `json:"attempt_id"`
		SubjectRoleID          string `json:"subject_role_id"`
		ExecutionPrincipalID   int64  `json:"execution_principal_id"`
		PrincipalKey           string `json:"principal_key"`
		DispatchActorRoleID    string `json:"dispatch_actor_role_id"`
		ValidFrom              string `json:"valid_from"`
		ValidUntil             string `json:"valid_until"`
		MaxInvocations         int    `json:"max_invocations"`
		CreatedByRoleID        string `json:"created_by_role_id"`
	}{organizationID, organizationRevisionID, taskID, attemptID, subjectRoleID, executionPrincipalID, principalKey, dispatchActorRoleID, formatTime(validFrom), formatTime(validUntil), maxInvocations, createdByRoleID})
	if err != nil {
		return "", err
	}
	return sha256Hex(body), nil
}

// AssignmentScopeHash covers the immutable scope of an assignment: principal
// identity, task/attempt, subject, dispatcher, revision, quota and vigency. It
// is what invocations pin and compare against on every dispatch to detect drift.
func AssignmentScopeHash(organizationID string, organizationRevisionID, taskID, attemptID int64, subjectRoleID, dispatchActorRoleID string, executionPrincipalID int64, maxInvocations int, validFrom, validUntil time.Time) (string, error) {
	body, err := json.Marshal(struct {
		OrganizationID         string `json:"organization_id"`
		OrganizationRevisionID int64  `json:"organization_revision_id"`
		TaskID                 int64  `json:"task_id"`
		AttemptID              int64  `json:"attempt_id"`
		SubjectRoleID          string `json:"subject_role_id"`
		DispatchActorRoleID    string `json:"dispatch_actor_role_id"`
		ExecutionPrincipalID   int64  `json:"execution_principal_id"`
		MaxInvocations         int    `json:"max_invocations"`
		ValidFrom              string `json:"valid_from"`
		ValidUntil             string `json:"valid_until"`
	}{organizationID, organizationRevisionID, taskID, attemptID, subjectRoleID, dispatchActorRoleID, executionPrincipalID, maxInvocations, formatTime(validFrom), formatTime(validUntil)})
	if err != nil {
		return "", err
	}
	return sha256Hex(body), nil
}

func UsageHash(assignmentID int64, assignmentHash string, invocationID, dispatchAttemptID, executionPrincipalID int64, usedAt time.Time) (string, error) {
	body, err := json.Marshal(struct {
		AssignmentID         int64  `json:"assignment_id"`
		AssignmentHash       string `json:"assignment_hash"`
		InvocationID         int64  `json:"invocation_id"`
		DispatchAttemptID    int64  `json:"dispatch_attempt_id"`
		ExecutionPrincipalID int64  `json:"execution_principal_id"`
		UsedAt               string `json:"used_at"`
	}{assignmentID, assignmentHash, invocationID, dispatchAttemptID, executionPrincipalID, formatTime(usedAt)})
	if err != nil {
		return "", err
	}
	return sha256Hex(body), nil
}
