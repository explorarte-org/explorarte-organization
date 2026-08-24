//go:build integration

package gitsource_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence/gitsource"
)

// AUTONOMY-SMOKE-017-R6 measured what alphabetical discovery costs when the
// budget is finite. A required subject was mentioned by eighteen files; the
// file that DECLARES it sorted thirteenth; discovery truncated at eight
// files, so the declaration site was never even a candidate. The snapshot
// then carried fixtures and application sites, the honest classifier refused
// to call any of them a definition, and the host stopped the campaign with
// host_evidence_insufficient before the worker spent one token.
//
// The property under test: for a required subject, a declaration site that
// exists in the pinned tree must survive discovery truncation and reach the
// gathered bundle -- without raising any limit.
func TestARequiredSubjectsDeclarationSiteSurvivesDiscoveryTruncation(t *testing.T) {
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

	const symbol = "MaxDesignRounds"
	run("init", "-b", "main")

	// Fourteen fixture/test files mention the symbol as data: string
	// literals, struct-literal keys and uses. Alphabetically they all sort
	// BEFORE the declaration site, exactly as the executive's own
	// evidence_*_test.go family did in R6.
	for index := 0; index < 14; index++ {
		name := fmt.Sprintf("internal/executive/evidence_fixture_%02d_test.go", index)
		write(name, "package executive\n\n"+
			"var _ = []EvidenceRequirement{{Subject: \""+symbol+"\", Relation: \"definition\"}}\n")
	}
	// Two production files apply it mid-line.
	write("internal/executive/orchestrator.go",
		"package executive\n\nfunc rounds(limits Limits, round int) bool {\n\treturn round >= limits."+symbol+"\n}\n")
	write("internal/executive/design_review.go",
		"package executive\n\nfunc reviewRounds(limits Limits) int {\n\treturn limits."+symbol+"\n}\n")
	// The declaration site sorts LAST of all of them.
	write("internal/executive/types.go",
		"package executive\n\ntype Limits struct {\n\t"+symbol+" int\n}\n")
	for _, name := range []string{
		"internal/executive/orchestrator.go", "internal/executive/design_review.go",
		"internal/executive/types.go",
	} {
		run("add", "--", name)
	}
	for index := 0; index < 14; index++ {
		run("add", "--", fmt.Sprintf("internal/executive/evidence_fixture_%02d_test.go", index))
	}
	run("commit", "-m", "world")
	sha := run("rev-parse", "HEAD")

	source, err := gitsource.New(root, "/usr/bin/git", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	explorer, err := repositoryevidence.NewExplorer("explorarte-organization", sha, source, repositoryevidence.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	// The goal names only the package; the subject is an obligation, so it
	// is seeded as a term ahead of anything the prose derives.
	goal := "Diagnosticar como internal/executive gobierna los replans departamentales. " +
		"Ambito: codigo productivo de internal/executive."
	selection := repositoryevidence.SelectionForRequirements(goal, []string{symbol}, 24)

	fragments, err := repositoryevidence.Gather(ctx, explorer, selection)
	if err != nil {
		t.Fatal(err)
	}

	definitionFound := false
	applications := 0
	for _, fragment := range fragments {
		relation, mentions := repositoryevidence.ClassifyExcerpt(fragment.Content, symbol)
		if !mentions {
			continue
		}
		switch relation {
		case repositoryevidence.RelationDefinition:
			if fragment.Path == "internal/executive/types.go" {
				definitionFound = true
			} else {
				t.Errorf("definition classification came from %s, not the declaration site", fragment.Path)
			}
		case repositoryevidence.RelationApplication:
			applications++
		}
	}
	if !definitionFound {
		t.Fatalf("the declaration site at internal/executive/types.go did not reach the bundle: "+
			"%d fragments, %d applications; the host would stop this campaign with "+
			"host_evidence_insufficient again", len(fragments), applications)
	}
	if applications == 0 {
		t.Fatalf("fixing definitions must not starve applications: %d fragments seen", len(fragments))
	}
	searches, files, ranges, _ := explorer.Spent()
	if files > explorer.Limits.MaxFiles || searches > explorer.Limits.MaxSearches ||
		ranges > explorer.Limits.MaxRanges {
		t.Fatalf("budget was exceeded: %d searches/%d files/%d ranges", searches, files, ranges)
	}
}

// The ablation twin: the same world with the declaration REMOVED. Discovery
// must not manufacture one -- no excerpt may classify as a definition, which
// is what keeps the host's ErrEvidenceInsufficient verdict honest.
func TestWhenNoDeclarationExistsNoneIsSupplied(t *testing.T) {
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

	const symbol = "MaxDesignRounds"
	run("init", "-b", "main")
	for index := 0; index < 10; index++ {
		name := fmt.Sprintf("internal/executive/evidence_fixture_%02d_test.go", index)
		write(name, "package executive\n\n"+
			"var _ = []EvidenceRequirement{{Subject: \""+symbol+"\", Relation: \"definition\"}}\n")
	}
	write("internal/executive/orchestrator.go",
		"package executive\n\nfunc rounds(limits Limits, round int) bool {\n\treturn round >= limits."+symbol+"\n}\n")
	for index := 0; index < 10; index++ {
		run("add", "--", fmt.Sprintf("internal/executive/evidence_fixture_%02d_test.go", index))
	}
	run("add", "--", "internal/executive/orchestrator.go")
	run("commit", "-m", "world-without-declaration")
	sha := run("rev-parse", "HEAD")

	source, err := gitsource.New(root, "/usr/bin/git", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	explorer, err := repositoryevidence.NewExplorer("explorarte-organization", sha, source, repositoryevidence.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	goal := "Ambito: codigo productivo de internal/executive."
	selection := repositoryevidence.SelectionForRequirements(goal, []string{symbol}, 24)
	fragments, err := repositoryevidence.Gather(ctx, explorer, selection)
	if err != nil {
		t.Fatal(err)
	}

	for _, fragment := range fragments {
		if relation, mentions := repositoryevidence.ClassifyExcerpt(fragment.Content, symbol); mentions &&
			relation == repositoryevidence.RelationDefinition {
			t.Fatalf("%s classified as a definition although the world declares nothing: "+
				"a fabricated definition would silence ErrEvidenceInsufficient", fragment.Reference())
		}
	}
}

// The mirror of the R6 wound: a name declared in MORE places than the limit
// can hold. Prioritising declarations must not expel every application of
// the same subject -- a contract asking for definition AND application would
// otherwise starve on the second slot while applications sat just outside
// the truncated discovery list.
func TestApplicationsSurviveADeclarationFlood(t *testing.T) {
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

	const symbol = "MaxRetries"
	run("init", "-b", "main")
	// Ten distinct declaring files -- more than DefaultLimits.MaxFiles.
	for index := 0; index < 10; index++ {
		name := fmt.Sprintf("internal/executive/limits_decl_%02d.go", index)
		write(name, "package executive\n\ntype Limits"+fmt.Sprintf("%02d", index)+" struct {\n\t"+symbol+" int\n}\n")
		run("add", "--", name)
	}
	// Two production applications of the same subject.
	write("internal/executive/orchestrator.go",
		"package executive\n\nfunc retry(limits Limits00, attempt int) bool {\n\treturn attempt > limits."+symbol+"\n}\n")
	write("internal/executive/dispatch.go",
		"package executive\n\nfunc budget(limits Limits01) int {\n\treturn limits."+symbol+"\n}\n")
	run("add", "--", "internal/executive/orchestrator.go", "internal/executive/dispatch.go")
	run("commit", "-m", "flooded-world")
	sha := run("rev-parse", "HEAD")

	source, err := gitsource.New(root, "/usr/bin/git", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	explorer, err := repositoryevidence.NewExplorer("explorarte-organization", sha, source, repositoryevidence.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	goal := "Ambito: codigo productivo de internal/executive."
	selection := repositoryevidence.SelectionForRequirements(goal, []string{symbol}, 24)
	fragments, err := repositoryevidence.Gather(ctx, explorer, selection)
	if err != nil {
		t.Fatal(err)
	}

	definitions, applications := 0, 0
	for _, fragment := range fragments {
		relation, mentions := repositoryevidence.ClassifyExcerpt(fragment.Content, symbol)
		if !mentions {
			continue
		}
		switch relation {
		case repositoryevidence.RelationDefinition:
			definitions++
		case repositoryevidence.RelationApplication:
			applications++
		}
	}
	if definitions < 1 || applications < 1 {
		t.Fatalf("truncation expelled a role: %d definition fragments, %d application fragments "+
			"(%d total); both must survive when both exist in the world", definitions, applications, len(fragments))
	}
}
