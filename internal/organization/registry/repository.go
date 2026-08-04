package registry

import (
	"context"
	"errors"
)

var ErrDatabaseUnavailable = errors.New("organization registry database unavailable")

type Reader interface {
	GetOrganization(context.Context, string) (Organization, error)
	ListUnits(context.Context, string) ([]Unit, error)
	GetUnit(context.Context, string, string) (Unit, error)
	GetRole(context.Context, string, string) (Role, error)
	ListRoles(context.Context, string, RoleFilter) ([]Role, error)
	GetLeader(context.Context, string, string) (Role, error)
	ListWorkers(context.Context, string, string) ([]Role, error)
	GetCurrentRevision(context.Context, string) (*Revision, error)
	LoadCurrentSnapshot(context.Context, string) (*Snapshot, error)
}

type Repository interface {
	Reader
	Apply(context.Context, Snapshot) (SyncResult, error)
}
