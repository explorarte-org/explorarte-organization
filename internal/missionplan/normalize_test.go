package missionplan

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
)

// Root 1062 (2026-09-21), the planner's patches for testPath, byte for byte as the
// model emitted them (read back from its invocation results). Attempts 2 and 3 are
// this text: the right row in the right place, a header that declares 6 old / 7 new
// lines over a body of 4 / 5, and no a/ b/ prefixes. Attempt 1 is the same text plus
// one more context line whose newline is missing.
const root1062Patch = "--- internal/identifiers/identifiers_test.go\n+++ internal/identifiers/identifiers_test.go\n@@ -19,6 +19,7 @@\n" +
	" \t\t{\"leading zeros are preserved literally, not numerically normalized\", \"ticket 007 vs ticket 7\", []string{\"007\", \"7\"}},\n" +
	" \t\t{\"empty input\", \"\", []string{}},\n" +
	"+\t\t{\"digits adjacent to letters\", \"abc123def45\", []string{\"123\", \"45\"}},\n" +
	" \t}\n" +
	" \tfor _, tc := range cases {\n"

const root1062PatchWithoutFinalNewline = root1062Patch + " \t\tt.Run(tc.name, func(t *testing.T) {"

func normalize(t *testing.T, declared, raw string) NormalizedPatch {
	t.Helper()
	got, problem := NormalizePatch(declared, raw)
	if problem != nil {
		t.Fatalf("NormalizePatch refused a patch it should canonicalize: %v\n%s", problem, raw)
	}
	return got
}

// bodyLines are the lines the host must never change: everything after the first
// hunk header that is not a hunk header.
func bodyLines(patch string) []string {
	var body []string
	seen := false
	for _, line := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		if strings.HasPrefix(line, "@@") {
			seen = true
			continue
		}
		if seen {
			body = append(body, line)
		}
	}
	return body
}

// headerLines are what remains: the preamble and every hunk header.
func headerLines(patch string) []string {
	var head []string
	seen := false
	for _, line := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		if strings.HasPrefix(line, "@@") {
			seen = true
			head = append(head, line)
			continue
		}
		if !seen {
			head = append(head, line)
		}
	}
	return head
}

// The root-1062 patch, as the model wrote it, is made canonical, and the canonical
// form is a patch the structural check accepts.
func TestRoot1062PatchIsMadeCanonicalWithoutTouchingItsBody(t *testing.T) {
	got := normalize(t, testPath, root1062Patch)

	want := "--- a/" + testPath + "\n+++ b/" + testPath + "\n@@ -19,4 +19,5 @@\n" +
		strings.SplitN(root1062Patch, "@@ -19,6 +19,7 @@\n", 2)[1]
	if got.Patch != want {
		t.Fatalf("canonical patch =\n%s\nwant\n%s", got.Patch, want)
	}
	if !reflect.DeepEqual(got.Provenance.Normalizations, []string{NormalizationPathPrefix, NormalizationHunkRecount}) {
		t.Fatalf("normalizations = %v", got.Provenance.Normalizations)
	}
	if problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch}); problem != nil {
		t.Fatalf("the canonical patch was refused: %v", problem)
	}
	// And the raw one was not acceptable as it stood: this is the run's failure.
	if problem := CheckPatchStructure(Change{Path: testPath, Patch: root1062Patch}); problem == nil || problem.Check != CheckHunkBody {
		t.Fatalf("the raw patch should have failed on its hunk counts: %v", problem)
	}
}

// Attempt 1 of the same run ended without its final newline. Adding it is not a
// rewrite of header metadata, so the host does not: it recounts, leaves the ending as
// the model wrote it, and the structural check tells the planner what is wrong.
func TestAMissingFinalNewlineIsNotSuppliedByTheHost(t *testing.T) {
	got := normalize(t, testPath, root1062PatchWithoutFinalNewline)
	if strings.HasSuffix(got.Patch, "\n") {
		t.Fatal("the host added a newline the model did not write")
	}
	if !strings.Contains(got.Patch, "@@ -19,5 +19,6 @@") {
		t.Fatalf("the hunk was not recounted:\n%s", got.Patch)
	}
	problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch})
	if problem == nil || problem.Check != CheckUnifiedDiff || !strings.Contains(problem.Detail, "must end with a newline") {
		t.Fatalf("problem = %v", problem)
	}
}

