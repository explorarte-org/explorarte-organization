package contextdisclosure

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

func sampleResource() ContextResource {
	const version = "v3"
	digest := strings.Repeat("b", 64)
	return ContextResource{
		Handle:               handleForIdentity(ResourceKindRAGEvidence, version, digest).Encode(),
		Kind:                 ResourceKindRAGEvidence,
		SourceReference:      "rag/knowledge/doc-42",
		SourceVersion:        version,
		AuthorityTier:        AuthorityTier(ResourceKindRAGEvidence),
		InstructionClass:     InstructionClassM2a,
		TrustClass:           TrustClassM2a,
		DataClass:            contextengine.DataSanitized,
		MayGrantCapabilities: false,
		ContentDigest:        digest,
		Content:              "hello world",
		ByteCount:            int64(len("hello world")),
	}
}

// sampleAggregateMember round-8 fix: the handle must be built with the SAME
// kind/version/digest this member itself carries -- the round-7 fixture
// bug the round-8 review demonstrated used validHandle().Encode() (always
// k=rag_evidence, v=v3, d=aaa...) regardless of the kind parameter, so
// AggregateMember.Validate() previously accepted a member whose Handle and
// Kind/SourceVersion/ContentDigest fields silently contradicted each other.
func sampleAggregateMember(kind ResourceKind) AggregateMember {
	const version = "v1"
	digest := strings.Repeat("c", 64)
	return AggregateMember{
		Handle:               handleForIdentity(kind, version, digest).Encode(),
		Kind:                 kind,
		SourceReference:      "ref",
		SourceVersion:        version,
		AuthorityTier:        AuthorityTier(kind),
		InstructionClass:     InstructionClassM2a,
		TrustClass:           TrustClassM2a,
		DataClass:            contextengine.DataPublic,
		MayGrantCapabilities: false,
		ContentDigest:        digest,
		ByteCount:            5,
	}
}

// TestContextResource_JSONRoundTrip is TEST_PLAN.md's M2.0-slice
// "ContextResource shape validation" requirement: every field must survive
// a JSON marshal/unmarshal round trip unchanged, since ContextResource
// travels inside ContextToolResult.Content as JSON (DESIGN.md §9C).
func TestContextResource_JSONRoundTrip(t *testing.T) {
	want := sampleResource()
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ContextResource
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch:\n want: %+v\n got:  %+v", want, got)
	}
}

