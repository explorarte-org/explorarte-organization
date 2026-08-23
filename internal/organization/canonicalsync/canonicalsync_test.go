package canonicalsync

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

// TestBindingHappensEvenWhenTheRegistryChangedNothing is the incident.
//
// An operator ran the registry sync, got "already synchronized", and stopped.
// The revision was current and unbound, and the next campaign died at its
// first model call. Skipping the egress step because the registry reported a
// no-op is not an optimization -- it is precisely how the gap survives, since
// whether the registry changed anything says nothing about whether the
// deployment can dispatch.
func TestBindingHappensEvenWhenTheRegistryChangedNothing(t *testing.T) {
	egress := &fakeEgress{}
	applier := Applier{Registry: fakeRegistry{result: registry.SyncResult{NoOp: true}}, Egress: egress}
	result, err := applier.Apply(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if egress.syncCalls != 1 {
		t.Fatalf("the egress binding must be established regardless of what the registry did, calls=%d", egress.syncCalls)
	}
	if !result.Registry.NoOp {
		t.Fatal("the registry half must still be reported")
	}
}

func TestApplyBindsTheRevisionItJustMadeCurrent(t *testing.T) {
	egress := &fakeEgress{}
	applier := Applier{
		Registry: fakeRegistry{result: registry.SyncResult{Applied: true, CanonicalHash: "abc"}},
		Egress:   egress,
	}
	result, err := applier.Apply(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !egress.appliedWith {
		t.Fatal("an applying registry sync must apply the egress binding too, not merely plan it")
	}
	if !result.Registry.Applied {
		t.Fatalf("both halves must be reported, got %+v", result)
	}
}

// A dry run must change nothing on either side. Binding during a --dry-run
// would make the preview a mutation.
func TestDryRunBindsNothing(t *testing.T) {
	egress := &fakeEgress{}
	applier := Applier{Registry: fakeRegistry{result: registry.SyncResult{}}, Egress: egress}
	if _, err := applier.Apply(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if egress.appliedWith {
		t.Fatal("a dry run must not bind anything")
	}
	if egress.syncCalls != 1 {
		t.Fatal("a dry run should still report what the egress half would do")
	}
}

// A failed binding leaves a durable, correct registry revision that nothing
// can dispatch under. The operation must say exactly that: reporting success
// would hand back a deployment that looks applied and cannot run, and
// reporting a plain failure would suggest the registry did not apply.
func TestAFailedBindingIsReportedAsAnUnboundRevision(t *testing.T) {
	egress := &fakeEgress{syncErr: errors.New("policy version conflict")}
	applier := Applier{Registry: fakeRegistry{result: registry.SyncResult{Applied: true}}, Egress: egress}
	result, err := applier.Apply(context.Background(), true)
	if !errors.Is(err, ErrRevisionUnbound) {
		t.Fatalf("want ErrRevisionUnbound, got %v", err)
	}
	if !errors.Is(err, egress.syncErr) {
		t.Fatalf("the underlying cause must survive so an operator can act on it, got %v", err)
	}
	if !result.Registry.Applied {
		t.Fatal("the registry half is durable and must still be reported; discarding it would suggest nothing happened")
	}
	if !strings.Contains(err.Error(), "re-running") {
		t.Fatalf("the error must say the state is repairable, got %v", err)
	}
}

func TestVerifyRefusesAnUnboundCurrentRevision(t *testing.T) {
	unbound := Applier{Egress: &fakeEgress{status: modelegress.RegistryStatus{OrganizationRevisionID: 6, Synchronized: false}}}
	status, err := unbound.Verify(context.Background())
	if !errors.Is(err, ErrRevisionUnbound) {
		t.Fatalf("want ErrRevisionUnbound, got %v", err)
	}
	if !strings.Contains(err.Error(), "6") {
		t.Fatalf("the refusal must name the revision that cannot dispatch, got %v", err)
	}
	if status.OrganizationRevisionID != 6 {
		t.Fatal("the status must still be returned so a caller can report it")
	}

	bound := Applier{Egress: &fakeEgress{status: modelegress.RegistryStatus{OrganizationRevisionID: 6, Synchronized: true}}}
	if _, err = bound.Verify(context.Background()); err != nil {
		t.Fatalf("a bound revision must verify: %v", err)
	}
}

// ---- fakes -----------------------------------------------------------

type fakeRegistry struct {
	result registry.SyncResult
	err    error
}

func (f fakeRegistry) SynchronizeCanonical(context.Context, bool) (registry.SyncResult, error) {
	return f.result, f.err
}

type fakeEgress struct {
	syncCalls   int
	appliedWith bool
	syncErr     error
	status      modelegress.RegistryStatus
}

func (f *fakeEgress) Sync(_ context.Context, apply bool) (modelegress.RegistrySyncResult, error) {
	f.syncCalls++
	if apply {
		f.appliedWith = true
	}
	if f.syncErr != nil {
		return modelegress.RegistrySyncResult{}, f.syncErr
	}
	return modelegress.RegistrySyncResult{Applied: apply}, nil
}

func (f *fakeEgress) Status(context.Context) (modelegress.RegistryStatus, error) {
	return f.status, nil
}
