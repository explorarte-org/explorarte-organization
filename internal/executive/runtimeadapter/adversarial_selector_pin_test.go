package runtimeadapter

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/designreview"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// The adversarial selector is one contract spelled out in four packages that
// deliberately do not import each other. contextengine now REFUSES to build a
// restricted context unless all of it matches exactly, so a rename on any one
// side would stop being a compile error and start being a review that silently
// falls back to the ordinary organizational assembly and dies at egress.
//
// This test is the only place all four sides are in scope at once, so it is
// where the contract is pinned.
func TestAdversarialSelectorIsOneContractAcrossPackages(t *testing.T) {
	for _, tc := range []struct{ name, engine, source string }{
		{"legacy purpose", contextengine.AdversarialReviewPurpose, executive.PurposeAdversarialReview.LegacyPurpose()},
		{"execution purpose", contextengine.AdversarialReviewExecutionPurpose, string(executive.PurposeAdversarialReview)},
		{"task class", contextengine.AdversarialReviewTaskClass, executive.TaskClassCoordinationAdversarialReview},
		{"reviewer unit", contextengine.AdversarialReviewerUnitID, designreview.ReviewerUnitID},
	} {
		if tc.engine != tc.source {
			t.Errorf("%s drifted: contextengine has %q, its source of truth has %q", tc.name, tc.engine, tc.source)
		}
	}
	if contextengine.AdversarialReviewPurpose == contextengine.AdversarialReviewExecutionPurpose {
		t.Error("the underscored legacy purpose and the hyphenated execution purpose are distinct load-bearing strings; collapsing them would let a partial selector look complete")
	}
}
