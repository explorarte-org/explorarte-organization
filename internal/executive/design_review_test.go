package executive

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const testDesignDigest = "1111111111111111111111111111111111111111111111111111111111111111"

func testDesign() DesignIdentity {
	return DesignIdentity{DesignID: "m2-1-context-memory", DesignVersion: "v1", DesignDigest: testDesignDigest}
}

// ---------------------------------------------------------------- purposes

func TestAdversarialAndAdjudicationPurposesAreDistinctDurableIdentities(t *testing.T) {
	for _, purpose := range []ExecutionPurpose{PurposeAdversarialReview, PurposeDesignAdjudication} {
		if !purpose.Valid() {
			t.Fatalf("%s is not valid", purpose)
		}
		if purpose.LegacyPurpose() == "" {
			t.Fatalf("%s has no legacy purpose", purpose)
		}
	}
	if got := PurposeAdversarialReview.LegacyPurpose(); got != "adversarial_review" {
		t.Fatalf("adversarial legacy purpose=%q -- modelegress.ExecutiveScopeMarker keys on this exact string", got)
	}
	if got := PurposeDesignAdjudication.LegacyPurpose(); got != "design_adjudication" {
		t.Fatalf("adjudication legacy purpose=%q", got)
	}

	// Every purpose must remain a distinct durable identity: the run id is
	// what makes a resume continue the same trajectory, so two purposes
	// sharing one would silently merge two histories.
	seen := map[string]ExecutionPurpose{}
	legacy := map[string]ExecutionPurpose{}
	for _, purpose := range []ExecutionPurpose{
		PurposeCEOPlan, PurposeDepartmentPlan, PurposeDepartmentWorker,
		PurposeDepartmentReview, PurposeCEOClosure,
		PurposeAdversarialReview, PurposeDesignAdjudication,
	} {
		runID := harnessRunID("explorarte", 7, 3, purpose)
		if other, clash := seen[runID]; clash {
			t.Fatalf("%s and %s produce the same run identity", purpose, other)
		}
		seen[runID] = purpose
		if other, clash := legacy[purpose.LegacyPurpose()]; clash {
			t.Fatalf("%s and %s share legacy purpose %q", purpose, other, purpose.LegacyPurpose())
		}
		legacy[purpose.LegacyPurpose()] = purpose
	}

	// Specifically: adjudication is not planning, and review of a design is
	// not review of a department's execution.
	if harnessRunID("explorarte", 7, 3, PurposeDesignAdjudication) == harnessRunID("explorarte", 7, 3, PurposeCEOPlan) {
		t.Fatal("design adjudication shares a run identity with ceo planning")
	}
	if harnessRunID("explorarte", 7, 3, PurposeAdversarialReview) == harnessRunID("explorarte", 7, 3, PurposeDepartmentReview) {
		t.Fatal("adversarial review shares a run identity with department review")
	}
}

func TestUnknownPurposeStaysInvalid(t *testing.T) {
	for _, purpose := range []ExecutionPurpose{"", "adversarial_review", "design-freeze", "AdversarialReview"} {
		if purpose.Valid() {
			t.Fatalf("%q was accepted as a purpose", purpose)
		}
	}
}

// ------------------------------------------------- adversarial review/v1

func validReviewJSON() string {
	return `{
	  "schema_version":"adversarial-review/v1",
	  "verdict":"revise",
	  "findings":[
	    {"id":"AR-001","severity":"critical","claim":"The freeze gate trusts a model-attested digest.",
	     "affected_requirement":"design-freeze binding","required_correction":"Bind the digest host-side.",
	     "evidence_refs":["task:41:result"]}
	  ],
	  "contradictions":["Section 4 and section 9 disagree on who may promote."],
	  "unverified_assumptions":["Assumes the lease outlives the provider call."],
	  "security_findings":[],
	  "authority_findings":["The reviewer role is granted task.review."],
	  "recovery_findings":[],
	  "memory_epistemic_findings":[],
	  "evidence_refs":["task:40:context"]
	}`
}

