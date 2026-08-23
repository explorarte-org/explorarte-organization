package repositoryevidence

import (
	"context"
	"fmt"
	"strings"
)

// Source is the repository, read at one commit.
//
// Every method takes the commit explicitly. There is no "current" anything
// here: a Source that could answer about HEAD would eventually be asked to,
// and the answer would be evidence about a repository nobody decided on.
type Source interface {
	// Search returns candidate LOCATIONS for a query. It is DISCOVERY: it
	// may be backed by an index, may be approximate, and may be stale
	// without consequence, because nothing it returns is quoted to anyone.
	//
	// Locations rather than paths, because "which file mentions this" is
	// rarely the question. What a designer needs is the code around the
	// mention, and a path alone forces reading a file from the top and
	// hoping.
	Search(ctx context.Context, baseSHA, query string, limit int) ([]Match, error)
	// ReadRange returns the exact lines of a file at a commit. It is
	// AUTHORITY: everything an agent cites comes from here.
	ReadRange(ctx context.Context, baseSHA, path string, start, end int) (string, error)
	// Lines reports a file's length at a commit, so a range can be bounded
	// without reading the file to find out.
	Lines(ctx context.Context, baseSHA, path string) (int, error)
}

// Match is a place worth reading. It is not evidence: nothing here is quoted
// until the source is read at that location.
type Match struct {
	Path string
	Line int
}

// Explorer turns a question about the repository into citable excerpts.
type Explorer struct {
	Repository string
	BaseSHA    string
	Source     Source
	Limits     Limits

	searches int
	files    map[string]struct{}
	ranges   int
	bytes    int
}

func NewExplorer(repository, baseSHA string, source Source, limits Limits) (*Explorer, error) {
	if strings.TrimSpace(repository) == "" || source == nil {
		return nil, fmt.Errorf("%w: an explorer needs a repository and a source", ErrInvalidFragment)
	}
	if !shaPattern.MatchString(baseSHA) {
		return nil, fmt.Errorf("%w: base_sha must be a full commit id, got %q", ErrInvalidFragment, baseSHA)
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Explorer{Repository: repository, BaseSHA: baseSHA, Source: source, Limits: limits, files: map[string]struct{}{}}, nil
}

// Search suggests where to look. Nothing it returns is evidence.
func (e *Explorer) Search(ctx context.Context, query string) ([]Match, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: empty search", ErrInvalidFragment)
	}
	if e.searches >= e.Limits.MaxSearches {
		return nil, fmt.Errorf("%w: %d searches", ErrBudgetExhausted, e.Limits.MaxSearches)
	}
	e.searches++
	matches, err := e.Source.Search(ctx, e.BaseSHA, query, e.Limits.MaxFiles)
	if err != nil {
		return nil, err
	}
	kept := make([]Match, 0, len(matches))
	for _, candidate := range matches {
		if ValidatePath(candidate.Path) == nil && candidate.Line > 0 {
			kept = append(kept, candidate)
		}
	}
	return kept, nil
}

// ReadAround produces the excerpt surrounding a location.
//
// A declaration is rarely understandable from its own line: the window is what
// turns "this symbol exists here" into something a reader can reason about,
// and it is what a citation has to contain for the claim resting on it to be
// checkable.
func (e *Explorer) ReadAround(ctx context.Context, match Match, window int) (Fragment, error) {
	if window < 0 {
		window = 0
	}
	start := match.Line - window
	if start < 1 {
		start = 1
	}
	return e.Read(ctx, match.Path, start, match.Line+window)
}

// Read produces one citable excerpt, within budget.
//
// The range is clamped to the file and to MaxLines rather than refused: an
// agent asking for lines 1-100000 is asking to see a file, and the useful
// answer is the beginning of it, not an error.
func (e *Explorer) Read(ctx context.Context, filePath string, start, end int) (Fragment, error) {
	if err := ValidatePath(filePath); err != nil {
		return Fragment{}, err
	}
	if start < 1 {
		start = 1
	}
	if end < start {
		return Fragment{}, fmt.Errorf("%w: line range %d-%d is not a range", ErrInvalidFragment, start, end)
	}
	if _, known := e.files[filePath]; !known && len(e.files) >= e.Limits.MaxFiles {
		return Fragment{}, fmt.Errorf("%w: %d files", ErrBudgetExhausted, e.Limits.MaxFiles)
	}
	if e.ranges >= e.Limits.MaxRanges {
		return Fragment{}, fmt.Errorf("%w: %d ranges", ErrBudgetExhausted, e.Limits.MaxRanges)
	}
	total, err := e.Source.Lines(ctx, e.BaseSHA, filePath)
	if err != nil {
		return Fragment{}, err
	}
	if total < 1 {
		return Fragment{}, fmt.Errorf("%w: %s is empty at %s", ErrInvalidFragment, filePath, e.BaseSHA)
	}
	if start > total {
		return Fragment{}, fmt.Errorf("%w: %s has %d lines at %s", ErrInvalidFragment, filePath, total, e.BaseSHA)
	}
	if end > total {
		end = total
	}
	if end-start+1 > e.Limits.MaxLines {
		end = start + e.Limits.MaxLines - 1
	}
	content, err := e.Source.ReadRange(ctx, e.BaseSHA, filePath, start, end)
	if err != nil {
		return Fragment{}, err
	}
	if e.bytes+len(content) > e.Limits.MaxBytes {
		return Fragment{}, fmt.Errorf("%w: %d bytes", ErrBudgetExhausted, e.Limits.MaxBytes)
	}
	fragment := Fragment{
		Repository: e.Repository, BaseSHA: e.BaseSHA, Path: filePath,
		LineStart: start, LineEnd: end, Content: content, Digest: DigestOf(content),
	}
	if err := fragment.Validate(); err != nil {
		return Fragment{}, err
	}
	e.files[filePath] = struct{}{}
	e.ranges++
	e.bytes += len(content)
	return fragment, nil
}

// Spent reports what the exploration used, so a caller can record it.
func (e *Explorer) Spent() (searches, files, ranges, bytes int) {
	return e.searches, len(e.files), e.ranges, e.bytes
}
