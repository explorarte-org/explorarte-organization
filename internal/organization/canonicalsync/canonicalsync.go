// Package canonicalsync applies the organization's canonical state to the
// database as one operation.
//
// It exists because that operation was two commands and the seam between them
// was an unwritten rule. `registry sync --apply` creates a new organization
// revision and makes it current in a single transaction. Every execution binds
// to the current revision, and dispatching under a revision requires a durable
// model-egress binding for that exact revision -- which the registry sync does
// not create. So the moment the sync commits, the organization is current on a
// revision nothing may dispatch under, and stays there until somebody
// separately remembers `model egress sync --apply`.
//
// Nothing detected the gap. The first thing to notice was a dispatch failing
// deep inside Model Runtime with "model egress policy not found", after the
// campaign had been created and paid for. That is what happened to root 124.
//
// The two steps are not independent operations that happen to be run together.
// The egress policy is part of the canonical state, and binding it to the
// revision that canonical state produced is what finishes applying it. So they
// are one operation here, and the incoherent intermediate state is something
// this package refuses to leave behind rather than something an operator has
// to remember.
package canonicalsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

// ErrRevisionUnbound means the organization is current on a revision that has
// no durable egress binding, so nothing can dispatch under it.
//
// It is a distinct error because it describes a specific, repairable state
// rather than a failure to act: the registry revision is durable and correct,
// and re-running the apply completes it.
var ErrRevisionUnbound = errors.New("organization revision has no model egress binding")

// RegistryApplier applies the canonical registry documents.
type RegistryApplier interface {
	SynchronizeCanonical(ctx context.Context, apply bool) (registry.SyncResult, error)
}

// EgressBinder binds the canonical egress policy to whichever revision is
// current. Its Sync always targets the current revision, which is what makes
// running it after the registry apply the correct order rather than merely a
// convention.
type EgressBinder interface {
	Sync(ctx context.Context, apply bool) (modelegress.RegistrySyncResult, error)
	Status(ctx context.Context) (modelegress.RegistryStatus, error)
}

// Applier composes the two into the operation an operator actually intends.
type Applier struct {
	Registry RegistryApplier
	Egress   EgressBinder
}

// Result reports both halves, because an operator who is told only that the
// registry applied has been told the less important half.
type Result struct {
	Registry registry.SyncResult            `json:"registry"`
	Egress   modelegress.RegistrySyncResult `json:"egress"`
}

// Apply brings the database to the canonical state and refuses to report
// success while the current revision is unbound.
//
// The egress binding is established even when the registry itself is a no-op.
// That case is not an optimization to skip: it is exactly how the gap survives
// unnoticed. A previous apply that created the revision and then failed, or an
// operator who ran the registry sync and stopped, both leave a database whose
// registry reports "already synchronized" while nothing can dispatch. Asking
// the registry whether it changed anything tells you nothing about whether the
// deployment is executable.
func (a Applier) Apply(ctx context.Context, apply bool) (Result, error) {
	if a.Registry == nil || a.Egress == nil {
		return Result{}, errors.New("canonicalsync requires both a registry and an egress binder")
	}
	registryResult, err := a.Registry.SynchronizeCanonical(ctx, apply)
	if err != nil {
		return Result{}, fmt.Errorf("apply canonical registry: %w", err)
	}
	// A dry run must not bind anything, but it should still say whether the
	// deployment would be left executable.
	egressResult, err := a.Egress.Sync(ctx, apply)
	if err != nil {
		// The registry revision is durable and correct; only the binding is
		// missing. Saying so is the difference between a failure an operator
		// can act on and one they discover from a dead campaign.
		return Result{Registry: registryResult}, fmt.Errorf(
			"%w: the registry revision is applied and current, but binding the canonical egress policy to it failed; nothing can dispatch until this succeeds, and re-running this command completes it: %w",
			ErrRevisionUnbound, err)
	}
	return Result{Registry: registryResult, Egress: egressResult}, nil
}

// Verify reports whether the current revision is executable, without changing
// anything.
//
// It answers the question a deployment check actually has -- "can this
// organization dispatch right now" -- which "is the registry synchronized"
// never did.
func (a Applier) Verify(ctx context.Context) (modelegress.RegistryStatus, error) {
	if a.Egress == nil {
		return modelegress.RegistryStatus{}, errors.New("canonicalsync requires an egress binder")
	}
	status, err := a.Egress.Status(ctx)
	if err != nil {
		return modelegress.RegistryStatus{}, err
	}
	if !status.Synchronized {
		return status, fmt.Errorf("%w: revision %d is current but its canonical egress policy is not bound",
			ErrRevisionUnbound, status.OrganizationRevisionID)
	}
	return status, nil
}
