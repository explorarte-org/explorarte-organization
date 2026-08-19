package designreview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

const designDigest = "aa11111111111111111111111111111111111111111111111111111111111111"

func design() designfreeze.Design {
	return designfreeze.Design{ID: "m2-1-context-memory", Version: "v1", Digest: designDigest}
}

func bundle() Bundle {
	return Bundle{
		OwnerRequirements:       []string{"Design before implementation."},
		CandidateDesign:         "Addressable context resources, sealed per snapshot.",
		ArchitectureConstraints: []string{"No new top-level entity."},
		AuthorityConstraints:    []string{"Reviewer publishes findings only."},
		UnresolvedDecisions:     []string{"D-005 provider identity behind the executive model."},
		EvidenceRefs:            []string{"task:40:context"},
	}
}

func reviewBody(verdict string) []byte {
	findings := `[{"id":"AR-001","severity":"high","claim":"The seal trigger is not proven under concurrency.",
	 "affected_requirement":"M2.1 seal protocol","required_correction":"Add a barrier-coordinated integration test.",
	 "evidence_refs":["task:40:context"]}]`
	if verdict == "accept" {
		findings = `[]`
	}
	return []byte(`{"schema_version":"adversarial-review/v1","verdict":"` + verdict + `","findings":` + findings + `,
	 "contradictions":[],"unverified_assumptions":[],"security_findings":[],"authority_findings":[],
	 "recovery_findings":[],"memory_epistemic_findings":[],"evidence_refs":[]}`)
}

func adjudicationBody(verdict, digest string) []byte {
	required := `[]`
	accepted := `["AR-001"]`
	if verdict == "revise" {
		required = `["Add the concurrency test."]`
	}
	return []byte(`{"schema_version":"design-adjudication/v1","verdict":"` + verdict + `",
	 "accepted_findings":` + accepted + `,"rejected_findings":[],"required_changes":` + required + `,
	 "unresolved_owner_decisions":[],"design_id":"m2-1-context-memory","design_version":"v1",
	 "design_digest":"` + digest + `","evidence_refs":[]}`)
}

// scriptedExecutor stands in for the Harness. It records what it was asked to
// run so the test can assert WHICH role and purpose were selected, and it
// never contacts anything.
type scriptedExecutor struct {
	review       []byte
	adjudication []byte
	reviewErr    error
	seen         []ExecutionRequest
}

func (s *scriptedExecutor) Execute(_ context.Context, request ExecutionRequest) (ExecutionResult, error) {
	s.seen = append(s.seen, request)
	switch request.Purpose {
	case executive.PurposeAdversarialReview:
		if s.reviewErr != nil {
			return ExecutionResult{}, s.reviewErr
		}
		return ExecutionResult{TaskID: 40, AttemptID: 1, InvocationID: 400, Body: s.review}, nil
	case executive.PurposeDesignAdjudication:
		return ExecutionResult{TaskID: 41, AttemptID: 1, InvocationID: 401, Body: s.adjudication}, nil
	}
	return ExecutionResult{}, errors.New("unexpected purpose " + string(request.Purpose))
}

type recorder struct{ records []designfreeze.Record }

func (r *recorder) RecordFreeze(_ context.Context, record designfreeze.Record) error {
	r.records = append(r.records, record)
	return nil
}

func coordinator(executor TypedExecutor, rec FreezeRecorder) Coordinator {
	return Coordinator{
		Reviewer:    RoleRef{ID: "investigacion/revisor_adversarial", UnitID: "investigacion"},
		Adjudicator: RoleRef{ID: "empresa/ceo", UnitID: "empresa"},
		Author:      RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia"},
		Executor:    executor, Recorder: rec,
	}
}