// TestContextResource_ExactWireShape is the round-7 P1 fix's regression
// test: the JSON bytes ContextResource actually produces must match §11's
// frozen snake_case field names exactly, and Content must be plain UTF-8
// text, NEVER base64 -- a []byte field would have silently broken this
// (encoding/json base64-encodes []byte), which is exactly what happened
// before this fix. This test compares actual marshaled JSON structure, not
// merely a marshal->unmarshal round trip (which cannot detect a
// field-name/encoding regression on its own, since encode and decode would
// drift together).
func TestContextResource_ExactWireShape(t *testing.T) {
	r := sampleResource()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}

	wantKeys := []string{"handle", "kind", "source_reference", "source_version", "authority_tier", "instruction_class", "trust_class", "data_class", "may_grant_capabilities", "content_digest", "content", "byte_count"}
	for _, key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("marshaled JSON is missing expected key %q; got keys %v", key, mapKeys(raw))
		}
	}
	// No PascalCase Go field names must leak into the wire format.
	for _, badKey := range []string{"Handle", "Kind", "SourceReference", "ContentDigest", "Content", "ByteCount"} {
		if _, ok := raw[badKey]; ok {
			t.Errorf("marshaled JSON leaked PascalCase Go field name %q", badKey)
		}
	}
	// Content MUST be the literal plaintext string, never a base64 blob --
	// assert the raw JSON value is exactly the expected quoted string, not
	// merely that it decodes to the right bytes after further processing.
	contentValue, ok := raw["content"].(string)
	if !ok {
		t.Fatalf("content field is not a JSON string: %T", raw["content"])
	}
	if contentValue != r.Content {
		t.Fatalf("content = %q, want %q (must be plain text, never base64)", contentValue, r.Content)
	}
	if !strings.Contains(string(data), `"content":"hello world"`) {
		t.Fatalf("marshaled JSON does not contain the literal plaintext content field; got: %s", data)
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestContextResource_Validate exercises the round-7 P2 fix: Validate()
// must reject every internally-incoherent ContextResource this package can
// construct, mirroring context_addressable_resources' own CHECK
// constraints (DESIGN.md §6.1) at the Go level, for a wire object that
// never went through a database at all.
func TestContextResource_Validate(t *testing.T) {
	base := sampleResource()
	if err := base.Validate(); err != nil {
		t.Fatalf("valid resource failed Validate(): %v", err)
	}

	mutate := func(f func(*ContextResource)) ContextResource {
		r := base
		f(&r)
		return r
	}

	invalid := map[string]ContextResource{
		"empty handle":                mutate(func(r *ContextResource) { r.Handle = "" }),
		"invalid kind":                mutate(func(r *ContextResource) { r.Kind = "role_profile" }),
		"authority tier mismatch":     mutate(func(r *ContextResource) { r.AuthorityTier = "approved_memory" }), // r.Kind is rag_evidence
		"wrong instruction class":     mutate(func(r *ContextResource) { r.InstructionClass = "role_instruction" }),
		"wrong trust class":           mutate(func(r *ContextResource) { r.TrustClass = "authoritative" }),
		"may grant capabilities true": mutate(func(r *ContextResource) { r.MayGrantCapabilities = true }),
		"invalid data class":          mutate(func(r *ContextResource) { r.DataClass = "secret" }),
		"short content digest":        mutate(func(r *ContextResource) { r.ContentDigest = "abc" }),
		"empty source reference":      mutate(func(r *ContextResource) { r.SourceReference = "" }),
		"empty source version":        mutate(func(r *ContextResource) { r.SourceVersion = "" }),
		"negative byte count":         mutate(func(r *ContextResource) { r.ByteCount = -1 }),
		// Round-8 addition (P2 finding): validators must reproduce the
		// schema's real 1..500/1..240 character bounds, not merely
		// non-empty checks.
		"oversized source reference": mutate(func(r *ContextResource) { r.SourceReference = strings.Repeat("x", sourceReferenceMaxLen+1) }),
		"oversized source version":   mutate(func(r *ContextResource) { r.SourceVersion = strings.Repeat("x", sourceVersionMaxLen+1) }),
		// Round-8 additions (P1 finding): the handle's own encoded identity
		// must agree with the resource's Kind/SourceVersion/ContentDigest --
		// a wire object must never assert two identities at once.
		"handle kind contradicts resource kind": mutate(func(r *ContextResource) {
			r.Handle = handleForIdentity(ResourceKindApprovedMemory, r.SourceVersion, r.ContentDigest).Encode()
		}),
		"handle version contradicts source version": mutate(func(r *ContextResource) {
			r.Handle = handleForIdentity(r.Kind, "some-other-version", r.ContentDigest).Encode()
		}),
		"handle digest contradicts content digest": mutate(func(r *ContextResource) {
			r.Handle = handleForIdentity(r.Kind, r.SourceVersion, strings.Repeat("d", 64)).Encode()
		}),
	}
	for name, r := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := r.Validate(); err == nil {
				t.Fatalf("Validate() succeeded for invalid resource (%s), want error", name)
			}
		})
	}
}

// TestContextResource_M2aAuthorityCeiling asserts the frozen invariant
// (DESIGN.md I-4/§6.1, corrected round 6.1) that the M2a ceiling constants
// are exactly contextengine's own InstructionData/TrustUntrusted values --
// reused, never redeclared (round-7 correction, P1 finding).
func TestContextResource_M2aAuthorityCeiling(t *testing.T) {
	if InstructionClassM2a != contextengine.InstructionData {
		t.Errorf("InstructionClassM2a = %q, want contextengine.InstructionData (%q)", InstructionClassM2a, contextengine.InstructionData)
	}
	if TrustClassM2a != contextengine.TrustUntrusted {
		t.Errorf("TrustClassM2a = %q, want contextengine.TrustUntrusted (%q)", TrustClassM2a, contextengine.TrustUntrusted)
	}
	if MayGrantCapabilitiesM2a != false {
		t.Errorf("MayGrantCapabilitiesM2a = %v, want false", MayGrantCapabilitiesM2a)
	}
}

