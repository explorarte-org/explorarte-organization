package modeldispatch

import "errors"

var (
	ErrInvalidRequest      = errors.New("model dispatch invalid request")
	ErrNotFound            = errors.New("model dispatch entity not found")
	ErrConflict            = errors.New("model dispatch conflict")
	ErrAuthorizationDenied = errors.New("model dispatch authorization denied")
	ErrRoleNotEligible     = errors.New("role is not eligible as a dispatch actor")
	ErrPrincipalDisabled   = errors.New("model execution principal disabled")
	ErrPrincipalMismatch   = errors.New("model execution principal mismatch")
	ErrAssignmentNotFound  = errors.New("model dispatcher assignment not found")
	ErrAssignmentInactive  = errors.New("model dispatcher assignment is not active")
	ErrAssignmentExpired   = errors.New("model dispatcher assignment expired")
	ErrAssignmentExhausted = errors.New("model dispatcher assignment exhausted")
	ErrAssignmentRevoked   = errors.New("model dispatcher assignment revoked")
	ErrAssignmentConflict  = errors.New("an active dispatcher assignment already exists for this attempt")
	ErrRevisionDrift       = errors.New("model dispatch organization revision drift")
	ErrTaskAttemptRejected = errors.New("model dispatch task attempt rejected")
	ErrDatabaseUnavailable = errors.New("model dispatch database unavailable")
)
