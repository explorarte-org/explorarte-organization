package roles

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type RoleReader interface {
	GetRole(context.Context, string, string) (registry.Role, error)
}

// Resolver derives RAG namespaces from the materialized registry so a
// namespace can never be supplied as free text by a prompt or a model.
type Resolver struct {
	reader RoleReader
}

func New(reader RoleReader) (*Resolver, error) {
	if reader == nil {
		return nil, errors.New("rag namespace resolver requires a role reader")
	}
	return &Resolver{reader: reader}, nil
}

var _ rag.NamespaceResolver = (*Resolver)(nil)

func (r *Resolver) ResolveNamespace(ctx context.Context, organizationID, actorRoleID string, kind rag.NamespaceKind) (string, error) {
	organizationID = strings.TrimSpace(organizationID)
	actorRoleID = strings.TrimSpace(actorRoleID)
	role, err := r.reader.GetRole(ctx, organizationID, actorRoleID)
	if err != nil {
		return "", fmt.Errorf("resolve rag namespace: %w", err)
	}
	switch kind {
	case rag.NamespaceOwn:
		if strings.TrimSpace(role.ID) == "" {
			return "", fmt.Errorf("%w: role has no own namespace", rag.ErrInvalidNamespace)
		}
		return role.ID, nil
	case rag.NamespaceDepartment:
		if strings.TrimSpace(role.UnitID) == "" {
			return "", fmt.Errorf("%w: role has no department namespace", rag.ErrInvalidNamespace)
		}
		return role.UnitID, nil
	default:
		return "", fmt.Errorf("%w: unknown namespace kind %q", rag.ErrInvalidNamespace, kind)
	}
}