func TestParseAdversarialReviewAcceptsAWellFormedResult(t *testing.T) {
	review, err := ParseAdversarialReview([]byte(validReviewJSON()), DefaultLimits())
	if err != nil {
		t.Fatalf("valid review rejected: %v", err)
	}
	if review.Verdict != AdversarialRevise || len(review.Findings) != 1 {
		t.Fatalf("review=%+v", review)
	}
	if review.Findings[0].ID != "AR-001" || review.Findings[0].Severity != SeverityCritical {
		t.Fatalf("finding=%+v", review.Findings[0])
	}
}

// The reviewer has no authority over the task graph. department-review/v1
// carries proposed_followup_tasks; this contract must not, and a result that
// smuggles them in is refused rather than ignored.
func TestAdversarialReviewHasNoFollowupTaskAuthority(t *testing.T) {
	body := strings.Replace(validReviewJSON(),
		`"evidence_refs":["task:40:context"]`,
		`"evidence_refs":["task:40:context"],
		 "proposed_followup_tasks":[{"client_key":"k","assigned_role_id":"ingenieria_ia/qa","task_class":"general.work",
		 "title":"fix it","instructions":"do the thing","acceptance_criteria":["done"],"dependencies":[],
		 "requirements":[],"priority":1}]`, 1)
	_, err := ParseAdversarialReview([]byte(body), DefaultLimits())
	if err == nil {
		t.Fatal("reviewer was allowed to propose followup tasks")
	}
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("err=%v", err)
	}
}

func TestAdversarialReviewRejectsUnknownAndForbiddenFields(t *testing.T) {
	unknown := strings.Replace(validReviewJSON(), `"verdict":"revise"`, `"verdict":"revise","confidence":0.9`, 1)
	if _, err := ParseAdversarialReview([]byte(unknown), DefaultLimits()); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("unknown field accepted: %v", err)
	}
	// A reviewer trying to select its own model, or to assert an approval, is
	// caught by the existing forbidden-key scan before any field is read.
	for _, injected := range []string{`"provider":"deepseek"`, `"model":"grok-4.6"`, `"approval_decision":"approved"`, `"authority":"owner"`} {
		body := strings.Replace(validReviewJSON(), `"verdict":"revise"`, `"verdict":"revise",`+injected, 1)
		if _, err := ParseAdversarialReview([]byte(body), DefaultLimits()); !errors.Is(err, ErrForbiddenField) {
			t.Fatalf("%s was not rejected as forbidden: %v", injected, err)
		}
	}
}

