package coderunner

import (
	"strings"
	"testing"
)

// The real output of a failed go test ./... run: one line per package, the
// failure in the middle, and no summary at the end.
//
// Keeping the tail alone recorded exit code 1 followed by fourteen hundred
// bytes of "ok", which is a gate that speaks and says nothing.
func goTestOutput() string {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString("ok  \tgithub.com/org/repo/internal/aaa" + strings.Repeat("a", i) + "\t0.01s\n")
	}
	b.WriteString("--- FAIL: TestTheOneThatBroke (0.02s)\n")
	b.WriteString("    thing_test.go:41: wanted 3, got 4\n")
	b.WriteString("FAIL\tgithub.com/org/repo/internal/middle\t0.03s\n")
	for i := 0; i < 60; i++ {
		b.WriteString("ok  \tgithub.com/org/repo/internal/zzz" + strings.Repeat("z", i) + "\t0.01s\n")
	}
	return b.String()
}

func TestTheExcerptKeepsTheFailureAndNotTheSuccesses(t *testing.T) {
	excerpt := failureExcerpt(goTestOutput())
	for _, want := range []string{"--- FAIL: TestTheOneThatBroke", "FAIL\tgithub.com/org/repo/internal/middle"} {
		if !strings.Contains(excerpt, want) {
			t.Errorf("the excerpt must name the failure; missing %q", want)
		}
	}
	if strings.Contains(excerpt, "ok  \tgithub.com/org/repo/internal/zzz") {
		t.Error("the alphabetical remainder of passing packages is not the reason anything failed")
	}
	if len(excerpt) > failureExcerptBytes+8 {
		t.Fatalf("the excerpt must stay bounded, got %d bytes", len(excerpt))
	}
}

// A compiler names the package with # and the file with error:. Both must
// survive, because between them they say where and what.
func TestABuildFailureKeepsBothItsLines(t *testing.T) {
	excerpt := failureExcerpt("# github.com/org/repo/internal/thing\n" +
		"./thing.go:12:6: undefined: Missing\n" +
		"ok  \tgithub.com/org/repo/internal/other\t0.01s\n")
	for _, want := range []string{"# github.com/org/repo/internal/thing", "undefined: Missing"} {
		if !strings.Contains(excerpt, want) {
			t.Errorf("missing %q from %q", want, excerpt)
		}
	}
}

// Falling back is what keeps this from being a heuristic that can lose the
// output: a command whose failures look like nothing recognised is reported
// exactly as it was before.
func TestUnrecognisedOutputFallsBackToTheTail(t *testing.T) {
	output := strings.Repeat("some tool that announces nothing\n", 200) + "the last thing it said"
	excerpt := failureExcerpt(output)
	if !strings.Contains(excerpt, "the last thing it said") {
		t.Fatal("with no marker to key on, the tail is still better than nothing")
	}
	if len(excerpt) > failureExcerptBytes+8 {
		t.Fatalf("the fallback must stay bounded, got %d bytes", len(excerpt))
	}
}

func TestEmptyOutputStaysEmpty(t *testing.T) {
	if got := failureExcerpt("  \n\t "); got != "" {
		t.Fatalf("nothing must stay nothing, got %q", got)
	}
}

// A panic is how a test binary reports that it died rather than failed, and
// it is the line that matters most when it happens.
func TestAPanicIsKept(t *testing.T) {
	excerpt := failureExcerpt("ok  \tgithub.com/org/repo/a\t0.01s\npanic: runtime error: index out of range [3]\nok  \tgithub.com/org/repo/b\t0.01s\n")
	if !strings.Contains(excerpt, "panic: runtime error") {
		t.Fatalf("a panic must survive: %q", excerpt)
	}
}
