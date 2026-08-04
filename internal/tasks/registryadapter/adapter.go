package registryadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type Adapter struct {
	reader registry.Reader
}

func New(reader registry.Reader) (*Adapter, error) {
	if reader == nil {
		return nil, errors.New("task registry adapter requires a registry reader")
	}
	return &Adapter{reader: reader}, nil
}

func (a *Adapter) CurrentRevision(ctx context.Context, organizationID string) (tasks.RevisionRef, error) {
	revision, err := a.reader.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return tasks.RevisionRef{}, mapError(err)
	}
	if revision == nil || revision.ID <= 0 {
		return tasks.RevisionRef{}, tasks.ErrNotFound
	}
	return tasks.RevisionRef{ID: revision.ID}, nil
}

func (a *Adapter) GetRole(ctx context.Context, organizationID, roleID string) (tasks.RoleRef, error) {
	role, err := a.reader.GetRole(ctx, organizationID, roleID)
	if err != nil {
		return tasks.RoleRef{}, mapError(err)
	}
	return tasks.RoleRef{
		ID: role.ID, UnitID: role.UnitID, Enabled: role.Enabled, Executable: role.Executable,
		Retired: role.RetiredAt != nil,
	}, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, registry.ErrNotFound):
		return tasks.ErrNotFound
	case errors.Is(err, registry.ErrDatabaseUnavailable):
		return fmt.Errorf("%w: %v", tasks.ErrDatabaseUnavailable, err)
	default:
		return err
	}
}

var _ tasks.Catalog = (*Adapter)(nil)
