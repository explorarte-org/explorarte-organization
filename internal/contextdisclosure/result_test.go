package contextdisclosure

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

func sampleResource() ContextResource {
	return ContextResource{
		Handle:               validHandle().Encode(),
		Kind:                 ResourceKindRAGEvidence,
		SourceReference:      "rag/knowledge/doc-42",
		SourceVersion:        "v3",
		AuthorityTier:        AuthorityTier(ResourceKindRAGEvidence),
		InstructionClass:     InstructionClassM2a,
		TrustClass:           TrustClassM2a,
		DataClass:            contextengine.DataSanitized,
		MayGrantCapabilities: false,
		ContentDigest:        strings.Repeat("b", 64),
		Content:              "hello world",
		ByteCount:            int64(len("hello world")),
	}
}

func sampleAggregateMember(kind ResourceKind) AggregateMember {
	return AggregateMember{
		Handle:               validHandle().Encode(),
		Kind:                 kind,
		SourceReference:      "ref",
		SourceVersion:        "v1",
		AuthorityTier:        AuthorityTier(kind),
		InstructionClass:     InstructionClassM2a,
		TrustClass:           TrustClassM2a,
		DataClass:            contextengine.DataPublic,
		MayGrantCapabilities: false,
		ContentDigest:        strings.Repeat("c", 64),
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
		"byte count mismatch":         mutate(func(r *ContextResource) { r.ByteCount = 999 }),
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
		ByteCount: 5,
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
// representation): a multi-member aggregate with genuinely different
// Kind/DataClass per member must be representable and valid, and an
// aggregate whose byte count doesn't match its content must be rejected.
func TestAggregateResult_Validate(t *testing.T) {
	valid := AggregateResult{
		Members: []AggregateMember{
			sampleAggregateMember(ResourceKindApprovedMemory),
			sampleAggregateMember(ResourceKindRAGEvidence),
		},
		Content:   "AABBB",
		ByteCount: 5,
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
		t.Fatal("Validate() succeeded for a byte-count/content mismatch, want error")
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
