package contextdisclosure

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ContextHandle is the frozen, structured, opaque-to-the-model,
// server-validated handle identity (DESIGN.md §7 -- this is the literal Go
// struct §7 already froze; M2.0 adds only Encode/Decode and syntax
// validation around it, never trust-bearing logic, which belongs to
// contextdisclosure.BindingResolver in a later slice).
//
// Every field here is re-derived from context_addressable_resources/
// context_snapshots at validation time in a later slice (I-2) -- Decode
// (below) only parses the string back into these fields; it never proves
// they correspond to a real, authorized row. Callers MUST NOT treat a
// successfully-Decoded ContextHandle as authoritative.
type ContextHandle struct {
	OrganizationID  string
	SnapshotID      int64
	ResourceID      int64
	ResourceVersion string // source_version, not a row-mutation version
	ContentDigest   string // sha256, defense-in-depth against silent DB drift
	Kind            ResourceKind
}

var (
	// ErrMalformedHandle is returned by Decode for any handle whose syntax
	// -- not whose authority -- is invalid: the INVALID_REQUEST case
	// DESIGN.md §17's failure model reserves for "malformed handle syntax
	// | never reaches storage". A malformed-syntax handle is rejected
	// before any DB lookup is ever attempted, in every later slice that
	// consumes Decode.
	ErrMalformedHandle = errors.New("contextdisclosure: malformed context handle")
)

// handleScheme is the illustrative scheme DESIGN.md §7 names --
// ctx://snapshot/482/resource/91?v=3&d=1a2b... -- adopted here as the
// actual encoding, extended with the organization segment and kind query
// parameter §7's struct also requires. RATIONALE for keeping the encoding
// human-legible (DESIGN.md §7): context.inspect output needs to show
// handles the model can carry across turns, and the host needs to be
// debuggable in orgctl tooling.
const handleScheme = "ctx"

// sourceVersionMaxLen mirrors context_addressable_resources.source_version's
// own bound (DESIGN.md §6.1: same bound as context_segments.source_version,
// migration 000006: length(trim(...)) BETWEEN 1 AND 240). PostgreSQL's
// length(TEXT) counts characters, not bytes -- Validate (below) uses
// utf8.RuneCountInString, never len(string), to match that semantics
// honestly (round-7 correction, P3 finding: the original check used
// len(h.ResourceVersion), a byte count, which only happened to agree with
// PostgreSQL's character count for ASCII-only version strings).
const sourceVersionMaxLen = 240

// Encode produces the canonical opaque string form of h. Encode and Decode
// are exact inverses for any ContextHandle whose fields already pass
// Validate -- Encode does not itself re-validate h, so an invalid
// ContextHandle can still be encoded (the resulting string will simply fail
// Decode's own validation, or a later slice's server-side re-derivation,
// exactly as an adversarially-forged handle would). Encode's output is, by
// construction, the ONE canonical string for a given ContextHandle -- query
// parameters are always emitted in the same order (url.Values.Encode sorts
// keys), never duplicated, never carrying an unrecognized key, and never
// carrying a fragment. Decode (below) relies on this to detect and reject
// any non-canonical encoding of an otherwise-valid handle.
func (h ContextHandle) Encode() string {
	values := url.Values{}
	values.Set("v", h.ResourceVersion)
	values.Set("d", h.ContentDigest)
	values.Set("k", string(h.Kind))
	return fmt.Sprintf("%s://%s/snapshot/%d/resource/%d?%s",
		handleScheme,
		url.PathEscape(h.OrganizationID),
		h.SnapshotID,
		h.ResourceID,
		values.Encode(),
	)
}