func TestAnUnprefixedPathIsCanonicalized(t *testing.T) {
	raw := "--- " + testPath + "\n+++ " + testPath + "\n@@ -1,2 +1,3 @@\n a\n+b\n c\n"
	got := normalize(t, testPath, raw)
	want := "--- a/" + testPath + "\n+++ b/" + testPath + "\n@@ -1,2 +1,3 @@\n a\n+b\n c\n"
	if got.Patch != want {
		t.Fatalf("patch =\n%s", got.Patch)
	}
	if !reflect.DeepEqual(got.Provenance.Normalizations, []string{NormalizationPathPrefix}) {
		t.Fatalf("normalizations = %v", got.Provenance.Normalizations)
	}
}

func TestAWrongHunkCountIsRecomputed(t *testing.T) {
	for name, header := range map[string]string{
		"too many":       "@@ -1,6 +1,7 @@",
		"too few":        "@@ -1,1 +1,1 @@",
		"omitted counts": "@@ -1 +1 @@",
	} {
		t.Run(name, func(t *testing.T) {
			raw := diffFor(testPath, header+"\n a\n+b\n c\n")
			got := normalize(t, testPath, raw)
			if !strings.Contains(got.Patch, "\n@@ -1,2 +1,3 @@\n") {
				t.Fatalf("patch =\n%s", got.Patch)
			}
			if !reflect.DeepEqual(got.Provenance.Normalizations, []string{NormalizationHunkRecount}) {
				t.Fatalf("normalizations = %v", got.Provenance.Normalizations)
			}
			if problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch}); problem != nil {
				t.Fatalf("canonical patch refused: %v", problem)
			}
		})
	}
}

func TestBothAnomaliesTogetherAndSeveralHunks(t *testing.T) {
	raw := "--- " + testPath + "\n+++ " + testPath + "\n@@ -1,9 +1,9 @@ func A() {\n a\n-b\n+c\n d\n@@ -20 +20 @@\n x\n+y\n"
	got := normalize(t, testPath, raw)
	want := "--- a/" + testPath + "\n+++ b/" + testPath + "\n@@ -1,3 +1,3 @@ func A() {\n a\n-b\n+c\n d\n@@ -20,1 +20,2 @@\n x\n+y\n"
	// The first hunk has 3 old (a, b, d) and 3 new (a, c, d); the second 1 old, 2 new.
	if got.Patch != want {
		t.Fatalf("patch =\n%s\nwant\n%s", got.Patch, want)
	}
	if !reflect.DeepEqual(got.Provenance.Normalizations, []string{NormalizationPathPrefix, NormalizationHunkRecount}) {
		t.Fatalf("normalizations = %v", got.Provenance.Normalizations)
	}
	if problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch}); problem != nil {
		t.Fatalf("canonical patch refused: %v", problem)
	}
}

// A hunk whose counts are already right is not rewritten, even when the header
// spells them in another legal way, so a canonical patch is returned byte for byte.
func TestACanonicalPatchIsReturnedUnchanged(t *testing.T) {
	for _, raw := range []string{
		diffFor(testPath, "@@ -3,4 +3,5 @@\n a\n b\n+c\n d\n e\n"),
		diffFor(testPath, "@@ -3 +3 @@\n-old\n+new\n"),
		diffFor(testPath, "@@ -3,1 +3,1 @@ func F() {\n-old\n+new\n"),
		diffFor(testPath, "@@ -1,3 +1,4 @@\n a\n\n+b\n c\n"),
		diffFor(testPath, "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file\n"),
		diffFor(testPath, "@@ -1,2 +1,3 @@\n a\n+x\n b\n@@ -10,2 +11,2 @@\n j\n-k\n+l\n"),
		"diff --git a/internal/x/new.go b/internal/x/new.go\nnew file mode 100644\n--- /dev/null\n+++ b/internal/x/new.go\n@@ -0,0 +1,2 @@\n+package x\n+\n",
	} {
		declared := testPath
		if strings.Contains(raw, "new.go") {
			declared = "internal/x/new.go"
		}
		got := normalize(t, declared, raw)
		if got.Patch != raw || len(got.Provenance.Normalizations) != 0 {
			t.Errorf("canonical patch was altered:\n%s\n->\n%s (%v)", raw, got.Patch, got.Provenance.Normalizations)
		}
		if got.Provenance.RawSHA256 != got.Provenance.NormalizedSHA256 {
			t.Errorf("a patch that did not change has two different digests")
		}
	}
}

