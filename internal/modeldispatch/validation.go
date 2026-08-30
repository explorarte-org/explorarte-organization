package modeldispatch

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	roleIDPattern       = regexp.MustCompile(`^[a-z0-9]+(?:[._/-][a-z0-9]+)*$`)
	principalKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9]+(?:[._/-][a-zA-Z0-9]+)*$`)
	reasonCodePattern   = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	sha256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const maxAssignmentQuota = 1000000

func validPrincipalKind(kind PrincipalKind) bool {
	switch kind {
	case PrincipalLocalProcess, PrincipalCellProcess:
		return true
	default:
		return false
	}
}

func validReasonCode(code string) bool {
	return len(code) >= 1 && len(code) <= 120 && reasonCodePattern.MatchString(code)
}

func PrepareRegisterCommand(c RegisterPrincipalCommand) (RegisterPrincipalCommand, error) {
	c.OrganizationID = strings.TrimSpace(c.OrganizationID)
	c.PrincipalKey = strings.TrimSpace(c.PrincipalKey)
	c.DispatchActorRoleID = strings.TrimSpace(c.DispatchActorRoleID)
	c.IdempotencyKey = strings.TrimSpace(c.IdempotencyKey)
	if c.OrganizationID == "" {
		return c, fmt.Errorf("%w: organization is required", ErrInvalidRequest)
	}
	if len(c.PrincipalKey) < 1 || len(c.PrincipalKey) > 200 || !principalKeyPattern.MatchString(c.PrincipalKey) {
		return c, fmt.Errorf("%w: invalid principal key", ErrInvalidRequest)
	}
	if !roleIDPattern.MatchString(c.DispatchActorRoleID) {
		return c, fmt.Errorf("%w: invalid dispatch actor role", ErrInvalidRequest)
	}
	if !validPrincipalKind(c.PrincipalKind) {
		return c, fmt.Errorf("%w: invalid principal kind", ErrInvalidRequest)
	}
	if len(c.IdempotencyKey) < 1 || len(c.IdempotencyKey) > 200 {
		return c, fmt.Errorf("%w: idempotency key outside allowed length", ErrInvalidRequest)
	}
	return c, nil
}

func PrepareCreateAssignmentCommand(c CreateAssignmentCommand, now time.Time, defaultTTL, maxTTL time.Duration) (CreateAssignmentCommand, time.Time, time.Time, error) {
	c.OrganizationID = strings.TrimSpace(c.OrganizationID)
	c.SubjectRoleID = strings.TrimSpace(c.SubjectRoleID)
	c.ExecutionPrincipalKey = strings.TrimSpace(c.ExecutionPrincipalKey)
	c.IdempotencyKey = strings.TrimSpace(c.IdempotencyKey)
	if c.OrganizationID == "" || c.TaskID <= 0 || c.AttemptID <= 0 {
		return c, time.Time{}, time.Time{}, fmt.Errorf("%w: organization, task and attempt are required", ErrInvalidRequest)
	}
	if !roleIDPattern.MatchString(c.SubjectRoleID) {
		return c, time.Time{}, time.Time{}, fmt.Errorf("%w: invalid subject role", ErrInvalidRequest)
	}
	if len(c.ExecutionPrincipalKey) < 1 || len(c.ExecutionPrincipalKey) > 200 || !principalKeyPattern.MatchString(c.ExecutionPrincipalKey) {
		return c, time.Time{}, time.Time{}, fmt.Errorf("%w: invalid execution principal key", ErrInvalidRequest)
	}
	if c.MaxInvocations < 1 || c.MaxInvocations > maxAssignmentQuota {
		return c, time.Time{}, time.Time{}, fmt.Errorf("%w: max invocations outside allowed range", ErrInvalidRequest)
	}
	if len(c.IdempotencyKey) < 1 || len(c.IdempotencyKey) > 200 {
		return c, time.Time{}, time.Time{}, fmt.Errorf("%w: idempotency key outside allowed length", ErrInvalidRequest)
	}
	if c.ValidUntil != nil && c.TTL != nil {
		return c, time.Time{}, time.Time{}, fmt.Errorf("%w: valid_until and ttl are mutually exclusive", ErrInvalidRequest)
	}
	validFrom := now.UTC()
	var validUntil time.Time
	switch {
	case c.ValidUntil != nil:
		validUntil = c.ValidUntil.UTC()
	case c.TTL != nil:
		if *c.TTL <= 0 {
			return c, time.Time{}, time.Time{}, fmt.Errorf("%w: ttl must be positive", ErrInvalidRequest)
		}
		validUntil = validFrom.Add(*c.TTL)
	default:
		validUntil = validFrom.Add(defaultTTL)
	}
	if !validUntil.After(validFrom) {
		return c, time.Time{}, time.Time{}, fmt.Errorf("%w: valid_until must be after valid_from", ErrInvalidRequest)
	}
	if validUntil.Sub(validFrom) > maxTTL {
		return c, time.Time{}, time.Time{}, fmt.Errorf("%w: assignment vigency exceeds the configured maximum TTL", ErrInvalidRequest)
	}
	return c, validFrom, validUntil, nil
}

func validateTaskAttemptForAssignment(ref TaskAttemptRef, organizationID string, taskID, attemptID int64, subjectRoleID string, now time.Time) error {
	if ref.TaskID != taskID || ref.AttemptID != attemptID {
		return fmt.Errorf("%w: task/attempt mismatch", ErrTaskAttemptRejected)
	}
	if ref.OrganizationID != organizationID {
		return fmt.Errorf("%w: organization mismatch", ErrTaskAttemptRejected)
	}
	if ref.AssignedRoleID != subjectRoleID {
		return fmt.Errorf("%w: subject role must be the task's assigned role", ErrTaskAttemptRejected)
	}
	if ref.TaskStatus != "running" || ref.AttemptStatus != "running" {
		return fmt.Errorf("%w: task and attempt must be running", ErrTaskAttemptRejected)
	}
	if strings.TrimSpace(ref.LeaseHolderID) == "" {
		return fmt.Errorf("%w: active lease holder is required", ErrTaskAttemptRejected)
	}
	if !ref.LeaseExpiresAt.After(now) {
		return fmt.Errorf("%w: task lease expired", ErrTaskAttemptRejected)
	}
	return nil
}

func eligibleDispatchActorRole(role RoleRef) bool {
	return role.Enabled && role.Executable && role.AuthorityClass == "execution_service"
}
