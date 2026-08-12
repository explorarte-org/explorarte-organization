package corpuscuration

import "testing"

func validOutput() CurationOutput {
	return CurationOutput{
		ClusterID: "cluster-1",
		Works: []CurationOutputWork{
			{WorkID: "w1", Tier: "P0"},
			{WorkID: "w2", Tier: "P1"},
			{WorkID: "w3", Tier: "silver_only"},
		},
	}
}

func expectedWorkIDs() []string {
	return []string{"w1", "w2", "w3"}
}

func TestValidateCurationOutputContract_ExactValidOutputAccepted(t *testing.T) {
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), validOutput())
	if violation != nil {
		t.Fatalf("expected nil violation for a valid output, got: %v", violation)
	}
}

func TestValidateCurationOutputContract_MissingWorkRejected(t *testing.T) {
	output := validOutput()
	output.Works = output.Works[:2] // drop w3, mirrors the real BUG 2 case (8 sent, 7 tiered)
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected a violation when a Work silently vanishes from the output")
	}
	if violation.Classification != CurationOutputContractInvalid {
		t.Fatalf("classification = %q, want %q", violation.Classification, CurationOutputContractInvalid)
	}
	if len(violation.MissingWorkIDs) != 1 || violation.MissingWorkIDs[0] != "w3" {
		t.Fatalf("missing_work_ids = %v, want [w3]", violation.MissingWorkIDs)
	}
	if len(violation.ExtraWorkIDs) != 0 || len(violation.DuplicateWorkIDs) != 0 || len(violation.InvalidTierWorkIDs) != 0 || violation.ClusterIDMismatch {
		t.Fatalf("unexpected additional violation fields set: %+v", violation)
	}
}

func TestValidateCurationOutputContract_ExtraWorkRejected(t *testing.T) {
	output := validOutput()
	output.Works = append(output.Works, CurationOutputWork{WorkID: "w4", Tier: "P1"})
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected a violation when the output contains a Work beyond the requested set")
	}
	if len(violation.ExtraWorkIDs) != 1 || violation.ExtraWorkIDs[0] != "w4" {
		t.Fatalf("extra_work_ids = %v, want [w4]", violation.ExtraWorkIDs)
	}
}

func TestValidateCurationOutputContract_DuplicateWorkRejected(t *testing.T) {
	output := validOutput()
	output.Works = append(output.Works, CurationOutputWork{WorkID: "w1", Tier: "P1"})
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected a violation when a work_id is tiered more than once")
	}
	if len(violation.DuplicateWorkIDs) != 1 || violation.DuplicateWorkIDs[0] != "w1" {
		t.Fatalf("duplicate_work_ids = %v, want [w1]", violation.DuplicateWorkIDs)
	}
}

func TestValidateCurationOutputContract_UnknownWorkRejected(t *testing.T) {
	output := CurationOutput{
		ClusterID: "cluster-1",
		Works: []CurationOutputWork{
			{WorkID: "w1", Tier: "P0"},
			{WorkID: "w2", Tier: "P1"},
			{WorkID: "unknown-work-999", Tier: "P1"}, // never sent to the model at all
		},
	}
	violation := ValidateCurationOutputContract("cluster-1", []string{"w1", "w2"}, output)
	if violation == nil {
		t.Fatal("expected a violation when the output tiers a work_id that was never part of the cluster sent")
	}
	if len(violation.ExtraWorkIDs) != 1 || violation.ExtraWorkIDs[0] != "unknown-work-999" {
		t.Fatalf("extra_work_ids = %v, want [unknown-work-999]", violation.ExtraWorkIDs)
	}
	if len(violation.MissingWorkIDs) != 0 {
		t.Fatalf("missing_work_ids = %v, want none", violation.MissingWorkIDs)
	}
}

// R9.1 fix 2 ("cluster_id removed from generative responsibility"): a
// mismatched echoed cluster_id alone must NOT reject an otherwise-exact
// output. R9 measured DeepSeek mistyping this string in ~27% of
// clusters (e.g. dropping the leading "s" of "scluster-...") while
// still returning the fully correct Work set for the cluster it was
// actually given -- rejecting those wasted real provider spend for a
// defect the work_id set-equality check below already guards against
// structurally (a genuinely wrong-cluster response would also fail
// work_id equality). This test replaces
// TestValidateCurationOutputContract_WrongClusterIDRejected.
func TestValidateCurationOutputContract_ClusterIDMismatchAloneIsAcceptedButNoted(t *testing.T) {
	output := validOutput()
	output.ClusterID = "cluster-999"
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), output)
	if violation != nil {
		t.Fatalf("expected acceptance (cluster_id mismatch alone is informational only), got violation: %+v", violation)
	}
}

// A genuinely wrong-cluster response -- different cluster_id AND a
// Work set that does not match what was requested -- must still be
// rejected, via the independent work_id equality check.
func TestValidateCurationOutputContract_WrongClusterAndWrongWorkSetRejected(t *testing.T) {
	output := validOutput()
	output.ClusterID = "cluster-999"
	output.Works = []CurationOutputWork{{WorkID: "work-999", Tier: "P0"}}
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected a violation when the work_id set does not match, independent of cluster_id")
	}
	if len(violation.MissingWorkIDs) == 0 || len(violation.ExtraWorkIDs) == 0 {
		t.Fatalf("expected missing+extra work_id violations: %+v", violation)
	}
}

func TestValidateCurationOutputContract_EmptyWorksRejected(t *testing.T) {
	output := CurationOutput{ClusterID: "cluster-1", Works: nil}
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected a violation when the output has no works at all despite works being expected")
	}
	if len(violation.MissingWorkIDs) != 3 {
		t.Fatalf("missing_work_ids = %v, want all 3 expected work_ids", violation.MissingWorkIDs)
	}
}

func TestValidateCurationOutputContract_InvalidTierRejected(t *testing.T) {
	output := validOutput()
	output.Works[0].Tier = "P2" // outside the closed set
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected a violation when a Work's tier is outside {P0, P1, silver_only, review_required}")
	}
	if got, ok := violation.InvalidTierWorkIDs["w1"]; !ok || got != "P2" {
		t.Fatalf("invalid_tier_work_ids = %v, want w1 -> P2", violation.InvalidTierWorkIDs)
	}
}

func TestValidateCurationOutputContract_MissingTierRejected(t *testing.T) {
	output := validOutput()
	output.Works[1].Tier = ""
	violation := ValidateCurationOutputContract("cluster-1", expectedWorkIDs(), output)
	if violation == nil {
		t.Fatal("expected a violation when a Work is missing a tier entirely")
	}
	if got, ok := violation.InvalidTierWorkIDs["w2"]; !ok || got != "" {
		t.Fatalf("invalid_tier_work_ids = %v, want w2 -> \"\"", violation.InvalidTierWorkIDs)
	}
}
