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

type EvaluationStore interface {
	PersistPreSendAllowAndMarkSendStarted(context.Context, PersistAllowCommand) error
	PersistPreSendDenyAndFail(context.Context, PersistDenyCommand) error
}
