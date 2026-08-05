package modelruntime

const (
	AuditRegistryValidated    = "model.registry_validated"
	AuditRegistrySynced       = "model.registry_synced"
	AuditInvocationRequested  = "model.invocation_requested"
	AuditInvocationReused     = "model.invocation_reused"
	AuditInvocationClaimed    = "model.invocation_claimed"
	AuditInvocationDispatched = "model.invocation_dispatched"
	AuditInvocationSucceeded  = "model.invocation_succeeded"
	AuditInvocationFailed     = "model.invocation_failed"
	AuditInvocationCancelled  = "model.invocation_cancelled"
	AuditInvocationTimedOut   = "model.invocation_timed_out"
	AuditInvocationAmbiguous  = "model.invocation_ambiguous"
	AuditInvocationReconciled = "model.invocation_reconciled"
)

var AllowedAuditEvents = map[string]struct{}{AuditRegistryValidated: {}, AuditRegistrySynced: {}, AuditInvocationRequested: {}, AuditInvocationReused: {}, AuditInvocationClaimed: {}, AuditInvocationDispatched: {}, AuditInvocationSucceeded: {}, AuditInvocationFailed: {}, AuditInvocationCancelled: {}, AuditInvocationTimedOut: {}, AuditInvocationAmbiguous: {}, AuditInvocationReconciled: {}}
var AllowedOutboxEvents = map[string]struct{}{AuditInvocationRequested: {}, AuditInvocationSucceeded: {}, AuditInvocationFailed: {}, AuditInvocationCancelled: {}, AuditInvocationTimedOut: {}, AuditInvocationAmbiguous: {}}
