package contextdisclosure

import "testing"

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
// this test simply documents/locks that shape decision via reflection-free
// field access -- if a future edit ever added a Content field here, this
// test file would need to change to reference it, making the addition
// visible in review.
func TestResourceDescriptor_CarriesNoContent(t *testing.T) {
	d := ResourceDescriptor{
		Handle:          validHandle().Encode(),
		Kind:            ResourceKindApprovedMemory,
		SourceReference: "memory/entry-7",
		ByteCount:       1024,
		TrustClass:      TrustClassUntrusted,
		DataClass:       DataClassOrganizational,
	}
	if d.Handle == "" || d.SourceReference == "" {
		t.Fatal("fixture is incomplete")
	}
	// Compile-time shape lock: ResourceDescriptor has exactly these six
	// fields per DESIGN.md §11 -- {handle, kind, source_reference,
	// byte_count, trust_class, data_class}.
	_ = struct {
		Handle          string
		Kind            ResourceKind
		SourceReference string
		ByteCount       int64
		TrustClass      string
		DataClass       DataClass
	}(d)
}
