package corpuscuration

import (
	"fmt"
	"sort"
	"strings"
)

// CurationOutputWork is one Work entry inside a raw curator model
// response, as constrained by curation_schema.json (work_id and tier
// are required strings; the schema itself only enforces "string", not
// the closed tier vocabulary -- that gap is exactly what BUG 2/P0-C
// closes here).
//
// Note: the wire-level tier strings a curator model actually emits
// ("P0", "P1", "silver_only", "review_required" -- confirmed against
// the real canary run's tier_counts) are a distinct vocabulary from
// WorkCuration.Tier's stored values (TierP0Core = "P0_core",
// TierP1Strong = "P1_strong", ...). Mapping wire tiers to the stored
// WorkCuration.Tier enum happens at a later persistence stage, out of
// scope for this validator, which only checks the raw output contract.
type CurationOutputWork struct {
	WorkID string `json:"work_id"`
	Tier   string `json:"tier"`
}

// CurationOutput is the raw, already schema-valid JSON a curator model
// invocation returned for one cluster (see curation_schema.json:
// cluster_id, rubric_version, works[], cluster). Only the fields this
// validator needs are represented here.
type CurationOutput struct {
	ClusterID string               `json:"cluster_id"`
	Works     []CurationOutputWork `json:"works"`
}

// validOutputTiers is the exact closed set a schema-valid curation
// output's per-Work tier must belong to (owner decision, BUG 2/P0-C).
var validOutputTiers = map[string]bool{
	"P0":              true,
	"P1":              true,
	"silver_only":     true,
	"review_required": true,
}

// CurationOutputContractInvalid is the stable classification code a
// caller (the driver or any future orchestrator) uses to distinguish
// this failure mode -- syntactically valid JSON that nonetheless
// violates the domain contract -- from provider/transport/schema
// errors, when deciding bounded-retry vs terminal-failure.
const CurationOutputContractInvalid = "curation_output_contract_invalid"

// OutputContractViolation is the structured, typed reason a curation
// output failed the semantic domain contract that JSON-schema
// validation alone cannot express (BUG 2/P0-C: a schema-valid response
// silently dropped a Work and was still marked "succeeded"). Every
// populated field is a distinct, independently-detected failure mode;
// more than one can be set at once (e.g. wrong cluster_id AND missing
// works).
type OutputContractViolation struct {
	Classification string `json:"classification"`

	// ExpectedClusterID/ActualClusterID/ClusterIDMismatch are
	// DIAGNOSTIC/INFORMATIONAL ONLY (R9.1 fix 2: "cluster_id removed
	// from generative responsibility"). R9 measured DeepSeek echoing
	// cluster_id back with minor typos (missing/extra characters, e.g.
	// "cluster-..." instead of "scluster-...") in ~27% of clusters --
	// a string-copying reliability defect, not evidence the model
	// processed the wrong cluster. The real structural safeguard
	// against a wrong-cluster response is the exact work_id set
	// equality check below, which is unaffected by this field no
	// longer gating acceptance -- if the model actually described a
	// different cluster's Works, that check catches it independently.
	// A mismatch here is recorded for audit/telemetry but never causes
	// found=true / a rejection on its own.
	ExpectedClusterID string `json:"expected_cluster_id,omitempty"`
	ActualClusterID   string `json:"actual_cluster_id,omitempty"`
	ClusterIDMismatch bool   `json:"cluster_id_mismatch,omitempty"`

	// MissingWorkIDs: expected but absent from output.works entirely.
	MissingWorkIDs []string `json:"missing_work_ids,omitempty"`
	// ExtraWorkIDs: present in output.works but not in the expected set
	// (unknown work_id the caller never asked this cluster to tier).
	ExtraWorkIDs []string `json:"extra_work_ids,omitempty"`
	// DuplicateWorkIDs: the same work_id appears more than once in
	// output.works.
	DuplicateWorkIDs []string `json:"duplicate_work_ids,omitempty"`
	// InvalidTierWorkIDs maps work_id -> the tier value it carried, for
	// every Work whose tier is empty or outside validOutputTiers.
	InvalidTierWorkIDs map[string]string `json:"invalid_tier_work_ids,omitempty"`
}

func (v *OutputContractViolation) Error() string {
	var reasons []string
	if v.ClusterIDMismatch {
		reasons = append(reasons, fmt.Sprintf("cluster_id mismatch: expected %q, got %q", v.ExpectedClusterID, v.ActualClusterID))
	}
	if len(v.MissingWorkIDs) > 0 {
		reasons = append(reasons, fmt.Sprintf("missing work_ids: %s", strings.Join(v.MissingWorkIDs, ", ")))
	}
	if len(v.ExtraWorkIDs) > 0 {
		reasons = append(reasons, fmt.Sprintf("extra/unknown work_ids: %s", strings.Join(v.ExtraWorkIDs, ", ")))
	}
	if len(v.DuplicateWorkIDs) > 0 {
		reasons = append(reasons, fmt.Sprintf("duplicate work_ids: %s", strings.Join(v.DuplicateWorkIDs, ", ")))
	}
	if len(v.InvalidTierWorkIDs) > 0 {
		ids := make([]string, 0, len(v.InvalidTierWorkIDs))
		for id := range v.InvalidTierWorkIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			tier := v.InvalidTierWorkIDs[id]
			if tier == "" {
				parts = append(parts, fmt.Sprintf("%s (missing tier)", id))
			} else {
				parts = append(parts, fmt.Sprintf("%s (invalid tier %q)", id, tier))
			}
		}
		reasons = append(reasons, fmt.Sprintf("invalid tiers: %s", strings.Join(parts, ", ")))
	}
	if len(reasons) == 0 {
		return "curation output contract invalid"
	}
	return "curation output contract invalid: " + strings.Join(reasons, "; ")
}

// ValidateCurationOutputContract enforces the semantic domain contract
// a schema-valid curation output must additionally satisfy (BUG
// 2/P0-C): the echoed cluster_id must match what was requested, the
// set of tiered work_ids must exactly equal the set of Works actually
// sent (no silent drops, no unknown extras, no duplicates), and every
// Work must carry a tier from the closed set. Returns nil when the
// output satisfies the contract, or a non-nil *OutputContractViolation
// (whose Classification is always CurationOutputContractInvalid)
// describing every violation found.
func ValidateCurationOutputContract(expectedClusterID string, expectedWorkIDs []string, output CurationOutput) *OutputContractViolation {
	violation := &OutputContractViolation{Classification: CurationOutputContractInvalid}
	found := false

	if output.ClusterID != expectedClusterID {
		// Informational only -- does not set found=true (R9.1 fix 2).
		violation.ClusterIDMismatch = true
		violation.ExpectedClusterID = expectedClusterID
		violation.ActualClusterID = output.ClusterID
	}

	expected := make(map[string]bool, len(expectedWorkIDs))
	for _, id := range expectedWorkIDs {
		expected[id] = true
	}

	seenCount := make(map[string]int, len(output.Works))
	invalidTiers := make(map[string]string)
	for _, w := range output.Works {
		seenCount[w.WorkID]++
		if !validOutputTiers[w.Tier] {
			invalidTiers[w.WorkID] = w.Tier
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

	if len(invalidTiers) > 0 {
		violation.InvalidTierWorkIDs = invalidTiers
		found = true
	}

	if !found {
		return nil
	}
	return violation
}
