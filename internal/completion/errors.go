package completion

import "errors"

var (
	ErrInvalidRequest   = errors.New("completion: invalid verification request")
	ErrTaskNotFound     = errors.New("completion: task not found")
	ErrArtifactNotFound = errors.New("completion: artifact not found")
)
