package corpuscuration

// ExternalResultBatchEntry is one line of a quarantine-only external
// result import (R10.5 -- Kaggle/local-model benchmark results). This is
// deliberately NOT a new output contract: it reuses
// ValidateCurationOutputContract/CurationOutput exactly, because the R10.5
// benchmark bundle exports the SAME rubric_version/output_schema used by
// every provider in this system (DeepSeek, MiMo) -- the contract is
// identical, only the source of compute differs. cluster_id here is
// RUNTIME-OWNED (the benchmark driver's own bookkeeping, not model-echoed),
// so it is safe to use directly for looking up ExpectedWorkIDs -- unlike a
// model-generated cluster_id, which is never trusted for identity (see
// corpuscuration_output_contract.go's ClusterIDMismatch handling).
type ExternalResultBatchEntry struct {
	ClusterID       string         `json:"cluster_id"`
	ExpectedWorkIDs []string       `json:"expected_work_ids"`
	Output          CurationOutput `json:"output"`
	TerminalValid   bool           `json:"terminal_valid"`
}

// ExternalImportOutcome is the quarantine-import verdict for one entry --
// never a write to Knowledge/Silver tables (R10.5 section 42/43: Kaggle
// output is UNTRUSTED EXTERNAL COMPUTE, benchmark/quarantine evidence
// only).
type ExternalImportOutcome struct {
	ClusterID string                   `json:"cluster_id"`
	Accepted  bool                     `json:"accepted"`
	Violation *OutputContractViolation `json:"violation,omitempty"`
	Reason    string                   `json:"reason,omitempty"`
}

// ValidateExternalResultBatch validates a full batch of quarantine-import
// candidates against the exact same domain contract every provider result
// must satisfy (ValidateCurationOutputContract, unmodified). An entry the
// driver itself already marked !TerminalValid (e.g. a runtime/schema
// failure, malformed JSON, or exhausted retries) is rejected without
// re-validation -- there is nothing to check, it never reached a
// syntactically valid state.
func ValidateExternalResultBatch(entries []ExternalResultBatchEntry) []ExternalImportOutcome {
	outcomes := make([]ExternalImportOutcome, 0, len(entries))
	for _, entry := range entries {
		if !entry.TerminalValid {
			outcomes = append(outcomes, ExternalImportOutcome{
				ClusterID: entry.ClusterID, Accepted: false,
				Reason: "driver_reported_non_terminal_valid",
			})
			continue
		}
		violation := ValidateCurationOutputContract(entry.ClusterID, entry.ExpectedWorkIDs, entry.Output)
		if violation != nil {
			outcomes = append(outcomes, ExternalImportOutcome{
				ClusterID: entry.ClusterID, Accepted: false,
				Violation: violation, Reason: "output_contract_invalid",
			})
			continue
		}
		outcomes = append(outcomes, ExternalImportOutcome{ClusterID: entry.ClusterID, Accepted: true})
	}
	return outcomes
}
