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

// Within one file, at most ONE CANDIDATE PER ROLE survives: the earliest
// declaring line and the earliest applying line are different epistemic
// roles and both deserve representation, while twenty mentions of either
// role stay one candidate. AUTONOMY-SMOKE-017-R12 measured the collapse of
// these roles into a single best[path]: the declaration replaced the use and
// the file reached downstream classifiers as its declaration alone.
//
// Across files, git's own order stands so reading stays reproducible, with
// the reserved application seat preferring a file other than the declaring
// file when one exists.
func TestPerFileBestLineAndStableOrdering(t *testing.T) {
	out := strings.Join([]string{
		hit("internal/executive/beta.go", 3, "\tuse(MaxDesignRounds)"),
		hit("internal/executive/beta.go", 7, "\tuseAgain(MaxDesignRounds)"),
		hit("internal/executive/beta.go", 11, "\tMaxDesignRounds int"),
		hit("internal/executive/alpha.go", 5, "\tif round >= o.limits.MaxDesignRounds {"),
	}, "\n")

	matches := rankHits(out, sha, "MaxDesignRounds", 8)

	if len(matches) != 3 {
		t.Fatalf("one candidate per role per file: got %d", len(matches))
	}
	if matches[0].Path != "internal/executive/beta.go" || matches[0].Line != 11 {
		t.Fatalf("the declaring line should lead: %+v", matches[0])
	}
	if matches[1].Path != "internal/executive/alpha.go" {
		t.Fatalf("the reserved application seat prefers another file: %+v", matches[1])
	}
	if matches[2].Path != "internal/executive/beta.go" || matches[2].Line != 3 {
		t.Fatalf("beta's earliest use should be its second candidate: %+v", matches[2])
	}
}

// A file that both declares and applies the symbol contributes two
// candidates -- never more -- even when the uses vastly outnumber them.
func TestSameFileKeepsOneSeatPerRole(t *testing.T) {
	out := strings.Join([]string{
		hit("internal/executive/service.go", 20, "\tuseAgain(MaxRetries)"),
		hit("internal/executive/service.go", 24, "\tyetMore(MaxRetries)"),
		hit("internal/executive/service.go", 10, "\tMaxRetries int"),
		hit("internal/executive/service.go", 31, "\tlater(MaxRetries)"),
	}, "\n")

	matches := rankHits(out, sha, "MaxRetries", 8)

	if len(matches) != 2 {
		t.Fatalf("declaration plus first use, nothing else: got %d (%+v)", len(matches), matches)
	}
	if matches[0].Path != "internal/executive/service.go" || matches[0].Line != 10 {
		t.Fatalf("the declaration should be the file's first seat: %+v", matches[0])
	}
	if matches[1].Path != "internal/executive/service.go" || matches[1].Line != 20 {
		t.Fatalf("the earliest use should be the file's second seat: %+v", matches[1])
	}
}

// Twenty applications inside ONE file are one place to look, not twenty:
// the per-role cap exists so discovery cannot be flooded from a single path.
func TestTwentyUsesYieldOneApplicationCandidate(t *testing.T) {
	var lines []string
	for line := 1; line <= 20; line++ {
		lines = append(lines, hit("internal/executive/hot.go", line,
			"\tif n > limits.MaxRetries {"))
	}

	matches := rankHits(strings.Join(lines, "\n"), sha, "MaxRetries", 8)

	if len(matches) != 1 || matches[0].Path != "internal/executive/hot.go" || matches[0].Line != 1 {
		t.Fatalf("a single-file flood must collapse to its earliest use: %+v", matches)
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

// A name can be declared in many places legitimately. When declaring files
// outnumber the limit, prioritising declarations must not expel every
// application of the same subject: a contract asking for definition AND
// application would otherwise go evidence_insufficient on the second slot
// while perfectly good applications sat just outside the truncated list.
func TestApplicationsSurviveADeclarationFlood(t *testing.T) {
	const limit = 8
	var lines []string
	// Ten files declare MaxRetries; all sort before any application.
	for index := 1; index <= limit+2; index++ {
		path := "internal/executive/decl_" + itoa(index) + ".go"
		lines = append(lines, hit(path, 4, "\tMaxRetries int"))
	}
	// Two real applications, reachable in the same world.
	lines = append(lines,
		hit("internal/executive/orchestrator.go", 9, "\tif attempt > o.limits.MaxRetries {"),
		hit("internal/executive/dispatch.go", 17, "\treturn limits.MaxRetries"),
	)

	matches := rankHits(strings.Join(lines, "\n"), sha, "MaxRetries", limit)

	if len(matches) != limit {
		t.Fatalf("the limit must still bind: got %d matches", len(matches))
	}
	declarations, applications := 0, 0
	for _, match := range matches {
		switch match.Path {
		case "internal/executive/orchestrator.go", "internal/executive/dispatch.go":
			applications++
		default:
			declarations++
		}
	}
	if matches[0].Path != "internal/executive/decl_1.go" || matches[0].Line != 4 {
		t.Fatalf("the first declaration should lead: %+v", matches[0])
	}
	if declarations < 1 || applications < 1 {
		t.Fatalf("at least one candidate per role must survive truncation: %d declarations, %d applications", declarations, applications)
	}
}

// With only one role present the ordering is a no-op: declarations alone or
// applications alone are returned exactly as git produced them.
func TestSingleRoleWorldsAreUntouched(t *testing.T) {
	var declarationOnly []string
	for index := 1; index <= 3; index++ {
		declarationOnly = append(declarationOnly, hit("pkg/struct_"+itoa(index)+".go", 2, "\tMaxRetries int"))
	}
	matches := rankHits(strings.Join(declarationOnly, "\n"), sha, "MaxRetries", 8)
	if len(matches) != 3 || matches[0].Path != "pkg/struct_1.go" {
		t.Fatalf("declarations-only world must keep git order: %+v", matches)
	}

	applicationsOnly := strings.Join([]string{
		hit("pkg/use_b.go", 5, "\treturn limits.MaxRetries"),
		hit("pkg/use_a.go", 6, "\tif n > limits.MaxRetries {"),
	}, "\n")
	matches = rankHits(applicationsOnly, sha, "MaxRetries", 8)
	if len(matches) != 2 || matches[0].Path != "pkg/use_b.go" {
		t.Fatalf("applications-only world must keep git order: %+v", matches)
	}
}