func TestDevNullIsPreservedForCreateAndDelete(t *testing.T) {
	create := "--- /dev/null\n+++ internal/x/new.go\n@@ -0,0 +1,5 @@\n+package x\n+\n+func F() {}\n"
	got := normalize(t, "internal/x/new.go", create)
	if want := "--- /dev/null\n+++ b/internal/x/new.go\n@@ -0,0 +1,3 @@\n+package x\n+\n+func F() {}\n"; got.Patch != want {
		t.Fatalf("create =\n%s", got.Patch)
	}
	if problem := CheckPatchStructure(Change{Path: "internal/x/new.go", Patch: got.Patch}); problem != nil {
		t.Fatalf("canonical creation refused: %v", problem)
	}

	remove := "--- internal/x/old.go\n+++ /dev/null\n@@ -1,9 +0,0 @@\n-package x\n-\n"
	got = normalize(t, "internal/x/old.go", remove)
	if want := "--- a/internal/x/old.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-package x\n-\n"; got.Patch != want {
		t.Fatalf("delete =\n%s", got.Patch)
	}
	// The new-side start of a deletion is not the host's to change, and neither is the
	// (already right) count of an empty side.
	if !strings.Contains(got.Patch, "+0,0 @@") {
		t.Fatalf("the empty side was disturbed:\n%s", got.Patch)
	}
}

// A patch is a change to ONE declared file. The host canonicalizes only that file's
// header; it neither rewrites another path nor lets the rewrite through.
func TestUnsafeAndForeignPathsAreNeverRewrittenIntoAcceptance(t *testing.T) {
	for name, header := range map[string]string{
		"traversal":      "--- ../etc/passwd\n+++ ../etc/passwd\n",
		"absolute":       "--- /etc/passwd\n+++ /etc/passwd\n",
		"dot dot in a/":  "--- a/../../etc/passwd\n+++ b/../../etc/passwd\n",
		"other file":     "--- internal/authorization/policy.go\n+++ internal/authorization/policy.go\n",
		"other prefixed": "--- a/internal/authorization/policy.go\n+++ b/internal/authorization/policy.go\n",
		"tilde":          "--- ~/x.go\n+++ ~/x.go\n",
	} {
		t.Run(name, func(t *testing.T) {
			raw := header + "@@ -1,1 +1,2 @@\n a\n+b\n"
			got := normalize(t, testPath, raw)
			if !reflect.DeepEqual(headerLines(got.Patch)[:2], headerLines(raw)[:2]) {
				t.Fatalf("a foreign path header was rewritten:\n%s", got.Patch)
			}
			problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch})
			if problem == nil || problem.Check != CheckDeclaredPath {
				t.Fatalf("problem = %v, want the declared-path refusal", problem)
			}
		})
	}
	// An unsafe DECLARED path is refused before anything is rewritten.
	for _, declared := range []string{"../x.go", "/etc/passwd", "~/x.go", "a\\b.go", ""} {
		if _, problem := NormalizePatch(declared, "--- "+declared+"\n+++ "+declared+"\n@@ -1 +1 @@\n-a\n+b\n"); problem == nil {
			t.Errorf("declared path %q was accepted by the normalizer", declared)
		}
	}
}

// ---/+++ that name different files are still refused after normalization.
func TestAMismatchedHeaderPairIsStillRefused(t *testing.T) {
	other := "internal/identifiers/other.go"
	for name, raw := range map[string]string{
		"prefixed":   "--- a/" + testPath + "\n+++ b/" + other + "\n@@ -1,1 +1,2 @@\n a\n+b\n",
		"unprefixed": "--- " + testPath + "\n+++ " + other + "\n@@ -1,1 +1,2 @@\n a\n+b\n",
		"rename":     "--- a/" + other + "\n+++ b/" + testPath + "\n@@ -1,1 +1,2 @@\n a\n+b\n",
	} {
		t.Run(name, func(t *testing.T) {
			got := normalize(t, testPath, raw)
			problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch})
			if problem == nil || problem.Check != CheckDeclaredPath {
				t.Fatalf("problem = %v", problem)
			}
		})
	}
}

