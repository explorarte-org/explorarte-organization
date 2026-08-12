package contextcompiler

// CorpusCurateOutputTokenBudgetFloor is the corrected minimum
// max_output_tokens for research.corpus_curate/v1 small clusters (R10.3
// Part A / R10.4.1 section 4). R10/R10.2/R10.3 all measured
// response_truncated_empty terminal failures on both DeepSeek and MiMo at
// the historical floor of 3000 -- confirmed structurally caused by the
// reasoning/thinking budget being consumed from the same
// max_output_tokens pool before the final answer (see
// docs/reports/MIMO_V25_INTEGRATION_AUDIT.md section E and
// docs/reports/R10_3_OUTPUT_BUDGET_CALIBRATION.md). MiMo recovered the
// sentinel cluster scluster-9f9da5855df592ce at exactly 4500 in R10.3
// (the first value tried, per the stop-at-first-success experiment
// design) -- this constant carries that evidence-based floor forward as
// production config, scoped to research.corpus_curate/v1 only.
const CorpusCurateOutputTokenBudgetFloor = 4500

// corpusCurateOutputTokenBudgetCap is the pre-existing upper cap from the
// historical formula (max(3000, min(16000, 600+1200*n_works))) --
// unchanged by this correction.
const corpusCurateOutputTokenBudgetCap = 16000

// corpusCurateOutputTokenBudgetHistoricalFloor is the OLD floor this
// function corrects. A cluster whose raw scaled value is at or below this
// historical floor is exactly the case the historical formula clamped to
// 3000 -- those clusters now get CorpusCurateOutputTokenBudgetFloor
// instead. Every other cluster (raw value already above 3000) is
// completely unaffected -- this correction never lowers a budget that was
// already working, only raises the ones that were provably insufficient.
const corpusCurateOutputTokenBudgetHistoricalFloor = 3000

// CorpusCurateOutputTokenBudget computes max_output_tokens for a
// research.corpus_curate/v1 cluster of the given size, replacing the
// historical formula's floor of 3000 (which R10/R10.2/R10.3 evidence
// showed was insufficient for small clusters under reasoning-enabled
// providers) with the corrected floor of 4500, while leaving every other
// cluster's budget byte-for-byte identical to the historical formula
// (R10.4.1 section 4: "No cambiar los budgets de clusters que ya tengan
// un valor superior por la fórmula actual"). Scoped to
// research.corpus_curate/v1 only -- not generalized to any other task
// class (section 4: "No generalizar todavía esta regla fuera de
// research.corpus_curate").
func CorpusCurateOutputTokenBudget(workCount int) int {
	raw := 600 + 1200*workCount
	if raw <= corpusCurateOutputTokenBudgetHistoricalFloor {
		return CorpusCurateOutputTokenBudgetFloor
	}
	if raw > corpusCurateOutputTokenBudgetCap {
		return corpusCurateOutputTokenBudgetCap
	}
	return raw
}
