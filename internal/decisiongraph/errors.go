package decisiongraph

import "errors"

var (
	ErrInvalidGraph        = errors.New("invalid decision graph")
	ErrDuplicateNode       = errors.New("duplicate decision node")
	ErrUnknownNode         = errors.New("unknown decision node")
	ErrDuplicateEdge       = errors.New("duplicate decision edge")
	ErrDependencyCycle     = errors.New("decision dependency cycle")
	ErrInvalidTransition   = errors.New("invalid decision state transition")
	ErrInvalidBudget       = errors.New("invalid decision budget")
	ErrBudgetExceeded      = errors.New("decision budget exceeded")
	ErrBudgetOverflow      = errors.New("decision budget overflow")
	ErrInvalidDecision     = errors.New("invalid decision record")
	ErrInvalidRun          = errors.New("invalid decision run")
	ErrInvalidClaim        = errors.New("invalid decision node claim")
	ErrInvalidExecution    = errors.New("invalid decision node execution")
	ErrInvalidObservation  = errors.New("invalid decision observation")
	ErrInvalidVerification = errors.New("invalid decision verification")
	ErrRunNotMutable       = errors.New("decision run is not mutable")
	ErrRunNotActive        = errors.New("decision run is not active")
	ErrRunDeadlineExceeded = errors.New("decision run deadline exceeded")
	ErrClaimUnavailable    = errors.New("no decision node available to claim")
	ErrStaleClaim          = errors.New("stale decision node claim")
	ErrNotFound            = errors.New("decision graph record not found")
	ErrIdempotencyConflict = errors.New("decision run idempotency conflict")
)