// Where two readings are possible the host refuses instead of choosing.
func TestAmbiguityFailsClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		raw   string
		check string
		text  string
	}{
		"trailing empty line in a hunk that needs a recount": {
			raw:   diffFor(testPath, "@@ -1,9 +1,9 @@\n a\n+b\n\n"),
			check: CheckHunkBody, text: "empty line",
		},
		"a line that is not a hunk line": {
			raw:   diffFor(testPath, "@@ -1,9 +1,9 @@\n a\nb\n"),
			check: CheckHunkBody, text: "does not begin with",
		},
		"another file section": {
			raw:   diffFor(testPath, "@@ -1,9 +1,9 @@\n a\n+b\ndiff --git a/x b/x\n"),
			check: CheckUnifiedDiff, text: "another file section",
		},
		"two file headers": {
			raw:   "--- " + testPath + "\n+++ " + testPath + "\n--- " + testPath + "\n+++ " + testPath + "\n@@ -1 +1,2 @@\n a\n+b\n",
			check: CheckUnifiedDiff, text: "ambiguous",
		},
		"a header without its pair": {
			raw:   "--- " + testPath + "\n@@ -1 +1,2 @@\n a\n+b\n",
			check: CheckUnifiedDiff, text: "ambiguous",
		},
		"a hunk with no body": {
			raw:   diffFor(testPath, "@@ -1,2 +1,2 @@\n@@ -9,1 +9,2 @@\n a\n+b\n"),
			check: CheckHunkBody, text: "no body",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, problem := NormalizePatch(testPath, tc.raw)
			if problem == nil {
				t.Fatalf("an ambiguous patch was canonicalized:\n%s", tc.raw)
			}
			if problem.Check != tc.check || !strings.Contains(problem.Detail, tc.text) {
				t.Fatalf("problem = %v, want %s mentioning %q", problem, tc.check, tc.text)
			}
		})
	}
	// The same trailing empty line in a hunk that is already consistent is not the
	// normalizer's business: it changes nothing and CheckPatchStructure judges it as it
	// always did.
	raw := diffFor(testPath, "@@ -1,2 +1,3 @@\n a\n+b\n c\n\n")
	if got := normalize(t, testPath, raw); got.Patch != raw {
		t.Fatalf("a consistent patch with trailing padding was altered:\n%s", got.Patch)
	}
}

// A declared path that itself begins with a/ makes "--- a/x" mean two different things.
func TestADeclaredPathThatLooksLikeAPrefixIsNotRewritten(t *testing.T) {
	raw := "--- a/x.go\n+++ a/x.go\n@@ -1,1 +1,2 @@\n a\n+b\n"
	got := normalize(t, "a/x.go", raw)
	if got.Patch != raw {
		t.Fatalf("an ambiguous header was rewritten:\n%s", got.Patch)
	}
}

// What the normalizer does not understand it hands back untouched, so the structural
// check gives the precise diagnosis: a placeholder does not become a coordinate.
func TestWhatItCannotRecountIsHandedBackUntouched(t *testing.T) {
	for name, raw := range map[string]string{
		"placeholder": diffFor(testPath, "@@ -X,X +X,X @@\n a\n+b\n"),
		"no hunk":     "--- " + testPath + "\n+++ " + testPath + "\n",
		"not a diff":  "not a diff at all",
		"empty":       "",
	} {
		t.Run(name, func(t *testing.T) {
			got := normalize(t, testPath, raw)
			if got.Patch != raw || len(got.Provenance.Normalizations) != 0 {
				t.Fatalf("patch = %q, normalizations = %v", got.Patch, got.Provenance.Normalizations)
			}
			if problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch}); problem == nil {
				t.Fatal("a patch the normalizer could not read was accepted")
			}
		})
	}
	// A placeholder next to a hunk that CAN be recounted: only the latter is rewritten.
	raw := diffFor(testPath, "@@ -1,9 +1,9 @@\n a\n+b\n@@ -X,X +X,X @@\n c\n")
	got := normalize(t, testPath, raw)
	if !strings.Contains(got.Patch, "@@ -1,1 +1,2 @@") || !strings.Contains(got.Patch, "@@ -X,X +X,X @@") {
		t.Fatalf("patch =\n%s", got.Patch)
	}
	if problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch}); problem == nil || problem.Check != CheckHunkHeader {
		t.Fatalf("the placeholder hunk survived normalization: %v", problem)
	}
}

// A patch that is wrong in CONTENT stays wrong. Content is not the host's to fix, and
// nothing here is about where a hunk lands; the same is proven against real git in
// internal/executive/bootstrap.
func TestNormalizationDoesNotMakeAnInvalidPatchValid(t *testing.T) {
	// The body is untouched, so what it says about the file is untouched: a hunk that
	// removes a line no one would find there still removes it.
	raw := "--- " + testPath + "\n+++ " + testPath + "\n@@ -300,9 +300,9 @@\n-this line is not in the file\n+replacement\n"
	got := normalize(t, testPath, raw)
	if !reflect.DeepEqual(bodyLines(got.Patch), []string{"-this line is not in the file", "+replacement"}) {
		t.Fatalf("body = %v", bodyLines(got.Patch))
	}
	if !strings.Contains(got.Patch, "@@ -300,1 +300,1 @@") {
		t.Fatalf("the offset was changed:\n%s", got.Patch)
	}
}

