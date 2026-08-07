package roles

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type fakeRoleReader struct {
	role registry.Role
	err  error
}

func (f fakeRoleReader) GetRole(context.Context, string, string) (registry.Role, error) {
	return f.role, f.err
}

func TestResolveNamespaceDerivesFromRegistryNotFreeText(t *testing.T) {
	resolver, err := New(fakeRoleReader{role: registry.Role{ID: "ingenieria_ia/frontend", UnitID: "ingenieria_ia"}})
	if err != nil {
		t.Fatal(err)
	}
	own, err := resolver.ResolveNamespace(context.Background(), "explorarte", "ingenieria_ia/frontend", rag.NamespaceOwn)
	if err != nil || own != "ingenieria_ia/frontend" {
		t.Fatalf("own=%q err=%v", own, err)
	}
	department, err := resolver.ResolveNamespace(context.Background(), "explorarte", "ingenieria_ia/frontend", rag.NamespaceDepartment)
	if err != nil || department != "ingenieria_ia" {
		t.Fatalf("department=%q err=%v", department, err)
	}
}

func TestResolveNamespaceFailsWhenRoleHasNoUnit(t *testing.T) {
	resolver, err := New(fakeRoleReader{role: registry.Role{ID: "empresa/human", UnitID: ""}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveNamespace(context.Background(), "explorarte", "empresa/human", rag.NamespaceDepartment); !errors.Is(err, rag.ErrInvalidNamespace) {
		t.Fatalf("missing unit err=%v", err)
	}
}

func TestResolveNamespacePropagatesReaderError(t *testing.T) {
	resolver, err := New(fakeRoleReader{err: errors.New("role not found")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveNamespace(context.Background(), "explorarte", "unknown/role", rag.NamespaceOwn); err == nil {
		t.Fatal("expected reader error to propagate")
	}
}
