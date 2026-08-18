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

// TestResourceKind_Valid asserts M2a's exactly-two-kind closed set --
// DESIGN.md §6.1's resource_kind CHECK, projected to Go.
func TestResourceKind_Valid(t *testing.T) {
	for _, k := range []ResourceKind{ResourceKindApprovedMemory, ResourceKindRAGEvidence} {
		if !k.Valid() {
			t.Errorf("ResourceKind(%q).Valid() = false, want true", k)
		}
	}
	for _, k := range []ResourceKind{"", "web_evidence", "role_profile", "approved_skill", "task_context", "project_context", "canonical_document", "organization_agent", "department_agent", "owner_constraint"} {
		if k.Valid() {
			t.Errorf("ResourceKind(%q).Valid() = true, want false", k)
		}
	}
}
