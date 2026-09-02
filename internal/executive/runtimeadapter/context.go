package runtimeadapter

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

type Context struct {
	Service        contextengine.Service
	Assembly       contextcompiler.ContextAssemblyService
	OrganizationID string
}

// Build returns the snapshot together with its provider-visible render. The
// render is not a convenience: Model Runtime requires an invocation's stable
// prefix to be the byte-exact provider-visible render of the snapshot it
// references, so a caller that only knew the ID could not construct a valid
// model input at all.
//
// The render goes through the exact same contextcompiler.ResolveProviderContext
// Model Runtime's own context adapter uses (internal/modelruntime/bootstrap),
// instead of the plain canonical contextengine.Service.Render envelope: the
// two previously diverged for any projected task class (e.g.
// research.corpus_curate/v1), which made a stable prefix Executive sent
// through the Harness fail Model Runtime's byte-exact binding check. This is
// the single shared provider-visible resolution algorithm; do not
// reimplement any part of it here.
func (a Context) Build(ctx context.Context, request executive.ContextRequest) (executive.ContextSnapshot, error) {
	result, err := a.Service.Build(ctx, contextengine.BuildRequest{
		OrganizationID:         a.OrganizationID,
		OrganizationRevisionID: request.OrganizationRevisionID,
		ActorRoleID:            request.ActorRoleID,
		Purpose:                request.Purpose,
		TaskRef:                request.TaskRef,
		TaskClass:              request.TaskClass,
		ExecutionPurpose:       request.ExecutionPurpose,
		ActorUnitID:            request.ActorUnitID,
		RepositoryBaseSHA:      request.RepositoryBaseSHA,
		RepositoryQuery:        request.RepositoryQuery,
		RepositorySubjects:     request.RepositorySubjects,
		RepositorySlots:        mapSlots(request.RepositorySlots),
		IdempotencyKey:         request.IdempotencyKey,
		CorrelationID:          request.CorrelationID,
		CausationID:            request.CausationID,
	})
	if err != nil {
		// G1-005: a content-rejection from context assembly (most commonly
		// a role's PERFIL.md missing from the physical file tree despite
		// the role being present and executable in the canonical/DB role
		// catalog) is a host finding Executive must classify cleanly, never
		// a raw filesystem error propagated as-is. This is the one seam
		// where contextengine's own error space is translated into
		// Executive's, exactly so internal/executive itself never needs to
		// import internal/contextengine to recognize it.
		if errors.Is(err, contextengine.ErrRejected) {
			return executive.ContextSnapshot{}, fmt.Errorf("%w: %s", executive.ErrContextSourceRejected, err.Error())
		}
		return executive.ContextSnapshot{}, err
	}
	// Fetch and validate the canonical snapshot the same way
	// contextengine.Service.Render did internally, since this adapter now
	// resolves the provider-visible render itself rather than delegating
	// the whole fetch+validate+render sequence to Service.Render.
	snapshot, err := a.Service.Get(ctx, result.Snapshot.ID, true)
	if err != nil {
		return executive.ContextSnapshot{}, fmt.Errorf("get context snapshot %d: %w", result.Snapshot.ID, err)
	}
	if snapshot.Status == contextengine.SnapshotInvalidated {
		return executive.ContextSnapshot{}, contextengine.ErrSnapshotInvalidated
	}
	validation, err := a.Service.Validate(ctx, result.Snapshot.ID)
	if err != nil {
		return executive.ContextSnapshot{}, fmt.Errorf("validate context snapshot %d: %w", result.Snapshot.ID, err)
	}
	if !validation.Valid {
		return executive.ContextSnapshot{}, contextengine.ErrSnapshotStale
	}
	// ResolveAndPersist resolves through the same
	// contextcompiler.ResolveProviderContext Model Runtime uses, and
	// durably records the result (M1.1): the same canonical snapshot
	// resolved by Model Runtime's own context adapter returns the
	// identical persisted ExecutionContextView, not merely equal bytes.
	view, err := a.Assembly.ResolveAndPersist(ctx, snapshot)
	if err != nil {
		return executive.ContextSnapshot{}, fmt.Errorf("resolve provider-visible context %d: %w", result.Snapshot.ID, err)
	}
	// Digest is recomputed from the exact bytes just resolved, not passed
	// through from Snapshot.RenderedHash (the canonical render's own
	// digest): the Harness independently re-derives sha256(Content) and
	// compares it against Digest, so this stays self-consistent regardless
	// of whether projection changed the bytes relative to canonical.
	return executive.ContextSnapshot{
		ID:                     result.Snapshot.ID,
		Version:                strconv.FormatInt(result.Snapshot.Version, 10),
		Digest:                 view.ProviderVisibleDigest,
		Content:                string(view.ProviderVisibleBytes),
		ExecutionContextViewID: view.ID,
	}, nil
}

var _ executive.ContextCoordinator = Context{}

// mapSlots carries the normative (subject, relation) pairs across the port
// boundary unchanged: checkpoint D's whole point is that the relation
// survives the journey into selection.
func mapSlots(slots []executive.EvidenceSlot) []contextengine.RepositorySlot {
	if len(slots) == 0 {
		return nil
	}
	out := make([]contextengine.RepositorySlot, 0, len(slots))
	for _, slot := range slots {
		out = append(out, contextengine.RepositorySlot{Subject: slot.Subject, Relation: slot.Relation})
	}
	return out
}
