package contextdisclosure

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// TestSliceInput_Validate covers DESIGN.md §17's "Read exceeds bounds |
// INVALID_REQUEST" case at the input-shape layer (this package has no
// resolved resource to check the range against yet -- that is a later
// slice's job; this only rejects a range that is nonsensical on its own
// terms).
func TestSliceInput_Validate(t *testing.T) {
	valid := SliceInput{Handle: validHandle().Encode(), Offset: 0, Length: 100}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid slice input failed Validate(): %v", err)
	}

	invalid := map[string]SliceInput{
		"negative offset": {Handle: valid.Handle, Offset: -1, Length: 100},
		"zero length":     {Handle: valid.Handle, Offset: 0, Length: 0},
		"negative length": {Handle: valid.Handle, Offset: 0, Length: -1},
	}
	for name, in := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := in.Validate(); err == nil {
				t.Fatalf("Validate() succeeded for invalid slice input (%s), want error", name)
			}
		})
	}
}

// TestResourceDescriptor_CarriesNoContent asserts context.inspect's output
// shape structurally excludes content -- DESIGN.md §11: "metadata only, no
// content." There is no Content field on ResourceDescriptor at all, so
// this test simply documents/locks that shape decision via a compile-time
// struct conversion -- if a future edit ever added a Content field here,
// this test file would need to change to reference it, making the
// addition visible in review.
func TestResourceDescriptor_CarriesNoContent(t *testing.T) {
	d := ResourceDescriptor{
		Handle:          handleForIdentity(ResourceKindApprovedMemory, "v1", strings.Repeat("a", 64)).Encode(),
		Kind:            ResourceKindApprovedMemory,
		SourceReference: "memory/entry-7",
		ByteCount:       1024,
		TrustClass:      TrustClassM2a,
		DataClass:       contextengine.DataOrganizational,
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("valid descriptor failed Validate(): %v", err)
	}
	// Compile-time shape lock: ResourceDescriptor has exactly these six
	// fields per DESIGN.md §11 -- {handle, kind, source_reference,
	// byte_count, trust_class, data_class}.
	_ = struct {
		Handle          string
		Kind            ResourceKind
		SourceReference string
		ByteCount       int64
		TrustClass      TrustClass
		DataClass       DataClass
	}(d)
}

// TestResourceDescriptor_Validate exercises the round-7 P2 addition.
func TestResourceDescriptor_Validate(t *testing.T) {
	base := ResourceDescriptor{
		Handle:          handleForIdentity(ResourceKindApprovedMemory, "v1", strings.Repeat("a", 64)).Encode(),
		Kind:            ResourceKindApprovedMemory,
		SourceReference: "memory/entry-7",
		ByteCount:       1024,
		TrustClass:      TrustClassM2a,
		DataClass:       contextengine.DataOrganizational,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid descriptor failed Validate(): %v", err)
	}
	invalid := base
	invalid.TrustClass = "authoritative"
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a descriptor with a non-untrusted trust class, want error")
	}
	invalid = base
	invalid.DataClass = "secret"
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a descriptor with an inadmissible data class, want error")
	}
	// Round-8 addition (P1 finding): handle kind must agree with Kind.
	invalid = base
	invalid.Handle = handleForIdentity(ResourceKindRAGEvidence, "v1", strings.Repeat("a", 64)).Encode()
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a descriptor whose handle kind contradicts its own Kind, want error")
	}
}

// TestSearchResult_CarriesDataClass is TEST_PLAN.md L6's direct M2.0-scope
// regression test (round-7 P1 fix): a sanitized-classified resource's
// snippet must keep its data_class alongside it -- both in the Go struct
// and in the actual marshaled JSON wire shape, matching the same
// exact-wire-shape rigor TestContextResource_ExactWireShape applies.
func TestSearchResult_CarriesDataClass(t *testing.T) {
	result := SearchResult{
		Handle:    validHandle().Encode(),
		Kind:      ResourceKindRAGEvidence,
		Snippet:   "a sanitized excerpt",
		Score:     0.5,
		DataClass: contextengine.DataSanitized,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("valid search result failed Validate(): %v", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}
	dataClass, ok := raw["data_class"].(string)
	if !ok {
		t.Fatalf("marshaled JSON is missing string field \"data_class\"; got: %s", data)
	}
	if dataClass != string(contextengine.DataSanitized) {
		t.Fatalf("data_class = %q, want %q -- sanitized classification must not be lost in the search path (TEST_PLAN.md L6)", dataClass, contextengine.DataSanitized)
	}

	invalid := result
	invalid.DataClass = "secret"
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a search result with an inadmissible data class, want error")
	}
}

// TestAggregateMember_Validate_Bounds is the round-8 P2 fix's direct
// regression test: AggregateMember.Validate previously only checked
// SourceReference/SourceVersion != "", not the schema's real 1..500/1..240
// character bounds context_addressable_resources' own CHECK constraints
// impose (DESIGN.md §6.1).
func TestAggregateMember_Validate_Bounds(t *testing.T) {
	base := sampleAggregateMember(ResourceKindApprovedMemory)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid member failed Validate(): %v", err)
	}

	oversizedRef := base
	oversizedRef.SourceReference = strings.Repeat("x", sourceReferenceMaxLen+1)
	if err := oversizedRef.Validate(); err == nil {
		t.Fatal("Validate() succeeded for an oversized source reference, want error")
	}

	oversizedVersion := base
	oversizedVersion.SourceVersion = strings.Repeat("x", sourceVersionMaxLen+1)
	if err := oversizedVersion.Validate(); err == nil {
		t.Fatal("Validate() succeeded for an oversized source version, want error")
	}
}
