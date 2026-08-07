package shadowverifier

import "errors"

var (
	ErrInvalidRequest      = errors.New("shadowverifier: invalid verification request")
	ErrSnapshotUnavailable = errors.New("shadowverifier: organization snapshot unavailable")
	ErrMatrixUnavailable   = errors.New("shadowverifier: capability matrix unavailable")
	ErrMatrixInvalid       = errors.New("shadowverifier: capability matrix invalid")
	ErrOrganizationRetired = errors.New("shadowverifier: organization is retired")
	ErrRunNotFound         = errors.New("shadowverifier: verification run not found")
)
