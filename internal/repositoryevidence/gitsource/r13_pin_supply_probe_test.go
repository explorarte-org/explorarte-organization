package gitsource

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
)

// TestR12RejectedObligationsSupplyableAtPin answers, with the NEW code, the
// exact capability question R12's adjudication failed: are
// DesignBaseSHAReference/application and replanCapacityRemains/application
// supplyable at pin c8981e53? Plus the goal's own durable requirements.
func TestR12RejectedObligationsSupplyableAtPin(t *testing.T) {
	const baseSHA = "c8981e5334ccd777ce8e27757f2d3859655a3c57"
	src, err := New("/opt/explorarte/organization-v2-program", "/usr/bin/git", 2<<20)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	cases := []struct {
		subject   string
		relations []string
	}{
		{"DesignBaseSHAReference", []string{"definition", "application"}},
		{"replanCapacityRemains", []string{"definition", "application"}},
		{"MaxDesignRounds", []string{"definition", "application"}},
		{"MaxDepartmentReplans", []string{"definition", "application"}},
	}
	for _, tc := range cases {
		supplied, err := repositoryevidence.ProbeSubjectSupply(
			context.Background(), "explorarte-organization", baseSHA, src,
			repositoryevidence.DefaultLimits(), tc.subject, tc.relations, 24)
		if err != nil {
			t.Fatalf("%s: probe error: %v", tc.subject, err)
		}
		for _, rel := range tc.relations {
			t.Logf("%s/%s -> supplied=%v", tc.subject, rel, supplied[rel])
			if !supplied[rel] {
				t.Errorf("%s/%s NOT supplyable at %s", tc.subject, rel, baseSHA)
			}
		}
	}
}
