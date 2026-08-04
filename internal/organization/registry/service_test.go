package registry

import (
	"context"
	"testing"
	"time"
)

type fakeRepository struct {
	current *Snapshot
	applied int
}

func (f *fakeRepository) GetOrganization(context.Context, string) (Organization, error) {
	return f.current.Organization, nil
}
func (f *fakeRepository) ListUnits(context.Context, string) ([]Unit, error) {
	return f.current.Units, nil
}
func (f *fakeRepository) GetUnit(context.Context, string, string) (Unit, error) {
	return Unit{}, ErrNotFound
}
func (f *fakeRepository) GetRole(context.Context, string, string) (Role, error) {
	return Role{}, ErrNotFound
}
func (f *fakeRepository) ListRoles(context.Context, string, RoleFilter) ([]Role, error) {
	return f.current.Roles, nil
}
func (f *fakeRepository) GetLeader(context.Context, string, string) (Role, error) {
	return Role{}, ErrNotFound
}
func (f *fakeRepository) ListWorkers(context.Context, string, string) ([]Role, error) {
	return nil, nil
}
func (f *fakeRepository) GetCurrentRevision(context.Context, string) (*Revision, error) {
	if f.current == nil {
		return nil, ErrNotFound
	}
	return &Revision{ID: 1, CanonicalHash: f.current.CanonicalHash}, nil
}
func (f *fakeRepository) LoadCurrentSnapshot(context.Context, string) (*Snapshot, error) {
	return f.current, nil
}
func (f *fakeRepository) Apply(_ context.Context, s Snapshot) (SyncResult, error) {
	f.applied++
	d := CompareSnapshots(f.current, s)
	f.current = &s
	return SyncResult{Applied: true, CanonicalHash: s.CanonicalHash, Diff: d}, nil
}

func TestDryRunDoesNotWriteAndNoOpIsDetected(t *testing.T) {
	loader := mustLoader(t, canonicalDirForTest(t))
	repo := &fakeRepository{}
	service, err := NewService(loader, repo, "explorarte", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if repo.applied != 0 || result.Applied || result.NoOp {
		t.Fatalf("dry run wrote: result=%+v applied=%d", result, repo.applied)
	}
	result, err = service.SynchronizeCanonical(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || repo.applied != 1 {
		t.Fatalf("apply=%+v count=%d", result, repo.applied)
	}
	result, err = service.SynchronizeCanonical(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoOp || repo.applied != 1 {
		t.Fatalf("no-op=%+v count=%d", result, repo.applied)
	}
}
