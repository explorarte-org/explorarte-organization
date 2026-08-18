package contextdisclosure

import (
	"errors"
	"strings"
	"testing"
)

func validHandle() ContextHandle {
	return ContextHandle{
		OrganizationID:  "explorarte",
		SnapshotID:      482,
		ResourceID:      91,
		ResourceVersion: "v3",
		ContentDigest:   strings.Repeat("a", 64),
		Kind:            ResourceKindRAGEvidence,
	}
}

// handleForIdentity is validHandle's parameterized form, added round 8 so
// callers that need a handle whose own encoded Kind/ResourceVersion/
// ContentDigest agree with some other identity under test (e.g. a
// ContextResource/AggregateMember's own Kind/SourceVersion/ContentDigest
// fields) don't have to hardcode validHandle()'s fixed field values and
// silently disagree with them -- the exact shape of the round-7 test
// fixture bug the round-8 review caught in sampleAggregateMember
// (result_test.go): Handle: validHandle().Encode() always encoded
// k=rag_evidence regardless of the Kind actually being tested.
func handleForIdentity(kind ResourceKind, version, digest string) ContextHandle {
	h := validHandle()
	h.Kind = kind
	h.ResourceVersion = version
	h.ContentDigest = digest
	return h
}

// TestContextHandle_EncodeDecodeRoundTrip is TEST_PLAN.md's M2.0-slice
// "handle encode/decode round-tripping" requirement: Encode then Decode
// MUST reproduce every field exactly, for a range of valid field values.
func TestContextHandle_EncodeDecodeRoundTrip(t *testing.T) {
	cases := []ContextHandle{
		validHandle(),
		{
			OrganizationID:  "org-with-dashes_and.dots",
			SnapshotID:      1,
			ResourceID:      1,
			ResourceVersion: "2026-08-17T00:00:00Z",
			ContentDigest:   strings.Repeat("0", 64),
			Kind:            ResourceKindApprovedMemory,
		},
		{
			// version containing characters that need escaping in a query
			// value, to prove Encode/Decode don't silently corrupt it.
			OrganizationID:  "org",
			SnapshotID:      9223372036854775807,
			ResourceID:      2,
			ResourceVersion: "v1 with spaces & special=chars?",
			ContentDigest:   strings.Repeat("f", 64),
			Kind:            ResourceKindRAGEvidence,
		},
	}
	for i, want := range cases {
		encoded := want.Encode()
		got, err := Decode(encoded)
		if err != nil {
			t.Fatalf("case %d: Decode(%q) failed: %v", i, encoded, err)
		}
		if got != want {
			t.Fatalf("case %d: round-trip mismatch:\n encoded: %q\n want: %+v\n got:  %+v", i, encoded, want, got)
		}
		// Encoding the decoded value again must reproduce the same string --
		// Encode is deterministic and canonical for a given ContextHandle.
		if reencoded := got.Encode(); reencoded != encoded {
			t.Fatalf("case %d: re-encode mismatch:\n first:  %q\n second: %q", i, encoded, reencoded)
		}
	}
}

// TestContextHandle_DecodeRejectsMalformedSyntax covers DESIGN.md §17's
// "Malformed handle syntax | INVALID_REQUEST | never reaches storage" case
// at the Decode layer -- every one of these must fail with
// ErrMalformedHandle, never panic, never silently decode to a zero-value
// handle.
func TestContextHandle_DecodeRejectsMalformedSyntax(t *testing.T) {
	cases := map[string]string{
		"empty string":            "",
		"wrong scheme":            "http://snapshot/1/resource/1?v=v1&d=" + strings.Repeat("a", 64) + "&k=rag_evidence",
		"missing organization":    "ctx:///snapshot/1/resource/1?v=v1&d=" + strings.Repeat("a", 64) + "&k=rag_evidence",
		"wrong path shape":        "ctx://org/wrong/1/shape/1?v=v1&d=" + strings.Repeat("a", 64) + "&k=rag_evidence",
		"non-numeric snapshot id": "ctx://org/snapshot/abc/resource/1?v=v1&d=" + strings.Repeat("a", 64) + "&k=rag_evidence",
		"zero snapshot id":        "ctx://org/snapshot/0/resource/1?v=v1&d=" + strings.Repeat("a", 64) + "&k=rag_evidence",
		"negative resource id":    "ctx://org/snapshot/1/resource/-1?v=v1&d=" + strings.Repeat("a", 64) + "&k=rag_evidence",
		"missing version":         "ctx://org/snapshot/1/resource/1?d=" + strings.Repeat("a", 64) + "&k=rag_evidence",
		"malformed digest short":  "ctx://org/snapshot/1/resource/1?v=v1&d=abc&k=rag_evidence",
		"malformed digest upper":  "ctx://org/snapshot/1/resource/1?v=v1&d=" + strings.Repeat("A", 64) + "&k=rag_evidence",
		"invalid kind":            "ctx://org/snapshot/1/resource/1?v=v1&d=" + strings.Repeat("a", 64) + "&k=role_profile",
		"missing kind":            "ctx://org/snapshot/1/resource/1?v=v1&d=" + strings.Repeat("a", 64),
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(encoded)
			if err == nil {
				t.Fatalf("Decode(%q) succeeded, want ErrMalformedHandle", encoded)
			}
			if !errors.Is(err, ErrMalformedHandle) {
				t.Fatalf("Decode(%q) error = %v, want wrapping ErrMalformedHandle", encoded, err)
			}
		})
	}
}

