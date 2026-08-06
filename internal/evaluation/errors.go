package evaluation

import "errors"

var (
	ErrInvalidTraceRef     = errors.New("invalid trace reference")
	ErrInvalidTrace        = errors.New("invalid evaluation trace")
	ErrTraceHashMismatch   = errors.New("evaluation trace hash mismatch")
	ErrInvalidSuite        = errors.New("invalid evaluation suite")
	ErrEmptySuite          = errors.New("evaluation suite has no cases")
	ErrDuplicateCase       = errors.New("duplicate evaluation case")
	ErrInvalidCase         = errors.New("invalid evaluation case")
	ErrInvalidRequest      = errors.New("invalid evaluation request")
	ErrInvalidResult       = errors.New("invalid evaluation result")
	ErrCaseMismatch        = errors.New("evaluation results reference different cases")
	ErrIncomparableResults = errors.New("evaluation results are not comparable")
)
