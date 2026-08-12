package contextengine

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// ProviderRenderVersion identifies the deterministic serialization contract
// implemented by BuildProviderRender. Bump this string (and add a new
// versioned build function) whenever ordering, serialization, the
// stable/dynamic partition, or a provider-specific wrapper changes -- never
// mutate the meaning of an already-shipped version in place (R10.4 section
// 14).
const (
	ProviderRenderVersionV1 = "research-corpus-curate-render/v1"
	ProviderRenderVersionV2 = "research-corpus-curate-render/v2" // with structural wrapping for untrusted data
)

// providerRenderSeparator joins segment contents deterministically. A fixed
// two-newline separator, never a random/timestamped boundary.
var providerRenderSeparator = []byte("\n\n")

// dynamicAuthorityTiers is the closed set of AuthorityTier values whose
// segment content is instance-scoped (varies per task/request), never
// actor/role/policy-scoped. This is a structural property of the tier
// vocabulary itself (internal/contextengine/domain.go), not a
// research.corpus_curate-specific rule -- any task class using these tiers
// gets the same stable/dynamic partition for free, without a provider- or
// task-class-specific branch (R10.4 section 15).
//
// This is the ONLY place this set is declared (R31 hardening §18.3):
// internal/contextcompiler's telemetry used to independently re-declare a
// narrower version of this rule (only TierTask, missing
// TierProject/TierRAGEvidence/TierApprovedMemory/TierApprovedSkill) --
// confirmed as a real inconsistency, not a hypothetical. Both packages now
// call IsDynamicProviderTier below instead of maintaining their own
// map/switch.
var dynamicAuthorityTiers = map[AuthorityTier]bool{
	TierTask:           true,
	TierProject:        true,
	TierRAGEvidence:    true,
	TierApprovedMemory: true,
	TierApprovedSkill:  true,
}

// IsDynamicProviderTier is the single source of truth for the
// StablePrefix/DynamicSuffix partition rule used by both ProviderRender
// (this package) and internal/contextcompiler's telemetry. A tier not in
// this closed set is stable (actor/role/policy-scoped, byte-identical
// across invocations of the same actor+task-class+profile); a tier in this
// set is dynamic (varies per task/request instance).
func IsDynamicProviderTier(tier AuthorityTier) bool {
	return dynamicAuthorityTiers[tier]
}

// ProviderRender is the provider-visible content the model actually
// receives, split into a StablePrefix (byte-identical across invocations of
// the same actor/task-class/profile/policy-versions) and a DynamicSuffix
// (the per-task payload). It deliberately excludes every AuditEnvelope
// field (snapshot_id, task_ref, request_hash, canonical_bundle_hash,
// precedence_hash, timestamps, invocation IDs) -- those remain fully
// persisted and auditable on Snapshot/model_invocations, just never
// serialized into the prompt itself (R10.4 sections 3-5).
type ProviderRender struct {
	Version              string
	StablePrefix         []byte
	DynamicSuffix        []byte
	StablePrefixHash     string
	StablePrefixBytes    int
	DynamicSuffixHash    string
	DynamicSuffixBytes   int
	ProviderRenderHash   string
	ProviderVisibleBytes int
}

// Bytes returns the exact concatenation dispatched to the provider --
// StablePrefix followed by DynamicSuffix, the same order ProviderRenderHash
// was computed over. This is the ONLY function that may produce the
// provider-visible byte stream; every caller (dispatch and the pre-dispatch
// integrity hash) must go through the same ProviderRender value, never
// recompute independently (R10.4 section 12 -- this is exactly the
// single-source-of-truth invariant that fixed the R10
// context_render_hash_mismatch bug, extended to this new layer).
func (r ProviderRender) Bytes() []byte {
	return append(append([]byte(nil), r.StablePrefix...), r.DynamicSuffix...)
}

