package modelegress

import "context"

type OrganizationRef struct {
	ID                 string
	RevisionID         int64
	PolicyDocumentHash string
}

type OrganizationCatalog interface {
	CurrentOrganization(context.Context, string) (OrganizationRef, error)
}

type ProviderCatalog interface {
	ProviderIDs(string) ([]string, error)
}

type RegistryStore interface {
	RecordValidated(context.Context, string, int64, string) error
	Status(context.Context, RegistryPlan) (RegistryStatus, error)
	Apply(context.Context, RegistryPlan) (RegistrySyncResult, error)
	ResolveForRevision(context.Context, string, int64) (ResolvedPolicy, error)
}

type PolicyCatalog interface {
	ResolveForRevision(context.Context, string, int64) (ResolvedPolicy, error)
}

// PersistAllowCommand and PersistDenyCommand (see domain.go) are carried by
// modelruntime's own EgressEvaluationStore interface. The pre-send transaction
// that consumes them now lives in internal/modelruntime/postgres, because it
// must also write model_dispatcher_assignment_uses in the same atomic
// transaction — a single implementation, not one per module (branch 10).
