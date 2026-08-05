package contextengine

import (
	"errors"
	"fmt"
)

type ReasonCode string

const (
	ReasonInvalidRequest            ReasonCode = "invalid_request"
	ReasonOrganizationMismatch      ReasonCode = "organization_mismatch"
	ReasonRevisionMismatch          ReasonCode = "revision_mismatch"
	ReasonRoleNotFound              ReasonCode = "role_not_found"
	ReasonRoleDisabled              ReasonCode = "role_disabled"
	ReasonRoleRetired               ReasonCode = "role_retired"
	ReasonRoleNotExecutable         ReasonCode = "role_not_executable"
	ReasonPrecedenceInvalid         ReasonCode = "precedence_invalid"
	ReasonPrecedenceHashMismatch    ReasonCode = "precedence_hash_mismatch"
	ReasonCanonicalBundleDrift      ReasonCode = "canonical_bundle_drift"
	ReasonSourceNotFound            ReasonCode = "source_not_found"
	ReasonSourcePathEscape          ReasonCode = "source_path_escape"
	ReasonSourceSymlinkEscape       ReasonCode = "source_symlink_escape"
	ReasonSourceTooLarge            ReasonCode = "source_too_large"
	ReasonInvalidUTF8               ReasonCode = "invalid_utf8"
	ReasonNULByteRejected           ReasonCode = "nul_byte_rejected"
	ReasonInvalidFrontmatter        ReasonCode = "invalid_frontmatter"
	ReasonDuplicateFrontmatterKey   ReasonCode = "duplicate_frontmatter_key"
	ReasonSourceIdentityMismatch    ReasonCode = "source_identity_mismatch"
	ReasonSkillNotFound             ReasonCode = "skill_not_found"
	ReasonSkillNotActive            ReasonCode = "skill_not_active"
	ReasonSkillNotAssigned          ReasonCode = "skill_not_assigned"
	ReasonSkillSourceHashMismatch   ReasonCode = "skill_source_hash_mismatch"
	ReasonMemoryNotApproved         ReasonCode = "memory_not_approved"
	ReasonMemoryScopeMismatch       ReasonCode = "memory_scope_mismatch"
	ReasonRAGScopeMismatch          ReasonCode = "rag_scope_mismatch"
	ReasonForbiddenDataClass        ReasonCode = "forbidden_data_class"
	ReasonSecretDataRejected        ReasonCode = "secret_data_rejected"
	ReasonClinicalDataRejected      ReasonCode = "clinical_data_rejected"
	ReasonContextBudgetExceeded     ReasonCode = "context_budget_exceeded"
	ReasonIdempotencyConflict       ReasonCode = "idempotency_conflict"
	ReasonSnapshotNotFound          ReasonCode = "snapshot_not_found"
	ReasonSnapshotInvalidated       ReasonCode = "snapshot_invalidated"
	ReasonSnapshotStale             ReasonCode = "snapshot_stale"
	ReasonSourceProviderUnavailable ReasonCode = "source_provider_unavailable"
	ReasonStructuredConflict        ReasonCode = "structured_conflict"
	ReasonUnsafeInstructionSource   ReasonCode = "unsafe_instruction_source"
	ReasonProfileDrift              ReasonCode = "profile_drift"
	ReasonAgentDrift                ReasonCode = "agent_drift"
	ReasonSkillStateDrift           ReasonCode = "skill_state_drift"
	ReasonSkillSourceDrift          ReasonCode = "skill_source_drift"
	ReasonMemoryVersionDrift        ReasonCode = "memory_version_drift"
	ReasonProjectVersionDrift       ReasonCode = "project_version_drift"
	ReasonTaskVersionDrift          ReasonCode = "task_version_drift"
	ReasonRAGVersionDrift           ReasonCode = "rag_version_drift"
	ReasonRenderedHashMismatch      ReasonCode = "rendered_hash_mismatch"
)

var (
	ErrInvalidRequest            = errors.New("context request is invalid")
	ErrRejected                  = errors.New("context rejected by policy or validation")
	ErrIdempotencyConflict       = errors.New("context idempotency conflict")
	ErrSnapshotNotFound          = errors.New("context snapshot not found")
	ErrSnapshotInvalidated       = errors.New("context snapshot is invalidated")
	ErrSnapshotStale             = errors.New("context snapshot is stale")
	ErrSourceProviderUnavailable = errors.New("context source provider unavailable")
	ErrDatabaseUnavailable       = errors.New("context PostgreSQL unavailable")
)

type RejectionError struct {
	Code      ReasonCode
	Reference string
	Message   string
	Cause     error
}

func (e *RejectionError) Error() string {
	if e == nil {
		return "context rejected"
	}
	message := e.Message
	if message == "" {
		message = string(e.Code)
	}
	if e.Reference != "" {
		message = fmt.Sprintf("%s (%s)", message, e.Reference)
	}
	return message
}

func (e *RejectionError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrRejected
}

func Reject(code ReasonCode, reference, message string) error {
	return &RejectionError{Code: code, Reference: reference, Message: message, Cause: ErrRejected}
}

func ReasonOf(err error) ReasonCode {
	var rejection *RejectionError
	if errors.As(err, &rejection) {
		return rejection.Code
	}
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return ReasonIdempotencyConflict
	case errors.Is(err, ErrSnapshotNotFound):
		return ReasonSnapshotNotFound
	case errors.Is(err, ErrSnapshotInvalidated):
		return ReasonSnapshotInvalidated
	case errors.Is(err, ErrSnapshotStale):
		return ReasonSnapshotStale
	case errors.Is(err, ErrSourceProviderUnavailable):
		return ReasonSourceProviderUnavailable
	default:
		return ""
	}
}
