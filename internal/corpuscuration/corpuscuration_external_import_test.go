package corpuscuration

import "testing"

func validExternalEntry() ExternalResultBatchEntry {
	return ExternalResultBatchEntry{
		ClusterID: "cluster-r105-1", ExpectedWorkIDs: []string{"w1", "w2"}, TerminalValid: true,
		Output: CurationOutput{
			ClusterID: "cluster-r105-1",
			Works: []CurationOutputWork{
				{WorkID: "w1", Tier: "P0"},
				{WorkID: "w2", Tier: "silver_only"},
			},
		},
	}
}

func TestValidateExternalResultBatch_ValidEntryAccepted(t *testing.T) {
	outcomes := ValidateExternalResultBatch([]ExternalResultBatchEntry{validExternalEntry()})
	if len(outcomes) != 1 || !outcomes[0].Accepted {
		t.Fatalf("expected 1 accepted outcome, got: %+v", outcomes)
	}
}

func TestValidateExternalResultBatch_MalformedNonTerminalRejected(t *testing.T) {
	entry := validExternalEntry()
	entry.TerminalValid = false
	outcomes := ValidateExternalResultBatch([]ExternalResultBatchEntry{entry})
	if outcomes[0].Accepted {
		t.Fatal("expected a driver-reported non-terminal-valid entry to be rejected without re-validation")
	}
	if outcomes[0].Reason != "driver_reported_non_terminal_valid" {
		t.Fatalf("expected reason driver_reported_non_terminal_valid, got %q", outcomes[0].Reason)
	}
}

func TestValidateExternalResultBatch_UnknownWorkRejected(t *testing.T) {
	entry := validExternalEntry()
	entry.Output.Works = append(entry.Output.Works, CurationOutputWork{WorkID: "w999-unknown", Tier: "P1"})
	outcomes := ValidateExternalResultBatch([]ExternalResultBatchEntry{entry})
	if outcomes[0].Accepted {
		t.Fatal("expected rejection for unknown work_id")
	}
	if outcomes[0].Violation == nil || len(outcomes[0].Violation.ExtraWorkIDs) != 1 {
		t.Fatalf("expected extra_work_ids violation, got: %+v", outcomes[0].Violation)
	}
}

func TestValidateExternalResultBatch_DuplicateWorkRejected(t *testing.T) {
	entry := validExternalEntry()
	entry.Output.Works = append(entry.Output.Works, CurationOutputWork{WorkID: "w1", Tier: "P1"})
	outcomes := ValidateExternalResultBatch([]ExternalResultBatchEntry{entry})
	if outcomes[0].Accepted {
		t.Fatal("expected rejection for duplicate work_id")
	}
	if outcomes[0].Violation == nil || len(outcomes[0].Violation.DuplicateWorkIDs) != 1 {
		t.Fatalf("expected duplicate_work_ids violation, got: %+v", outcomes[0].Violation)
	}
}

func TestValidateExternalResultBatch_MissingWorkRejected(t *testing.T) {
	entry := validExternalEntry()
	entry.Output.Works = entry.Output.Works[:1]
	outcomes := ValidateExternalResultBatch([]ExternalResultBatchEntry{entry})
	if outcomes[0].Accepted {
		t.Fatal("expected rejection for missing work_id")
	}
	if outcomes[0].Violation == nil || len(outcomes[0].Violation.MissingWorkIDs) != 1 {
		t.Fatalf("expected missing_work_ids violation, got: %+v", outcomes[0].Violation)
	}
}

func TestValidateExternalResultBatch_MalformedTierRejected(t *testing.T) {
	entry := validExternalEntry()
	entry.Output.Works[0].Tier = "definitely_not_a_real_tier"
	outcomes := ValidateExternalResultBatch([]ExternalResultBatchEntry{entry})
	if outcomes[0].Accepted {
		t.Fatal("expected rejection for invalid tier value")
	}
	if outcomes[0].Violation == nil || len(outcomes[0].Violation.InvalidTierWorkIDs) != 1 {
		t.Fatalf("expected invalid_tier_work_ids violation, got: %+v", outcomes[0].Violation)
	}
}

func TestValidateExternalResultBatch_BatchIsIndependentPerEntry(t *testing.T) {
	good := validExternalEntry()
	bad := validExternalEntry()
	bad.ClusterID = "cluster-r105-2"
	bad.Output.ClusterID = "cluster-r105-2"
	bad.Output.Works = bad.Output.Works[:1] // missing w2
	outcomes := ValidateExternalResultBatch([]ExternalResultBatchEntry{good, bad})
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(outcomes))
	}
	if !outcomes[0].Accepted {
		t.Fatal("expected first (good) entry accepted regardless of second entry's failure")
	}
	if outcomes[1].Accepted {
		t.Fatal("expected second (bad) entry rejected regardless of first entry's success")
	}
}
