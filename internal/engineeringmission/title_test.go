package engineeringmission

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The objective that stopped a real campaign. It froze its design, produced a
// valid implementation plan, and never started its runner, because 248 bytes
// did not fit a 240-byte title. Eight bytes, and no code ran.
const stalledObjective = "Produce the ImplementationPlan for the frozen design: create docs/implementation/autonomy-smoke/AUTONOMY_SMOKE_010.md as minimal documentary evidence of the autonomous cycle, with no other repository path touched and every host gate left to decide."

func TestTheObjectiveThatStoppedACampaignNowFits(t *testing.T) {
	if len(stalledObjective) <= maxMissionTitleBytes {
		t.Fatalf("the regression fixture must actually exceed the limit, got %d bytes", len(stalledObjective))
	}
	title := missionTitle(stalledObjective)
	if len(title) > maxMissionTitleBytes {
		t.Fatalf("title is %d bytes, limit is %d", len(title), maxMissionTitleBytes)
	}
	if title == "" {
		t.Fatal("a mission must be nameable")
	}
	if !strings.HasPrefix(title, "Produce the ImplementationPlan") {
		t.Fatalf("the title must still say which mission it is: %q", title)
	}
	if !strings.HasSuffix(title, "...") {
		t.Fatalf("a shortened title should show it was shortened: %q", title)
	}
}

// Cutting a multi-byte character in half produces a title that is short
// enough and no longer valid UTF-8. These objectives are routinely Spanish.
func TestTruncationNeverSplitsARune(t *testing.T) {
	for _, filler := range []string{"ñ", "é", "—", "🙂"} {
		t.Run(filler, func(t *testing.T) {
			// Sweep lengths so the cut lands mid-rune at some point.
			for extra := 0; extra < 12; extra++ {
				objective := strings.Repeat(filler, 200) + strings.Repeat("a", extra)
				title := missionTitle(objective)
				if len(title) > maxMissionTitleBytes {
					t.Fatalf("extra=%d: title is %d bytes", extra, len(title))
				}
				if !utf8.ValidString(title) {
					t.Fatalf("extra=%d: truncation produced invalid UTF-8", extra)
				}
			}
		})
	}
}

func TestAShortObjectiveIsUsedUnchanged(t *testing.T) {
	const objective = "Create the smoke evidence document"
	if got := missionTitle(objective); got != objective {
		t.Fatalf("a title that already fits must not be altered: %q", got)
	}
}

func TestAnEmptyObjectiveStillYieldsANameableMission(t *testing.T) {
	for _, objective := range []string{"", "   ", "\n\t"} {
		title := missionTitle(objective)
		if strings.TrimSpace(title) == "" {
			t.Fatal("a mission with no title cannot be created, and the engine's own error would not say which mission it was")
		}
		if len(title) > maxMissionTitleBytes {
			t.Fatalf("fallback title is %d bytes", len(title))
		}
	}
}

// Whatever the objective, the title must be creatable. This is the general
// form: the field is free-form model text and the limit is fixed, so the host
// adapts rather than hoping.
func TestNoObjectiveCanProduceAnUnusableTitle(t *testing.T) {
	for _, objective := range []string{
		strings.Repeat("x", 1),
		strings.Repeat("x", maxMissionTitleBytes),
		strings.Repeat("x", maxMissionTitleBytes+1),
		strings.Repeat("x", 10_000),
		strings.Repeat("ñ", 10_000),
		"   " + strings.Repeat("y", 400) + "   ",
	} {
		title := missionTitle(objective)
		if n := len(title); n < 1 || n > maxMissionTitleBytes {
			t.Fatalf("objective of %d bytes produced a %d-byte title", len(objective), n)
		}
		if !utf8.ValidString(title) {
			t.Fatalf("objective of %d bytes produced invalid UTF-8", len(objective))
		}
	}
}
