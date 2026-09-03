package executive

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// R7 closed as R7_BLOCKED_WORKER_RESULT_CONTRACT with common cause
// PROMPT_CONTRACT_MISMATCH + RETRY_FEEDBACK_TOO_COARSE: the host rejected a
// worker-result whose summary exceeded Limits.MaxStringBytes, but the model
// had never been told the limit existed and every retry only ever read
// "invalid summary". These guards pin the repair on both ends of that loop:
// the contract must state the byte limit before the attempt, and the
// rejection must measure the violation after it.

// byteLimitedProperties are the worker-result fields the host actually runs
// through validateRequiredString(value, limits.MaxStringBytes, ...):
// ParseWorkerResult validates summary, evidence.claim, evidence.subject and
// evidence.ref with exactly that limit, and validateStrings wraps every
// evidence_refs element in the same check -- hence "evidence_refs[]" here.
// If a field starts or stops using MaxStringBytes, this list must follow.
func byteLimitedProperties(t *testing.T, schema json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Properties struct {
			Summary      json.RawMessage `json:"summary"`
			EvidenceRefs struct {
				Items json.RawMessage `json:"items"`
			} `json:"evidence_refs"`
			Evidence struct {
				Items struct {
					Properties struct {
						Claim   json.RawMessage `json:"claim"`
						Subject json.RawMessage `json:"subject"`
						Ref     json.RawMessage `json:"ref"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"evidence"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("worker-result schema is not valid JSON: %v", err)
	}
	return map[string]json.RawMessage{
		"summary":          doc.Properties.Summary,
		"evidence_refs[]":  doc.Properties.EvidenceRefs.Items,
		"evidence.claim":   doc.Properties.Evidence.Items.Properties.Claim,
		"evidence.subject": doc.Properties.Evidence.Items.Properties.Subject,
		"evidence.ref":     doc.Properties.Evidence.Items.Properties.Ref,
	}
}

// GUARD A -- CONTRACT COMMUNICATION.
//
// maxLength alone does NOT express the host contract: JSON-Schema maxLength
// counts code points while validateRequiredString measures UTF-8 bytes with
// len(string). Every byte-limited field must therefore carry BOTH the
// keyword AND a description that states the byte rule in words, and the
// number must be derived from the same Limits the validator reads -- not
// duplicated from a second literal.
func TestWorkerResultSchemaCommunicatesTheHostByteLimit(t *testing.T) {
	limits := DefaultLimits()
	schema := WorkerResultOutputSchemaFor(limits)
	for field, property := range byteLimitedProperties(t, schema) {
		var declared struct {
			Type        string `json:"type"`
			MaxLength   int    `json:"maxLength"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(property, &declared); err != nil {
			t.Fatalf("%s: malformed property schema: %v", field, err)
		}
		if declared.MaxLength != limits.MaxStringBytes {
			t.Fatalf("%s: maxLength=%d, want %d derived from Limits.MaxStringBytes", field, declared.MaxLength, limits.MaxStringBytes)
		}
		if !strings.Contains(declared.Description, "UTF-8") {
			t.Fatalf("%s: description must name UTF-8 explicitly, got %q", field, declared.Description)
		}
		want := "must not exceed 4000 bytes"
		if !strings.Contains(declared.Description, want) {
			t.Fatalf("%s: description must state the byte limit (%q), got %q", field, want, declared.Description)
		}
		if !strings.Contains(declared.Description, "non-empty") {
			t.Fatalf("%s: description must state non-emptiness, got %q", field, declared.Description)
		}
	}
}

// The number is built from Limits, not pasted: a different limit produces a
// different contract, which is what keeps the schema and validateRequiredString
// from drifting apart.
func TestWorkerResultSchemaLimitIsDerivedFromLimitsNotALiteral(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxStringBytes = 777
	for field, property := range byteLimitedProperties(t, WorkerResultOutputSchemaFor(limits)) {
		body := string(property)
		if !strings.Contains(body, `"maxLength":777`) || strings.Contains(body, `"maxLength":4000`) {
			t.Fatalf("%s: schema did not follow Limits.MaxStringBytes=777: %s", field, body)
		}
	}
}

// HOST/SCHEMA SYMMETRY FOR ARRAY ELEMENTS.
//
// evidence_refs[] shares MaxStringBytes host-side: validateStrings runs every
// element through validateRequiredString(value, limits.MaxStringBytes, ...)
// exactly like summary. A schema that let any string through would recreate
// PROMPT_CONTRACT_MISMATCH for elements -- the host rejecting a ref over a
// bound the model was never told about. Both halves must agree in one place:
// the host rejects the 4200-byte element AND the schema declares that bound.
func TestEvidenceRefElementOverByteLimitIsRejectedAndItsSchemaDeclaresTheSameBound(t *testing.T) {
	limits := DefaultLimits()

	long := strings.Repeat("a", limits.MaxStringBytes+200)
	if len(long) != 4200 {
		t.Fatalf("fixture lost its point: element is %d bytes", len(long))
	}
	artifact := []byte(`{"schema_version":"worker-result/v1","summary":"ok","evidence_refs":["` + long + `"],"evidence":[]}`)
	_, err := ParseWorkerResult(artifact, limits)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("an over-long evidence_refs element must stay a contract rejection: %v", err)
	}
	for _, want := range []string{"evidence_refs[0]", "4200", "4000", "bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection feedback must contain %q for the retry to act on, got: %v", want, err)
		}
	}

	property := byteLimitedProperties(t, WorkerResultOutputSchemaFor(limits))["evidence_refs[]"]
	var declared struct {
		MaxLength int `json:"maxLength"`
	}
	if err := json.Unmarshal(property, &declared); err != nil {
		t.Fatalf("malformed items schema for evidence_refs[]: %v", err)
	}
	if declared.MaxLength != limits.MaxStringBytes {
		t.Fatalf("schema told maxLength=%d while the host rejects elements above %d", declared.MaxLength, limits.MaxStringBytes)
	}
}

