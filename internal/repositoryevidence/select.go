package repositoryevidence

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// Selection is what the host decided is worth looking at, before any model
// runs.
//
// It is deliberately not chosen by an agent. Context is assembled before the
// model is called, so something has to decide what the design will be able to
// see, and a host-side rule that anyone can read and reproduce is a better
// starting point than a capability nobody has audited yet. An agent choosing
// its own reading is the next layer, and it is work the organization can do
// once it can see at all.
type Selection struct {
	// Paths are repository paths or directory prefixes named directly.
	Paths []string
	// Terms are literal strings to look for. Identifiers, mostly.
	Terms []string
	// Window is how many lines around a match make it understandable.
	Window int
}

// pathPattern recognises a repository path inside prose: two or more segments
// of lowercase words joined by slashes, optionally ending in a file.
var pathPattern = regexp.MustCompile(`\b(?:[a-z0-9_.-]+/){1,6}[a-z0-9_.-]+\b`)

// symbolPattern recognises an exported Go identifier or a Test name, which is
// what a goal usually names when it means something specific.
var symbolPattern2 = regexp.MustCompile(`\b(?:Test[A-Za-z0-9_]{3,}|[A-Z][A-Za-z0-9]{4,})\b`)

// SelectionFromText derives what to look at from the text of a goal.
//
// Deterministic and explainable on purpose: the same goal always produces the
// same reading, so a design can be reproduced, and anyone asking "why did it
// see this?" gets an answer that does not require replaying a model.
// SelectionForRequirements is the same reading, with obligations first.
//
// A subject the host is already obliged to ground is a typed fact, not
// something to rediscover from prose. Re-deriving it would put a known
// requirement back in competition with incidental words for the same budget --
// which is how AUTONOMY-SMOKE-017-R5 lost the file declaring both limits --
// and would quietly make the extractor a second source of normative truth.
//
// Seeds are prepended and never dropped: what must be grounded is searched
// before anything discovered.
func SelectionForRequirements(text string, subjects []string, window int) Selection {
	selection := SelectionFromText(text, window)
	if len(subjects) == 0 {
		return selection
	}
	seeded := make([]string, 0, len(subjects)+len(selection.Terms))
	seen := map[string]struct{}{}
	for _, subject := range subjects {
		subject = strings.TrimSpace(subject)
		if subject == "" {
			continue
		}
		if _, already := seen[subject]; already {
			continue
		}
		seen[subject] = struct{}{}
		seeded = append(seeded, subject)
	}
	for _, term := range selection.Terms {
		if _, already := seen[term]; already {
			continue
		}
		seen[term] = struct{}{}
		seeded = append(seeded, term)
	}
	selection.Terms = seeded
	return selection
}

func SelectionFromText(text string, window int) Selection {
	if window < 1 {
		window = 24
	}
	selection := Selection{Window: window}
	seenPath := map[string]struct{}{}
	for _, candidate := range pathPattern.FindAllString(text, -1) {
		candidate = strings.Trim(candidate, "./")
		if ValidatePath(candidate) != nil {
			continue
		}
		// A path with no extension is a directory prefix worth searching
		// under; one with an extension is a file worth reading.
		if _, already := seenPath[candidate]; already {
			continue
		}
		seenPath[candidate] = struct{}{}
		selection.Paths = append(selection.Paths, candidate)
	}
	seenTerm := map[string]struct{}{}
	for _, candidate := range symbolPattern2.FindAllString(text, -1) {
		if _, already := seenTerm[candidate]; already {
			continue
		}
		seenTerm[candidate] = struct{}{}
		selection.Terms = append(selection.Terms, candidate)
	}
	sort.Strings(selection.Paths)
	rankTerms(selection.Terms)
	return selection
}

