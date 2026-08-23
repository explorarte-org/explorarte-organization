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
// OrganizationalSource is one piece of repository evidence together with the
// citation that names it. The reference travels so that a refusal can say
// WHICH evidence was reproduced: a path, a line range and a commit are
// provenance metadata, which may cross the boundary that the source text may
// not. Naming it costs nothing and saves reconstructing the match by hand from
// the database, which is what AUTONOMY-SMOKE-017-R2 required.
type OrganizationalSource struct {
	Reference string
	Content   string
}

func DeclassifyCandidate(candidate string, organizational []OrganizationalSource) error {
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
		for _, haystack := range haystacks {
			if run, shared := sharedRun(source.Content, haystack); shared {
				if reference := strings.TrimSpace(source.Reference); reference != "" {
					return fmt.Errorf("%w: it reproduces %d characters of %s",
						ErrCandidateContaminated, len(run), reference)
				}
				return fmt.Errorf("%w: it reproduces %d characters of source", ErrCandidateContaminated, len(run))
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

// sharedRun reports the first contiguous span of at least declassifyMinimumRun
// characters that the candidate and the source have in common, in normal form.
//
// A path, a symbol or a short idiomatic fragment must cross freely: those are
// the metadata a grounded claim is made of. A long contiguous span of the
// source is not a reference to the code, it is the code.
//
// The comparison is exact at the stated threshold rather than sampled. An
// earlier version windowed the SOURCE at a stride and asked whether the
// candidate contained one of those windows, which made detection depend on
// where the windows happened to land: a rendered excerpt carries a one-line
// provenance header, so for a short body every window straddled the header and
// no window lay inside the source text at all. A verbatim copy of a 53-char
// function then crossed while the threshold said 48. Indexing every offset of
// the source removes the alignment luck -- any shared run of the threshold
// length is found, whatever the payload's framing.
//
// Lines are joined before comparison so that a copy which re-wraps or
// re-indents the original still matches: the run is over the normalized text,
// not over the file's layout.
func sharedRun(source, haystack string) (string, bool) {
	normalized := normalizeForDeclassify(source)
	if len(normalized) < declassifyMinimumRun || len(haystack) < declassifyMinimumRun {
		return "", false
	}
	// Hashing the source's windows keeps the index linear in the evidence
	// size; a hit is confirmed against the text itself, so a collision
	// cannot invent contamination that is not there.
	index := make(map[uint64]struct{}, len(normalized))
	for start := 0; start+declassifyMinimumRun <= len(normalized); start++ {
		index[declassifyHash(normalized[start:start+declassifyMinimumRun])] = struct{}{}
	}
	for start := 0; start+declassifyMinimumRun <= len(haystack); start++ {
		run := haystack[start : start+declassifyMinimumRun]
		if _, candidate := index[declassifyHash(run)]; !candidate {
			continue
		}
		if strings.Contains(normalized, run) {
			return run, true
		}
	}
	return "", false
}

func declassifyHash(run string) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	hash := uint64(offset64)
	for i := 0; i < len(run); i++ {
		hash ^= uint64(run[i])
		hash *= prime64
	}
	return hash
}