// The dynamic worker schema must narrow refs to the same host-classified
// supply that the validator receives. In particular, a nearby but unshown
// postrun.go range must not become an allowed enum value merely because the
// file name is plausible.
func TestWorkerResultSlotsSchemaEnumeratesOnlyAvailableRefs(t *testing.T) {
	required := []EvidenceRequirement{
		{Subject: "PostRun", Relations: []string{"definition"}},
		{Subject: "RetryPolicy", Relations: []string{"application"}},
	}
	available := map[EvidenceSlot][]string{
		{Subject: "PostRun", Relation: "definition"}: {
			"repository://explorarte-organization@pin/internal/executive/postrun.go#L1-L20",
		},
		{Subject: "RetryPolicy", Relation: "application"}: {
			"repository://explorarte-organization@pin/internal/executive/retry.go#L20-L30",
			"repository://explorarte-organization@pin/internal/executive/retry.go#L20-L30",
		},
	}
	schema := WorkerResultOutputSchemaForSlots(DefaultLimits(), required, available, nil)
	var document struct {
		Properties struct {
			Evidence struct {
				Items struct {
					Properties struct {
						Subject struct {
							Enum []string `json:"enum"`
						} `json:"subject"`
						Ref struct {
							Enum []string `json:"enum"`
						} `json:"ref"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"evidence"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &document); err != nil {
		t.Fatalf("dynamic worker schema is not valid JSON: %v", err)
	}
	if got := document.Properties.Evidence.Items.Properties.Subject.Enum; len(got) != 2 || got[0] != "PostRun" || got[1] != "RetryPolicy" {
		t.Fatalf("subject enum=%v, want sorted required subjects", got)
	}
	refs := document.Properties.Evidence.Items.Properties.Ref.Enum
	for _, want := range []string{
		"repository://explorarte-organization@pin/internal/executive/postrun.go#L1-L20",
		"repository://explorarte-organization@pin/internal/executive/retry.go#L20-L30",
	} {
		found := false
		for _, ref := range refs {
			if ref == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("available ref %q is missing from enum %v", want, refs)
		}
	}
	if len(refs) != 2 {
		t.Fatalf("refs were not deduplicated: %v", refs)
	}
	for _, ref := range refs {
		if ref == "repository://explorarte-organization@pin/internal/executive/postrun.go#L65-L113" {
			t.Fatalf("unavailable postrun.go range appeared in ref enum: %v", refs)
		}
	}
}

// GUARD B -- MULTIBYTE ABLATION.
//
// Protects the byte semantics against a future "simplification" that swaps
// len(string) for a rune/character count or trusts schema maxLength alone:
// a value can be well under the character limit and still exceed the byte
// limit, and the host must keep rejecting it.
func TestMultibyteSummaryUnderCharacterLimitButOverByteLimitIsRejected(t *testing.T) {
	limits := DefaultLimits()
	// 2500 characters ('é' is 2 bytes in UTF-8) = 2500 chars <= 4000 chars,
	// but 5000 bytes > 4000 bytes. The exact trap maxLength-only validation
	// would wave through.
	multibyte := strings.Repeat("é", limits.MaxStringBytes/2+500)
	runes := len([]rune(multibyte))
	bytesLen := len(multibyte)
	if runes > limits.MaxStringBytes || bytesLen <= limits.MaxStringBytes {
		t.Fatalf("fixture lost its point: chars=%d bytes=%d limit=%d", runes, bytesLen, limits.MaxStringBytes)
	}
	artifact := []byte(`{"schema_version":"worker-result/v2","summary":"` + multibyte + `","evidence_refs":[],"evidence":[]}`)
	_, err := ParseWorkerResult(artifact, limits)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a %d-byte summary (%d characters) must stay a contract rejection: %v", bytesLen, runes, err)
	}
	if !strings.Contains(err.Error(), "5000") || !strings.Contains(err.Error(), "4000") {
		t.Fatalf("rejection must measure the violation in bytes, got: %v", err)
	}

	// The same artifact one byte under the limit is accepted: the boundary
	// being tested is bytes, not shape.
	fits := strings.Repeat("é", (limits.MaxStringBytes-1)/2)
	artifact = []byte(`{"schema_version":"worker-result/v2","summary":"` + fits + `","evidence_refs":[],"evidence":[]}`)
	if _, err := ParseWorkerResult(artifact, limits); err != nil {
		t.Fatalf("a summary within the byte limit was refused: %v", err)
	}
}

// GUARD C -- MEASURED RETRY FEEDBACK.
//
// The reason a long summary is rejected must carry the observed size and the
// bound so the next attempt can comply; whitespace and NUL get their own
// named causes. The message stays classification (field + cause + measure),
// never the offending content itself.
func TestLengthRejectionFeedbackCarriesFieldCauseAndMeasure(t *testing.T) {
	limits := DefaultLimits()
	long := strings.Repeat("a", 4200)
	artifact := []byte(`{"schema_version":"worker-result/v2","summary":"` + long + `","evidence_refs":[],"evidence":[]}`)
	_, err := ParseWorkerResult(artifact, limits)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("an over-long summary is a contract rejection, not anything else: %v", err)
	}
	for _, want := range []string{"summary", "4200", "4000", "bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection feedback must contain %q for the retry to act on, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), long) {
		t.Fatalf("feedback must classify, not echo the offending content")
	}

	empty := []byte(`{"schema_version":"worker-result/v2","summary":"   ","evidence_refs":[],"evidence":[]}`)
	_, err = ParseWorkerResult(empty, limits)
	if !errors.Is(err, ErrContractRejected) || !strings.Contains(err.Error(), "invalid summary: required string is empty after trimming") {
		t.Fatalf("whitespace-only feedback must name trimming, got: %v", err)
	}

	nul := []byte("{\"schema_version\":\"worker-result/v2\",\"summary\":\"bad\\u0000seed\",\"evidence_refs\":[],\"evidence\":[]}")
	_, err = ParseWorkerResult(nul, limits)
	if !errors.Is(err, ErrContractRejected) || !strings.Contains(err.Error(), "invalid summary: contains NUL byte") {
		t.Fatalf("NUL feedback must name the NUL byte, got: %v", err)
	}
}
