package authorization

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	sha256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	roleIDPattern       = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*/[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	identifierPattern   = regexp.MustCompile(`^[a-z0-9]+(?:[-_.][a-z0-9]+)*$`)
	organizationPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
)

func DigestAction(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func ValidateDigest(value string) error {
	if !sha256Pattern.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("%w: action digest must be lowercase SHA-256", ErrInvalidInput)
	}
	return nil
}

func validateEvaluationRequest(request EvaluationRequest) error {
	if !organizationPattern.MatchString(request.OrganizationID) || request.OrganizationRevisionID <= 0 {
		return fmt.Errorf("%w: organization and revision are required", ErrInvalidInput)
	}
	if !roleIDPattern.MatchString(strings.TrimSpace(request.ActorRoleID)) {
		return fmt.Errorf("%w: actor role is invalid", ErrInvalidInput)
	}
	if len(request.CapabilityID) > 160 || !identifierPattern.MatchString(request.CapabilityID) {
		return fmt.Errorf("%w: capability is invalid", ErrInvalidInput)
	}
	if len(request.ResourceType) > 80 || !identifierPattern.MatchString(request.ResourceType) {
		return fmt.Errorf("%w: resource type is invalid", ErrInvalidInput)
	}
	if request.CapabilityID == "model.invoke" && request.ResourceType != "model_invocation" {
		return fmt.Errorf("%w: model.invoke is restricted to model_invocation resources", ErrInvalidInput)
	}
	if request.CapabilityID == "task.execute" && request.ResourceType == "model_invocation" {
		return fmt.Errorf("%w: task.execute cannot authorize model dispatch", ErrInvalidInput)
	}
	if request.ResourceID != strings.TrimSpace(request.ResourceID) || request.ResourceID == "" || len(request.ResourceID) > 240 || strings.ContainsRune(request.ResourceID, 0) {
		return fmt.Errorf("%w: resource ID is invalid", ErrInvalidInput)
	}
	if err := ValidateDigest(request.ActionDigest); err != nil {
		return err
	}
	if request.ApprovalRequestID != nil && *request.ApprovalRequestID <= 0 {
		return fmt.Errorf("%w: approval request ID must be positive", ErrInvalidInput)
	}
	return nil
}

func validateRequestCommand(command RequestApprovalCommand, defaultTTL, maxTTL time.Duration) (time.Duration, error) {
	if !roleIDPattern.MatchString(strings.TrimSpace(command.ActorRoleID)) {
		return 0, fmt.Errorf("%w: actor role is invalid", ErrInvalidInput)
	}
	if len(command.CapabilityID) > 160 || !identifierPattern.MatchString(command.CapabilityID) {
		return 0, fmt.Errorf("%w: capability is invalid", ErrInvalidInput)
	}
	if len(command.ResourceType) > 80 || !identifierPattern.MatchString(command.ResourceType) {
		return 0, fmt.Errorf("%w: resource type is invalid", ErrInvalidInput)
	}
	if command.ResourceID != strings.TrimSpace(command.ResourceID) || command.ResourceID == "" || len(command.ResourceID) > 240 || strings.ContainsRune(command.ResourceID, 0) {
		return 0, fmt.Errorf("%w: resource ID is invalid", ErrInvalidInput)
	}
	if err := ValidateDigest(command.ActionDigest); err != nil {
		return 0, err
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" || len(command.IdempotencyKey) > 200 || strings.ContainsRune(command.IdempotencyKey, 0) {
		return 0, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidInput)
	}
	if strings.TrimSpace(command.Reason) == "" || len(command.Reason) > 2000 || strings.ContainsRune(command.Reason, 0) {
		return 0, fmt.Errorf("%w: reason is invalid", ErrInvalidInput)
	}
	for _, value := range []string{command.CorrelationID, command.CausationID} {
		if len(value) > 200 || strings.ContainsRune(value, 0) {
			return 0, fmt.Errorf("%w: correlation metadata is invalid", ErrInvalidInput)
		}
	}
	ttl := command.TTL
	if ttl == 0 {
		ttl = defaultTTL
	}
	if ttl <= 0 || ttl > maxTTL {
		return 0, fmt.Errorf("%w: approval TTL must be positive and no greater than %s", ErrInvalidInput, maxTTL)
	}
	return ttl, nil
}

func normalizeOptional(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
