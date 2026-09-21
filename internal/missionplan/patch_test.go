package missionplan

import (
	"strings"
	"testing"
)

const testPath = "internal/identifiers/identifiers_test.go"

func diffFor(path, hunks string) string {
	return "diff --git a/" + path + " b/" + path + "\n--- a/" + path + "\n+++ b/" + path + "\n" + hunks
}

func check(t *testing.T, hunks string) *PatchProblem {
	t.Helper()
	return CheckPatchStructure(Change{Path: testPath, Intent: "x", Patch: diffFor(testPath, hunks)})
}

func mustPass(t *testing.T, hunks string) {
	t.Helper()
	if problem := check(t, hunks); problem != nil {
		t.Fatalf("a well-formed patch was rejected: %v", problem)
	}
}

func mustFail(t *testing.T, hunks, wantCheck, wantDetail string) {
	t.Helper()
	problem := check(t, hunks)
	if problem == nil {
		t.Fatalf("a malformed patch was accepted:\n%s", hunks)
	}
	if problem.Check != wantCheck || !strings.Contains(problem.Detail, wantDetail) {
		t.Fatalf("problem = %v, want check %q mentioning %q", problem, wantCheck, wantDetail)
	}
}

// Production root 1007 (2026-09-21): the implementation plan's patch carried the
// literal hunk header "@@ -X,X +X,X @@". git apply --check failed on it five times
// in the code-runner. It is now refused before any mission exists.
func TestAPlaceholderHunkHeaderIsRefused(t *testing.T) {
	mustFail(t, "@@ -X,X +X,X @@\n \t\t{\n+\t\t\tname: \"digits adjacent to letters\",\n \t\t},\n",
		CheckHunkHeader, "placeholder coordinates")
}

func TestAWellFormedPatchPasses(t *testing.T) {
	mustPass(t, "@@ -3,4 +3,5 @@\n a\n b\n+c\n d\n e\n")
	// Omitted counts mean one line.
	mustPass(t, "@@ -3 +3 @@\n-old\n+new\n")
	// A blank context line is a legal hunk line, written empty by some tools.
	mustPass(t, "@@ -1,3 +1,4 @@\n a\n\n+b\n c\n")
	// "\ No newline at end of file" describes the previous line and is not counted.
	mustPass(t, "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file\n")
	// Several hunks, each checked against its own header.
	mustPass(t, "@@ -1,2 +1,3 @@\n a\n+x\n b\n@@ -10,2 +11,2 @@\n j\n-k\n+l\n")
	// New file: nothing on the old side.
	problem := CheckPatchStructure(Change{Path: "internal/x/new.go", Patch: "diff --git a/internal/x/new.go b/internal/x/new.go\nnew file mode 100644\n--- /dev/null\n+++ b/internal/x/new.go\n@@ -0,0 +1,2 @@\n+package x\n+\n"})
	if problem != nil {
		t.Fatalf("a new-file patch was rejected: %v", problem)
	}
}

func TestHunkLineCountsMustMatchTheBody(t *testing.T) {
	mustFail(t, "@@ -1,4 +1,5 @@\n a\n b\n+c\n d\n", CheckHunkBody, "short")
	mustFail(t, "@@ -1,1 +1,1 @@\n-a\n+b\n c\n", CheckHunkBody, "more lines than")
	mustFail(t, "@@ -1,2 +1,2 @@\n a\nb\n", CheckHunkBody, "does not begin with")
	mustFail(t, "@@ -1,2 +1,3 @@\n a\n+b\n+c\n d\n", CheckHunkBody, "more lines than")
}

func TestAPatchWithNoHunkOrABrokenHeaderIsRefused(t *testing.T) {
	mustFail(t, "", CheckHunkHeader, "no hunk")
	mustFail(t, "@@ nonsense @@\n a\n", CheckHunkHeader, "not a valid hunk header")
	mustFail(t, "@@ -1,2 +1,2\n a\n b\n", CheckHunkHeader, "not a valid hunk header")
}

// A change's patch may touch only its own declared path: the declared path is what
// scope and denylist are checked against, so a patch touching another file would
// escape both.
func TestAPatchMayTouchOnlyItsDeclaredPath(t *testing.T) {
	other := "internal/authorization/policy.go"
	problem := CheckPatchStructure(Change{Path: testPath, Patch: diffFor(other, "@@ -1 +1 @@\n-a\n+b\n")})
	if problem == nil || problem.Check != CheckDeclaredPath || !strings.Contains(problem.Detail, other) {
		t.Fatalf("problem = %v", problem)
	}
	two := diffFor(testPath, "@@ -1 +1 @@\n-a\n+b\n") + diffFor(other, "@@ -1 +1 @@\n-a\n+b\n")
	if problem := CheckPatchStructure(Change{Path: testPath, Patch: two}); problem == nil || problem.Check != CheckDeclaredPath {
		t.Fatalf("a two-file patch under one declared path was accepted: %v", problem)
	}
}

func TestUnsafeDeclaredOrPatchedPathsAreRefused(t *testing.T) {
	for _, declared := range []string{"/etc/passwd", "../outside.go", "~/x.go", "a\\b.go", ""} {
		if problem := CheckPatchStructure(Change{Path: declared, Patch: diffFor("x.go", "@@ -1 +1 @@\n-a\n+b\n")}); problem == nil {
			t.Errorf("declared path %q was accepted", declared)
		}
	}
	if problem := CheckPatchStructure(Change{Path: testPath, Patch: "not a diff at all"}); problem == nil || problem.Check != CheckUnifiedDiff {
		t.Fatalf("a non-diff was accepted: %v", problem)
	}
	if problem := CheckPatchStructure(Change{Path: testPath, Patch: diffFor("../escape.go", "@@ -1 +1 @@\n-a\n+b\n")}); problem == nil {
		t.Fatal("a traversal path in the diff was accepted")
	}
}

func TestPatchProblemFeedbackIsBounded(t *testing.T) {
	long := "@@ " + strings.Repeat("Z", 500) + " @@\n"
	problem := check(t, long)
	if problem == nil || len(problem.Detail) > 400 {
		t.Fatalf("detail is not bounded: %v", problem)
	}
}

func TestPathPermittedMatchesWhatDeriveWouldAllow(t *testing.T) {
	for _, tc := range []struct {
		scope Scope
		path  string
		want  bool
	}{
		{ScopeInternalCode, "internal/identifiers/identifiers_test.go", true},
		{ScopeInternalCode, "docs/implementation/x.md", true},
		{ScopeDocumentation, "internal/identifiers/identifiers_test.go", false},
		{ScopeInternalCode, "internal/authorization/policy.go", false},
		{ScopeInternalCode, "go.mod", false},
		{ScopeInternalCode, "../x.go", false},
		{ScopeInternalCode, "/abs/x.go", false},
		{Scope("nonsense"), "internal/x.go", false},
	} {
		if got := PathPermitted(tc.scope, tc.path); got != tc.want {
			t.Errorf("PathPermitted(%s, %q) = %v, want %v", tc.scope, tc.path, got, tc.want)
		}
	}
}
