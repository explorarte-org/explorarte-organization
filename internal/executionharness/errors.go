package executionharness

import "errors"

var (
	ErrInvalidRun       = errors.New("invalid execution run")
	ErrAuthorityDenied  = errors.New("execution authority denied")
	ErrHistoryConflict  = errors.New("execution history sequence conflict")
	ErrHistoryCorrupt   = errors.New("execution history is corrupt")
	ErrRunIdentityDrift = errors.New("stable run identity drift")
	ErrUnknownTool      = errors.New("unknown tool")
	ErrToolNotAllowed   = errors.New("tool not allowed by run")
	ErrToolReplay       = errors.New("tool call replay")
)
