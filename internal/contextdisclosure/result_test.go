package contextdisclosure

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func sampleResource() ContextResource {
	return ContextResource{
		Handle:               validHandle().Encode(),
		Kind:                 ResourceKindRAGEvidence,
		SourceReference:      "rag/knowledge/doc-42",
		SourceVersion:        "v3",
		AuthorityTier:        "rag_evidence",
		InstructionClass:     InstructionClassData,
		TrustClass:           TrustClassUntrusted,
		DataClass:            DataClassSanitized,
		MayGrantCapabilities: false,
		ContentDigest:        strings.Repeat("b", 64),
		Content:              []byte("hello world"),
		ByteCount:            int64(len("hello world")),
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

// TestContextResource_M2aAuthorityCeiling asserts the frozen invariant
// (DESIGN.md I-4/§6.1, corrected round 6.1) that a ContextResource built
// from constants is exactly InstructionClassData/TrustClassUntrusted/
// MayGrantCapabilitiesM2a -- never a different value -- for both admitted
// ResourceKinds. This is a shape/constant-usage assertion, not a
// runtime-enforced invariant (M2.0 has no construction path from a real
// source yet); it exists to catch a drift between the frozen constants and
// how a later slice actually populates them.
func TestContextResource_M2aAuthorityCeiling(t *testing.T) {
	if InstructionClassData != "data" {
		t.Errorf("InstructionClassData = %q, want %q", InstructionClassData, "data")
	}
	if TrustClassUntrusted != "untrusted" {
		t.Errorf("TrustClassUntrusted = %q, want %q", TrustClassUntrusted, "untrusted")
	}
	if MayGrantCapabilitiesM2a != false {
		t.Errorf("MayGrantCapabilitiesM2a = %v, want false", MayGrantCapabilitiesM2a)
	}
}

// TestDataClass_Valid asserts the closed set mirrored from
// context_segments.data_class (migration 000006).
func TestDataClass_Valid(t *testing.T) {
	for _, d := range []DataClass{DataClassPublic, DataClassOrganizational, DataClassSanitized} {
		if !d.Valid() {
			t.Errorf("DataClass(%q).Valid() = false, want true", d)
		}
	}
	for _, d := range []DataClass{"", "secret", "clinical"} {
		if d.Valid() {
			t.Errorf("DataClass(%q).Valid() = true, want false", d)
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
			{Handle: resource.Handle, Kind: resource.Kind, Snippet: "an excerpt", Score: 0.87},
		})},
		{"ok/search-empty", NewOKSearchResult(nil)},
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
	if result.Resources != nil || result.Results != nil {
		t.Error("Resources/Results must stay nil for a fetch/slice/aggregate-shaped result")
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
		if result.Resource != nil || result.Resources != nil || result.Results != nil {
			t.Errorf("code %q: a denied result must carry no Resource/Resources/Results", code)
		}
	}
}
