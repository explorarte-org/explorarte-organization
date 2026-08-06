package decisiongraphtrace

import "errors"

var (
	ErrInvalidRun           = errors.New("invalid decision graph run reference")
	ErrRunNotSucceeded      = errors.New("decision graph run is not succeeded")
	ErrOrganizationMismatch = errors.New("trace organization does not match store organization")
)
