package repositoryevidence

import (
	"context"
	"errors"
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
	// RequiredTerms is the subset of Terms that carry an obligation: the
	// subjects a round MUST ground. AUTONOMY-SMOKE-017-R10 showed what
	// treating them as merely first-in-line costs -- one subject's matches
	// consumed the whole file budget before the next required subject was
	// ever searched, so a slot supplied in an earlier round vanished in the
	// next. Required subjects get a reserved pass before anything else reads.
	RequiredTerms []string
	// Slots are the normative (subject, relation) demands of this round, when
	// the caller carries them. When present they drive PASS 0: for each slot
	// the gatherer finds and reads an excerpt that the round's own classifier
	// (ExcerptRelations) confirms satisfies THAT relation, before any other
	// pass spends a byte of budget. Slots also invert the path-prefix rule:
	// a mandatory candidate is never dropped for lying outside the query's
	// incidental scope -- the obligation defines relevance, not the other way
	// around. Checkpoint D (AUTONOMY-SMOKE-017-R15): without slots the
	// selection delivered driveDesignFreeze's declaration while the round
	// demanded its application, and the preflight killed the worker before a
	// model call.
	Slots []EvidenceSlot
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
	required := make([]string, 0, len(subjects))
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
		required = append(required, subject)
	}
	for _, term := range selection.Terms {
		if _, already := seen[term]; already {
			continue
		}
		seen[term] = struct{}{}
		seeded = append(seeded, term)
	}
	selection.Terms = seeded
	selection.RequiredTerms = required
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

// requiredHeadSize is how many ranked candidates each required subject
// reserves in the coverage pass. rankHits already orders one subject's hits
// declaration-first, application-second when both exist, so the head of a
// search is exactly the pair of roles an evidence slot can demand.
const requiredHeadSize = 2

// Gather reads the selection into citable excerpts, within the explorer's
// budget.
//
// Running out of budget is not an error. An exploration that read eight of the
// twelve places it wanted to is still eight places the design can cite, and
// refusing the lot because the ninth did not fit would trade partial sight for
// none.
func Gather(ctx context.Context, explorer *Explorer, selection Selection) ([]Fragment, error) {
	fragments, _, err := gather(ctx, explorer, selection, false)
	return fragments, err
}

// GatherWithCoverage is Gather plus the answer admission needs: which
// demanded slots the gathered excerpts actually satisfy. It is the delivery
// half of checkpoint D -- the admission half (PlanSlots) runs this same code
// path against the same limits, so what was proven deliverable at acceptance
// time is what delivery reproduces.
func GatherWithCoverage(ctx context.Context, explorer *Explorer, selection Selection) ([]Fragment, []EvidenceSlot, error) {
	return gather(ctx, explorer, selection, true)
}

