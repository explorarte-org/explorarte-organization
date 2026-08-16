package executionharness

import "errors"

var (
	ErrInvalidRun      = errors.New("invalid execution run")
	ErrAuthorityDenied = errors.New("execution authority denied")
	// ErrAuthorityUnavailable means authority could NOT be evaluated, which is
	// categorically different from evaluating it and being refused. It must
	// never be reported as a denial: a denial is a durable statement that the
	// principal or lease lost standing, and fabricating that from a database
	// outage both misleads operators and destroys a run that was still valid.
	ErrAuthorityUnavailable = errors.New("execution authority unavailable")
	ErrHistoryConflict      = errors.New("execution history sequence conflict")
	ErrHistoryCorrupt       = errors.New("execution history is corrupt")
	ErrRunIdentityDrift     = errors.New("stable run identity drift")
	ErrUnknownTool          = errors.New("unknown tool")
	ErrToolNotAllowed       = errors.New("tool not allowed by run")
	ErrToolReplay           = errors.New("tool call replay")
)
