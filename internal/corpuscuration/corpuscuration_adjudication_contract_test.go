package corpuscuration

import "testing"

func validAdjudicationOutput() AdjudicationOutput {
	return AdjudicationOutput{
		ClusterID: "cluster-1",
		Decisions: []AdjudicationDecisionWork{
			{WorkID: "w1", Decision: "KEEP"},
			{WorkID: "w2", Decision: "DISCARD"},
			{WorkID: "w3", Decision: "REVIEW"},
		},
	}
}

func candidateWorkIDs() []string {
	return []string{"w1", "w2", "w3"}
}

// 1. exact candidate Work set
func TestValidateAdjudicationOutputContract_ExactCandidateSetAccepted(t *testing.T) {
	violation := ValidateAdjudicationOutputContract("cluster-1", candidateWorkIDs(), validAdjudicationOutput())
	if violation != nil {
		t.Fatalf("expected nil violation for an exact candidate set, got: %v", violation)
	}
}

// 2. missing candidate rejected
func TestValidateAdjudicationOutputContract_MissingCandidateRejected(t *testing.T) {
	output := validAdjudicationOutput()
	output.Decisions = output.Decisions[:2] // drop w3
	violation := ValidateAdjudicationOutputContract("cluster-1", candidateWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected violation for missing candidate work_id")
	}
	if len(violation.MissingWorkIDs) != 1 || violation.MissingWorkIDs[0] != "w3" {
		t.Fatalf("expected missing_work_ids=[w3], got: %v", violation.MissingWorkIDs)
	}
}

// 3. extra candidate rejected
func TestValidateAdjudicationOutputContract_ExtraCandidateRejected(t *testing.T) {
	output := validAdjudicationOutput()
	output.Decisions = append(output.Decisions, AdjudicationDecisionWork{WorkID: "w4", Decision: "KEEP"})
	violation := ValidateAdjudicationOutputContract("cluster-1", candidateWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected violation for extra/unknown candidate work_id")
	}
	if len(violation.ExtraWorkIDs) != 1 || violation.ExtraWorkIDs[0] != "w4" {
		t.Fatalf("expected extra_work_ids=[w4], got: %v", violation.ExtraWorkIDs)
	}
}

// 4. duplicate candidate rejected
func TestValidateAdjudicationOutputContract_DuplicateCandidateRejected(t *testing.T) {
	output := validAdjudicationOutput()
	output.Decisions = append(output.Decisions, AdjudicationDecisionWork{WorkID: "w1", Decision: "REVIEW"})
	violation := ValidateAdjudicationOutputContract("cluster-1", candidateWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected violation for duplicate candidate work_id")
	}
	if len(violation.DuplicateWorkIDs) != 1 || violation.DuplicateWorkIDs[0] != "w1" {
		t.Fatalf("expected duplicate_work_ids=[w1], got: %v", violation.DuplicateWorkIDs)
	}
}

// 5. unknown candidate rejected (candidate_work_ids not part of the
// original cluster at all -- same mechanism as "extra", distinct test per
// the R10.3 spec's enumerated list)
func TestValidateAdjudicationOutputContract_UnknownCandidateRejected(t *testing.T) {
	output := AdjudicationOutput{
		ClusterID: "cluster-1",
		Decisions: []AdjudicationDecisionWork{
			{WorkID: "w1", Decision: "KEEP"},
			{WorkID: "w2", Decision: "DISCARD"},
			{WorkID: "w999-unknown", Decision: "KEEP"},
		},
	}
	violation := ValidateAdjudicationOutputContract("cluster-1", []string{"w1", "w2"}, output)
	if violation == nil {
		t.Fatal("expected violation for unknown candidate work_id")
	}
	if len(violation.ExtraWorkIDs) != 1 || violation.ExtraWorkIDs[0] != "w999-unknown" {
		t.Fatalf("expected extra_work_ids=[w999-unknown], got: %v", violation.ExtraWorkIDs)
	}
}

// 6. invalid decision rejected
func TestValidateAdjudicationOutputContract_InvalidDecisionRejected(t *testing.T) {
	output := validAdjudicationOutput()
	output.Decisions[1].Decision = "MAYBE"
	violation := ValidateAdjudicationOutputContract("cluster-1", candidateWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected violation for invalid decision value")
	}
	if got := violation.InvalidDecisionWorkIDs["w2"]; got != "MAYBE" {
		t.Fatalf("expected invalid_decision_work_ids[w2]=MAYBE, got: %q", got)
	}
}

// 7. cluster_id mismatch non-causal -- mirrors R9.1 fix 2 exactly: a wrong
// cluster_id alone, with an otherwise-exact candidate decision set, must be
// ACCEPTED. The real safeguard is the work_id set equality check.
func TestValidateAdjudicationOutputContract_ClusterIDMismatchAloneIsAcceptedButNoted(t *testing.T) {
	output := validAdjudicationOutput()
	output.ClusterID = "clusterr-1-typo"
	violation := ValidateAdjudicationOutputContract("cluster-1", candidateWorkIDs(), output)
	if violation != nil {
		t.Fatalf("expected acceptance (cluster_id mismatch alone is informational only), got violation: %+v", violation)
	}
}

// Empty decisions rejected (mirrors EmptyWorksRejected on the primary
// contract) -- an empty decision set for a non-empty candidate set is
// always a missing-candidate violation, never a valid "no adjudication
// needed" signal.
func TestValidateAdjudicationOutputContract_EmptyDecisionsRejected(t *testing.T) {
	output := AdjudicationOutput{ClusterID: "cluster-1", Decisions: nil}
	violation := ValidateAdjudicationOutputContract("cluster-1", candidateWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected violation for empty decisions against a non-empty candidate set")
	}
	if len(violation.MissingWorkIDs) != 3 {
		t.Fatalf("expected all 3 candidates missing, got: %v", violation.MissingWorkIDs)
	}
}

// Missing decision value (empty string) rejected -- same as an invalid
// decision, distinct from a missing work_id entirely.
func TestValidateAdjudicationOutputContract_MissingDecisionValueRejected(t *testing.T) {
	output := validAdjudicationOutput()
	output.Decisions[0].Decision = ""
	violation := ValidateAdjudicationOutputContract("cluster-1", candidateWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected violation for empty decision value")
	}
	if got, ok := violation.InvalidDecisionWorkIDs["w1"]; !ok || got != "" {
		t.Fatalf("expected invalid_decision_work_ids[w1]=\"\" (missing decision), got: %q, present=%v", got, ok)
	}
}
