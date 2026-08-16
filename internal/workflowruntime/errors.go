package workflowruntime

import "errors"

var (
	ErrInvalidRequest       = errors.New("workflow runtime invalid request")
	ErrTaskBinding          = errors.New("workflow runtime task binding mismatch")
	ErrCompletionNotReady   = errors.New("workflow runtime completion is not ready")
	ErrTerminalReplay       = errors.New("workflow runtime terminal transition replay")
	ErrSameRoleCoordination = errors.New("workflow runtime same-role coordination is a task transition")
	ErrDecisionBinding      = errors.New("workflow runtime decision binding mismatch")
	ErrUnsupportedAction    = errors.New("workflow runtime unsupported branch action")
	ErrAuthorizationDenied  = errors.New("workflow runtime authorization denied")
)
