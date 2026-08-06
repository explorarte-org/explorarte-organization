package modelidentity

import (
	"context"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type CapabilityAuthorizer interface {
	Authorize(context.Context, string, int64, string, string) error
}

type OrganizationCatalog interface {
	CurrentRevision(context.Context, string) (int64, error)
	GetRole(context.Context, string, string) (modeldispatch.RoleRef, error)
}

type PrincipalResolver interface {
	ResolveByKey(context.Context, string, string) (modeldispatch.ExecutionPrincipal, error)
}

type PolicyStore interface {
	Status(context.Context, string, CanonicalPolicy) (RegistryStatus, error)
	Apply(context.Context, string, CanonicalPolicy) (RegistrySyncResult, error)
	ResolveActive(context.Context, string) (ResolvedPolicy, error)
	ResolveByID(context.Context, string, int64) (ResolvedPolicy, error)
}

type KeyStore interface {
	RegisterKey(context.Context, PreparedKey) (RegisterKeyResult, error)
	RotateKey(context.Context, PreparedKey) (RegisterKeyResult, error)
	GetKey(context.Context, int64) (ExecutionIdentityKey, error)
	ListKeys(context.Context, string, int64, int) ([]ExecutionIdentityKey, error)
	RetireKey(context.Context, int64, string) (ExecutionIdentityKey, error)
	RevokeKey(context.Context, int64, string, string) (ExecutionIdentityKey, error)
	ResolveActiveKeyByFingerprint(context.Context, string, int64, string) (ExecutionIdentityKey, error)
}

type ChallengeStore interface {
	CreateChallenge(context.Context, PreparedChallenge) (Challenge, error)
	GetChallenge(context.Context, int64) (Challenge, error)
}

type Store interface {
	PolicyStore
	KeyStore
	ChallengeStore
}
