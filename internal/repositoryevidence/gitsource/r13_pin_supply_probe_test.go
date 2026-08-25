//go:build integration

package gitsource_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence/gitsource"
)

// TestSupplyProbeAnswersAtARealPin drives ProbeSubjectSupply end to end
// against a REAL repository at a real commit: the goal's durable
// obligations (MaxDesignRounds, MaxDepartmentReplans) and the adjudication
// demands R12 rejected (DesignBaseSHAReference, replanCapacityRemains) are
// supplyable exactly when the pinned tree contains their definition and
// application -- and fail closed for a subject the tree cannot supply.
func TestSupplyProbeAnswersAtARealPin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("internal/executive/types.go", "package executive\n\ntype Limits struct {\n\tMaxDesignRounds        int\n\tMaxDepartmentReplans   int\n\tDesignBaseSHAReference string\n}\n")
	write("internal/executive/budget.go", "package executive\n\nfunc check(l Limits, key string) bool {\n\tif !replanCapacityRemains(key, l.MaxDepartmentReplans) {\n\t\treturn false\n\t}\n\tif l.MaxDesignRounds <= 0 || l.DesignBaseSHAReference == \"\" {\n\t\treturn false\n\t}\n\treturn true\n}\n")
	write("internal/executive/rounds.go", "package executive\n\nfunc replanCapacityRemains(key string, bound int) bool {\n\treturn bound > 0\n}\n")
	run("init", "-b", "main")
	run("add", "--", "internal/executive")
	run("commit", "-m", "supplying world")
	pin := run("rev-parse", "HEAD")

	source, err := gitsource.New(root, "git", 2<<20)
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
			context.Background(), "explorarte-organization", pin, source,
			repositoryevidence.DefaultLimits(), tc.subject, tc.relations, 24)
		if err != nil {
			t.Fatalf("%s: probe error: %v", tc.subject, err)
		}
		for _, rel := range tc.relations {
			t.Logf("%s/%s -> supplied=%v", tc.subject, rel, supplied[rel])
			if !supplied[rel] {
				t.Errorf("%s/%s NOT supplyable at %s", tc.subject, rel, pin)
			}
		}
	}

	// Fail closed: a subject the tree says nothing about supplies nothing.
	supplied, err := repositoryevidence.ProbeSubjectSupply(
		context.Background(), "explorarte-organization", pin, source,
		repositoryevidence.DefaultLimits(), "NoSuchSubjectAnywhere", []string{"definition"}, 24)
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if supplied["definition"] {
		t.Fatal("an absent subject must not be reported supplyable")
	}
}
