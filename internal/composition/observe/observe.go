// Package observe reads the real world and reports it as a
// composition.Observation.
//
// It exists as its own package so that internal/composition stays ignorant.
// Composition knows what a key means and what has to be true of one; it must
// never learn where the value comes from, because the moment it imports a
// store it stops being a description and starts being infrastructure.
//
// The rule this package keeps is that a value it cannot read is a value it
// does not report. A reader that fails omits its key and says why, and an
// omitted key denies admission rather than passing it. Nothing here ever
// fabricates a plausible number to keep an observation looking complete: a
// complete-looking observation built from a failed read is worse than an
// incomplete one, because the incomplete one is refused and the fabricated
// one is acted upon.
package observe

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/Mireuz13/explorarte-organization/internal/composition"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

// EgressStatusReader reports the canonical revision in force and whether the
// egress policy is bound to it. This is the same read canonicalsync.Verify
// makes; the difference is that here it becomes two separate observed values
// instead of one pass/fail.
type EgressStatusReader interface {
	Status(ctx context.Context) (modelegress.RegistryStatus, error)
}

// SchemaTipReader reports the highest migration the database has applied.
type SchemaTipReader interface {
	DatabaseSchemaTip(ctx context.Context) (int64, error)
}

// DesiredBuildReader reports the build the organization has decided it should
// be running -- the commit a promotion put on the target ref, not the one any
// process happens to be executing.
type DesiredBuildReader interface {
	DesiredSHA(ctx context.Context) (string, error)
}

// Observer assembles an Observation from whichever readers it was given. A
// nil reader is not an error: it is a fact this deployment cannot see yet,
// and the keys it would have filled stay unobserved.
type Observer struct {
	Egress  EgressStatusReader
	Schema  SchemaTipReader
	Desired DesiredBuildReader

	// Fleet reports what the running fleet is. It is a read like any
	// other, and deliberately NOT the local buildinfo: the process doing
	// the observing is not the process being observed, and substituting
	// one for the other produces a refusal whose reason is about the
	// wrong subject.
	Fleet FleetBuildReader
}

// Result is what a single observation pass produced.
type Result struct {
	Observation composition.Observation

	// Unobserved says, for each key that is missing, why. A reconciler
	// that reports "not admitted" without this is telling an operator
	// that something is wrong and refusing to say what.
	Unobserved map[composition.Key]string
}

// Observe reads every source once and returns what it managed to see.
//
// It does not stop at the first failure. A partial observation is useful --
// the keys that were read still gate what they gate, and the ones that were
// not deny admission on their own -- while an aborted pass leaves the
// reconciler with nothing at all and turns one unreachable store into total
// blindness. This is the same shape the Executive worker already uses, where
// a failed reconciliation sweep does not stop the rest of the turn.
func (o Observer) Observe(ctx context.Context) Result {
	result := Result{
		Observation: composition.Observation{},
		Unobserved:  map[composition.Key]string{},
	}

	o.observeFleet(ctx, &result)

	if o.Schema == nil {
		result.miss(composition.KeyDatabaseSchemaTip, "no schema reader configured")
	} else if tip, err := o.Schema.DatabaseSchemaTip(ctx); err != nil {
		result.miss(composition.KeyDatabaseSchemaTip, fmt.Sprintf("reading the database schema tip failed: %v", err))
	} else {
		result.Observation[composition.KeyDatabaseSchemaTip] = strconv.FormatInt(tip, 10)
	}

	o.observeCanonical(ctx, &result)

	if o.Desired == nil {
		result.miss(composition.KeyRuntimeDesiredSHA, "no desired-build reader configured")
	} else if sha, err := o.Desired.DesiredSHA(ctx); err != nil {
		result.miss(composition.KeyRuntimeDesiredSHA, fmt.Sprintf("reading the desired build failed: %v", err))
	} else if sha == "" {
		result.miss(composition.KeyRuntimeDesiredSHA, "no build has been promoted")
	} else {
		result.Observation[composition.KeyRuntimeDesiredSHA] = sha
	}

	return result
}

