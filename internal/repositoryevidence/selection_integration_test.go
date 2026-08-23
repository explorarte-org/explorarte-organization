//go:build integration

package repositoryevidence_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence/gitsource"
)

// The question AUTONOMY-SMOKE-016 could not answer: given a goal that names a
// package, can the design phase actually see that package's code?
//
// This runs against THIS repository at its own HEAD, so it fails if the
// selection stops finding real code -- which is the only way to know the
// sensor still works on the thing it exists to observe.
func TestAGoalNamingAPackageCanSeeThatPackage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := os.Getenv("ORG_REPOSITORY_ROOT")
	if root == "" {
		root = "../.."
	}
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skipf("not a git repository: %v", err)
	}
	sha := strings.TrimSpace(string(head))

	source, err := gitsource.New(root, "/usr/bin/git", 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	explorer, err := repositoryevidence.NewExplorer("explorarte-organization", sha, source, repositoryevidence.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	// The AUTONOMY-SMOKE-016 goal, in the shape it was actually submitted.
	goal := "Identify one small, self-contained maintainability improvement in " +
		"the Executive subsystem. Scope: internal/executive only. The design must " +
		"name the exact files. Consider Orchestrator and ValidatePlanDependencies."
	selection := repositoryevidence.SelectionFromText(goal, 20)

	if len(selection.Paths) == 0 {
		t.Fatalf("the goal names a package and no path was derived: %+v", selection)
	}
	fragments, err := repositoryevidence.Gather(context.Background(), explorer, selection)
	if err != nil {
		t.Fatal(err)
	}
	if len(fragments) == 0 {
		t.Fatal("a goal naming internal/executive produced no repository evidence: the design phase is still blind")
	}

	// Everything cited must be real, inside the scope, and about THIS commit.
	if err := repositoryevidence.ValidateBundle(fragments, sha); err != nil {
		t.Fatalf("gathered evidence must be valid for the commit explored: %v", err)
	}
	// The property is that the design can see REAL code from the scope it
	// was pointed at -- not that it saw any particular line. Asserting on
	// "package executive" tested how the excerpt happened to be framed: a
	// goal naming a directory gets windows around what it mentioned, which
	// land in the middle of files rather than at their heads.
	sawDeclaration, sawNamedSymbol := false, false
	for _, fragment := range fragments {
		if !strings.HasPrefix(fragment.Path, "internal/executive") {
			t.Fatalf("evidence outside the scope the goal named: %s", fragment.Path)
		}
		if strings.Contains(fragment.Content, "func ") || strings.Contains(fragment.Content, "type ") {
			sawDeclaration = true
		}
		if strings.Contains(fragment.Content, "Orchestrator") || strings.Contains(fragment.Content, "ValidatePlanDependencies") {
			sawNamedSymbol = true
		}
	}
	if !sawDeclaration {
		t.Fatal("no excerpt contained a Go declaration: the design would still be guessing")
	}
	if !sawNamedSymbol {
		t.Fatal("the goal named symbols and no excerpt showed one of them")
	}
	for _, fragment := range fragments {
		t.Logf("  cite: %s", fragment.Reference())
	}

	// And it stayed small: eyes, not the repository in the prompt.
	_, files, ranges, bytes := explorer.Spent()
	if files > repositoryevidence.DefaultLimits().MaxFiles || ranges > repositoryevidence.DefaultLimits().MaxRanges {
		t.Fatalf("exploration exceeded its budget: %d files, %d ranges", files, ranges)
	}
	t.Logf("the design would see %d excerpts from %d files, %d bytes", len(fragments), files, bytes)
}
