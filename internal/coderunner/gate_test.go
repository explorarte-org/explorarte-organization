package coderunner

import (
	"strings"
	"testing"
)

// "operation GO_TEST failed" is consistent with a broken test, an unwritable
// cache, a missing toolchain and a full disk, and distinguishes none of them.
// A real gate failure took an hour to narrow down, and the answer turned out
// to be that the tests passed in eighty-eight seconds -- so the failure was
// something else entirely, and nothing recorded had said what.
//
// The Result carrying exit code and output was already in hand at the line
// that built the error. It was simply dropped.
func TestAFailedOperationSaysWhatTheCommandSaid(t *testing.T) {
	err := operationFailure(Result{
		Type:     GoTest,
		ExitCode: 2,
		Output:   "FAIL\tgithub.com/x/y [build failed]\n# github.com/x/y\n./a.go:3:2: undefined: Missing",
	})
	if err == nil {
		t.Fatal("a failed operation must be an error")
	}
	for _, want := range []string{"GO_TEST", "exit code 2", "undefined: Missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure must carry %q: %v", want, err)
		}
	}
}

// The tail is what is kept: a compiler or test runner puts the summary of
// what went wrong at the end, so a head-truncated excerpt is reliably the
// least informative part.
func TestALongOutputKeepsItsEnd(t *testing.T) {
	tail := "./final.go:9:1: this is the actual failure"
	err := operationFailure(Result{
		Type:     GoBuild,
		ExitCode: 1,
		Output:   strings.Repeat("noise from an earlier package\n", 400) + tail,
	})
	message := err.Error()
	if !strings.Contains(message, tail) {
		t.Fatal("the end of the output is where the reason lives and must survive")
	}
	if len(message) > failureExcerptBytes+200 {
		t.Fatalf("the excerpt must stay bounded, got %d bytes", len(message))
	}
	if !strings.Contains(message, "...") {
		t.Error("a truncated excerpt should show that it was truncated")
	}
}

// A command that failed silently is itself the finding. An empty quotation
// reads like missing data and sends the reader looking for it.
func TestASilentFailureSaysItWasSilent(t *testing.T) {
	err := operationFailure(Result{Type: GoVet, ExitCode: 3, Output: "   \n\t "})
	if !strings.Contains(err.Error(), "produced no output") {
		t.Fatalf("silence must be reported as silence: %v", err)
	}
	if !strings.Contains(err.Error(), "exit code 3") {
		t.Fatalf("the exit code is the only signal left and must survive: %v", err)
	}
}

// Whatever the output, the failure must name the operation and its exit code
// -- those two are what make one gate failure distinguishable from another.
func TestEveryFailureNamesTheOperationAndExitCode(t *testing.T) {
	for _, r := range []Result{
		{Type: GoBuild, ExitCode: 1, Output: "x"},
		{Type: GoVet, ExitCode: 1, Output: ""},
		{Type: GoTest, ExitCode: 137, Output: strings.Repeat("y", 9000)},
		{Type: ApplyPatch, ExitCode: 128, Output: "error: patch does not apply"},
	} {
		message := operationFailure(r).Error()
		if !strings.Contains(message, string(r.Type)) {
			t.Errorf("%s: the operation must be named", r.Type)
		}
		if !strings.Contains(message, "exit code") {
			t.Errorf("%s: the exit code must be reported", r.Type)
		}
	}
}