// escapeUntrustedContent escapes content that may contain closing tags or
// markup-like sequences. This prevents prompt injection via forged wrappers.
// The escaped content CANNOT close/open wrapper delimiters after escaping.
//
// Escape rules (collision-safe):
//
//	< → &lt;    > → &gt;    & → &amp;
//	" → &quot;  ' → &#x27;  / → &#x2F;
func escapeUntrustedContent(content string) string {
	// Order matters: escape & FIRST, then other chars
	content = strings.ReplaceAll(content, "&", "&amp;")
	content = strings.ReplaceAll(content, "<", "&lt;")
	content = strings.ReplaceAll(content, ">", "&gt;")
	content = strings.ReplaceAll(content, "\"", "&quot;")
	content = strings.ReplaceAll(content, "'", "&#x27;")
	content = strings.ReplaceAll(content, "/", "&#x2F;")
	return content
}

// providerHeader is the only StablePrefix content that does not come from a
// Segment -- organization_id/actor_role_id/purpose are semantically
// relevant to the model (R10.4 audit section "riesgos de autoridad") and
// are stable across every invocation of the same actor under the same
// profile, so including them never breaks byte-identity. snapshot_id,
// task_ref, and request_hash are deliberately never part of this header --
// they are AuditEnvelope-only (R10.4 section 3).
func providerHeader(snapshot Snapshot) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "organization_id: %s\n", snapshot.OrganizationID)
	fmt.Fprintf(&out, "actor_role_id: %s\n", snapshot.ActorRoleID)
	fmt.Fprintf(&out, "purpose: %s", snapshot.Purpose)
	return out.Bytes()
}

// BuildProviderRender is the single deterministic constructor for
// ProviderRender using v2 serialization with structural differentiation.
// This is THE production path for all rendering — v1 is deprecated.
// v2 wraps untrusted data (RAG/Memory/Web) with collision-safe escaping
// and explicit authority=none / may_grant=false markers.
func BuildProviderRender(snapshot Snapshot) (ProviderRender, error) {
	// CRITICAL SECURITY: Always use v2 for production rendering.
	// This ensures untrusted data can NEVER be misinterpreted as authoritative instruction.
	return BuildProviderRenderV2(snapshot)
}

// BuildProviderRenderLegacy provides access to v1 format for testing/back compat ONLY.
// DO NOT USE IN PRODUCTION — this lacks the security hardening of v2.
func BuildProviderRenderLegacy(snapshot Snapshot) (ProviderRender, error) {
	if snapshot.Status == SnapshotInvalidated {
		return ProviderRender{}, ErrSnapshotInvalidated
	}
	if snapshot.Status != "" && snapshot.Status != SnapshotReady {
		return ProviderRender{}, errors.New("cannot render snapshot with unknown status")
	}

	stableParts := [][]byte{providerHeader(snapshot)}
	var dynamicParts [][]byte
	for _, segment := range snapshot.Segments {
		if !segment.Included {
			continue
		}
		if IsDynamicProviderTier(segment.AuthorityTier) {
			dynamicParts = append(dynamicParts, segment.Content)
		} else {
			stableParts = append(stableParts, segment.Content)
		}
	}

	stablePrefix := bytes.Join(stableParts, providerRenderSeparator)
	dynamicSuffix := bytes.Join(dynamicParts, providerRenderSeparator)

	render := ProviderRender{
		Version:            ProviderRenderVersionV1,
		StablePrefix:       stablePrefix,
		DynamicSuffix:      dynamicSuffix,
		StablePrefixHash:   DigestCanonicalBytes(stablePrefix),
		StablePrefixBytes:  len(stablePrefix),
		DynamicSuffixHash:  DigestCanonicalBytes(dynamicSuffix),
		DynamicSuffixBytes: len(dynamicSuffix),
	}
	// ProviderVisibleBytes/ProviderRenderHash are always derived from
	// Bytes() (StablePrefix immediately followed by DynamicSuffix, no
	// extra separator between the two halves) -- the same value every
	// caller of Bytes() gets, so these fields can never silently diverge
	// from what is actually dispatched.
	full := render.Bytes()
	render.ProviderVisibleBytes = len(full)
	render.ProviderRenderHash = DigestCanonicalBytes(full)
	return render, nil
}