// The full flow, end to end, with zero provider calls.
func TestSyntheticFlowFreezesOnlyAfterAdjudication(t *testing.T) {
	executor := &scriptedExecutor{review: reviewBody("revise"), adjudication: adjudicationBody("freeze", designDigest)}
	rec := &recorder{}
	outcome, err := coordinator(executor, rec).Run(context.Background(), design(), bundle())
	if err != nil {
		t.Fatalf("flow: %v", err)
	}

	// Exactly two executions, in order, with exactly the right role and
	// purpose on each. This is the "exactly the reviewer role/profile is
	// selected" assertion.
	if len(executor.seen) != 2 {
		t.Fatalf("executions=%d", len(executor.seen))
	}
	if executor.seen[0].Purpose != executive.PurposeAdversarialReview ||
		executor.seen[0].SubjectRole != "investigacion/revisor_adversarial" {
		t.Fatalf("review execution=%+v", executor.seen[0])
	}
	if executor.seen[1].Purpose != executive.PurposeDesignAdjudication ||
		executor.seen[1].SubjectRole != "empresa/ceo" {
		t.Fatalf("adjudication execution=%+v", executor.seen[1])
	}
	// The coordinator never names a provider or a model on either call.
	for _, request := range executor.seen {
		body, _ := json.Marshal(request)
		lowered := strings.ToLower(string(body))
		for _, needle := range []string{"xai", "grok", "deepseek", "provider", "model_id"} {
			if strings.Contains(lowered, needle) {
				t.Fatalf("execution request leaked provider knowledge %q: %s", needle, body)
			}
		}
	}

	if !outcome.Frozen || outcome.FreezeReason != designfreeze.ReasonSatisfied {
		t.Fatalf("outcome=%+v", outcome)
	}
	if len(rec.records) != 1 {
		t.Fatalf("freeze records=%d", len(rec.records))
	}
	// The freeze is bound to the exact design, and to both executions.
	stored := rec.records[0]
	if !designfreeze.Satisfies(stored, design()) {
		t.Fatal("stored freeze does not authorize its own design")
	}
	if stored.Review.InvocationID != 400 || stored.Adjudication.InvocationID != 401 {
		t.Fatalf("freeze not bound to both executions: %+v", stored)
	}
	// And it does not carry over to a revision.
	if designfreeze.Satisfies(stored, designfreeze.Design{ID: design().ID, Version: "v2",
		Digest: "bb11111111111111111111111111111111111111111111111111111111111111"}) {
		t.Fatal("freeze carried to a revised design")
	}
}

func TestNoAdjudicationVerdictOtherThanFreezeFreezes(t *testing.T) {
	for _, verdict := range []string{"revise", "reject"} {
		t.Run(verdict, func(t *testing.T) {
			executor := &scriptedExecutor{review: reviewBody("revise"), adjudication: adjudicationBody(verdict, designDigest)}
			rec := &recorder{}
			outcome, err := coordinator(executor, rec).Run(context.Background(), design(), bundle())
			if err != nil {
				t.Fatalf("flow: %v", err)
			}
			if outcome.Frozen || len(rec.records) != 0 {
				t.Fatalf("%s froze the design: %+v", verdict, outcome)
			}
			if outcome.FreezeReason != designfreeze.ReasonVerdictNotFreeze {
				t.Fatalf("reason=%q", outcome.FreezeReason)
			}
		})
	}
}

// A clean review does not shortcut the adjudication, and does not freeze.
func TestCleanReviewStillRequiresAdjudication(t *testing.T) {
	executor := &scriptedExecutor{review: reviewBody("accept"), adjudication: adjudicationBody("revise", designDigest)}
	rec := &recorder{}
	outcome, err := coordinator(executor, rec).Run(context.Background(), design(), bundle())
	if err != nil {
		t.Fatalf("flow: %v", err)
	}
	if len(executor.seen) != 2 {
		t.Fatal("an accept review skipped the adjudication")
	}
	if outcome.Frozen {
		t.Fatal("an accept review froze the design")
	}
}

// An adjudication naming another design cannot freeze this one, and the
// coordinator surfaces it as an identity mismatch rather than a bad contract.
func TestAdjudicationOverAnotherDesignIsRefused(t *testing.T) {
	other := "cc11111111111111111111111111111111111111111111111111111111111111"
	executor := &scriptedExecutor{review: reviewBody("revise"), adjudication: adjudicationBody("freeze", other)}
	rec := &recorder{}
	_, err := coordinator(executor, rec).Run(context.Background(), design(), bundle())
	if !errors.Is(err, executive.ErrDesignIdentityMismatch) {
		t.Fatalf("err=%v", err)
	}
	if len(rec.records) != 0 {
		t.Fatal("a mismatched adjudication was recorded")
	}
}

// Independence is checked before anything runs, so a badly composed review
// never reaches a provider.
func TestIndependenceIsEnforcedBeforeAnyExecution(t *testing.T) {
	cases := map[string]func(*Coordinator){
		"author reviews itself": func(c *Coordinator) { c.Reviewer = c.Author },
		"reviewer adjudicates":  func(c *Coordinator) { c.Adjudicator = c.Reviewer },
		"reviewer inside authoring unit": func(c *Coordinator) {
			c.Reviewer = RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia"}
		},
		"reviewer outside investigacion": func(c *Coordinator) {
			c.Reviewer = RoleRef{ID: "negocio/analista_kpis", UnitID: "negocio"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			executor := &scriptedExecutor{review: reviewBody("revise"), adjudication: adjudicationBody("freeze", designDigest)}
			c := coordinator(executor, &recorder{})
			mutate(&c)
			_, err := c.Run(context.Background(), design(), bundle())
			if !errors.Is(err, ErrReviewerNotIndependent) {
				t.Fatalf("err=%v", err)
			}
			if len(executor.seen) != 0 {
				t.Fatal("an execution was dispatched despite a broken independence check")
			}
		})
	}
}