// TestValidDataClassM2a asserts the closed set mirrored from
// context_segments.data_class (migration 000006) -- a strict SUBSET of
// contextengine.DataClass's own five-value set (DataSecret/DataClinical
// are real contextengine values but not admissible for any M2a resource).
func TestValidDataClassM2a(t *testing.T) {
	for _, d := range []DataClass{contextengine.DataPublic, contextengine.DataOrganizational, contextengine.DataSanitized} {
		if !ValidDataClassM2a(d) {
			t.Errorf("ValidDataClassM2a(%q) = false, want true", d)
		}
	}
	for _, d := range []DataClass{"", contextengine.DataSecret, contextengine.DataClinical} {
		if ValidDataClassM2a(d) {
			t.Errorf("ValidDataClassM2a(%q) = true, want false", d)
		}
	}
}

// TestOutcome_Valid asserts the frozen six-value vocabulary DESIGN.md §9C
// requires ("the exact same vocabulary context_disclosure_events.outcome
// already uses").
func TestOutcome_Valid(t *testing.T) {
	for _, o := range []Outcome{OutcomeOK, OutcomeInvalidRequest, OutcomeNotFound, OutcomeForbidden, OutcomeStaleDrift, OutcomeOperationalFailure} {
		if !o.Valid() {
			t.Errorf("Outcome(%q).Valid() = false, want true", o)
		}
	}
	for _, o := range []Outcome{"", "success", "denied", "unauthorized"} {
		if o.Valid() {
			t.Errorf("Outcome(%q).Valid() = true, want false", o)
		}
	}
}

// TestContextToolResult_RoundTripsForEveryOutcome is the M2.0-scoped half
// of TEST_PLAN.md category M's determinism requirement: every one of
// DESIGN.md §17's six outcomes must round-trip through ContextToolResult's
// JSON shape with `ok`/`code` set correctly. (Category M's other half --
// that these travel through ToolExecutor.Execute with a nil error and keep
// the run alive -- requires executionharness wiring and belongs to M2.3,
// TEST_PLAN.md M1-M3.)
func TestContextToolResult_RoundTripsForEveryOutcome(t *testing.T) {
	resource := sampleResource()
	aggregate := AggregateResult{
		Members:   []AggregateMember{sampleAggregateMember(ResourceKindApprovedMemory), sampleAggregateMember(ResourceKindRAGEvidence)},
		Content:   "AABBB",
		ByteCount: 10, // sum of both members' ByteCount (5+5) -- round-8 semantics
	}
	cases := []struct {
		name string
		want ContextToolResult
	}{
		{"ok/fetch-shaped", NewOKResourceResult(resource)},
		{"ok/inspect-shaped", NewOKInspectResult([]ResourceDescriptor{
			{Handle: resource.Handle, Kind: resource.Kind, SourceReference: resource.SourceReference, ByteCount: resource.ByteCount, TrustClass: resource.TrustClass, DataClass: resource.DataClass},
		})},
		{"ok/inspect-empty", NewOKInspectResult(nil)},
		{"ok/search-shaped", NewOKSearchResult([]SearchResult{
			{Handle: resource.Handle, Kind: resource.Kind, Snippet: "an excerpt", Score: 0.87, DataClass: resource.DataClass},
		})},
		{"ok/search-empty", NewOKSearchResult(nil)},
		{"ok/aggregate-shaped", NewOKAggregateResult(aggregate)},
		{"invalid_request", NewDeniedResult(OutcomeInvalidRequest, "malformed handle")},
		{"not_found", NewDeniedResult(OutcomeNotFound, "no matching resource")},
		{"forbidden", NewDeniedResult(OutcomeForbidden, "action not granted")},
		{"stale_drift", NewDeniedResult(OutcomeStaleDrift, "content digest mismatch")},
		{"operational_failure", NewDeniedResult(OutcomeOperationalFailure, "storage unavailable")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.want.Code.Valid() {
				t.Fatalf("test fixture uses invalid Outcome %q", tc.want.Code)
			}
			if err := tc.want.Validate(); err != nil {
				t.Fatalf("test fixture is not internally coherent: %v", err)
			}
			data, err := tc.want.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got, err := UnmarshalContextToolResult(data)
			if err != nil {
				t.Fatalf("UnmarshalContextToolResult: %v", err)
			}
			if !reflect.DeepEqual(tc.want, got) {
				t.Fatalf("round-trip mismatch:\n want: %+v\n got:  %+v", tc.want, got)
			}
		})
	}
}

