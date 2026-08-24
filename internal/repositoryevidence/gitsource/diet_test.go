package gitsource

import (
	"strings"
	"testing"
)

// The evidence diet must be applied BEFORE ranking and truncation. If test
// hits were filtered after the limit was applied, a flood of them could evict
// production applications that ranked below -- spending the candidate budget
// on files the host refuses anyway.
func TestSearchPrefiltersIneligibleHitsBeforeTheLimit(t *testing.T) {
	var lines []string
	for index := 1; index <= 8; index++ {
		lines = append(lines, hit("internal/executive/fixture_"+itoa(index)+"_test.go", 3, "\tuse(MaxDesignRounds)"))
	}
	lines = append(lines,
		hit("internal/executive/orchestrator.go", 9, "\tif round >= o.limits.MaxDesignRounds {"),
		hit("internal/executive/types.go", 42, "\tMaxDesignRounds int"),
	)
	raw := strings.Join(lines, "\n")

	filtered := filterIneligibleHits(sha, raw)
	if strings.Contains(filtered, "_test.go") {
		t.Fatal("test-file hits survived the pre-filter")
	}

	matches := rankHits(filtered, sha, "MaxDesignRounds", 8)
	if len(matches) != 2 {
		t.Fatalf("production hits lost to the test flood: %+v", matches)
	}
	if matches[0].Path != "internal/executive/types.go" {
		t.Fatalf("the declaration did not lead once tests were out of the way: %+v", matches[0])
	}
}

// Malformed and foreign lines are not the diet's business: they pass through
// untouched for rankHits to drop exactly as before.
func TestFilterKeepsMalformedLinesForRankingToDrop(t *testing.T) {
	raw := strings.Join([]string{
		"garbage-without-colons",
		hit("internal/executive/x_test.go", 1, "\tMaxRetries int"),
		hit("internal/executive/types.go", 42, "\tMaxRetries int"),
	}, "\n")

	filtered := filterIneligibleHits(sha, raw)
	if !strings.Contains(filtered, "garbage-without-colons") {
		t.Fatal("the filter started interpreting malformed lines")
	}
	if strings.Contains(filtered, "x_test.go") {
		t.Fatal("an ineligible path survived the filter")
	}
}