func TestAdversarialReviewRejectsMalformedContent(t *testing.T) {
	cases := map[string]string{
		"wrong schema version": strings.Replace(validReviewJSON(), "adversarial-review/v1", "adversarial-review/v2", 1),
		"invalid verdict":      strings.Replace(validReviewJSON(), `"verdict":"revise"`, `"verdict":"approve"`, 1),
		"adjudication verdict": strings.Replace(validReviewJSON(), `"verdict":"revise"`, `"verdict":"freeze"`, 1),
		"invalid severity":     strings.Replace(validReviewJSON(), `"severity":"critical"`, `"severity":"blocker"`, 1),
		"malformed finding id": strings.Replace(validReviewJSON(), `"id":"AR-001"`, `"id":"finding one"`, 1),
		"empty claim":          strings.Replace(validReviewJSON(), `"claim":"The freeze gate trusts a model-attested digest."`, `"claim":"   "`, 1),
		"empty body":           "",
		"trailing json":        validReviewJSON() + `{"schema_version":"adversarial-review/v1"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAdversarialReview([]byte(body), DefaultLimits()); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestAdversarialReviewVerdictMustMatchItsFindings(t *testing.T) {
	// revise/block with nothing found is a verdict without content.
	empty := `{"schema_version":"adversarial-review/v1","verdict":"block","findings":[],
	 "contradictions":[],"unverified_assumptions":[],"security_findings":[],"authority_findings":[],
	 "recovery_findings":[],"memory_epistemic_findings":[],"evidence_refs":[]}`
	if _, err := ParseAdversarialReview([]byte(empty), DefaultLimits()); err == nil {
		t.Fatal("block with no findings was accepted")
	}
	// accept while reporting a critical problem is the dangerous direction.
	contradictory := strings.Replace(validReviewJSON(), `"verdict":"revise"`, `"verdict":"accept"`, 1)
	if _, err := ParseAdversarialReview([]byte(contradictory), DefaultLimits()); err == nil {
		t.Fatal("accept alongside a critical finding was allowed")
	}
	// A clean accept with no findings is fine.
	clean := strings.Replace(empty, `"verdict":"block"`, `"verdict":"accept"`, 1)
	if _, err := ParseAdversarialReview([]byte(clean), DefaultLimits()); err != nil {
		t.Fatalf("clean accept rejected: %v", err)
	}
}

func TestAdversarialReviewRejectsDuplicateFindingIDs(t *testing.T) {
	body := strings.Replace(validReviewJSON(), `"evidence_refs":["task:41:result"]}`,
		`"evidence_refs":["task:41:result"]},
		 {"id":"AR-001","severity":"low","claim":"second","affected_requirement":"x",
		  "required_correction":"y","evidence_refs":[]}`, 1)
	if _, err := ParseAdversarialReview([]byte(body), DefaultLimits()); err == nil {
		t.Fatal("duplicate finding ids accepted -- an adjudication could not name one unambiguously")
	}
}

// ------------------------------------------------ design adjudication/v1

func adjudicationJSON(verdict string, digest string) string {
	required := `[]`
	if verdict == "revise" {
		required = `["Bind the digest host-side."]`
	}
	return `{
	  "schema_version":"design-adjudication/v1",
	  "verdict":"` + verdict + `",
	  "accepted_findings":["AR-001"],
	  "rejected_findings":[],
	  "required_changes":` + required + `,
	  "unresolved_owner_decisions":[],
	  "design_id":"m2-1-context-memory",
	  "design_version":"v1",
	  "design_digest":"` + digest + `",
	  "evidence_refs":["task:41:result"]
	}`
}

func TestAdjudicationAcceptsEveryVerdictAgainstTheExactDesign(t *testing.T) {
	for _, verdict := range []string{"freeze", "revise", "reject"} {
		t.Run(verdict, func(t *testing.T) {
			out, err := ParseDesignAdjudication([]byte(adjudicationJSON(verdict, testDesignDigest)), testDesign(), DefaultLimits())
			if err != nil {
				t.Fatalf("%s rejected: %v", verdict, err)
			}
			if string(out.Verdict) != verdict {
				t.Fatalf("verdict=%q", out.Verdict)
			}
		})
	}
}

// The single most important refusal in this slice: a freeze that names a
// different design than the one under adjudication.
func TestAdjudicationRefusesADifferentDesign(t *testing.T) {
	other := "2222222222222222222222222222222222222222222222222222222222222222"
	_, err := ParseDesignAdjudication([]byte(adjudicationJSON("freeze", other)), testDesign(), DefaultLimits())
	if !errors.Is(err, ErrDesignIdentityMismatch) {
		t.Fatalf("freeze over a foreign digest was not refused as an identity mismatch: %v", err)
	}
	// Drifting only the version label is still a different artifact.
	body := strings.Replace(adjudicationJSON("freeze", testDesignDigest), `"design_version":"v1"`, `"design_version":"v2"`, 1)
	if _, err = ParseDesignAdjudication([]byte(body), testDesign(), DefaultLimits()); !errors.Is(err, ErrDesignIdentityMismatch) {
		t.Fatalf("version drift accepted: %v", err)
	}
	// And a malformed digest is refused before it can be compared.
	body = strings.Replace(adjudicationJSON("freeze", testDesignDigest), testDesignDigest, "not-a-digest", 1)
	if _, err = ParseDesignAdjudication([]byte(body), testDesign(), DefaultLimits()); err == nil {
		t.Fatal("malformed digest accepted")
	}
}

func TestAdjudicationRejectsIncoherentVerdicts(t *testing.T) {
	base := adjudicationJSON("freeze", testDesignDigest)
	cases := map[string]string{
		"freeze with required changes": strings.Replace(base, `"required_changes":[]`, `"required_changes":["still fix this"]`, 1),
		"freeze with open owner decision": strings.Replace(base, `"unresolved_owner_decisions":[]`,
			`"unresolved_owner_decisions":["D-007 pending"]`, 1),
		"revise with no changes": strings.Replace(adjudicationJSON("revise", testDesignDigest),
			`"required_changes":["Bind the digest host-side."]`, `"required_changes":[]`, 1),
		"finding both accepted and rejected": strings.Replace(base, `"rejected_findings":[]`, `"rejected_findings":["AR-001"]`, 1),
		"malformed finding reference":        strings.Replace(base, `"accepted_findings":["AR-001"]`, `"accepted_findings":["the first one"]`, 1),
		"unknown field":                      strings.Replace(base, `"verdict":"freeze"`, `"verdict":"freeze","rationale":"because"`, 1),
		"wrong schema":                       strings.Replace(base, "design-adjudication/v1", "design-adjudication/v2", 1),
		"review verdict":                     strings.Replace(base, `"verdict":"freeze"`, `"verdict":"block"`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDesignAdjudication([]byte(body), testDesign(), DefaultLimits()); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestAdjudicationRequiresAValidHostIdentity(t *testing.T) {
	_, err := ParseDesignAdjudication([]byte(adjudicationJSON("freeze", testDesignDigest)), DesignIdentity{}, DefaultLimits())
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("adjudication ran without a host design identity: %v", err)
	}
}

// Both schemas must stay inside the JSON-Schema subset Model Runtime accepts,
// and inside what xAI's structured outputs will not reject outright: no
// pattern, no const, no $ref, and never items as an array.
func TestOutputSchemasStayInsideTheSupportedSubset(t *testing.T) {
	allowed := map[string]struct{}{
		"type": {}, "required": {}, "properties": {}, "items": {},
		"additionalProperties": {}, "enum": {}, "description": {},
	}
	var walk func(node any, path string)
	walk = func(node any, path string) {
		switch value := node.(type) {
		case map[string]any:
			for key, child := range value {
				if _, isKeyword := allowed[key]; isKeyword {
					if key == "items" {
						if _, isArray := child.([]any); isArray {
							t.Fatalf("%s.items is an array; xAI rejects that shape", path)
						}
					}
					if key == "enum" {
						variants, _ := child.([]any)
						if len(variants) == 0 {
							t.Fatalf("%s.enum has zero variants", path)
						}
					}
					if key == "properties" {
						if properties, ok := child.(map[string]any); ok {
							for name, sub := range properties {
								walk(sub, path+".properties."+name)
							}
						}
						continue
					}
					walk(child, path+"."+key)
					continue
				}
				t.Fatalf("%s contains unsupported keyword %q", path, key)
			}
		case []any:
			for _, child := range value {
				walk(child, path+"[]")
			}
		}
	}
	for name, schema := range map[string]json.RawMessage{
		"adversarial-review/v1":  AdversarialReviewOutputSchema(),
		"design-adjudication/v1": DesignAdjudicationOutputSchema(),
	} {
		var document any
		if err := json.Unmarshal(schema, &document); err != nil {
			t.Fatalf("%s is not valid JSON: %v", name, err)
		}
		walk(document, name)
	}
}

// The exported accessors must not hand out the package's own buffer.
func TestOutputSchemaAccessorsReturnCopies(t *testing.T) {
	first := AdversarialReviewOutputSchema()
	first[0] = 'X'
	if AdversarialReviewOutputSchema()[0] == 'X' {
		t.Fatal("caller mutated the package schema")
	}
}
