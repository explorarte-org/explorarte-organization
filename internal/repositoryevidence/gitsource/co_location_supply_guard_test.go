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

// AUTONOMY-SMOKE-017-R12 exposed two collapses of epistemic roles. The first
// lives in discovery: one best[path] per file let a declaration replace an
// earlier use, so the file reached every downstream consumer represented by
// its declaration alone. These guards pin the repair contract end to end,
// against a REAL git repository at a pinned commit.
//
// The mandatory guard is the close-co-location one: a declaration and an
// application closer than one excerpt window must STILL supply both roles.
// If reading cannot separate the roles, the host-side classification must --
// shrinking windows or trusting discovery hints would hide the wound, not
// close it.

func guardWorld(t *testing.T, body string) (string, *gitsource.Source) {
	t.Helper()
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
	run("init", "-b", "main")
	path := filepath.Join(root, "internal/executive/service.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "--", "internal/executive/service.go")
	run("commit", "-m", "guard-world")
	sha := run("rev-parse", "HEAD")
	source, err := gitsource.New(root, "/usr/bin/git", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return sha, source
}

const guardPadding = "// padding line with no bearing on the symbol\n"

// GUARD 1: same file, declaration and application WIDELY separated. Discovery
// must return both locations and the probe must supply both roles.
func TestWidelySeparatedRolesInOneFileSupplyBothSlots(t *testing.T) {
	var body strings.Builder
	body.WriteString("package executive\n\n")
	body.WriteString("type ServiceConfig struct {\n\tMaxRetries int\n}\n\n")
	for i := 0; i < 37; i++ {
		body.WriteString(guardPadding)
	}
	body.WriteString("func consume(cfg *ServiceConfig) int {\n\treturn cfg.MaxRetries\n}\n")

	sha, source := guardWorld(t, body.String())
	ctx := context.Background()
	explorer, err := repositoryevidence.NewExplorer("explorarte-organization", sha, source, repositoryevidence.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	matches, err := explorer.Search(ctx, "MaxRetries")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("one file holding both roles must yield two candidates: %+v", matches)
	}

	supplied, err := repositoryevidence.ProbeSubjectSupply(ctx, "explorarte-organization", sha, source,
		repositoryevidence.DefaultLimits(), "MaxRetries",
		[]string{repositoryevidence.RelationDefinition, repositoryevidence.RelationApplication}, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !supplied[repositoryevidence.RelationDefinition] || !supplied[repositoryevidence.RelationApplication] {
		t.Fatalf("widely separated roles must supply BOTH slots: %+v", supplied)
	}
}

// GUARD 2 -- MANDATORY: same file, declaration and application CLOSER than
// one excerpt window. Every read excerpt physically contains both roles, so
// the HOST's classification must report both; otherwise Foo/application stays
// invisible no matter how many candidates discovery emits.
func TestCoLocatedRolesCloserThanTheWindowStillSupplyBothSlots(t *testing.T) {
	var body strings.Builder
	body.WriteString("package executive\n\ntype TightConfig struct {\n")
	for i := 0; i < 6; i++ {
		body.WriteString(guardPadding)
	}
	body.WriteString("\tMaxRetries int\n}\n")
	for i := 0; i < 8; i++ {
		body.WriteString(guardPadding)
	}
	body.WriteString("func Tight(cfg *TightConfig) int {\n\treturn cfg.MaxRetries\n}\n")

	sha, source := guardWorld(t, body.String())
	ctx := context.Background()
	explorer, err := repositoryevidence.NewExplorer("explorarte-organization", sha, source, repositoryevidence.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	matches, err := explorer.Search(ctx, "MaxRetries")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("co-located roles are still two discovery candidates: %+v", matches)
	}
	for _, match := range matches {
		fragment, readErr := explorer.ReadAround(ctx, match, 24)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(fragment.Content, "MaxRetries int") ||
			!strings.Contains(fragment.Content, "cfg.MaxRetries") {
			t.Fatalf("guard premise broken: excerpt %s does not contain both roles", fragment.Reference())
		}
	}

	supplied, err := repositoryevidence.ProbeSubjectSupply(ctx, "explorarte-organization", sha, source,
		repositoryevidence.DefaultLimits(), "MaxRetries",
		[]string{repositoryevidence.RelationDefinition, repositoryevidence.RelationApplication}, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !supplied[repositoryevidence.RelationDefinition] || !supplied[repositoryevidence.RelationApplication] {
		t.Fatalf("co-located roles MUST still supply both slots: %+v "+
			"(the host saw excerpts containing declaration and use and reported only one role)", supplied)
	}
}

// GUARD 3: twenty applications inside ONE file collapse to a single
// application candidate -- the per-role cap holds against flooding.
func TestTwentyApplicationsInOneFileYieldOneCandidate(t *testing.T) {
	var body strings.Builder
	body.WriteString("package executive\n\n")
	for i := 1; i <= 20; i++ {
		body.WriteString("// note " + strings.Repeat("x", i) + ": consult MaxRetries before retrying\n")
	}
	body.WriteString("func budget(cfg *ServiceConfig) int {\n\treturn cfg.MaxRetries\n}\n")

	sha, source := guardWorld(t, body.String())
	ctx := context.Background()
	explorer, err := repositoryevidence.NewExplorer("explorarte-organization", sha, source, repositoryevidence.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	matches, err := explorer.Search(ctx, "MaxRetries")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Path != "internal/executive/service.go" {
		t.Fatalf("twenty uses in one file are ONE place to look: %+v", matches)
	}

	supplied, err := repositoryevidence.ProbeSubjectSupply(ctx, "explorarte-organization", sha, source,
		repositoryevidence.DefaultLimits(), "MaxRetries",
		[]string{repositoryevidence.RelationDefinition, repositoryevidence.RelationApplication}, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !supplied[repositoryevidence.RelationApplication] {
		t.Fatalf("the flood must not drown the application role: %+v", supplied)
	}
	if supplied[repositoryevidence.RelationDefinition] {
		t.Fatalf("a world that declares nothing must not report a definition: %+v", supplied)
	}
}
