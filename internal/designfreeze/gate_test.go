package designfreeze

import "testing"

const (
	digestA = "1111111111111111111111111111111111111111111111111111111111111111"
	digestB = "2222222222222222222222222222222222222222222222222222222222222222"
	resultD = "3333333333333333333333333333333333333333333333333333333333333333"
	resultE = "4444444444444444444444444444444444444444444444444444444444444444"
)

func designA() Design {
	return Design{ID: "m2-1-context-memory", Version: "v1", Digest: digestA}
}

func fullFreeze() Request {
	return Request{
		Design:             designA(),
		Review:             ExecutionRef{TaskID: 10, AttemptID: 1, InvocationID: 100, ResultDigest: resultD, Verdict: "revise"},
		ReviewDesign:       designA(),
		Adjudication:       ExecutionRef{TaskID: 11, AttemptID: 1, InvocationID: 101, ResultDigest: resultE, Verdict: "freeze"},
		AdjudicationDesign: designA(),
	}
}

func TestFreezeRequiresEveryBinding(t *testing.T) {
	decision := Evaluate(fullFreeze())
	if !decision.Satisfied || decision.ReasonCode != ReasonSatisfied {
		t.Fatalf("complete binding did not satisfy: %+v", decision)
	}
	if decision.Record.Digest == "" || decision.Record.SchemaVersion != RecordSchemaVersion {
		t.Fatalf("record incomplete: %+v", decision.Record)
	}
}

// A candidate design on its own is the starting state, not an authorization.
func TestCandidateDesignAloneCannotFreeze(t *testing.T) {
	decision := Evaluate(Request{Design: designA()})
	if decision.Satisfied {
		t.Fatal("a design with no review and no adjudication froze itself")
	}
	if decision.ReasonCode != ReasonReviewMissing {
		t.Fatalf("reason=%q", decision.ReasonCode)
	}
}

// The reviewer is the loudest voice in the flow and has none of the authority.
// These two cases are the ones a careless implementation gets wrong.
func TestAdversarialReviewAloneCannotFreeze(t *testing.T) {
	request := fullFreeze()
	request.Adjudication = ExecutionRef{}
	request.AdjudicationDesign = Design{}
	decision := Evaluate(request)
	if decision.Satisfied {
		t.Fatal("a review with no adjudication froze the design")
	}
	if decision.ReasonCode != ReasonAdjudicationMissing {
		t.Fatalf("reason=%q", decision.ReasonCode)
	}
}

func TestAdversarialReviewAcceptCannotFreeze(t *testing.T) {
	request := fullFreeze()
	request.Review.Verdict = "accept"
	request.Adjudication = ExecutionRef{}
	request.AdjudicationDesign = Design{}
	decision := Evaluate(request)
	if decision.Satisfied {
		t.Fatal("review verdict accept was treated as an approval")
	}
}

// Nor does a hostile review hold a veto: with a freeze adjudication over it,
// even a "block" review still freezes. The reviewer informs; it does not rule.
func TestAdversarialReviewBlockDoesNotVetoAFreezeAdjudication(t *testing.T) {
	request := fullFreeze()
	request.Review.Verdict = "block"
	if decision := Evaluate(request); !decision.Satisfied {
		t.Fatalf("block review vetoed the executive decision: %+v", decision)
	}
}

func TestOnlyFreezeVerdictSatisfies(t *testing.T) {
	for _, verdict := range []string{"revise", "reject", "accept", "", "FREEZE", "freeze "} {
		request := fullFreeze()
		request.Adjudication.Verdict = verdict
		decision := Evaluate(request)
		if decision.Satisfied {
			t.Fatalf("adjudication verdict %q froze the design", verdict)
		}
	}
}