// TestContextHandle_DecodeRejectsNonCanonicalForm covers the round-7 fix
// (P2 finding): a syntactically-parseable handle whose string is NOT the
// canonical Encode() output for its own fields must be rejected, not
// silently normalized -- otherwise two different strings could represent
// the same identity while ActionDigest (a later slice) is defined over the
// raw handle string.
func TestContextHandle_DecodeRejectsNonCanonicalForm(t *testing.T) {
	h := validHandle()
	canonical := h.Encode()
	// Sanity: the canonical form itself must still decode successfully.
	if _, err := Decode(canonical); err != nil {
		t.Fatalf("canonical form failed to decode: %v", err)
	}

	cases := map[string]string{
		"extra unknown query parameter": canonical + "&extra=1",
		"duplicate v parameter":         strings.Replace(canonical, "v=v3", "v=v3&v=v3", 1),
		"trailing fragment":             canonical + "#fragment",
	}
	for name, malformed := range cases {
		t.Run(name, func(t *testing.T) {
			if malformed == canonical {
				t.Fatalf("test fixture bug: mutated form equals canonical form")
			}
			_, err := Decode(malformed)
			if err == nil {
				t.Fatalf("Decode(%q) succeeded, want rejection for non-canonical form", malformed)
			}
			if !errors.Is(err, ErrMalformedHandle) {
				t.Fatalf("Decode(%q) error = %v, want wrapping ErrMalformedHandle", malformed, err)
			}
		})
	}
}

// TestContextHandle_Validate exercises Validate() directly against
// hand-constructed handles (never round-tripped through Encode/Decode),
// mirroring the same bounds context_addressable_resources' own CHECK
// constraints impose (DESIGN.md §6.1) at the Go level.
func TestContextHandle_Validate(t *testing.T) {
	base := validHandle()
	if err := base.Validate(); err != nil {
		t.Fatalf("valid handle failed Validate(): %v", err)
	}

	mutate := func(f func(*ContextHandle)) ContextHandle {
		h := base
		f(&h)
		return h
	}

	invalid := map[string]ContextHandle{
		"empty organization":   mutate(func(h *ContextHandle) { h.OrganizationID = "" }),
		"zero snapshot id":     mutate(func(h *ContextHandle) { h.SnapshotID = 0 }),
		"negative snapshot id": mutate(func(h *ContextHandle) { h.SnapshotID = -1 }),
		"zero resource id":     mutate(func(h *ContextHandle) { h.ResourceID = 0 }),
		"empty version":        mutate(func(h *ContextHandle) { h.ResourceVersion = "" }),
		"oversized version":    mutate(func(h *ContextHandle) { h.ResourceVersion = strings.Repeat("v", sourceVersionMaxLen+1) }),
		"short digest":         mutate(func(h *ContextHandle) { h.ContentDigest = "abc" }),
		"uppercase digest":     mutate(func(h *ContextHandle) { h.ContentDigest = strings.Repeat("A", 64) }),
		"empty kind":           mutate(func(h *ContextHandle) { h.Kind = "" }),
		"excluded kind":        mutate(func(h *ContextHandle) { h.Kind = "web_evidence" }),
		"non-evidence kind":    mutate(func(h *ContextHandle) { h.Kind = "role_profile" }),
	}
	for name, h := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := h.Validate(); err == nil {
				t.Fatalf("Validate() succeeded for invalid handle (%s), want error", name)
			}
		})
	}

	// Boundary: exactly sourceVersionMaxLen characters must still be valid.
	atBound := mutate(func(h *ContextHandle) { h.ResourceVersion = strings.Repeat("v", sourceVersionMaxLen) })
	if err := atBound.Validate(); err != nil {
		t.Fatalf("version at exactly the max length failed Validate(): %v", err)
	}
}

// TestContextHandle_Validate_CharacterCountNotByteCount is the round-7 P3
// fix's regression test: sourceVersionMaxLen is a CHARACTER bound
// (PostgreSQL length(TEXT)), not a byte bound. A multi-byte-UTF-8 version
// string at exactly 240 characters (each character 2+ bytes) must still
// pass -- it would incorrectly fail if Validate used len(string) (bytes)
// instead of utf8.RuneCountInString.
func TestContextHandle_Validate_CharacterCountNotByteCount(t *testing.T) {
	// "é" is 2 bytes in UTF-8 but 1 character/rune.
	version := strings.Repeat("é", sourceVersionMaxLen)
	if len(version) <= sourceVersionMaxLen {
		t.Fatalf("test fixture bug: byte length %d is not greater than character length %d, this test proves nothing", len(version), sourceVersionMaxLen)
	}
	h := validHandle()
	h.ResourceVersion = version
	if err := h.Validate(); err != nil {
		t.Fatalf("240-character (480-byte) version incorrectly rejected: %v", err)
	}

	// One character over the bound must still fail, even though it's well
	// under 240 bytes.
	h.ResourceVersion = strings.Repeat("é", sourceVersionMaxLen+1)
	if err := h.Validate(); err == nil {
		t.Fatalf("241-character version incorrectly accepted")
	}
}

// TestResourceKind_ValidResourceKind asserts M2a's exactly-two-kind closed
// set -- DESIGN.md §6.1's resource_kind CHECK, projected to Go, and that
// contextdisclosure now consumes contextengine.SourceKind directly rather
// than a parallel type (round-7 correction, P1 finding).
func TestResourceKind_ValidResourceKind(t *testing.T) {
	for _, k := range []ResourceKind{ResourceKindApprovedMemory, ResourceKindRAGEvidence} {
		if !ValidResourceKind(k) {
			t.Errorf("ValidResourceKind(%q) = false, want true", k)
		}
	}
	for _, k := range []ResourceKind{"", "web_evidence", "role_profile", "approved_skill", "task_context", "project_context", "canonical_document", "organization_agent", "department_agent", "owner_constraint"} {
		if ValidResourceKind(k) {
			t.Errorf("ValidResourceKind(%q) = true, want false", k)
		}
	}
}