// observeCanonical turns one egress status into two observed values.
//
// The revision in force is a number the status carries. The revision egress
// is bound to is only knowable from this read when the two agree: an
// unsynchronized status says the binding is not current, but it does not say
// which older revision it is on. So when they disagree, the binding is
// reported as unobserved rather than guessed -- which denies admission,
// which is the same refusal canonicalsync already makes, arrived at without
// inventing a number to justify it.
func (o Observer) observeCanonical(ctx context.Context, result *Result) {
	if o.Egress == nil {
		result.miss(composition.KeyOrganizationRevision, "no egress status reader configured")
		result.miss(composition.KeyEgressBoundRevision, "no egress status reader configured")
		return
	}
	status, err := o.Egress.Status(ctx)
	if err != nil {
		reason := fmt.Sprintf("reading canonical egress status failed: %v", err)
		result.miss(composition.KeyOrganizationRevision, reason)
		result.miss(composition.KeyEgressBoundRevision, reason)
		return
	}
	if status.OrganizationRevisionID <= 0 {
		result.miss(composition.KeyOrganizationRevision, "the canonical registry reports no current revision")
		result.miss(composition.KeyEgressBoundRevision, "there is no current revision for a binding to be current with")
		return
	}
	revision := strconv.FormatInt(status.OrganizationRevisionID, 10)
	result.Observation[composition.KeyOrganizationRevision] = revision
	if status.Synchronized {
		result.Observation[composition.KeyEgressBoundRevision] = revision
		return
	}
	result.miss(composition.KeyEgressBoundRevision, fmt.Sprintf(
		"revision %s is current but its canonical egress policy is not bound to it, and the status does not say which revision it is bound to",
		revision))
}

// observeFleet asks the running fleet what it is.
//
// There is no fallback to the local binary, and that absence is the point. A
// reconciler runs beside the runtime, not inside it, so its own commit and
// migration tip say nothing about the thing it is judging. Reporting them
// under these keys produces an answer shaped like a diagnosis and aimed at
// the wrong subject -- which is worse than no answer, because no answer is
// refused and a wrong one is acted upon.
func (o Observer) observeFleet(ctx context.Context, result *Result) {
	if o.Fleet == nil {
		const reason = "no fleet build reader configured; this process must not report its own build as the fleet's"
		result.miss(composition.KeyRuntimeObservedSHA, reason)
		result.miss(composition.KeyRuntimeSchemaCompatibility, reason)
		return
	}
	info, err := o.Fleet.FleetBuild(ctx)
	if err != nil {
		reason := fmt.Sprintf("asking the fleet what it is failed: %v", err)
		result.miss(composition.KeyRuntimeObservedSHA, reason)
		result.miss(composition.KeyRuntimeSchemaCompatibility, reason)
		return
	}
	if info.Commit != "" && info.Commit != "unknown" {
		result.Observation[composition.KeyRuntimeObservedSHA] = info.Commit
	} else {
		result.miss(composition.KeyRuntimeObservedSHA, "the fleet reports no commit; it was built without one injected")
	}
	// Today a binary accepts exactly the migration it was compiled at, so
	// this is a set of one and MemberOf degenerates to equality. That is
	// the truth and it is worth stating plainly rather than widening the
	// set to look flexible. The set-valued key is what lets a binary that
	// genuinely does span two migrations say so later, without the
	// predicate or anything reading it having to change.
	if info.MigrationTip > 0 {
		result.Observation[composition.KeyRuntimeSchemaCompatibility] = strconv.FormatInt(info.MigrationTip, 10)
	} else {
		result.miss(composition.KeyRuntimeSchemaCompatibility, "the fleet reports no migration tip")
	}
}

func (r *Result) miss(key composition.Key, reason string) {
	r.Unobserved[key] = reason
}

// Missing returns the unobserved keys, sorted, for a stable report.
func (r Result) Missing() []composition.Key {
	out := make([]composition.Key, 0, len(r.Unobserved))
	for k := range r.Unobserved {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
