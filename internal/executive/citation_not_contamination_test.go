package executive

import (
	"errors"
	"strings"
	"testing"
)

// AUTONOMY-SMOKE-017-R3 followed the rule exactly: it reproduced no source and
// cited the files it had read. It was refused anyway, because a path in its
// citation matched the same path in an excerpt's provenance header. All seven
// matched windows lay in headers; the bodies shared nothing.
//
// A boundary that refuses the behaviour it demands is worse than no boundary:
// it teaches that following the rule does not help.

const headerFixture = "internal/executive/campaign_budget_integration_test.go lines 12-40 at " +
	"410f7358de45c4958a589fa3771e79116d06a96d"

func TestCitingAnExcerptIsNotReproducingIt(t *testing.T) {
	body := "func TestCampaignBudget(t *testing.T) {\n\tt.Parallel()\n}\n"
	// The citation is longer than the threshold on its own, which is the
	// whole reason the old scan matched it.
	if len(normalizeForDeclassify(headerFixture)) <= declassifyMinimumRun {
		t.Fatalf("fixture header is too short to reproduce the incident: %d", len(normalizeForDeclassify(headerFixture)))
	}
	candidate := "The replan bound is exercised in " +
		"internal/executive/campaign_budget_integration_test.go lines 12-40, which the design leaves untouched."
	err := DeclassifyCandidate(candidate, []OrganizationalSource{{
		Reference: "repository://org/internal/executive/campaign_budget_integration_test.go#L12-L40",
		Content:   headerFixture + "\n" + body,
	}})
	if err != nil {
		t.Fatalf("a design that only cited its evidence was refused: %v", err)
	}
}

// Stripping the label must not stop the detector reading the thing labelled.
func TestTheBodyIsStillScannedAfterTheHeaderIsDropped(t *testing.T) {
	body := "maxLeader := (2*departments + 2*l.MaxDepartmentReplans) * governedTaskAttempts"
	err := DeclassifyCandidate("keep this exactly: "+body, []OrganizationalSource{{
		Reference: "repository://org/internal/executive/budget.go#L50-L60",
		Content:   headerFixture + "\n" + body,
	}})
	if !errors.Is(err, ErrCandidateContaminated) {
		t.Fatalf("a verbatim copy of the body crossed once the header was dropped: %v", err)
	}
}

// A payload whose first line is ordinary source must keep it: dropping any
// first line would open a 48-character hole at the top of every excerpt.
func TestAnExcerptWithoutAHeaderKeepsItsFirstLine(t *testing.T) {
	first := "func replanCapacityRemains(reviewKey string, limit int) bool {"
	err := DeclassifyCandidate("the design keeps "+first, []OrganizationalSource{{
		Reference: "repository://org/internal/executive/orchestrator.go#L1-L4",
		Content:   first + "\n\treturn true\n}\n",
	}})
	if !errors.Is(err, ErrCandidateContaminated) {
		t.Fatalf("the first line of a header-less excerpt was treated as a label: %v", err)
	}
}

// The header is metadata and may cross; it must not be what a refusal reports.
func TestARefusalStillNamesTheCitation(t *testing.T) {
	const reference = "repository://org/internal/executive/budget.go#L50-L60"
	body := "maxLeader := (2*departments + 2*l.MaxDepartmentReplans) * governedTaskAttempts"
	err := DeclassifyCandidate("keep this: "+body, []OrganizationalSource{{Reference: reference, Content: headerFixture + "\n" + body}})
	if err == nil || !strings.Contains(err.Error(), reference) {
		t.Fatalf("the refusal no longer says which citation was reproduced: %v", err)
	}
}
