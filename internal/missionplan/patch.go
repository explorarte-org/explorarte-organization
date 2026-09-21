package missionplan

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
)

// PatchProblem says why one planned change's patch cannot be applied, in words a
// planner can act on. It is the deterministic half of validating a patch: what can
// be known from the diff text alone, before any repository is consulted. Whether
// the diff applies to the frozen tree is git's question, asked by the host through
// a port (executive.PatchWorkbench).
//
// The check exists because a unified diff written by a model is an artifact with a
// fixed grammar, and the commonest ways it is wrong are mechanical: a hunk header
// with placeholder coordinates, line counts that do not match the body, a patch
// that touches a file other than the one the plan declared. Each of those is caught
// here with a precise message, so the planner can regenerate the artifact instead of
// the code-runner discovering, five attempts later, that it never applied.
type PatchProblem struct {
	// Check names the rule that failed (stable, for metrics and tests).
	Check string
	// Detail is the specific finding, bounded and free of secrets.
	Detail string
}

func (p PatchProblem) Error() string { return p.Check + ": " + p.Detail }

const (
	CheckUnifiedDiff  = "unified_diff"
	CheckDeclaredPath = "declared_path"
	CheckHunkHeader   = "hunk_header"
	CheckHunkBody     = "hunk_body"
)

// hunkHeaderPattern is the whole grammar of a unified-diff hunk header:
// "@@ -start[,count] +start[,count] @@", optionally followed by a section heading.
var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: .*)?$`)

// CheckPatchStructure validates a change's patch from its text alone. It returns
// nil when the diff is well formed, names exactly the declared path, and every
// hunk's body matches the counts its header declares.
func CheckPatchStructure(change Change) *PatchProblem {
	declared, err := normalizePath(change.Path)
	if err != nil {
		return &PatchProblem{Check: CheckDeclaredPath, Detail: err.Error()}
	}
	paths, err := coderunner.ExtractPatchPaths(change.Patch)
	if err != nil {
		return &PatchProblem{Check: CheckUnifiedDiff, Detail: err.Error()}
	}
	sort.Strings(paths)
	for _, touched := range paths {
		if touched != declared {
			return &PatchProblem{Check: CheckDeclaredPath, Detail: fmt.Sprintf(
				"the patch touches %q but the change declares %q; a change's patch may touch only its own declared path (patch paths: %s)",
				touched, declared, strings.Join(paths, ", "))}
		}
	}
	return checkHunks(change.Patch)
}

func checkHunks(patch string) *PatchProblem {
	lines := strings.Split(patch, "\n")
	for n := len(lines); n > 0 && lines[n-1] == ""; n = len(lines) {
		lines = lines[:n-1]
	}
	hunks := 0
	for i := 0; i < len(lines); {
		line := lines[i]
		if !strings.HasPrefix(line, "@@") {
			i++
			continue
		}
		header := hunkHeaderPattern.FindStringSubmatch(line)
		if header == nil {
			return &PatchProblem{Check: CheckHunkHeader, Detail: fmt.Sprintf(
				"line %d: %q is not a valid hunk header. A header is \"@@ -<start>[,<count>] +<start>[,<count>] @@\" with the real numbers of the source file; placeholder coordinates (for example X) are not coordinates",
				i+1, truncateForFeedback(line, 80))}
		}
		hunks++
		oldRemaining, newRemaining := hunkCount(header[2]), hunkCount(header[4])
		start := i + 1
		i++
		for (oldRemaining > 0 || newRemaining > 0) && i < len(lines) {
			body := lines[i]
			switch {
			case body == "" || body[0] == ' ':
				oldRemaining--
				newRemaining--
			case body[0] == '-':
				oldRemaining--
			case body[0] == '+':
				newRemaining--
			case body[0] == '\\':
				// "\ No newline at end of file" describes the previous line.
			default:
				return &PatchProblem{Check: CheckHunkBody, Detail: fmt.Sprintf(
					"line %d: %q inside the hunk that starts at line %d does not begin with ' ', '+' or '-'", i+1, truncateForFeedback(body, 80), start)}
			}
			i++
			if oldRemaining < 0 || newRemaining < 0 {
				return &PatchProblem{Check: CheckHunkBody, Detail: fmt.Sprintf(
					"the hunk that starts at line %d has more lines than its header declares", start)}
			}
		}
		if oldRemaining > 0 || newRemaining > 0 {
			return &PatchProblem{Check: CheckHunkBody, Detail: fmt.Sprintf(
				"the hunk that starts at line %d declares %s old and %s new lines but its body is %d old and %d new lines short; recount the header from the body",
				start, header[2]+orOne(header[2]), header[4]+orOne(header[4]), oldRemaining, newRemaining)}
		}
		for i < len(lines) && strings.HasPrefix(lines[i], "\\") {
			i++
		}
		if i < len(lines) && !strings.HasPrefix(lines[i], "@@") && (lines[i] == "" || lines[i][0] == ' ' || lines[i][0] == '+' || lines[i][0] == '-') && !strings.HasPrefix(lines[i], "diff ") {
			return &PatchProblem{Check: CheckHunkBody, Detail: fmt.Sprintf(
				"line %d follows a complete hunk but is not a hunk header: the hunk that starts at line %d has more lines than its header declares", i+1, start)}
		}
	}
	if hunks == 0 {
		return &PatchProblem{Check: CheckHunkHeader, Detail: "the patch has no hunk (no \"@@ -a,b +c,d @@\" header)"}
	}
	return nil
}

// hunkCount reads a header count; an omitted count means one line.
func hunkCount(raw string) int {
	if raw == "" {
		return 1
	}
	count, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return count
}

func orOne(raw string) string {
	if raw == "" {
		return " (default 1)"
	}
	return ""
}

func truncateForFeedback(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

// PathPermitted reports whether a mission of the given scope may ever touch path:
// the same answer Derive gives (clean path, not structurally denied, not kernel
// governance, inside the scope). It lets the host decide which files are worth
// showing a planner without reading anything outside what the mission could change.
func PathPermitted(scope Scope, raw string) bool {
	clean, err := normalizePath(raw)
	if err != nil || denied(clean) != nil {
		return false
	}
	prefixes, known := scopePrefixes[scope]
	return known && withinScope(clean, prefixes)
}
