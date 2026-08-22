package executive

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ErrCandidateContaminated reports that a candidate design carries
// organizational repository source, or a reversible encoding of it, toward a
// reviewer not authorized to receive either.
var ErrCandidateContaminated = fmt.Errorf("%w: candidate design carries organizational repository source", ErrContractRejected)

// The boundary this enforces, stated precisely because a loose version of it
// is what let the bypass exist:
//
//	ALLOWED   semantic claims and provenance metadata about the repository --
//	          "driveDepartments mixes planning and coordination", a path, a
//	          symbol, a commit, a repository:// citation.
//	FORBIDDEN organizational source text, verbatim or near-verbatim.
//	FORBIDDEN any reversible representation of that text: base64, hex,
//	          JSON/Unicode escapes, or a code fence containing it. Changing
//	          the container's bytes does not stop it being exfiltration.
//
// What this does NOT claim: non-interference. A model that has read
// organizational data and is allowed to emit free text always has a
// steganographic channel, and repository evidence is untrusted input that can
// instruct the designer to use one. A cryptographically strong guarantee would
// require forbidding free text from that model altogether, which would leave
// the reviewer nothing to review.
//
// So this is a deliberate threat model, not a proof: it blocks reproduction --
// verbatim, whitespace-shifted, fenced, escaped, encoded -- and permits
// non-reversible abstraction. DataSanitized means exactly that here, and
// claiming more of it would be the same overreach that produced the bypass.
const declassifyMinimumRun = 48

var (
	base64Blob = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)
	hexBlob    = regexp.MustCompile(`(?i)(?:[0-9a-f]{2}[\s,]*){24,}`)
	escapeSeq  = regexp.MustCompile(`\\u[0-9a-fA-F]{4}|\\x[0-9a-fA-F]{2}|\\n|\\t|\\"|\\\\`)
)

// DeclassifyCandidate refuses a candidate design that reproduces organizational
// source, in any representation this host knows how to reverse.
//
// It refuses rather than redacting. Redaction would leave the reviewer judging
// sanitize(D) while the artifact's digest asserts the designer produced D --
// a fresh discrepancy between what was decided and what was reviewed, which is
// the class of defect this whole subsystem exists to remove.
//
// The comparison is against the union of ALL organizational evidence that fed
// the contributing deliverables, not only the evidence of whichever
// deliverable wrote a given passage. Egress is not a property of the author:
// if these bytes are organizational source and they are about to leave, it
// does not matter who put them there.
func DeclassifyCandidate(candidate string, organizational []string) error {
	if strings.TrimSpace(candidate) == "" || len(organizational) == 0 {
		return nil
	}
	// Every representation is reduced to the same normal form before
	// comparison, so a difference that only exists in the container -- an
	// indent, a fence, an escape, an encoding -- cannot hide the payload.
	haystacks := []string{normalizeForDeclassify(candidate)}
	for _, decoded := range reversibleDecodings(candidate) {
		haystacks = append(haystacks, normalizeForDeclassify(decoded))
	}

	for _, source := range organizational {
		for _, run := range significantRuns(source) {
			for _, haystack := range haystacks {
				if strings.Contains(haystack, run) {
					return fmt.Errorf("%w: it reproduces %d characters of source", ErrCandidateContaminated, len(run))
				}
			}
		}
	}
	return nil
}

// normalizeForDeclassify collapses everything that differs between a copy and
// its original without changing what the copy says: whitespace, indentation,
// line breaks, and the escape sequences a JSON or Markdown container adds.
func normalizeForDeclassify(text string) string {
	text = escapeSeq.ReplaceAllString(text, " ")
	var out strings.Builder
	out.Grow(len(text))
	space := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && out.Len() > 0 {
			out.WriteRune(' ')
		}
		space = false
		out.WriteRune(unicode.ToLower(r))
	}
	return out.String()
}

// reversibleDecodings returns what the candidate becomes if the encodings this
// host knows are undone. A payload the host can reverse is a payload the
// reviewer's provider can reverse.
func reversibleDecodings(candidate string) []string {
	var decoded []string
	for _, blob := range base64Blob.FindAllString(candidate, -1) {
		for _, padding := range []string{"", "=", "=="} {
			if raw, err := base64.StdEncoding.DecodeString(blob + padding); err == nil && len(raw) > 16 {
				decoded = append(decoded, string(raw))
				break
			}
		}
		if raw, err := base64.RawURLEncoding.DecodeString(blob); err == nil && len(raw) > 16 {
			decoded = append(decoded, string(raw))
		}
	}
	for _, blob := range hexBlob.FindAllString(candidate, -1) {
		cleaned := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || r == ',' {
				return -1
			}
			return r
		}, blob)
		if len(cleaned)%2 == 1 {
			cleaned = cleaned[:len(cleaned)-1]
		}
		if raw, err := hex.DecodeString(cleaned); err == nil && len(raw) > 16 {
			decoded = append(decoded, string(raw))
		}
	}
	return decoded
}

// significantRuns is what counts as reproduction rather than reference.
//
// A path, a symbol or a short idiomatic fragment must cross freely: those are
// the metadata a grounded claim is made of. A long contiguous span of the
// source is not a reference to the code, it is the code.
//
// Lines are joined before slicing so that a copy which re-wraps or re-indents
// the original still matches: the run is over the normalized text, not over
// the file's layout.
func significantRuns(source string) []string {
	normalized := normalizeForDeclassify(source)
	if len(normalized) < declassifyMinimumRun {
		return nil
	}
	runs := make([]string, 0, 8)
	step := declassifyMinimumRun / 2
	for start := 0; start+declassifyMinimumRun <= len(normalized); start += step {
		runs = append(runs, normalized[start:start+declassifyMinimumRun])
	}
	return runs
}