// gatherCore is the single reading algorithm both call. strict=true (the
// admission dry-run) propagates sensor failures instead of absorbing them:
// an admission verdict may never mistake a broken observer for an empty
// world. Budget exhaustion is not a sensor failure in either mode -- running
// out is how an over-demanded set says "not all of us fit", and it reports
// through the uncovered-slot list.
func gather(ctx context.Context, explorer *Explorer, selection Selection, strict bool) ([]Fragment, []EvidenceSlot, error) {
	if explorer == nil {
		return nil, nil, ErrInvalidFragment
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

	required := make(map[string]struct{}, len(selection.RequiredTerms))
	for _, term := range selection.RequiredTerms {
		required[term] = struct{}{}
	}

	// Searches are cached and report their errors: strict mode (admission)
	// must be able to tell a broken sensor from an empty world, while the
	// delivery passes keep their historical tolerance.
	cache := map[string][]Match{}
	search := func(term string) ([]Match, error) {
		if cached, ok := cache[term]; ok {
			return cached, nil
		}
		matches, err := explorer.Search(ctx, term)
		if err != nil {
			cache[term] = nil
			return nil, err
		}
		cache[term] = matches
		return matches, nil
	}
	searchTolerant := func(term string) []Match {
		matches, _ := search(term)
		return matches
	}

	// PASS 0 -- MANDATORY SLOT COVERAGE (checkpoint D). The normative unit is
	// the slot, not the subject: for every demanded (subject, relation) the
	// pass reads candidates until ExcerptRelations confirms one satisfies THAT
	// relation. Round-robin across slots keeps one hungry subject from eating
	// another's evidence; the path-prefix rule is INVERTED here because an
	// obligation defines relevance rather than competing with the query's
	// incidental scope; and only ErrBudgetExhausted ends the pass early --
	// capacity is exactly the thing joint admission already priced. Every
	// other read failure moves to the next candidate; in strict mode it aborts,
	// because admission may never mistake a broken observer for a verdict.
	uncovered := make([]EvidenceSlot, 0)
	if len(selection.Slots) > 0 {
		pending := make([]EvidenceSlot, len(selection.Slots))
		copy(pending, selection.Slots)
		candidates := map[string][]Match{}
		satisfied := map[EvidenceSlot]bool{}
		outOfCapacity := false
		for len(pending) > 0 && !outOfCapacity {
			progressed := false
			still := make([]EvidenceSlot, 0, len(pending))
			for _, slot := range pending {
				if satisfied[slot] {
					continue
				}
				matches, err := search(slot.Subject)
				if err != nil {
					if errors.Is(err, ErrBudgetExhausted) {
						outOfCapacity = true
						still = append(still, slot)
						break
					}
					if strict {
						return nil, nil, err
					}
					still = append(still, slot)
					continue
				}
				for len(matches) > 0 && !satisfied[slot] {
					match := matches[0]
					matches = matches[1:]
					fragment, readErr := explorer.ReadAround(ctx, match, window)
					if readErr != nil {
						if errors.Is(readErr, ErrBudgetExhausted) {
							outOfCapacity = true
							break
						}
						if strict {
							return nil, nil, readErr
						}
						continue
					}
					add(fragment)
					if ExcerptRelations(fragment.Content, slot.Subject)[slot.Relation] {
						satisfied[slot] = true
						progressed = true
					}
				}
				candidates[slot.Subject] = matches
				if !satisfied[slot] {
					still = append(still, slot)
				}
			}
			pending = still
			if !progressed && !outOfCapacity {
				// A full round satisfied nothing new: no candidate list can
				// advance further, so more rounds would only burn budget.
				break
			}
		}
		for _, slot := range selection.Slots {
			if !satisfied[slot] {
				uncovered = append(uncovered, slot)
			}
		}
	}

	// PASS 1 -- required coverage, ROUND-ROBIN: A's first candidates, then
	// B's first, then C's first; only then A's second, B's second... Under a
	// starving budget every obligation keeps its BEST candidate rather than
	// the earliest subject keeping all of its candidates. rankHits already
	// ordered each subject's hits definition-first, application-second, so
	// the interleaved heads are exactly the roles slots demand. Slots that
	// PASS 0 already satisfied made their subjects' best candidates count;
	// this pass now tops up context for the subjects themselves.
	heads := make(map[string][]Match, len(selection.RequiredTerms))
	order := make([]string, 0, len(selection.RequiredTerms))
	for _, term := range selection.Terms {
		if _, isRequired := required[term]; !isRequired {
			continue
		}
		head := searchTolerant(term)
		if len(head) > requiredHeadSize {
			head = head[:requiredHeadSize]
		}
		if len(head) == 0 {
			continue
		}
		heads[term] = head
		order = append(order, term)
	}
	for index := 0; index < requiredHeadSize; index++ {
		for _, term := range order {
			if index >= len(heads[term]) {
				continue
			}
			match := heads[term][index]
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

	// PASS 2 -- remaining capacity: named files orient a reader first (the
	// head of a Go file is its package, imports and first declarations),
	// then everything else in seeded order, extras of the required subjects
	// included.
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
	for _, term := range selection.Terms {
		for _, match := range searchTolerant(term) {
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
	return fragments, uncovered, nil
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
