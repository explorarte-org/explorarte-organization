// Package repositoryevidence lets an agent that is authorized to modify the
// repository also observe it, in bounded excerpts tied to one exact commit.
//
// The organization could reason, review and refuse correctly, and could not
// design a change to its own implementation, because the design phase never
// saw the code. Asked for a concrete improvement to a named package, it
// produced plausible file names and test names it had no way to check; the
// adversarial reviewer correctly reported the references as unverifiable and
// the adjudicator correctly refused to freeze. Every round did its job
// perfectly on a premise that could not hold.
//
// The cure is not the repository in the prompt. It is a small number of
// excerpts that can be cited: a claim about the code becomes checkable when
// the reader can go to the same path, at the same commit, over the same lines.
//
// Two rules give the package its shape.
//
// Discovery and authority are separate. An index, a search, an embedding may
// suggest WHERE to look and may be out of date without harm. What is read is
// always the source at the exact commit, so a stale index can waste a lookup
// but can never make an agent design against code that no longer exists.
//
// Staleness is not a matter of degree. An excerpt from a different commit is
// not slightly worse evidence about this one -- it is evidence about a
// different repository. There is no threshold at which "recent enough"
// becomes true.
package repositoryevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	ErrInvalidFragment = errors.New("repositoryevidence: invalid fragment")
	ErrStaleEvidence   = errors.New("repositoryevidence: evidence cites a different commit")
	ErrBudgetExhausted = errors.New("repositoryevidence: exploration budget exhausted")
	// ErrNoEvidenceFound reports that an execution asked to observe code was
	// given none. It is an error rather than an empty result because the two
	// are indistinguishable to the caller and only one of them is safe.
	ErrNoEvidenceFound = errors.New("repositoryevidence: no evidence found for this execution")
)