// TestContextToolResult_MarshalRejectsIncoherentState is the round-7 P2
// fix's direct regression test: Marshal MUST refuse to encode an
// impossible ContextToolResult, rather than silently emitting
// {"ok":false,"code":"ok"} or similar. This is the exact example the
// independent review flagged: NewDeniedResult(OutcomeOK, "...").
func TestContextToolResult_MarshalRejectsIncoherentState(t *testing.T) {
	cases := map[string]ContextToolResult{
		"denied result claiming ok code": NewDeniedResult(OutcomeOK, "should never be constructible this way"),
		"invalid outcome code":           {OK: false, Code: Outcome("banana")},
		"ok flag without ok code":        {OK: true, Code: OutcomeNotFound},
		"denied result carrying a resource": func() ContextToolResult {
			r := NewDeniedResult(OutcomeForbidden, "denied")
			res := sampleResource()
			r.Resource = &res
			return r
		}(),
	}
	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			if err := result.Validate(); err == nil {
				t.Fatalf("Validate() succeeded for incoherent result (%s), want error", name)
			}
			if _, err := result.Marshal(); err == nil {
				t.Fatalf("Marshal() succeeded for incoherent result (%s), want error", name)
			}
		})
	}
}

// TestAggregateResult_Validate exercises the round-7 P1 fix (aggregate
// representation) and the round-8 correction to its ByteCount semantics: a
// multi-member aggregate with genuinely different Kind/DataClass per member
// must be representable and valid, and AggregateResult.ByteCount must equal
// the SUM of each member's own raw ByteCount -- never a comparison against
// len(Content), which is the wrapped, model-visible representation and has
// no enforceable length relationship to the raw byte counts (round-8 P1
// finding: the previous Validate() compared ByteCount to len(Content) and
// never actually computed the member sum its own doc comment claimed to
// check).
func TestAggregateResult_Validate(t *testing.T) {
	members := []AggregateMember{
		sampleAggregateMember(ResourceKindApprovedMemory),
		sampleAggregateMember(ResourceKindRAGEvidence),
	}
	var wantByteSum int64
	for _, m := range members {
		wantByteSum += m.ByteCount
	}
	valid := AggregateResult{
		Members:   members,
		Content:   "AABBB",
		ByteCount: wantByteSum,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid aggregate failed Validate(): %v", err)
	}
	// Confirm members can genuinely differ in Kind -- this is exactly the
	// case ContextResource alone could not represent.
	if valid.Members[0].Kind == valid.Members[1].Kind {
		t.Fatal("test fixture bug: members must have different kinds to prove the fix")
	}

	noMembers := AggregateResult{Content: "", ByteCount: 0}
	if err := noMembers.Validate(); err == nil {
		t.Fatal("Validate() succeeded for an aggregate with zero members, want error")
	}

	byteMismatch := valid
	byteMismatch.ByteCount = 999
	if err := byteMismatch.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a byte-count/member-sum mismatch, want error")
	}

	// Round-8 regression: len(Content) ("AABBB" == 5 bytes) deliberately
	// does NOT equal wantByteSum (10) here -- proving Validate() no longer
	// compares ByteCount against len(Content) at all, only against the
	// member sum.
	if int64(len(valid.Content)) == wantByteSum {
		t.Fatal("test fixture bug: len(Content) must differ from the member byte sum to prove ByteCount is no longer compared to len(Content)")
	}

	badMember := valid
	badMember.Members = append([]AggregateMember{}, valid.Members...)
	badMember.Members[0].Kind = "role_profile"
	if err := badMember.Validate(); err == nil {
		t.Fatal("Validate() succeeded for an aggregate with an invalid member kind, want error")
	}
}