// BuildProviderRenderV2 builds a ProviderRender using v2 serialization with
// collision-safe structural wrappers around untrusted data sources (RAG, memory, web evidence).
//
// CRITICAL SECURITY CONSTRAINTS:
//  1. Untrusted data is marked with explicit authority=none, may_grant_capabilities=false
//  2. Content is HTML-escaped BEFORE being wrapped, preventing closing tag injection
//  3. Wrapper delimiters use [ ] syntax not angle brackets (second layer of protection)
//  4. Output is deterministic - same inputs always produce same bytes
//  5. Stable/Dynamic boundary preserved (R10.4 invariant)
//
// The escaped content CANNOT close or forge wrapper delimiters after escaping.
func BuildProviderRenderV2(snapshot Snapshot) (ProviderRender, error) {
	if snapshot.Status == SnapshotInvalidated {
		return ProviderRender{}, ErrSnapshotInvalidated
	}
	if snapshot.Status != "" && snapshot.Status != SnapshotReady {
		return ProviderRender{}, errors.New("cannot render snapshot with unknown status")
	}

	stableParts := [][]byte{providerHeader(snapshot)}
	var dynamicParts [][]byte

	for _, segment := range snapshot.Segments {
		if !segment.Included {
			continue
		}

		content := string(segment.Content)

		if dynamicAuthorityTiers[segment.AuthorityTier] {
			// Apply structural wrapper based on tier level
			wrappedContent := wrapUntrustedData(content, segment)
			dynamicParts = append(dynamicParts, wrappedContent)
		} else {
			// Stable tiers don't need special wrapping
			stableParts = append(stableParts, segment.Content)
		}
	}

	stablePrefix := bytes.Join(stableParts, providerRenderSeparator)
	dynamicSuffix := bytes.Join(dynamicParts, providerRenderSeparator)

	render := ProviderRender{
		Version:            ProviderRenderVersionV2,
		StablePrefix:       stablePrefix,
		DynamicSuffix:      dynamicSuffix,
		StablePrefixHash:   DigestCanonicalBytes(stablePrefix),
		StablePrefixBytes:  len(stablePrefix),
		DynamicSuffixHash:  DigestCanonicalBytes(dynamicSuffix),
		DynamicSuffixBytes: len(dynamicSuffix),
	}
	full := render.Bytes()
	render.ProviderVisibleBytes = len(full)
	render.ProviderRenderHash = DigestCanonicalBytes(full)
	return render, nil
}

// wrapUntrustedData wraps a segment's content with collision-safe structural markers.
// Authority tier determines the wrapper type; all untrusted tiers use authority=none
// and explicit may_grant_capabilities=false.
func wrapUntrustedData(content string, segment Segment) []byte {
	// Escape content FIRST to neutralize any malicious markup
	escaped := escapeUntrustedContent(content)

	// Determine wrapper type based on AuthorityTier
	var wrapperType string
	switch segment.AuthorityTier {
	case TierTask:
		wrapperType = "task-context"
	case TierProject:
		wrapperType = "project-context"
	case TierRAGEvidence:
		wrapperType = "rag-evidence"
	case TierApprovedMemory:
		wrapperType = "memory-entry"
	case TierApprovedSkill:
		wrapperType = "approved-skill"
	default:
		// Fallback for unknown tiers
		wrapperType = "untrusted-data"
	}

	// Use [ ] delimiter syntax for outer wrapper (not XML angle brackets)
	// This provides defense in depth even if escaping somehow fails
	var out bytes.Buffer
	priority, _ := AuthorityPriority(segment.AuthorityTier)
	fmt.Fprintf(&out, "[authority:tier=%d trust=untrusted may_grant=false type=\"%s\"]\n",
		priority, wrapperType)
	fmt.Fprintf(&out, "<%s content=\"escaped\">%s</%s>\n",
		wrapperType, escaped, wrapperType)
	fmt.Fprintf(&out, "[/authority]\n\n")

	return out.Bytes()
}

// Helper function that would check if AuthorityTier is trusted vs untrusted.
// In current domain.go: TierRAGEvidence, TierApprovedMemory, TierTask/Project/Skill are untrusted.