var (
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	symbolPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.*()\[\] ]{0,119}$`)
)

// Fragment is one excerpt of the repository, at one commit.
//
// Every field exists so that a design claim resting on it can be checked by
// someone who was not there: the repository and commit say which world, the
// path and line range say where in it, and the digest says that what is quoted
// is what was read.
type Fragment struct {
	Repository string `json:"repository"`
	BaseSHA    string `json:"base_sha"`
	Path       string `json:"path"`
	// LineStart and LineEnd are 1-based and inclusive.
	LineStart int `json:"line_start"`
	LineEnd   int `json:"line_end"`
	// Symbol names what the range is, when the range was chosen because of a
	// declaration rather than by coordinates. Optional: a range is citable
	// without one, but a symbol makes a claim survive the lines moving.
	Symbol  string `json:"symbol,omitempty"`
	Content string `json:"content"`
	Digest  string `json:"digest"`
}

// Reference is the stable identity of an excerpt, and is what a design cites.
func (f Fragment) Reference() string {
	return fmt.Sprintf("repository://%s@%s/%s#L%d-L%d", f.Repository, f.BaseSHA, f.Path, f.LineStart, f.LineEnd)
}

// DigestOf is the one definition of a fragment's digest.
func DigestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Validate refuses a fragment that could not have come from a real read.
func (f Fragment) Validate() error {
	if strings.TrimSpace(f.Repository) == "" || len(f.Repository) > 200 {
		return fmt.Errorf("%w: repository is required", ErrInvalidFragment)
	}
	if !shaPattern.MatchString(f.BaseSHA) {
		return fmt.Errorf("%w: base_sha must be a full 40-character commit id, got %q", ErrInvalidFragment, f.BaseSHA)
	}
	if err := ValidatePath(f.Path); err != nil {
		return err
	}
	if f.LineStart < 1 || f.LineEnd < f.LineStart {
		return fmt.Errorf("%w: line range %d-%d is not a range", ErrInvalidFragment, f.LineStart, f.LineEnd)
	}
	if f.Symbol != "" && !symbolPattern.MatchString(f.Symbol) {
		return fmt.Errorf("%w: symbol %q is not a symbol name", ErrInvalidFragment, f.Symbol)
	}
	if f.Content == "" {
		return fmt.Errorf("%w: an excerpt with no content proves nothing", ErrInvalidFragment)
	}
	// The digest is what makes the quotation checkable. A fragment whose
	// content does not hash to its digest has been edited since it was read,
	// by something, and there is no safe way to tell what.
	if f.Digest != DigestOf(f.Content) {
		return fmt.Errorf("%w: digest does not match content for %s", ErrInvalidFragment, f.Reference())
	}
	return nil
}

// ValidatePath refuses anything that is not a plain repository-relative path.
func ValidatePath(candidate string) error {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" || len(trimmed) > 400 {
		return fmt.Errorf("%w: path is required and must be at most 400 bytes", ErrInvalidFragment)
	}
	if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, `\`) || strings.Contains(trimmed, "\x00") {
		return fmt.Errorf("%w: path %q must be repository-relative", ErrInvalidFragment, candidate)
	}
	if cleaned := path.Clean(trimmed); cleaned != trimmed || strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("%w: path %q must be clean and inside the repository", ErrInvalidFragment, candidate)
	}
	return nil
}

// EligibleEvidencePath is the single authority on which repository paths may
// become citable worker-facing evidence: plain repository-relative paths,
// excluding the test corpus.
//
// *_test.go files are excluded because their value is TESTING the code, not
// declaring or applying it, and their long explanatory comments are prose a
// designer will echo. AUTONOMY-SMOKE-017-R11 lost a campaign to exactly that:
// a candidate design reproduced a test file's commentary nearly verbatim and
// the egress gate refused the bundle. Keeping tests out of the diet starves
// that channel at the source instead of tuning the detector downstream.
//
// Every consumer must go through this one predicate -- discovery, direct
// reads and the git backend's own pre-filter -- so the three cannot disagree
// about what counts as evidence. A slot satisfiable only through a test file
// answers "not supplyable": the honest fail-closed outcome, not a softer one.
func EligibleEvidencePath(candidate string) bool {
	return ValidatePath(candidate) == nil && !strings.HasSuffix(candidate, "_test.go")
}

// ValidFor reports whether this excerpt is evidence about the given commit.
//
// Equality, not recency. An excerpt of the same file taken one commit earlier
// describes a repository that no longer exists, and a design resting on it is
// resting on nothing.
func (f Fragment) ValidFor(baseSHA string) error {
	if f.BaseSHA != baseSHA {
		return fmt.Errorf("%w: evidence cites %s but the design is about %s", ErrStaleEvidence, f.BaseSHA, baseSHA)
	}
	return nil
}

// ValidateBundle checks a whole set against the commit it must describe.
func ValidateBundle(fragments []Fragment, baseSHA string) error {
	for _, fragment := range fragments {
		if err := fragment.Validate(); err != nil {
			return err
		}
		if err := fragment.ValidFor(baseSHA); err != nil {
			return err
		}
	}
	return nil
}

// EvidenceSlot is one normative demand: a subject and ONE relation that must
// be grounded in a worker's snapshot. It is the unit the whole evidence
// contract speaks in -- adjudication proposes them, admission plans them,
// delivery pins them into snapshots, and the preflight verifies them -- so it
// travels through every layer intact. Collapsing slots back into bare
// subjects is what let R15's round 2 demand driveDesignFreeze/application and
// then receive only its declaration: the relation was lost before selection
// ever ran.
type EvidenceSlot struct {
	Subject  string `json:"subject"`
	Relation string `json:"relation"`
}

// Limits bound what one exploration may read.
//
// The point of giving the design phase eyes was never to hand it the whole
// repository: that would trade one failure for a more expensive one. To change
// a transition in the Executive an agent should read the three files that
// transition lives in.
type Limits struct {
	MaxFiles    int
	MaxRanges   int
	MaxBytes    int
	MaxSearches int
	MaxLines    int
}

// DefaultLimits is deliberately small.
func DefaultLimits() Limits {
	return Limits{MaxFiles: 8, MaxRanges: 16, MaxBytes: 96 * 1024, MaxSearches: 12, MaxLines: 400}
}

func (l Limits) Validate() error {
	if l.MaxFiles < 1 || l.MaxRanges < 1 || l.MaxBytes < 1 || l.MaxSearches < 1 || l.MaxLines < 1 {
		return fmt.Errorf("%w: every exploration limit must be positive", ErrInvalidFragment)
	}
	return nil
}
