package repositoryevidence

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

// A content-aware source: Search finds the literal query on every matching
// line of every file, the way the real backend's fixed-string grep does.
// Worlds are keyed by commit -- a fake that ignored the SHA would make
// pinning tests prove nothing. Every SHA it is asked about is recorded.
type literalSource struct {
	worlds     map[string]map[string]string
	seenSHAs   []string
	failSearch error
}

func (s *literalSource) at(baseSHA string) map[string]string {
	return s.worlds[baseSHA]
}

func (s *literalSource) Search(_ context.Context, baseSHA, query string, limit int) ([]Match, error) {
	s.seenSHAs = append(s.seenSHAs, baseSHA)
	if s.failSearch != nil {
		return nil, s.failSearch
	}
	var out []Match
	for path, body := range s.worlds[baseSHA] {
		for index, line := range strings.Split(body, "\n") {
			if strings.Contains(line, query) {
				out = append(out, Match{Path: path, Line: index + 1})
			}
		}
	}
	sort.Slice(out, func(first, second int) bool {
		if out[first].Path != out[second].Path {
			return out[first].Path < out[second].Path
		}
		return out[first].Line < out[second].Line
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *literalSource) Lines(_ context.Context, baseSHA, path string) (int, error) {
	body, ok := s.at(baseSHA)[path]
	if !ok {
		return 0, nil
	}
	return len(strings.Split(body, "\n")), nil
}

func (s *literalSource) ReadRange(_ context.Context, baseSHA, path string, start, end int) (string, error) {
	body, ok := s.at(baseSHA)[path]
	if !ok {
		return "", nil
	}
	all := strings.Split(body, "\n")
	if start > len(all) {
		return "", nil
	}
	if end > len(all) {
		end = len(all)
	}
	return strings.Join(all[start-1:end], "\n"), nil
}

// The world R10 adjudicated against, reduced to its facts: "DesignBaseSHA"
// exists nowhere as a literal; "InvocationBudget.Validate" never appears as a
// qualified string even though both the type and the method are declared;
// driveDesignFreeze is declared once and used mid-line elsewhere.
func r10PinnedWorld() *literalSource {
	return &literalSource{worlds: map[string]map[string]string{probeSHA: {
		"internal/executive/types.go": `package executive

type FrozenDesign struct {
	BaseSHA string
	Version string
}
`,
		"internal/executive/budget.go": `package executive

type InvocationBudget struct {
	Calls int
}

func (b InvocationBudget) Validate(l Limits) error {
	return nil
}
`,
		"internal/executive/design_freeze_phase.go": `package executive

func (o *Orchestrator) driveDesignFreeze(ctx context.Context) (bool, error) {
	return false, nil
}
`,
		"internal/executive/orchestrator.go": `package executive

func step(o *Orchestrator) {
	done, err := o.driveDesignFreeze(context.Background())
	o.record(done, err)
}
`,
	}}}
}

const probeSHA = "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9"

// THE R10 SUBJECTS: a concept that exists nowhere in the tree and a composite
// method path that never appears as a literal cannot ground a slot -- no
// matter that the underlying type and method really exist.
func TestProbeRejectsSubjectsTheSensorCannotRepresent(t *testing.T) {
	ctx := context.Background()
	source := r10PinnedWorld()

	for _, subject := range []string{"DesignBaseSHA", "InvocationBudget.Validate"} {
		supplied, err := ProbeSubjectSupply(ctx, "explorarte-organization", probeSHA, source,
			DefaultLimits(), subject, []string{"definition", "application"}, 24)
		if err != nil {
			t.Fatalf("probe of %q errored: %v", subject, err)
		}
		if supplied["definition"] || supplied["application"] {
			t.Errorf("%q reported supplyable (%v), but no excerpt can classify for it", subject, supplied)
		}
	}
}

// A symbol that really exists is confirmed for exactly the roles it has.
func TestProbeConfirmsARealSymbolForExistingRoles(t *testing.T) {
	supplied, err := ProbeSubjectSupply(context.Background(), "explorarte-organization", probeSHA,
		r10PinnedWorld(), DefaultLimits(), "driveDesignFreeze",
		[]string{"definition", "application"}, 24)
	if err != nil {
		t.Fatalf("probe errored: %v", err)
	}
	if !supplied["definition"] {
		t.Errorf("the declaration was not recognised as a definition: %v", supplied)
	}
	if !supplied["application"] {
		t.Errorf("the mid-line use was not recognised as an application: %v", supplied)
	}
}

// The probe answers about the delivered commit only. Another commit where the
// subject DOES exist must not leak into the answer, and HEAD must never be
// consulted.
func TestProbeReadsOnlyTheDeliveredCommit(t *testing.T) {
	source := r10PinnedWorld()
	const otherWorld = "9999999999999999999999999999999999999999"
	source.worlds[otherWorld] = map[string]string{
		"internal/executive/concept.go": "package executive\n\ntype DesignBaseSHA struct{}\n",
	}

	supplied, err := ProbeSubjectSupply(context.Background(), "explorarte-organization", probeSHA,
		source, DefaultLimits(), "DesignBaseSHA", []string{"definition"}, 24)
	if err != nil {
		t.Fatalf("probe errored: %v", err)
	}
	if supplied["definition"] {
		t.Fatal("a definition from another commit leaked into the pinned answer")
	}
	for _, asked := range source.seenSHAs {
		if asked != probeSHA {
			t.Fatalf("the probe consulted %q instead of the pin %q", asked, probeSHA)
		}
	}
	if len(source.seenSHAs) == 0 {
		t.Fatal("the probe never queried the sensor")
	}
}

// A broken observer is not an answer. The probe reports the failure instead
// of dressing it up as "not supplyable".
func TestProbeSensorFailureIsReportedNotInterpreted(t *testing.T) {
	source := r10PinnedWorld()
	sentinel := errors.New("git index locked")
	source.failSearch = sentinel

	_, err := ProbeSubjectSupply(context.Background(), "explorarte-organization", probeSHA,
		source, DefaultLimits(), "driveDesignFreeze", []string{"definition"}, 24)
	if !errors.Is(err, sentinel) {
		t.Fatalf("sensor failure lost on the way back: %v", err)
	}
}
