package gitsource

import (
	"strings"
	"testing"
)

const sha = "c30328eda491241fccb81b8c83feb8a5b1e6cc35"

// hit renders one raw git grep line the way Search receives it.
func hit(path string, line int, text string) string {
	return sha + ":" + path + ":" + itoa(line) + ":" + text
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// A required subject mentioned by many fixture files and declared in a file
// that sorts last must still surface the declaring file first -- before the
// limit cuts discovery short.
func TestDeclarationSitesSurviveTheLimit(t *testing.T) {
	var lines []string
	for index := 1; index <= 14; index++ {
		path := "internal/executive/evidence_fixture_" + itoa(index) + "_test.go"
		lines = append(lines, hit(path, 3, "\t\t{Subject: \"MaxDesignRounds\", Relation: \"definition\"},"))
	}
	lines = append(lines,
		hit("internal/executive/orchestrator.go", 9, "\tif round >= o.limits.MaxDesignRounds {"),
		hit("internal/executive/types.go", 42, "\tMaxDesignRounds int"),
	)

	matches := rankHits(strings.Join(lines, "\n"), sha, "MaxDesignRounds", 8)

	if len(matches) != 8 {
		t.Fatalf("expected the limit to keep holding: got %d matches", len(matches))
	}
	if matches[0].Path != "internal/executive/types.go" || matches[0].Line != 42 {
		t.Fatalf("declaration site did not survive truncation as match #1: %+v", matches[0])
	}
}

// Within one file, a later introducing line outranks an earlier use; across
// files that merely use the symbol, git's own order stands so the reading
// stays reproducible.
func TestPerFileBestLineAndStableOrdering(t *testing.T) {
	out := strings.Join([]string{
		hit("internal/executive/beta.go", 3, "\tuse(MaxDesignRounds)"),
		hit("internal/executive/beta.go", 7, "\tuseAgain(MaxDesignRounds)"),
		hit("internal/executive/beta.go", 11, "\tMaxDesignRounds int"),
		hit("internal/executive/alpha.go", 5, "\tif round >= o.limits.MaxDesignRounds {"),
	}, "\n")

	matches := rankHits(out, sha, "MaxDesignRounds", 8)

	if len(matches) != 2 {
		t.Fatalf("one location per file: got %d", len(matches))
	}
	if matches[0].Path != "internal/executive/beta.go" || matches[0].Line != 11 {
		t.Fatalf("the declaring line should represent its file first: %+v", matches[0])
	}
	if matches[1].Path != "internal/executive/alpha.go" || matches[1].Line != 5 {
		t.Fatalf("git order should stand among non-declaring files: %+v", matches[1])
	}
}

// A world that never declares the symbol cannot have its fixtures promoted
// by the reordering: everything stays application-classed discovery.
func TestNoDeclarationMeansNothingIsPromoted(t *testing.T) {
	out := strings.Join([]string{
		hit("internal/executive/alpha_test.go", 3, "\t{Subject: \"MaxDesignRounds\"},"),
		hit("internal/executive/orchestrator.go", 9, "\treturn round >= limits.MaxDesignRounds"),
	}, "\n")

	matches := rankHits(out, sha, "MaxDesignRounds", 8)

	if len(matches) != 2 || matches[0].Path != "internal/executive/alpha_test.go" {
		t.Fatalf("without a declaration git order must stand untouched: %+v", matches)
	}
}

// Malformed or foreign-commit lines are ignored, exactly as before.
func TestMalformedLinesAreDropped(t *testing.T) {
	out := strings.Join([]string{
		"garbage-without-colons",
		sha + ":../escape.go:3:\tMaxDesignRounds int",
		sha + ":internal/executive/types.go:notanumber:\tMaxDesignRounds int",
		hit("internal/executive/types.go", 42, "\tMaxDesignRounds int"),
	}, "\n")

	matches := rankHits(out, sha, "MaxDesignRounds", 8)

	if len(matches) != 1 || matches[0].Path != "internal/executive/types.go" {
		t.Fatalf("only the well-formed hit should survive: %+v", matches)
	}
}
