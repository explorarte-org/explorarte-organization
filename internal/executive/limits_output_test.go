package executive

import "testing"

func TestDefaultLimitsGiveStructuredExecutivePlansOutputHeadroom(t *testing.T) {
	got := DefaultLimits()

	if got.MaxOutputTokens != 128000 {
		t.Fatalf(
			"MaxOutputTokens=%d, want 128000: the previous 8192 ceiling was exhausted by a real CEO structured planning call before the JSON contract completed",
			got.MaxOutputTokens,
		)
	}
}

// TestMaxOutputTokensForPurposeReducesOnlyDepartmentWorker (RECON-001,
// G1-001/MODEL-003): the shared 128000 ceiling stays the default for every
// purpose whose legitimate schema can need it, and is reduced ONLY for
// department-worker, the purpose this session traced a real production
// runaway to (invocation 134/140, finish_reason=length at 65,521 output
// tokens).
func TestMaxOutputTokensForPurposeReducesOnlyDepartmentWorker(t *testing.T) {
	limits := DefaultLimits()

	if got := limits.MaxOutputTokensFor(PurposeDepartmentWorker); got != 24000 {
		t.Fatalf("PurposeDepartmentWorker=%d, want 24000", got)
	}

	unchanged := []ExecutionPurpose{
		PurposeCEOPlan, PurposeDepartmentPlan, PurposeDepartmentReview,
		PurposeCEOClosure, PurposeAdversarialReview, PurposeDesignAdjudication,
		PurposeImplementationPlan,
	}
	for _, purpose := range unchanged {
		if got := limits.MaxOutputTokensFor(purpose); got != limits.MaxOutputTokens {
			t.Fatalf("%s=%d, want default %d (unchanged)", purpose, got, limits.MaxOutputTokens)
		}
	}
}

func TestMaxOutputTokensForPurposeHonorsLowerGlobalCeiling(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxOutputTokens = 2000
	for _, purpose := range []ExecutionPurpose{
		PurposeCEOPlan, PurposeDepartmentPlan, PurposeDepartmentWorker,
		PurposeDepartmentReview, PurposeCEOClosure, PurposeAdversarialReview,
		PurposeDesignAdjudication, PurposeImplementationPlan,
	} {
		if got := limits.MaxOutputTokensFor(purpose); got != 2000 {
			t.Fatalf("%s=%d, want the configured global ceiling 2000", purpose, got)
		}
	}
}
