package contextcompiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
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

	// SelectionKind/SelectorAlgorithmVersion are M1.3's durable selection
	// provenance (section 14): "why did this view use this profile,"
	// answerable after restart. Neither duplicates the selector facts
	// themselves (TaskClass/ExecutionPurpose/ActorUnitID already live
	// durably on the canonical context_snapshots row -- see M1.3 section
	// 14's "do not duplicate large selector payloads" instruction).
	SelectionKind            SelectionKind
	SelectorAlgorithmVersion string

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

// ErrExecutionContextViewIntegrity is returned when a view's declared
// ProviderVisibleByteCount or ProviderVisibleDigest does not match its own
// ProviderVisibleBytes -- see ValidateIntegrity, called both before Persist
// writes anything and on every read.
var ErrExecutionContextViewIntegrity = errors.New("execution context view integrity check failed")

// ErrExecutionContextViewNotFound is returned when no durable view exists
// for the requested ID or (organization, context snapshot) pair.
var ErrExecutionContextViewNotFound = errors.New("execution context view not found")

// SameLogicalView is the single shared definition of "these two attempts
// to persist a view for the same ContextSnapshotID describe the same
// logical execution view." Both ExecutionContextViewStore implementations
// (internal/contextcompiler/postgres and MemoryStore) MUST call this --
// never their own partial field comparison -- to decide between "return the
// existing idempotent view" and "reject as ErrExecutionContextViewDrift".
// It compares every durably meaningful field a caller supplies (everything
// except the store-assigned ID and CreatedAt): a mismatch in ANY of them,
// including metadata that never touches the provider-visible bytes
// themselves (FallbackReason, AuthorityOrderHash, SegmentDiffs, the
// StablePrefix/DynamicSuffix partition), is drift, not merely a mismatch in
// bytes/digest -- ExecutionContextView is an audited historical record, and
// a metadata-only divergence that went undetected here would let a later
// reader believe it reconstructed the original resolution when it did not.
func SameLogicalView(a, b ExecutionContextView) bool {
	if a.OrganizationID != b.OrganizationID ||
		a.ContextSnapshotID != b.ContextSnapshotID ||
		a.ContextProfileID != b.ContextProfileID ||
		a.ContextProfileVersion != b.ContextProfileVersion ||
		a.FellBackToCanonical != b.FellBackToCanonical ||
		a.FallbackReason != b.FallbackReason ||
		a.SelectionKind != b.SelectionKind ||
		a.SelectorAlgorithmVersion != b.SelectorAlgorithmVersion ||
		a.ProviderRenderVersion != b.ProviderRenderVersion ||
		a.StablePrefixHash != b.StablePrefixHash ||
		a.StablePrefixBytes != b.StablePrefixBytes ||
		a.DynamicSuffixHash != b.DynamicSuffixHash ||
		a.DynamicSuffixBytes != b.DynamicSuffixBytes ||
		a.AuthorityOrderHash != b.AuthorityOrderHash ||
		a.CompiledContentHash != b.CompiledContentHash ||
		a.ProviderVisibleDigest != b.ProviderVisibleDigest ||
		a.ProviderVisibleByteCount != b.ProviderVisibleByteCount {
		return false
	}
	if !bytes.Equal(a.ProviderVisibleBytes, b.ProviderVisibleBytes) {
		return false
	}
	return sameSegmentDiffs(a.SegmentDiffs, b.SegmentDiffs)
}

// sameSegmentDiffs treats a nil slice and an empty slice as equal (the
// PostgreSQL store normalizes nil to [] before persisting, since JSON null
// would otherwise fail the segment_diffs CHECK constraint) but is otherwise
// a strict, order-sensitive comparison -- SegmentDiffs is a positional audit
// record.
func sameSegmentDiffs(a, b []SegmentDiff) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// ValidateIntegrity is the single shared definition of "this view's
// declared metadata actually matches its own bytes." Both
// ExecutionContextViewStore implementations MUST call it on every Persist,
// BEFORE attempting to write anything, so an invalid record is rejected by
// Go with ErrExecutionContextViewIntegrity rather than relying solely on a
// database CHECK constraint to catch it (a store backed by a database
// without an equivalent constraint would otherwise silently accept
// corrupt content) -- and again on every Get/GetByContextSnapshot, so a
// record that became corrupt after being written (tampering, a storage
// bug) is never returned as valid either.
func ValidateIntegrity(view ExecutionContextView) error {
	if view.ProviderVisibleByteCount != len(view.ProviderVisibleBytes) {
		return fmt.Errorf("%w: declared byte count %d does not match %d actual bytes", ErrExecutionContextViewIntegrity, view.ProviderVisibleByteCount, len(view.ProviderVisibleBytes))
	}
	if view.ProviderVisibleDigest != sha256Hex(view.ProviderVisibleBytes) {
		return fmt.Errorf("%w: digest does not match bytes", ErrExecutionContextViewIntegrity)
	}
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ExecutionContextViewStore is the durable persistence boundary for
// ExecutionContextView. Implementations MUST be idempotent per
// ContextSnapshotID (Persist called twice for the same snapshot with the
// same resolved content returns the same ID) and MUST fail closed
// (ErrExecutionContextViewDrift) on any attempt to persist different
// content for a snapshot that already has a durable view.
type ExecutionContextViewStore interface {
	// Persist durably records view. If a view already exists for
	// view.ContextSnapshotID, Persist returns the EXISTING view when
	// SameLogicalView(existing, view) is true, or ErrExecutionContextViewDrift
	// when it is not -- implementations must not compare only a subset of
	// fields (e.g. bytes/digest alone); see SameLogicalView's doc comment.
	// Persist never updates or replaces an existing row. Implementations
	// must reject an invalid view (see ValidateIntegrity) before ever
	// attempting to write it.
	Persist(ctx context.Context, view ExecutionContextView) (ExecutionContextView, error)
	// Get loads a durable view by its own ID, running ValidateIntegrity on
	// read and returning ErrExecutionContextViewIntegrity if it fails.
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
//
// A durable view already on record for canonical.ID is returned AS-IS,
// without ever re-running ResolveProviderContext -- this is the entire
// point of ExecutionContextView being a durable, immutable, DERIVED
// record (see its own doc comment): once sealed, it must remain
// reloadable even after the compiler/selector algorithm that produced it
// changes. M1.3 made this correctness-critical rather than merely an
// optimization: a durable view's SelectorAlgorithmVersion is itself part
// of SameLogicalView's comparison, so a historical row sealed under an
// older algorithm version would otherwise be reported as drift the
// instant the algorithm version changes, even though its content never
// actually contradicts anything -- recompiling it was never the right
// question to ask in the first place. Store.Persist's own drift/race
// handling still applies to any genuinely concurrent or contradictory
// write attempt; this short-circuit only avoids asking the question for
// an artifact that already has a sealed, authoritative answer.
func (s ContextAssemblyService) ResolveAndPersist(ctx context.Context, canonical contextengine.Snapshot) (ExecutionContextView, error) {
	if s.Store == nil {
		return ExecutionContextView{}, errors.New("contextcompiler: ContextAssemblyService requires a Store")
	}
	if existing, getErr := s.Store.GetByContextSnapshot(ctx, canonical.OrganizationID, canonical.ID); getErr == nil {
		return existing, nil
	} else if !errors.Is(getErr, ErrExecutionContextViewNotFound) {
		return ExecutionContextView{}, fmt.Errorf("check for an existing execution context view for snapshot %d: %w", canonical.ID, getErr)
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
		SelectionKind:            compilation.SelectionKind,
		SelectorAlgorithmVersion: SelectorAlgorithmVersion,
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