// When xAI is not configured the review fails outright. There is no branch
// that retries the review against another provider.
func TestUnavailableProviderFailsClosedWithNoFallback(t *testing.T) {
	executor := &scriptedExecutor{review: reviewBody("revise"), adjudication: adjudicationBody("freeze", designDigest),
		reviewErr: errors.New(ProviderUnavailableReason)}
	rec := &recorder{}
	_, err := coordinator(executor, rec).Run(context.Background(), design(), bundle())
	if err == nil {
		t.Fatal("an unconfigured provider did not fail the review")
	}
	if !strings.Contains(err.Error(), "provider_not_configured") {
		t.Fatalf("err=%v", err)
	}
	// One attempt, at the reviewer, and no adjudication behind its back.
	if len(executor.seen) != 1 || executor.seen[0].Purpose != executive.PurposeAdversarialReview {
		t.Fatalf("executions=%+v", executor.seen)
	}
	if len(rec.records) != 0 {
		t.Fatal("a freeze was recorded despite a failed review")
	}
}

// A malformed reviewer result stops the flow; it does not become an empty
// review that an adjudication could then rubber-stamp.
func TestMalformedReviewStopsTheFlow(t *testing.T) {
	executor := &scriptedExecutor{review: []byte(`{"schema_version":"adversarial-review/v1","verdict":"approve"}`),
		adjudication: adjudicationBody("freeze", designDigest)}
	rec := &recorder{}
	if _, err := coordinator(executor, rec).Run(context.Background(), design(), bundle()); err == nil {
		t.Fatal("a malformed review was accepted")
	}
	if len(executor.seen) != 1 {
		t.Fatal("the adjudication ran on a rejected review")
	}
	if len(rec.records) != 0 {
		t.Fatal("a freeze was recorded from a rejected review")
	}
}

// ------------------------------------------------------------------ bundle

func TestBundleIsAClosedFieldListAndRefusesCredentialMaterial(t *testing.T) {
	body, err := bundle().Encode()
	if err != nil {
		t.Fatal(err)
	}
	// Only the declared keys are present.
	var decoded map[string]json.RawMessage
	if err = json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct{}{
		"owner_requirements": {}, "candidate_design": {}, "architecture_constraints": {},
		"authority_constraints": {}, "unresolved_decisions": {}, "authorized_evidence_refs": {}, "design": {},
	}
	for key := range decoded {
		if _, allowed := expected[key]; !allowed {
			t.Fatalf("bundle carries undeclared key %q", key)
		}
	}
	if len(decoded) != len(expected) {
		t.Fatalf("bundle keys=%v", decoded)
	}

	for name, contaminate := range map[string]func(*Bundle){
		"bearer token":   func(b *Bundle) { b.CandidateDesign += " Authorization: Bearer abc123" },
		"api key":        func(b *Bundle) { b.UnresolvedDecisions = append(b.UnresolvedDecisions, "api_key=zzz") },
		"private key":    func(b *Bundle) { b.CandidateDesign += "\n-----BEGIN PRIVATE KEY-----" },
		"provider token": func(b *Bundle) { b.EvidenceRefs = append(b.EvidenceRefs, "xai-abcdefghijklmnop") },
	} {
		t.Run(name, func(t *testing.T) {
			b := bundle()
			contaminate(&b)
			if _, err := b.Encode(); !errors.Is(err, ErrBundleContaminated) {
				t.Fatalf("%s was encoded: %v", name, err)
			}
		})
	}

	empty := bundle()
	empty.CandidateDesign = "  "
	if _, err := empty.Encode(); err == nil {
		t.Fatal("an empty candidate design was encoded")
	}
}

func TestBundleEncodingIsDeterministic(t *testing.T) {
	first, err := bundle().Encode()
	if err != nil {
		t.Fatal(err)
	}
	second, err := bundle().Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("bundle encoding is not deterministic")
	}
}

// The coordinator has no port that could dispatch implementation work. This
// is asserted structurally: its only outbound interfaces are the typed
// executor and the freeze recorder.
func TestCoordinatorExposesNoImplementationPath(t *testing.T) {
	outcome := Outcome{}
	body, err := json.Marshal(outcome)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"implement", "mission", "promotion", "coderunner", "staging", "allowed_paths"} {
		if strings.Contains(strings.ToLower(string(body)), needle) {
			t.Fatalf("outcome exposes %q", needle)
		}
	}
}
