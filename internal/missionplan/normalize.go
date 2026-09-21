package missionplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// A model writes the CONTENT of a patch well and its ARITHMETIC badly. Root 1062
// (2026-09-21) produced the right table row in the right place three times and got
// the hunk's line counts wrong three times, and wrote `--- internal/x.go` where a
// `git apply` with its default -p1 needs `--- a/internal/x.go`. Spending inferences
// to make a model do arithmetic and remember a path convention is spending them on
// the part of the artifact that is not a decision.
//
// NormalizePatch takes exactly that part out of the model's hands, and nothing else.
// It is the whole of what the host may change in a patch a model proposed:
//
//	hunk_recount                  the two counts in a hunk header are recomputed from
//	                              the hunk's own body
//	path_prefix_canonicalization  the declared path in `--- ` / `+++ ` becomes
//	                              `a/<path>` / `b/<path>`
//
// It never edits a body line, an offset, a context line, a file name other than the
// declared one, or the number of lines in the patch, and it never searches for where
// a hunk "should" go. Where two readings are possible it refuses instead of picking
// one, and the planner is asked again. The canonical patch is then judged like any
// other (CheckPatchStructure, then a plain `git apply --check`, no tolerance flags),
// so a wrong recount is still caught by git: the host's arithmetic is not trusted
// by the verifier that follows it.
const (
	NormalizationHunkRecount = "hunk_recount"
	NormalizationPathPrefix  = "path_prefix_canonicalization"
)

// PatchProvenance makes it demonstrable that the host did not alter what the model
// proposed. The raw patch is the model's own output (durable in its invocation
// result); the normalized patch is what the mission carries. BodySHA256 is computed
// over every line after the file header that is not a hunk header, on each side, and
// is asserted equal before a normalization is returned.
type PatchProvenance struct {
	Path             string   `json:"path"`
	RawSHA256        string   `json:"raw_patch_sha256"`
	NormalizedSHA256 string   `json:"normalized_patch_sha256"`
	BodySHA256       string   `json:"hunk_body_sha256"`
	Normalizations   []string `json:"normalizations"`
}

// NormalizedPatch is a patch in canonical form and how it got there.
type NormalizedPatch struct {
	Patch      string
	Provenance PatchProvenance
}

// NormalizePatch returns raw in canonical form for a change that declares
// declaredPath. A patch already in canonical form is returned byte for byte, with
// no normalizations, and normalizing a canonical patch again changes nothing.
//
// A patch it cannot make canonical WITHOUT choosing between readings, or that is not
// shaped like a single-file unified diff at all, yields a *PatchProblem. A patch it
// merely does not recognise (no hunk, a placeholder header) is returned unchanged
// for CheckPatchStructure to refuse with its own precise message: the normalizer
// invents nothing, including a diagnosis.
func NormalizePatch(declaredPath, raw string) (NormalizedPatch, *PatchProblem) {
	declared, err := normalizePath(declaredPath)
	if err != nil {
		return NormalizedPatch{}, &PatchProblem{Check: CheckDeclaredPath, Detail: err.Error()}
	}
	unchanged := func() (NormalizedPatch, *PatchProblem) {
		return NormalizedPatch{Patch: raw, Provenance: provenanceOf(declared, raw, raw, nil)}, nil
	}

	terminated := strings.HasSuffix(raw, "\n")
	text := raw
	if terminated {
		text = raw[:len(raw)-1]
	}
	lines := strings.Split(text, "\n")
	rawBody := bodyDigest(lines)

	first := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "@@") {
			first = i
			break
		}
	}
	if first < 0 {
		return unchanged()
	}

	var applied []string
	recordOnce := func(name string) {
		for _, have := range applied {
			if have == name {
				return
			}
		}
		applied = append(applied, name)
	}

	rewritten, problem := canonicalizeHeaders(lines[:first], declared)
	if problem != nil {
		return NormalizedPatch{}, problem
	}
	if rewritten {
		recordOnce(NormalizationPathPrefix)
	}

	// A hunk that is already consistent is not touched, however its header spells the
	// counts, so canonical input is a fixed point. `judged` ignores trailing empty
	// lines exactly as CheckPatchStructure does, so "already consistent" means the
	// same thing in both.
	judged := patchLines(strings.Join(lines, "\n"))
	headers := hunkHeaderIndexes(lines, first)
	for k, at := range headers {
		if _, problem := checkHunk(judged, at); problem == nil {
			continue
		}
		match := hunkHeaderPattern.FindStringSubmatch(lines[at])
		if match == nil {
			// Not a header with real coordinates (a placeholder such as X): there is
			// nothing to recount and nothing to invent. The structural check refuses it.
			continue
		}
		end := len(lines)
		if k+1 < len(headers) {
			end = headers[k+1]
		}
		oldCount, newCount, recountProblem := recountHunk(lines, at, end)
		if recountProblem != nil {
			return NormalizedPatch{}, recountProblem
		}
		lines[at] = fmt.Sprintf("@@ -%s,%d +%s,%d @@%s", match[1], oldCount, match[3], newCount, match[5])
		recordOnce(NormalizationHunkRecount)
	}

	normalized := strings.Join(lines, "\n")
	if terminated {
		normalized += "\n"
	}
	// The promise, checked rather than assumed: no body line changed, and no line was
	// added or removed. A normalizer that violated it must not hand its output to
	// anything that would execute it.
	if bodyDigest(lines) != rawBody || strings.Count(normalized, "\n") != strings.Count(raw, "\n") {
		return NormalizedPatch{}, &PatchProblem{Check: CheckUnifiedDiff, Detail: "the patch could not be canonicalized without changing more than header metadata; it was not modified"}
	}
	return NormalizedPatch{Patch: normalized, Provenance: provenanceOf(declared, raw, normalized, applied)}, nil
}