// TestNewOKResourceResult_SetsOK asserts the OK/Code invariant: a
// resource-carrying result is always OK==true, Code==OutcomeOK.
func TestNewOKResourceResult_SetsOK(t *testing.T) {
	result := NewOKResourceResult(sampleResource())
	if !result.OK {
		t.Error("OK = false, want true")
	}
	if result.Code != OutcomeOK {
		t.Errorf("Code = %q, want %q", result.Code, OutcomeOK)
	}
	if result.Resource == nil {
		t.Fatal("Resource is nil, want populated")
	}
	if result.Resources != nil || result.Results != nil || result.Aggregate != nil {
		t.Error("Resources/Results/Aggregate must stay nil for a fetch/slice-shaped result")
	}
}

// TestNewDeniedResult_NeverSetsOK asserts every non-ok outcome constructor
// sets OK=false -- the model-visible signal that distinguishes success
// from every denial category (DESIGN.md §9C).
func TestNewDeniedResult_NeverSetsOK(t *testing.T) {
	for _, code := range []Outcome{OutcomeInvalidRequest, OutcomeNotFound, OutcomeForbidden, OutcomeStaleDrift, OutcomeOperationalFailure} {
		result := NewDeniedResult(code, "message")
		if result.OK {
			t.Errorf("code %q: OK = true, want false", code)
		}
		if result.Resource != nil || result.Resources != nil || result.Results != nil || result.Aggregate != nil {
			t.Errorf("code %q: a denied result must carry no Resource/Resources/Results/Aggregate", code)
		}
	}
}

// TestNewOKInspectResult_EmptyListStaysOnWire is the round-8 P1 fix's direct
// regression test: DESIGN.md §11 is explicit that an empty resource list is
// "simply the true answer" for context.inspect, not itself a FORBIDDEN
// outcome -- so the wire result must still carry "resources":[] (not omit
// the field entirely). Before the fix, Resources was a plain
// []ResourceDescriptor with `omitempty`, which omits the field for BOTH nil
// AND a legitimately-empty slice, making a caller unable to tell "no
// Resources concept applies" from "Resources applies and is empty."
func TestNewOKInspectResult_EmptyListStaysOnWire(t *testing.T) {
	result := NewOKInspectResult(nil)
	if result.Resources == nil {
		t.Fatal("Resources is nil, want a non-nil pointer to an empty slice")
	}
	if len(*result.Resources) != 0 {
		t.Fatalf("Resources = %v, want empty", *result.Resources)
	}
	data, err := result.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"resources":[]`) {
		t.Fatalf("marshaled JSON does not carry \"resources\":[]; got: %s", data)
	}
}

// TestNewOKSearchResult_EmptyListStaysOnWire mirrors
// TestNewOKInspectResult_EmptyListStaysOnWire for context.search.
func TestNewOKSearchResult_EmptyListStaysOnWire(t *testing.T) {
	result := NewOKSearchResult(nil)
	if result.Results == nil {
		t.Fatal("Results is nil, want a non-nil pointer to an empty slice")
	}
	if len(*result.Results) != 0 {
		t.Fatalf("Results = %v, want empty", *result.Results)
	}
	data, err := result.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"results":[]`) {
		t.Fatalf("marshaled JSON does not carry \"results\":[]; got: %s", data)
	}
}

// TestContextToolResult_Validate_ExactlyOneVariant is the round-8 P1 fix's
// direct regression test: an OK result must carry EXACTLY one of
// Resource/Resources/Results/Aggregate -- previously Validate accepted
// zero, two, or all four simultaneously, as long as whichever happened to
// be non-nil individually validated.
func TestContextToolResult_Validate_ExactlyOneVariant(t *testing.T) {
	resource := sampleResource()
	descriptors := []ResourceDescriptor{}

	zeroVariants := ContextToolResult{OK: true, Code: OutcomeOK}
	if err := zeroVariants.Validate(); err == nil {
		t.Fatal("Validate() succeeded for an ok result with zero variants, want error")
	}

	twoVariants := ContextToolResult{OK: true, Code: OutcomeOK, Resource: &resource, Resources: &descriptors}
	if err := twoVariants.Validate(); err == nil {
		t.Fatal("Validate() succeeded for an ok result with two variants, want error")
	}
}

