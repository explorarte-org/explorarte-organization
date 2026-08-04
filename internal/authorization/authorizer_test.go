package authorization

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

type fakeReader struct {
	revision *registry.Revision
	roles    map[string]registry.Role
}

func (f *fakeReader) GetOrganization(context.Context, string) (registry.Organization, error) {
	return registry.Organization{}, nil
}
func (f *fakeReader) ListUnits(context.Context, string) ([]registry.Unit, error) { return nil, nil }
func (f *fakeReader) GetUnit(context.Context, string, string) (registry.Unit, error) {
	return registry.Unit{}, nil
}
func (f *fakeReader) GetRole(_ context.Context, _ string, id string) (registry.Role, error) {
	role, ok := f.roles[id]
	if !ok {
		return registry.Role{}, registry.ErrNotFound
	}
	return role, nil
}
func (f *fakeReader) ListRoles(context.Context, string, registry.RoleFilter) ([]registry.Role, error) {
	return nil, nil
}
func (f *fakeReader) GetLeader(context.Context, string, string) (registry.Role, error) {
	return registry.Role{}, nil
}
func (f *fakeReader) ListWorkers(context.Context, string, string) ([]registry.Role, error) {
	return nil, nil
}
func (f *fakeReader) GetCurrentRevision(context.Context, string) (*registry.Revision, error) {
	return f.revision, nil
}
func (f *fakeReader) LoadCurrentSnapshot(context.Context, string) (*registry.Snapshot, error) {
	return nil, nil
}

func TestDefaultDenyOwnerAndHardDeny(t *testing.T) {
	reader := &fakeReader{roles: map[string]registry.Role{
		"owner":  {ID: "owner", AuthorityClass: "owner", Enabled: true, Executable: false},
		"runner": {ID: "runner", AuthorityClass: "execution_service", Enabled: true, Executable: true},
	}}
	dir := filepath.Clean(filepath.Join("..", "..", "docs", "canonical"))
	authorizer, err := New(reader, "explorarte", dir)
	if err != nil {
		t.Fatal(err)
	}
	reader.revision = &registry.Revision{ID: 7, DocumentHashes: map[string]string{"capability-matrix.yaml": authorizer.MatrixHash()}}
	if err := authorizer.Authorize(context.Background(), "explorarte", 7, "owner", "code.commit"); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Authorize(context.Background(), "explorarte", 7, "owner", "cell.read_clinical_data"); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("hard deny got %v", err)
	}
	if err := authorizer.Authorize(context.Background(), "explorarte", 7, "runner", "code.stage_write"); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Authorize(context.Background(), "explorarte", 7, "runner", "project.create"); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("default deny got %v", err)
	}
	if err := authorizer.Authorize(context.Background(), "explorarte", 7, "runner", "unknown"); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("unknown got %v", err)
	}
}

func TestRevisionMismatchAndUnknownAuthority(t *testing.T) {
	reader := &fakeReader{roles: map[string]registry.Role{"x": {ID: "x", AuthorityClass: "mystery", Enabled: true, Executable: true}}}
	dir := filepath.Clean(filepath.Join("..", "..", "docs", "canonical"))
	authorizer, err := New(reader, "explorarte", dir)
	if err != nil {
		t.Fatal(err)
	}
	reader.revision = &registry.Revision{ID: 1, DocumentHashes: map[string]string{"capability-matrix.yaml": "bad"}}
	if err := authorizer.Authorize(context.Background(), "explorarte", 1, "x", "code.commit"); !errors.Is(err, ErrPolicyRevisionMismatch) {
		t.Fatalf("got %v", err)
	}
	reader.revision.DocumentHashes["capability-matrix.yaml"] = authorizer.MatrixHash()
	if err := authorizer.Authorize(context.Background(), "explorarte", 1, "x", "code.commit"); !errors.Is(err, ErrUnknownAuthorityClass) {
		t.Fatalf("got %v", err)
	}
}