// rankTerms decides which searches get the budget when not all of them fit.
//
// The order used to be alphabetical, which is stable and explainable and
// spends the budget on whatever happens to sort first. AUTONOMY-SMOKE-017-R5
// measured what that costs: fourteen terms were derived from the goal, and the
// two identifiers the goal actually named -- MaxDesignRounds and
// MaxDepartmentReplans -- sorted ninth and tenth, behind eight incidental
// capitalised words (ALCANCE, AUTONOMY, EVIDENCE, PERMITIDO, PROHIBIDO...).
// The eight-file budget was exhausted before either symbol could claim
// internal/executive/types.go, where both are declared. The design was then
// asked to cite a definition site it had never been shown, and cited a test
// fixture instead.
//
// So terms are ordered by how much they look like something a goal MEANT
// rather than something its prose happened to contain. A mixed-case
// identifier (MaxDesignRounds, ValidateSourceMetadata) is how Go names a
// declaration; a word in all capitals, or one that is merely capitalised
// because it began a sentence, is prose. Ordering is stable within each rank,
// so the reading a goal produces is still reproducible from the goal alone.
func rankTerms(terms []string) {
	sort.SliceStable(terms, func(first, second int) bool {
		return termRank(terms[first]) < termRank(terms[second])
	})
}

// termRank is lower for terms more likely to name real code.
func termRank(term string) int {
	switch {
	case looksLikeIdentifier(term):
		return 0
	case term == strings.ToUpper(term):
		// Shouting is prose: ALLOWED, FORBIDDEN, PROHIBIDO.
		return 2
	default:
		return 1
	}
}

// looksLikeIdentifier reports whether a term is shaped like a Go declaration:
// it starts upper, and it changes case at least once afterwards. "Document"
// and "Investigate" do not; "MaxDesignRounds" and "TestSomething" do.
func looksLikeIdentifier(term string) bool {
	if len(term) < 2 || term[0] < 'A' || term[0] > 'Z' {
		return false
	}
	sawLower := false
	for index := 1; index < len(term); index++ {
		character := term[index]
		switch {
		case character >= 'a' && character <= 'z':
			sawLower = true
		case character >= 'A' && character <= 'Z' && sawLower:
			return true
		}
	}
	return false
}

// Gather reads the selection into citable excerpts, within the explorer's
// budget.
//
// Running out of budget is not an error. An exploration that read eight of the
// twelve places it wanted to is still eight places the design can cite, and
// refusing the lot because the ninth did not fit would trade partial sight for
// none.
func Gather(ctx context.Context, explorer *Explorer, selection Selection) ([]Fragment, error) {
	if explorer == nil {
		return nil, ErrInvalidFragment
	}
	window := selection.Window
	if window < 1 {
		window = 24
	}
	fragments := make([]Fragment, 0, explorer.Limits.MaxRanges)
	seen := map[string]struct{}{}

	add := func(fragment Fragment) {
		reference := fragment.Reference()
		if _, already := seen[reference]; already {
			return
		}
		seen[reference] = struct{}{}
		fragments = append(fragments, fragment)
	}

	// Files named outright are read from the top: the head of a Go file is
	// its package, its imports and its first declarations, which is what
	// orients a reader who has never seen it.
	for _, candidate := range selection.Paths {
		if !strings.Contains(candidate, ".") {
			continue
		}
		fragment, err := explorer.Read(ctx, candidate, 1, window*2)
		if err != nil {
			continue
		}
		add(fragment)
	}

	// Then the terms, read where they actually occur.
	for _, term := range selection.Terms {
		matches, err := explorer.Search(ctx, term)
		if err != nil {
			break
		}
		for _, match := range matches {
			if !underAnyPrefix(match.Path, selection.Paths) {
				continue
			}
			fragment, readErr := explorer.ReadAround(ctx, match, window)
			if readErr != nil {
				continue
			}
			add(fragment)
		}
	}
	return fragments, nil
}

// underAnyPrefix keeps a search inside the scope the goal named.
//
// Without it a term like "Orchestrator" would drag in every file in the
// repository that happens to mention it, which is the failure this design
// exists to avoid: the point was a handful of relevant excerpts, not a cheaper
// way to send everything.
func underAnyPrefix(path string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		if path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			return true
		}
	}
	return false
}
