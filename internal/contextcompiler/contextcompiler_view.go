package contextcompiler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// ExecutionContextView is the durable, immutable record of the exact
// provider-visible view ResolveProviderContext resolved for one canonical
// contextengine.Snapshot. It is a DERIVED record, not a second source of
// truth: the canonical snapshot (contextengine.Snapshot, its Segments, and
// its own RenderedHash) remains the source-of-truth input and is never
// duplicated or reinterpreted here. This type persists R10's
// ExecutionContextView (CompilationResult) together with the resolved
// provider-visible bytes/digest, so a historical view remains reloadable
// and auditable even if compiler/profile code changes later.
type ExecutionContextView struct {
	// ID is the durable identity of this view. Executive and Model Runtime
	// share this exact ID for the same canonical snapshot: both resolve
	// through the same ContextAssemblyService, which persists (or finds)
	// exactly one view per context_snapshot_id.
	ID                    int64
	OrganizationID        string
	ContextSnapshotID     int64
	ContextProfileID      string
	ContextProfileVersion string

	FellBackToCanonical bool
	FallbackReason      string

	// ProviderRenderVersion/StablePrefixHash/DynamicSuffixHash are only
	// populated when FellBackToCanonical is false (a compiled
	// ProviderRender was produced).
	ProviderRenderVersion string
	StablePrefixHash      string
	StablePrefixBytes     int
	DynamicSuffixHash     string
	DynamicSuffixBytes    int

	AuthorityOrderHash  string
	CompiledContentHash string
	SegmentDiffs        []SegmentDiff

	// ProviderVisibleBytes/Digest/ByteCount are the exact bytes the
	// provider was allowed to see and their SHA-256 digest. Persisted
	// verbatim (not reconstructed from the canonical snapshot at read
	// time) so a historical view survives future compiler/profile
	// evolution -- see ResolveProviderContext's doc comment on why this is
	// the single resolution algorithm this type must never reimplement.
	ProviderVisibleBytes     []byte
	ProviderVisibleDigest    string
	ProviderVisibleByteCount int

	CreatedAt time.Time
}

// ErrExecutionContextViewDrift is returned when a caller attempts to
// persist a view for a context_snapshot_id that already has a durable view
// on record, and the new attempt's content/metadata does not match the
// existing one. This is a fail-closed collision, never a silent overwrite
// or a silent return of the caller's (possibly wrong) attempt: the existing
// durable record always wins, and drift is always reported as an error.
var ErrExecutionContextViewDrift = errors.New("execution context view drift: existing durable view does not match")

// ErrExecutionContextViewIntegrity is returned when a loaded
// ExecutionContextView's persisted digest does not match SHA-256 of its
// persisted bytes -- on-read tamper/corruption detection.
var ErrExecutionContextViewIntegrity = errors.New("execution context view integrity check failed")

// ErrExecutionContextViewNotFound is returned when no durable view exists
// for the requested ID or (organization, context snapshot) pair.
var ErrExecutionContextViewNotFound = errors.New("execution context view not found")

// ExecutionContextViewStore is the durable persistence boundary for
// ExecutionContextView. Implementations MUST be idempotent per
// ContextSnapshotID (Persist called twice for the same snapshot with the
// same resolved content returns the same ID) and MUST fail closed
// (ErrExecutionContextViewDrift) on any attempt to persist different
// content for a snapshot that already has a durable view.
type ExecutionContextViewStore interface {
	// Persist durably records view. If a view already exists for
	// view.ContextSnapshotID, Persist returns the EXISTING view when its
	// content matches, or ErrExecutionContextViewDrift when it does not.
	// Persist never updates or replaces an existing row.
	Persist(ctx context.Context, view ExecutionContextView) (ExecutionContextView, error)
	// Get loads a durable view by its own ID, verifying
	// SHA-256(ProviderVisibleBytes) == ProviderVisibleDigest on read and
	// returning ErrExecutionContextViewIntegrity if it does not.
	Get(ctx context.Context, id int64) (ExecutionContextView, error)
	// GetByContextSnapshot loads the durable view for a canonical snapshot,
	// if one exists, with the same integrity verification as Get.
	GetByContextSnapshot(ctx context.Context, organizationID string, contextSnapshotID int64) (ExecutionContextView, error)
}

// ContextAssemblyService is the owner of the durable ExecutionContextView
// boundary: it wraps the single shared ResolveProviderContext resolver with
// durable persistence, so Executive and Model Runtime both resolve AND
// durably record the same view identity for the same canonical snapshot,
// instead of each independently reconstructing an equal-but-unlinked byte
// slice. It does not reimplement any part of CompileForTaskClass,
// BuildProviderRender, or the PortableRenderer fallback.
type ContextAssemblyService struct {
	Store ExecutionContextViewStore
}

// ResolveAndPersist resolves canonical's provider-visible view through
// ResolveProviderContext (the single resolution algorithm) and durably
// records it through Store, returning the durable, idempotent
// ExecutionContextView. Calling this twice for the same canonical.ID
// returns the same view (same ID, same bytes, same digest); calling it for
// a snapshot whose durable view was already recorded with different
// content fails closed with ErrExecutionContextViewDrift.
func (s ContextAssemblyService) ResolveAndPersist(ctx context.Context, canonical contextengine.Snapshot) (ExecutionContextView, error) {
	if s.Store == nil {
		return ExecutionContextView{}, errors.New("contextcompiler: ContextAssemblyService requires a Store")
	}
	resolved, err := ResolveProviderContext(ctx, canonical)
	if err != nil {
		return ExecutionContextView{}, err
	}
	compilation := resolved.Compilation
	view := ExecutionContextView{
		OrganizationID:           canonical.OrganizationID,
		ContextSnapshotID:        canonical.ID,
		ContextProfileID:         compilation.ContextProfileID,
		ContextProfileVersion:    compilation.ContextProfileVersion,
		FellBackToCanonical:      resolved.FellBack,
		FallbackReason:           resolved.FallbackReason,
		AuthorityOrderHash:       compilation.AuthorityOrderHash,
		CompiledContentHash:      compilation.CompiledContentHash,
		SegmentDiffs:             compilation.SegmentDiffs,
		ProviderVisibleBytes:     resolved.Bytes,
		ProviderVisibleDigest:    resolved.Digest,
		ProviderVisibleByteCount: len(resolved.Bytes),
	}
	if !resolved.FellBack {
		view.ProviderRenderVersion = resolved.ProviderRender.Version
		view.StablePrefixHash = resolved.ProviderRender.StablePrefixHash
		view.StablePrefixBytes = resolved.ProviderRender.StablePrefixBytes
		view.DynamicSuffixHash = resolved.ProviderRender.DynamicSuffixHash
		view.DynamicSuffixBytes = resolved.ProviderRender.DynamicSuffixBytes
	} else {
		view.StablePrefixBytes = compilation.StablePrefixBytes
		view.DynamicSuffixBytes = compilation.DynamicSuffixBytes
	}
	persisted, err := s.Store.Persist(ctx, view)
	if err != nil {
		return ExecutionContextView{}, fmt.Errorf("persist execution context view for snapshot %d: %w", canonical.ID, err)
	}
	return persisted, nil
}