// Decode parses an opaque handle string back into a ContextHandle,
// rejecting anything that isn't syntactically well-formed with
// ErrMalformedHandle (wrapped with the specific reason). Decode performs
// ONLY syntax validation (DESIGN.md §17: "Malformed handle syntax |
// INVALID_REQUEST | never reaches storage") -- it never consults storage
// and never proves the decoded fields correspond to a real, authorized
// row (I-2; that is BindingResolver's job in a later slice, never this
// one's).
//
// Round-7 correction (P2 finding: "the handle parser is permissive of
// non-canonical forms"). A syntactically-parseable but non-canonical
// string -- unknown/duplicate query parameters, a fragment, alternate
// escaping of the same organization id, etc. -- previously decoded
// successfully to the same ContextHandle a canonical string would, which
// means two DIFFERENT strings could represent the SAME identity while
// ActionDigest (a later slice, DESIGN.md §10A) is defined over the raw
// handle string itself. Decode now re-encodes the parsed result and
// rejects the input outright unless it is byte-for-byte identical to
// ContextHandle.Encode()'s own canonical output for those exact fields --
// this closes the whole class of non-canonical-alias problems generically
// (extra params, duplicate params, reordered params, and fragments all
// produce a different re-encoded string, and are all rejected the same
// way) rather than special-casing each one.
func Decode(encoded string) (ContextHandle, error) {
	u, err := url.Parse(encoded)
	if err != nil {
		return ContextHandle{}, fmt.Errorf("%w: %v", ErrMalformedHandle, err)
	}
	if u.Scheme != handleScheme {
		return ContextHandle{}, fmt.Errorf("%w: unexpected scheme %q", ErrMalformedHandle, u.Scheme)
	}
	orgID, err := url.PathUnescape(u.Host)
	if err != nil || strings.TrimSpace(orgID) == "" {
		return ContextHandle{}, fmt.Errorf("%w: missing or invalid organization segment", ErrMalformedHandle)
	}
	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(pathParts) != 4 || pathParts[0] != "snapshot" || pathParts[2] != "resource" {
		return ContextHandle{}, fmt.Errorf("%w: unexpected path shape %q", ErrMalformedHandle, u.Path)
	}
	snapshotID, err := strconv.ParseInt(pathParts[1], 10, 64)
	if err != nil || snapshotID <= 0 {
		return ContextHandle{}, fmt.Errorf("%w: invalid snapshot id %q", ErrMalformedHandle, pathParts[1])
	}
	resourceID, err := strconv.ParseInt(pathParts[3], 10, 64)
	if err != nil || resourceID <= 0 {
		return ContextHandle{}, fmt.Errorf("%w: invalid resource id %q", ErrMalformedHandle, pathParts[3])
	}
	query := u.Query()
	version := query.Get("v")
	digest := query.Get("d")
	kind := ResourceKind(query.Get("k"))

	handle := ContextHandle{
		OrganizationID:  orgID,
		SnapshotID:      snapshotID,
		ResourceID:      resourceID,
		ResourceVersion: version,
		ContentDigest:   digest,
		Kind:            kind,
	}
	if err := handle.Validate(); err != nil {
		return ContextHandle{}, fmt.Errorf("%w: %v", ErrMalformedHandle, err)
	}
	if canonical := handle.Encode(); canonical != encoded {
		return ContextHandle{}, fmt.Errorf("%w: not in canonical form", ErrMalformedHandle)
	}
	return handle, nil
}

// Validate checks h's fields for well-formedness only -- the same syntax
// bounds the underlying schema/domain already enforce (organization id
// non-empty, snapshot/resource ids positive, source_version bounded per
// context_addressable_resources' own CHECK -- counted in characters, not
// bytes, matching PostgreSQL's length(TEXT) semantics, round-7 correction
// -- content_digest matching its own sha256-hex format CHECK, kind one of
// M2a's exactly-two admitted ResourceKinds). Validate never touches
// storage and never proves authority (I-2) -- it is the same "malformed
// handle syntax" gate Decode applies to a freshly-parsed handle, exposed
// separately so a caller-constructed ContextHandle (never round-tripped
// through Encode/Decode) can be checked the same way before Encode is even
// called.
func (h ContextHandle) Validate() error {
	if strings.TrimSpace(h.OrganizationID) == "" {
		return errors.New("organization id is required")
	}
	if h.SnapshotID <= 0 {
		return errors.New("snapshot id must be positive")
	}
	if h.ResourceID <= 0 {
		return errors.New("resource id must be positive")
	}
	if !validBoundedText(h.ResourceVersion, sourceVersionMaxLen) {
		return fmt.Errorf("resource version must be 1..%d characters", sourceVersionMaxLen)
	}
	if !contentDigestPattern.MatchString(h.ContentDigest) {
		return errors.New("content digest must be a 64-character hex sha256 digest")
	}
	if !ValidResourceKind(h.Kind) {
		return fmt.Errorf("kind %q is not one of M2a's admitted resource kinds", h.Kind)
	}
	return nil
}
