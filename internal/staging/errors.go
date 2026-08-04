package staging

import "errors"

var (
	ErrWorkspaceNotFound         = errors.New("staging workspace not found")
	ErrWorkspaceExists           = errors.New("staging workspace already exists")
	ErrWorkspaceNotActive        = errors.New("staging workspace is not active")
	ErrWorkspaceSealed           = errors.New("staging workspace is sealed")
	ErrWorkspaceDirty            = errors.New("staging workspace is dirty")
	ErrWorkspaceMissing          = errors.New("staging workspace directory is missing")
	ErrRepositoryDenied          = errors.New("repository denied")
	ErrTargetRefDenied           = errors.New("target ref denied")
	ErrCapabilityDenied          = errors.New("staging capability denied")
	ErrPolicyRevisionMismatch    = errors.New("staging policy revision mismatch")
	ErrLeaseRequired             = errors.New("active execution lease required")
	ErrLeaseInvalid              = errors.New("execution lease invalid")
	ErrGitConflict               = errors.New("git conflict")
	ErrNoChanges                 = errors.New("workspace has no changes")
	ErrArtifactCorrupt           = errors.New("artifact corrupt")
	ErrPromotionGatesUnsatisfied = errors.New("promotion gates unsatisfied")
	ErrTargetMoved               = errors.New("target ref moved")
	ErrUnsafeRepository          = errors.New("unsafe repository")
	ErrInvalidInput              = errors.New("invalid staging input")
	ErrInvalidTransition         = errors.New("invalid staging state transition")
	ErrConflict                  = errors.New("staging conflict")
	ErrDatabaseUnavailable       = errors.New("staging database unavailable")
)
