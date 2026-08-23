package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A rendered excerpt carries a one-line provenance header. The threshold the
// boundary states is 48 characters of SOURCE, so a body above it must not
// cross merely because the header pushed the sampling out of alignment.
func TestAShortExcerptIsNotCarriedByItsHeader(t *testing.T) {
	sha := strings.Repeat("a", 40)
	header := "internal/mission/doc.go lines 1-6 at " + sha
	body := "func (s *Store) Close() error {\n\treturn s.db.Close()\n}\n"
	if len(normalizeForDeclassify(body)) <= declassifyMinimumRun {
		t.Fatalf("fixture is below the threshold, so it would prove nothing: %d", len(normalizeForDeclassify(body)))
	}
	err := DeclassifyCandidate("The design should keep this exactly:\n"+body, evidenceOf(header+"\n"+body))
	if !errors.Is(err, ErrCandidateContaminated) {
		t.Fatalf("a verbatim body above the threshold crossed because its payload was headed: %v", err)
	}
}

// Detection must not depend on where the copy begins. A copy shorter than the
// old sampling stride plus the threshold slipped through whenever it fell
// between two sampled windows.
//
// Two earlier fixtures for this test passed with the sampling bug still in
// place, both because the source repeated: in repeating text every window
// occurs at every offset, so any sampling finds it. The fixture therefore
// asserts its own non-periodicity before it asserts anything about the
// detector -- otherwise this test proves its fixture and not the property.
func TestACopyIsFoundAtEveryOffsetOfTheSource(t *testing.T) {
	var builder strings.Builder
	state := uint64(0x9E3779B97F4A7C15)
	for i := 0; i < 400; i++ {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		builder.WriteByte(byte('a' + state%26))
	}
	text := builder.String()

	distinct := make(map[string]struct{})
	for start := 0; start+declassifyMinimumRun <= len(text); start++ {
		distinct[text[start:start+declassifyMinimumRun]] = struct{}{}
	}
	if windows := len(text) - declassifyMinimumRun + 1; len(distinct) != windows {
		t.Fatalf("the fixture repeats (%d distinct windows of %d), so sampling would find a copy at any offset and this test would pass without the property",
			len(distinct), windows)
	}

	for _, offset := range []int{0, 1, 7, 23, 25, 49, 113} {
		run := text[offset : offset+declassifyMinimumRun+2]
		if err := DeclassifyCandidate("as decided: "+run, evidenceOf(text)); !errors.Is(err, ErrCandidateContaminated) {
			t.Fatalf("a copy of %d characters starting at offset %d was not detected: %v",
				len(run), offset, err)
		}
	}
}

// Below the threshold stays permitted: the boundary is reproduction, not
// resemblance, and a stricter version would block ordinary grounded claims.
func TestAFragmentBelowTheThresholdStillCrosses(t *testing.T) {
	source := "internal/x/doc.go lines 1-2 at " + strings.Repeat("b", 40) + "\nfunc Enabled() bool {\n"
	if err := DeclassifyCandidate("Enabled() should stay a bool", evidenceOf(source)); err != nil {
		t.Fatalf("a short reference was refused: %v", err)
	}
}

// A pinned world that cannot be read back is not an ungrounded campaign.
// Losing the wiring must stop the review, not silently empty the evidence the
// candidate is declassified against.
func TestAPinnedWorldWithNoSnapshotReaderIsRefused(t *testing.T) {
	tasksPort := newMemoryTasks()
	const rootID int64 = 1
	pin := DesignBaseSHAReference + "1"
	tasksPort.tasks[rootID] = TaskRecord{
		ID: rootID, CorrelationID: "executive:pin", AssignedRoleID: CEORoleID,
		Evidence: []EvidenceRecord{{Reference: pin, Metadata: map[string]any{"design_base_sha": designSHA}}},
	}
	orchestrator := &Orchestrator{
		tasks: tasksPort,
		models: mismatchModels{
			invocation: InvocationRecord{ID: 5, TaskID: 2, ContextSnapshotID: 7},
			result:     InvocationResult{InvocationID: 5, ResponseHash: "d1", TextOutput: "see " + realCite},
		},
		snapshotSources: nil,
		programTarget:   &fakeProgramTarget{sha: designSHA},
	}
	_, _, err := orchestrator.verifiedDesignCitations(context.Background(),
		tasksPort.tasks[rootID],
		designArtifact{Units: []designUnitRef{{UnitID: "ingenieria_ia", TaskID: 2, InvocationID: 5, ResultHash: "d1"}}})
	if err == nil {
		t.Fatal("a design over a pinned world verified with no way to read what the workers saw")
	}
}