func TestFreezeRefusesCrossDesignBindings(t *testing.T) {
	other := Design{ID: "m2-1-context-memory", Version: "v2", Digest: digestB}

	// A review of a different design cannot be borrowed.
	request := fullFreeze()
	request.ReviewDesign = other
	if decision := Evaluate(request); decision.Satisfied || decision.ReasonCode != ReasonReviewDesignMismatch {
		t.Fatalf("borrowed review: %+v", decision)
	}

	// Nor can an adjudication of a different design.
	request = fullFreeze()
	request.AdjudicationDesign = other
	if decision := Evaluate(request); decision.Satisfied || decision.ReasonCode != ReasonAdjudicationDesignMismatch {
		t.Fatalf("borrowed adjudication: %+v", decision)
	}

	// Same digest, different declared version is still a different artifact.
	request = fullFreeze()
	request.AdjudicationDesign = Design{ID: designA().ID, Version: "v2", Digest: digestA}
	if decision := Evaluate(request); decision.Satisfied {
		t.Fatal("version drift under an identical digest was accepted")
	}
}

func TestFreezeRejectsMalformedIdentityAndRefs(t *testing.T) {
	cases := map[string]func(*Request){
		"empty design id":         func(r *Request) { r.Design.ID = "" },
		"empty version":           func(r *Request) { r.Design.Version = "" },
		"short digest":            func(r *Request) { r.Design.Digest = "abc" },
		"uppercase digest":        func(r *Request) { r.Design.Digest = "AAAA111111111111111111111111111111111111111111111111111111111111" },
		"review no task":          func(r *Request) { r.Review.TaskID = 0 },
		"review no invocation":    func(r *Request) { r.Review.InvocationID = 0 },
		"review no result":        func(r *Request) { r.Review.ResultDigest = "" },
		"unknown review verdict":  func(r *Request) { r.Review.Verdict = "approved" },
		"adjudication no attempt": func(r *Request) { r.Adjudication.AttemptID = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := fullFreeze()
			mutate(&request)
			if decision := Evaluate(request); decision.Satisfied {
				t.Fatalf("%s satisfied the gate", name)
			}
		})
	}
}

// The property the whole gate exists for: a freeze is about bytes, not about a
// name. Revising the design starts over from unfrozen.
func TestStoredFreezeDoesNotCarryToARevisedDesign(t *testing.T) {
	record := Evaluate(fullFreeze()).Record
	if !Satisfies(record, designA()) {
		t.Fatal("freeze did not authorize its own design")
	}
	revised := Design{ID: designA().ID, Version: "v2", Digest: digestB}
	if Satisfies(record, revised) {
		t.Fatal("freeze of v1 silently authorized v2")
	}
	sameLabelNewBytes := Design{ID: designA().ID, Version: "v1", Digest: digestB}
	if Satisfies(record, sameLabelNewBytes) {
		t.Fatal("freeze carried over to different bytes under the same label")
	}
}

// A stored record that was edited after the fact stops verifying. Without
// this, "frozen" would be a row anyone could write rather than a decision
// anyone can recompute.
func TestTamperedFreezeRecordStopsVerifying(t *testing.T) {
	record := Evaluate(fullFreeze()).Record

	tampered := record
	tampered.Adjudication.Verdict = "freeze"
	tampered.Design.Digest = digestB
	if Satisfies(tampered, Design{ID: designA().ID, Version: "v1", Digest: digestB}) {
		t.Fatal("a rewritten digest still verified")
	}

	tampered = record
	tampered.Review.InvocationID = 999
	if Satisfies(tampered, designA()) {
		t.Fatal("a rewritten review reference still verified")
	}

	forged := Record{SchemaVersion: RecordSchemaVersion, Design: designA(),
		Adjudication: ExecutionRef{TaskID: 1, AttemptID: 1, InvocationID: 1, ResultDigest: resultE, Verdict: "freeze"}}
	if Satisfies(forged, designA()) {
		t.Fatal("a hand-built record with no digest verified")
	}
}

func TestEvidencePayloadIsStable(t *testing.T) {
	first, err := EvidencePayload(Evaluate(fullFreeze()).Record)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EvidencePayload(Evaluate(fullFreeze()).Record)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("evidence payload is not deterministic:\n%s\n%s", first, second)
	}
}