func TestProvenanceProvesTheBodyWasNotAltered(t *testing.T) {
	got := normalize(t, testPath, root1062Patch)
	provenance := got.Provenance
	if provenance.Path != testPath {
		t.Fatalf("path = %q", provenance.Path)
	}
	if provenance.RawSHA256 != sha256Hex(root1062Patch) || provenance.NormalizedSHA256 != sha256Hex(got.Patch) || provenance.RawSHA256 == provenance.NormalizedSHA256 {
		t.Fatalf("digests do not describe the two representations: %+v", provenance)
	}
	// The body digest of the RAW text, computed independently of the normalizer.
	if want := bodyDigest(strings.Split(strings.TrimSuffix(root1062Patch, "\n"), "\n")); provenance.BodySHA256 != want {
		t.Fatalf("hunk_body_sha256 = %s, the raw body hashes to %s", provenance.BodySHA256, want)
	}
	// And that digest is sensitive to a body edit, so equality means something.
	edited := strings.Replace(root1062Patch, "+\t\t{\"digits", "+\t\t{\"Digits", 1)
	if bodyDigest(strings.Split(strings.TrimSuffix(edited, "\n"), "\n")) == provenance.BodySHA256 {
		t.Fatal("the body digest does not change when a body line does")
	}
}

// Derive is the only place a mission's operations are made, and it carries the
// canonical patch: what the host validated is what the code-runner applies.
func TestDeriveCarriesTheCanonicalPatchAndItsProvenance(t *testing.T) {
	derived, err := Derive(Request{
		BaseSHA: strings.Repeat("a", 40), Scope: ScopeInternalCode, Objective: "Add the case.",
		Changes:            []Change{{Path: testPath, Intent: "case", Patch: root1062Patch}},
		AcceptanceCriteria: []string{"tests pass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical := normalize(t, testPath, root1062Patch)
	var applied []string
	for _, operation := range derived.Plan.Operations {
		if operation.Type == coderunner.ApplyPatch {
			applied = append(applied, operation.Patch)
		}
	}
	if len(applied) != 1 || applied[0] != canonical.Patch {
		t.Fatalf("the mission applies %q, want the canonical patch %q", applied, canonical.Patch)
	}
	if len(derived.Patches) != 1 || derived.Patches[0].RawSHA256 != sha256Hex(root1062Patch) || derived.Patches[0].NormalizedSHA256 != sha256Hex(applied[0]) {
		t.Fatalf("provenance = %+v", derived.Patches)
	}
	// The generated plan still parses in the code-runner, whose own path rules see the
	// canonical headers.
	encoded, err := EncodePlan(derived.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = coderunner.ParsePlan(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestDeriveRefusesAPatchTheHostCannotCanonicalize(t *testing.T) {
	_, err := Derive(Request{
		BaseSHA: strings.Repeat("a", 40), Scope: ScopeInternalCode, Objective: "x",
		Changes:            []Change{{Path: testPath, Patch: diffFor(testPath, "@@ -1,9 +1,9 @@\n a\nb\n")}},
		AcceptanceCriteria: []string{"tests pass"},
	})
	if !errors.Is(err, ErrPlanInvalid) || !strings.Contains(err.Error(), "cannot be made canonical") {
		t.Fatalf("err = %v", err)
	}
}

// Properties over many generated patches. The generator writes hunks with random
// bodies -- including lines that look like headers or markers -- and then breaks
// their headers the way the model does.
func TestNormalizationProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(1062))
	contents := []string{"", "x", "\t\t{\"a\", \"b\"},", "-- comment", "++ plus", "@ at", "\\ backslash", "diff --git not a header", "@@ inside", "  two spaces", "\ttab"}
	for round := 0; round < 400; round++ {
		var b strings.Builder
		prefix := []string{"", "a/"}[rng.Intn(2)]
		newPrefix := []string{"", "b/"}[rng.Intn(2)]
		if rng.Intn(3) == 0 {
			b.WriteString("diff --git a/" + testPath + " b/" + testPath + "\n")
		}
		b.WriteString("--- " + prefix + testPath + "\n+++ " + newPrefix + testPath + "\n")
		wantOld, wantNew := []int{}, []int{}
		for h := 0; h <= rng.Intn(3); h++ {
			oldN, newN := 0, 0
			var body strings.Builder
			for l := 0; l <= rng.Intn(8); l++ {
				text := contents[rng.Intn(len(contents))]
				switch rng.Intn(3) {
				case 0:
					body.WriteString(" " + text + "\n")
					oldN++
					newN++
				case 1:
					body.WriteString("-" + text + "\n")
					oldN++
				default:
					body.WriteString("+" + text + "\n")
					newN++
				}
			}
			wantOld, wantNew = append(wantOld, oldN), append(wantNew, newN)
			declaredOld, declaredNew := oldN, newN
			if rng.Intn(3) != 0 {
				declaredOld, declaredNew = rng.Intn(20), rng.Intn(20)
			}
			heading := []string{"", " func F() {"}[rng.Intn(2)]
			fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@%s\n%s", 1+h*30, declaredOld, 1+h*30, declaredNew, heading, body.String())
		}
		raw := b.String()

		got, problem := NormalizePatch(testPath, raw)
		if problem != nil {
			t.Fatalf("round %d: refused an unambiguous patch: %v\n%s", round, problem, raw)
		}
		// 1. Every body line is byte for byte what the model wrote, and no line was added or removed.
		if !reflect.DeepEqual(bodyLines(got.Patch), bodyLines(raw)) {
			t.Fatalf("round %d: a body line changed\nraw:\n%s\nnormalized:\n%s", round, raw, got.Patch)
		}
		if strings.Count(got.Patch, "\n") != strings.Count(raw, "\n") {
			t.Fatalf("round %d: the number of lines changed", round)
		}
		// Only header lines differ.
		rawHead, gotHead := headerLines(raw), headerLines(got.Patch)
		if len(rawHead) != len(gotHead) {
			t.Fatalf("round %d: header line count changed", round)
		}
		for i := range rawHead {
			if rawHead[i] != gotHead[i] && !strings.HasPrefix(rawHead[i], "@@") && !strings.HasPrefix(rawHead[i], "--- ") && !strings.HasPrefix(rawHead[i], "+++ ") {
				t.Fatalf("round %d: a non-header line changed: %q -> %q", round, rawHead[i], gotHead[i])
			}
		}
		// 3. The recounted headers are right.
		hunk := 0
		for _, line := range strings.Split(got.Patch, "\n") {
			if m := hunkHeaderPattern.FindStringSubmatch(line); m != nil {
				if hunkCount(m[2]) != wantOld[hunk] || hunkCount(m[4]) != wantNew[hunk] {
					t.Fatalf("round %d: hunk %d header %q, want -%d +%d\n%s", round, hunk, line, wantOld[hunk], wantNew[hunk], got.Patch)
				}
				hunk++
			}
		}
		if hunk != len(wantOld) {
			t.Fatalf("round %d: %d headers, want %d", round, hunk, len(wantOld))
		}
		// 2. Idempotent.
		again, problem := NormalizePatch(testPath, got.Patch)
		if problem != nil || again.Patch != got.Patch || len(again.Provenance.Normalizations) != 0 {
			t.Fatalf("round %d: normalizing twice changed the patch (%v, %v)\n%s\n->\n%s", round, problem, again.Provenance.Normalizations, got.Patch, again.Patch)
		}
		// A patch that needed no rewrite comes back identical.
		if len(got.Provenance.Normalizations) == 0 && got.Patch != raw {
			t.Fatalf("round %d: no normalization reported but the patch differs", round)
		}
	}
}

// "\ No newline at end of file" describes the line before it; it is not a line of the
// old or the new file, so it is never counted.
func TestNoNewlineMarkersAreNotCountedWhenRecounting(t *testing.T) {
	raw := diffFor(testPath, "@@ -1,9 +1,9 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file\n")
	got := normalize(t, testPath, raw)
	if !strings.Contains(got.Patch, "\n@@ -1,2 +1,2 @@\n") {
		t.Fatalf("patch =\n%s", got.Patch)
	}
	if !reflect.DeepEqual(bodyLines(got.Patch), bodyLines(raw)) {
		t.Fatal("a marker line was changed")
	}
	if problem := CheckPatchStructure(Change{Path: testPath, Patch: got.Patch}); problem != nil {
		t.Fatalf("canonical patch refused: %v", problem)
	}
}