// TestUnmarshalContextToolResult_RejectsIncoherentState is the round-8 P2
// fix's direct regression test: UnmarshalContextToolResult must reject any
// payload Marshal itself would refuse to produce -- previously it returned
// json.Unmarshal's result unconditionally, without ever calling Validate.
func TestUnmarshalContextToolResult_RejectsIncoherentState(t *testing.T) {
	cases := map[string][]byte{
		"denied result claiming ok code": []byte(`{"ok":false,"code":"ok"}`),
		"invalid outcome code":           []byte(`{"ok":false,"code":"banana"}`),
		"ok result with zero variants":   []byte(`{"ok":true,"code":"ok"}`),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := UnmarshalContextToolResult(data); err == nil {
				t.Fatalf("UnmarshalContextToolResult(%s) succeeded, want error", data)
			}
		})
	}
}

// TestResourceDescriptor_Validate_HandleKindContradiction and
// TestSearchResult_Validate_HandleKindContradiction are the round-8 P1
// fix's direct regression tests for ResourceDescriptor/SearchResult's
// narrower (Kind-only) identity cross-check.
func TestResourceDescriptor_Validate_HandleKindContradiction(t *testing.T) {
	d := ResourceDescriptor{
		Handle:          handleForIdentity(ResourceKindRAGEvidence, "v1", strings.Repeat("a", 64)).Encode(),
		Kind:            ResourceKindApprovedMemory, // contradicts the handle's own k=rag_evidence
		SourceReference: "ref",
		ByteCount:       10,
		TrustClass:      TrustClassM2a,
		DataClass:       contextengine.DataPublic,
	}
	if err := d.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a descriptor whose handle kind contradicts its own Kind, want error")
	}
}

func TestSearchResult_Validate_HandleKindContradiction(t *testing.T) {
	s := SearchResult{
		Handle:    handleForIdentity(ResourceKindRAGEvidence, "v1", strings.Repeat("a", 64)).Encode(),
		Kind:      ResourceKindApprovedMemory, // contradicts the handle's own k=rag_evidence
		Snippet:   "excerpt",
		Score:     0.1,
		DataClass: contextengine.DataPublic,
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a search result whose handle kind contradicts its own Kind, want error")
	}
}

// TestAggregateMember_Validate_HandleIdentityContradiction is the round-8
// P1 fix's direct regression test, reproducing the exact bug the
// independent review demonstrated in this file's own (now-fixed)
// sampleAggregateMember: a member whose Handle encodes a different
// Kind/SourceVersion/ContentDigest than its own fields must be rejected.
func TestAggregateMember_Validate_HandleIdentityContradiction(t *testing.T) {
	base := sampleAggregateMember(ResourceKindApprovedMemory)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid aggregate member failed Validate(): %v", err)
	}

	kindContradiction := base
	kindContradiction.Handle = handleForIdentity(ResourceKindRAGEvidence, base.SourceVersion, base.ContentDigest).Encode()
	if err := kindContradiction.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a member whose handle kind contradicts its own Kind, want error")
	}

	versionContradiction := base
	versionContradiction.Handle = handleForIdentity(base.Kind, "some-other-version", base.ContentDigest).Encode()
	if err := versionContradiction.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a member whose handle version contradicts its own SourceVersion, want error")
	}

	digestContradiction := base
	digestContradiction.Handle = handleForIdentity(base.Kind, base.SourceVersion, strings.Repeat("e", 64)).Encode()
	if err := digestContradiction.Validate(); err == nil {
		t.Fatal("Validate() succeeded for a member whose handle digest contradicts its own ContentDigest, want error")
	}
}