// canonicalizeHeaders rewrites, in the preamble before the first hunk, the declared
// path of the `--- ` and `+++ ` lines into a/ and b/ form. It reports whether it
// changed anything.
//
// It rewrites a header only when the path it names IS the declared path, spelled
// without a prefix. `/dev/null` (create and delete) is left alone, and so is any
// other path: a header naming some other file is refused by the declared-path check,
// and rewriting it here could only help it through. A declared path that itself
// starts with a/ or b/ makes an unprefixed header indistinguishable from a prefixed
// one for a different file, so it is not rewritten either.
func canonicalizeHeaders(preamble []string, declared string) (bool, *PatchProblem) {
	var olds, news []int
	for i, line := range preamble {
		switch {
		case strings.HasPrefix(line, "--- "):
			olds = append(olds, i)
		case strings.HasPrefix(line, "+++ "):
			news = append(news, i)
		}
	}
	if len(olds) == 0 && len(news) == 0 {
		return false, nil
	}
	if len(olds) != 1 || len(news) != 1 || news[0] != olds[0]+1 {
		return false, &PatchProblem{Check: CheckUnifiedDiff, Detail: "the file header is ambiguous: exactly one `--- ` line immediately followed by one `+++ ` line is expected before the first hunk, so the host cannot tell which path to canonicalize"}
	}
	if strings.HasPrefix(declared, "a/") || strings.HasPrefix(declared, "b/") {
		return false, nil
	}
	changed := false
	for _, target := range []struct {
		at     int
		marker string
		prefix string
	}{{olds[0], "--- ", "a/"}, {news[0], "+++ ", "b/"}} {
		rest := strings.TrimPrefix(preamble[target.at], target.marker)
		token, suffix := rest, ""
		if tab := strings.IndexByte(rest, '\t'); tab >= 0 {
			token, suffix = rest[:tab], rest[tab:]
		}
		if token != declared {
			continue
		}
		preamble[target.at] = target.marker + target.prefix + declared + suffix
		changed = true
	}
	return changed, nil
}

// hunkHeaderIndexes are the lines that start a hunk. A body line begins with ' ',
// '+', '-' or '\', so a line beginning "@@" is always a header.
func hunkHeaderIndexes(lines []string, from int) []int {
	var indexes []int
	for i := from; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "@@") {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

// recountHunk reads the hunk whose header is lines[at] and whose body is the lines up
// to end, and returns how many old-side and new-side lines that body has. It refuses
// whenever a line's role is not certain.
func recountHunk(lines []string, at, end int) (oldCount, newCount int, problem *PatchProblem) {
	start := at + 1
	region := lines[at+1 : end]
	if len(region) == 0 {
		return 0, 0, &PatchProblem{Check: CheckHunkBody, Detail: fmt.Sprintf("the hunk that starts at line %d has no body lines", start)}
	}
	for offset, line := range region {
		number := at + 2 + offset
		switch {
		case strings.HasPrefix(line, "diff "):
			return 0, 0, &PatchProblem{Check: CheckUnifiedDiff, Detail: fmt.Sprintf(
				"line %d starts another file section; a change's patch is a single-file diff", number)}
		case line == "" || line[0] == ' ':
			oldCount++
			newCount++
		case line[0] == '-':
			oldCount++
		case line[0] == '+':
			newCount++
		case line[0] == '\\':
			// "\ No newline at end of file" describes the previous line.
		default:
			return 0, 0, &PatchProblem{Check: CheckHunkBody, Detail: fmt.Sprintf(
				"line %d: %q inside the hunk that starts at line %d does not begin with ' ', '+' or '-'; the host recomputes a header's counts but does not guess what a line is",
				number, truncateForFeedback(line, 80), start)}
		}
	}
	// An empty last line is a blank context line written without its space, or the
	// padding a tool leaves after a patch. Counting it, or not, changes the hunk.
	if region[len(region)-1] == "" {
		return 0, 0, &PatchProblem{Check: CheckHunkBody, Detail: fmt.Sprintf(
			"the hunk that starts at line %d ends with an empty line, which could be a blank context line or padding, so its counts cannot be recomputed unambiguously; write a blank context line as a single space",
			start)}
	}
	if oldCount == 0 && newCount == 0 {
		return 0, 0, &PatchProblem{Check: CheckHunkBody, Detail: fmt.Sprintf("the hunk that starts at line %d has no context, removed or added lines", start)}
	}
	return oldCount, newCount, nil
}

// bodyDigest hashes every line after the first hunk header that is not itself a hunk
// header: the hunk bodies, in order, and nothing the host may rewrite.
func bodyDigest(lines []string) string {
	sum := sha256.New()
	seenHunk := false
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			seenHunk = true
			continue
		}
		if !seenHunk {
			continue
		}
		sum.Write([]byte(strconv.Itoa(len(line))))
		sum.Write([]byte{':'})
		sum.Write([]byte(line))
		sum.Write([]byte{'\n'})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func provenanceOf(path, raw, normalized string, applied []string) PatchProvenance {
	terminated := strings.TrimSuffix(normalized, "\n")
	return PatchProvenance{
		Path:             path,
		RawSHA256:        sha256Hex(raw),
		NormalizedSHA256: sha256Hex(normalized),
		BodySHA256:       bodyDigest(strings.Split(terminated, "\n")),
		Normalizations:   append([]string{}, applied...),
	}
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
