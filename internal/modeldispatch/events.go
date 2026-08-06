package modeldispatch

const (
	AuditPrincipalRegistered = "model.execution_principal_registered"
	AuditPrincipalReused     = "model.execution_principal_reused"
	AuditPrincipalDisabled   = "model.execution_principal_disabled"
	AuditAssignmentCreated   = "model.dispatch_assignment_created"
	AuditAssignmentReused    = "model.dispatch_assignment_reused"
	AuditAssignmentRevoked   = "model.dispatch_assignment_revoked"
	AuditAssignmentExpired   = "model.dispatch_assignment_expired"
	AuditAssignmentExhausted = "model.dispatch_assignment_exhausted"
	AuditAssignmentConsumed  = "model.dispatch_assignment_consumed"
)

// No outbox events are published for principal/assignment lifecycle: these are
// internal administrative records, not terminal invocation outcomes. Only
// audit_events are written; the existing model invocation outbox is untouched.
