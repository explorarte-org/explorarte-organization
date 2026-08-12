package corpuscuration

import (
	"fmt"
	"sort"
	"strings"
)

// AdjudicationDecisionWork is one candidate Work's adjudication verdict, as
// constrained by the adjudication schema (R10.3 Part B): work_id, decision,
// confidence, criterion_tags, brief_basis. The adjudicator (a reviewer
// model, e.g. DeepSeek V4 Flash) only ever emits a decision for the
// candidate_work_ids it was given -- it never re-derives cluster_id or the
// candidate set itself, same generative-responsibility boundary already
// enforced for the primary curator (see corpuscuration_output_contract.go).
type AdjudicationDecisionWork struct {
	WorkID   string `json:"work_id"`
	Decision string `json:"decision"`
}

// AdjudicationOutput is the raw, already schema-valid JSON an adjudicator
// invocation returned for one cluster's candidate set.
type AdjudicationOutput struct {
	ClusterID string                     `json:"cluster_id"`
	Decisions []AdjudicationDecisionWork `json:"decisions"`
}

// validAdjudicationDecisions is the exact closed set a schema-valid
// adjudication decision must belong to (R10.3 section 15/16).
var validAdjudicationDecisions = map[string]bool{
	"KEEP":    true,
	"DISCARD": true,
	"REVIEW":  true,
}

// AdjudicationOutputContractInvalid is the stable classification code for
// this failure mode, mirroring CurationOutputContractInvalid.
const AdjudicationOutputContractInvalid = "adjudication_output_contract_invalid"

// AdjudicationOutputContractViolation is the structured reason an
// adjudication output failed the semantic domain contract. Mirrors
// OutputContractViolation exactly, adapted to the adjudication vocabulary
// (candidate_work_ids instead of the full cluster Work set, decision
// instead of tier).
type AdjudicationOutputContractViolation struct {
	Classification string `json:"classification"`

	// ClusterIDMismatch is DIAGNOSTIC/INFORMATIONAL ONLY, identical in
	// spirit to the primary curator's contract (R9.1 fix 2): the
	// adjudicator's self-reported cluster_id is never the structural
	// safeguard against a wrong-cluster response -- the exact
	// candidate_work_id set equality check below is, and is unaffected
	// by this field.
	ExpectedClusterID string `json:"expected_cluster_id,omitempty"`
	ActualClusterID   string `json:"actual_cluster_id,omitempty"`
	ClusterIDMismatch bool   `json:"cluster_id_mismatch,omitempty"`

	MissingWorkIDs         []string          `json:"missing_work_ids,omitempty"`
	ExtraWorkIDs           []string          `json:"extra_work_ids,omitempty"`
	DuplicateWorkIDs       []string          `json:"duplicate_work_ids,omitempty"`
	InvalidDecisionWorkIDs map[string]string `json:"invalid_decision_work_ids,omitempty"`
}

func (v *AdjudicationOutputContractViolation) Error() string {
	var reasons []string
	if v.ClusterIDMismatch {
		reasons = append(reasons, fmt.Sprintf("cluster_id mismatch: expected %q, got %q", v.ExpectedClusterID, v.ActualClusterID))
	}
	if len(v.MissingWorkIDs) > 0 {
		reasons = append(reasons, fmt.Sprintf("missing candidate work_ids: %s", strings.Join(v.MissingWorkIDs, ", ")))
	}
	if len(v.ExtraWorkIDs) > 0 {
		reasons = append(reasons, fmt.Sprintf("extra/unknown candidate work_ids: %s", strings.Join(v.ExtraWorkIDs, ", ")))
	}
	if len(v.DuplicateWorkIDs) > 0 {
		reasons = append(reasons, fmt.Sprintf("duplicate candidate work_ids: %s", strings.Join(v.DuplicateWorkIDs, ", ")))
	}
	if len(v.InvalidDecisionWorkIDs) > 0 {
		ids := make([]string, 0, len(v.InvalidDecisionWorkIDs))
		for id := range v.InvalidDecisionWorkIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			d := v.InvalidDecisionWorkIDs[id]
			if d == "" {
				parts = append(parts, fmt.Sprintf("%s (missing decision)", id))
			} else {
				parts = append(parts, fmt.Sprintf("%s (invalid decision %q)", id, d))
			}
		}
		reasons = append(reasons, fmt.Sprintf("invalid decisions: %s", strings.Join(parts, ", ")))
	}
	if len(reasons) == 0 {
		return "adjudication output contract invalid"
	}
	return "adjudication output contract invalid: " + strings.Join(reasons, "; ")
}

// ValidateAdjudicationOutputContract enforces the semantic domain contract
// an adjudication output must satisfy: the set of decided work_ids must
// exactly equal candidateWorkIDs (no silent drops, no unknown extras, no
// duplicates), and every decision must be one of KEEP/DISCARD/REVIEW.
// cluster_id mismatches are recorded but never gate acceptance, mirroring
// ValidateCurationOutputContract's R9.1 fix 2 invariant exactly.
func ValidateAdjudicationOutputContract(expectedClusterID string, candidateWorkIDs []string, output AdjudicationOutput) *AdjudicationOutputContractViolation {
	violation := &AdjudicationOutputContractViolation{Classification: AdjudicationOutputContractInvalid}
	found := false

	if output.ClusterID != expectedClusterID {
		violation.ClusterIDMismatch = true
		violation.ExpectedClusterID = expectedClusterID
		violation.ActualClusterID = output.ClusterID
	}

	expected := make(map[string]bool, len(candidateWorkIDs))
	for _, id := range candidateWorkIDs {
		expected[id] = true
	}

	seenCount := make(map[string]int, len(output.Decisions))
	invalidDecisions := make(map[string]string)
	for _, d := range output.Decisions {
		seenCount[d.WorkID]++
		if !validAdjudicationDecisions[d.Decision] {
			invalidDecisions[d.WorkID] = d.Decision
		}
	}

	var missing []string
	for id := range expected {
		if seenCount[id] == 0 {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		violation.MissingWorkIDs = missing
		found = true
	}

	var extra []string
	var duplicate []string
	for id, count := range seenCount {
		if !expected[id] {
			extra = append(extra, id)
		}
		if count > 1 {
			duplicate = append(duplicate, id)
		}
	}
	sort.Strings(extra)
	sort.Strings(duplicate)
	if len(extra) > 0 {
		violation.ExtraWorkIDs = extra
		found = true
	}
	if len(duplicate) > 0 {
		violation.DuplicateWorkIDs = duplicate
		found = true
	}

	if len(invalidDecisions) > 0 {
		violation.InvalidDecisionWorkIDs = invalidDecisions
		found = true
	}

	if !found {
		return nil
	}
	return violation
}
