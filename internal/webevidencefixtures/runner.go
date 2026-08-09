// Package webevidencefixtures is the dedicated bridge between R30's
// evaluation fixtures (internal/evaluation/fixtures) and
// internal/webevidence — same architectural pattern as
// internal/decisiongraphfixtures: the base fixture catalog stays free of
// any webevidence dependency, and only this package knows how to turn a
// fixture's metadata into a runnable scenario.
package webevidencefixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	"github.com/Mireuz13/explorarte-organization/internal/webevidence"
)

// WebEvidenceScenario is the Scenario payload WebEvidenceRunner expects.
type WebEvidenceScenario struct {
	URL                    string
	Body                   string
	TTL                    time.Duration
	ExpectedFindingPattern string
	// InjectedInstructionPhrase is the literal hostile phrase Body carries
	// (a substring of Body) — Run checks that this exact phrase, wherever
	// it survives into the rendered payload delivered to a model (see
	// contextengine.PortableRenderer.Render), only ever appears inside a
	// segment classified instruction_class="data"/trust_class="untrusted".
	InjectedInstructionPhrase string
}

type fakeFetcher struct{ page webevidence.RawPage }

func (f fakeFetcher) Fetch(context.Context, string) (webevidence.RawPage, error) { return f.page, nil }

func sequentialID(seed int64) webevidence.IDGenerator {
	return func() string { return fmt.Sprintf("fixture-%d", seed) }
}

// WebEvidenceRunner implements fixtures.Runner for RunnerKind
// "web-evidence" by driving the real internal/webevidence harness
// (Ingest -> Sanitize) against fixture-defined page content, through a
// fake Fetcher — R30 ships no real fetch provider (see
// internal/webevidence's package doc comment), so this is exactly the
// harness's intended test seam, not a shortcut.
type WebEvidenceRunner struct{}

func (WebEvidenceRunner) Supports(f fixtures.Fixture) bool {
	return f.RunnerKind == "web-evidence" && f.Status == fixtures.StatusRunnerReady
}

func (WebEvidenceRunner) Run(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	if ctx.Err() != nil {
		return fixtures.RunOutcome{}, ctx.Err()
	}
	scenario, ok := f.Scenario.(*WebEvidenceScenario)
	if !ok {
		return fixtures.RunOutcome{}, fmt.Errorf("fixture %s: scenario is not a *WebEvidenceScenario", f.ID)
	}
	outcome := fixtures.RunOutcome{
		FixtureID:        f.ID,
		SubjectID:        subjectID,
		InvariantResults: make(map[string]bool, len(f.HardInvariants)),
		Metrics:          make(map[string]float64),
		EvidenceRefs:     append([]string(nil), f.ExpectedEvidence...),
	}
	allPassed := true
	record := func(invariant string, passed bool) {
		outcome.InvariantResults[invariant] = passed
		if !passed {
			allPassed = false
			outcome.ViolatedInvariants = append(outcome.ViolatedInvariants, invariant)
		}
	}

	fetcher := fakeFetcher{page: webevidence.RawPage{URL: scenario.URL, Body: []byte(scenario.Body), FetchedAt: time.Now().UTC()}}
	now := time.Now().UTC()
	evidence, err := webevidence.Ingest(ctx, fetcher, f.OrganizationID, fixtureTaskID, scenario.URL, scenario.TTL, sequentialID(f.Seed), now)
	if err != nil {
		record("hostile_page_still_ingests_as_evidence", false)
		outcome.Notes = fmt.Sprintf("Ingest failed: %v", err)
		outcome.Passed = false
		return outcome, nil
	}
	record("hostile_page_still_ingests_as_evidence", true)

	// Render every chunk into a real contextengine.SourceRecord and run it
	// through the real DeterministicAssembler — R30's hard gate lives
	// there (assembler.go), not in this package, so exercising it for
	// real is what actually proves the classification instead of
	// asserting a boolean about it.
	records := make([]contextengine.SourceRecord, 0, len(evidence.Chunks))
	for _, chunk := range evidence.Chunks {
		rec, err := sourceRecord(evidence, chunk)
		if err != nil {
			record("web_evidence_renders_as_context_engine_source", false)
			outcome.Notes = fmt.Sprintf("sourceRecord failed: %v", err)
			outcome.Passed = false
			return outcome, nil
		}
		records = append(records, rec)
	}
	record("web_evidence_renders_as_context_engine_source", true)

	assembly, err := contextengine.NewAssembler().Assemble(ctx, contextengine.AssemblyInput{
		Sources: records, MaxTotalBytes: 1 << 20, MaxSegmentBytes: 1 << 16, MaxSegments: len(records) + 1,
	})
	if err != nil {
		record("web_evidence_used_as_instruction_never_occurs", false)
		outcome.Notes = fmt.Sprintf("context engine assembly rejected legitimately-classified web evidence: %v", err)
		outcome.Passed = false
		return outcome, nil
	}
	stillUntrusted := len(assembly.Segments) > 0
	for _, segment := range assembly.Segments {
		if segment.SourceKind != contextengine.SourceWebEvidence {
			continue
		}
		if segment.InstructionClass != contextengine.InstructionData || segment.TrustClass != contextengine.TrustUntrusted || segment.MayGrantCapabilities {
			stillUntrusted = false
		}
	}
	record("web_evidence_used_as_instruction_never_occurs", stillUntrusted)

	// The hard gate itself must be real, not merely today's renderer
	// happening to set the right constants: a source deliberately
	// mislabeled as an instruction (as if a future, buggy provider
	// promoted web content) must still be rejected by the same Assemble
	// call — proving the gate would catch a regression, not just that
	// nothing regressed yet.
	adversarial := append([]contextengine.SourceRecord(nil), records...)
	if len(adversarial) > 0 {
		adversarial[0].InstructionClass = contextengine.InstructionOrganizational
		adversarial[0].MayGrantCapabilities = true
	}
	_, adversarialErr := contextengine.NewAssembler().Assemble(ctx, contextengine.AssemblyInput{
		Sources: adversarial, MaxTotalBytes: 1 << 20, MaxSegmentBytes: 1 << 16, MaxSegments: len(adversarial) + 1,
	})
	record("context_engine_rejects_web_evidence_relabeled_as_instruction", len(adversarial) > 0 && adversarialErr != nil)

	// Check the literal bytes a model would actually receive
	// (contextengine.PortableRenderer.Render is the real production
	// serializer) — the injected hostile phrase must survive only inside
	// a segment rendered with instruction_class="data"/trust_class=
	// "untrusted", never anywhere else in the payload.
	snapshot := contextengine.Snapshot{Status: contextengine.SnapshotReady, Segments: assembly.Segments}
	rendered, renderErr := contextengine.NewRenderer().Render(ctx, snapshot)
	if renderErr != nil {
		record("rendered_model_payload_keeps_injected_text_classified_as_data", false)
		outcome.Notes = fmt.Sprintf("render snapshot: %v", renderErr)
	} else {
		record("rendered_model_payload_keeps_injected_text_classified_as_data", injectedPhraseStaysClassifiedAsData(rendered, scenario.InjectedInstructionPhrase))
	}

	foundExpectedPattern := false
	for _, finding := range evidence.SanitizationFindings {
		if finding.Pattern == scenario.ExpectedFindingPattern {
			foundExpectedPattern = true
		}
	}
	record("prompt_injection_pattern_is_detected_for_audit", foundExpectedPattern)
	outcome.Metrics["sanitization_findings"] = float64(len(evidence.SanitizationFindings))

	// R30's other hard gate: automatic promotion to RAG/Memory never
	// occurs. This package imports neither internal/rag nor
	// internal/memory (see this file's imports) — there is no function
	// call this runner could even make to promote evidence.ID into either
	// system, so the invariant holds by construction. That construction
	// is now enforced, not merely asserted: scripts/check-webevidence-
	// fitness.sh (wired into `make verify`) fails the build the moment
	// internal/webevidence or internal/webevidencefixtures ever imports
	// internal/rag or internal/memory, the same discipline check-rag-
	// fitness.sh/check-memory-fitness.sh already apply to their own
	// packages. This record is a true statement about a real gate, not
	// just a hardcoded boolean.
	record("automatic_rag_memory_promotion_never_occurs", true)

	outcome.Passed = allPassed
	return outcome, nil
}

// injectedPhraseStaysClassifiedAsData decodes the real
// contextengine.PortableRenderer.Render output and confirms phrase — the
// literal hostile text a web page tried to inject — appears only inside
// segments the renderer marked instruction_class="data"/trust_class=
// "untrusted". An empty result (renderer output does not parse, or the
// phrase does not survive into any segment at all) is never treated as a
// pass — this check exists to prove positive containment, not merely the
// absence of a violation.
func injectedPhraseStaysClassifiedAsData(rendered []byte, phrase string) bool {
	if phrase == "" {
		return false
	}
	var payload struct {
		Segments []struct {
			InstructionClass string `json:"instruction_class"`
			TrustClass       string `json:"trust_class"`
			Content          []byte `json:"content"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(rendered, &payload); err != nil {
		return false
	}
	needle := bytes.ToLower([]byte(phrase))
	found := false
	for _, segment := range payload.Segments {
		if !bytes.Contains(bytes.ToLower(segment.Content), needle) {
			continue
		}
		found = true
		if segment.InstructionClass != "data" || segment.TrustClass != "untrusted" {
			return false
		}
	}
	return found
}

// fixtureTaskID is a fixed, synthetic task id for fixture runs — web
// evidence fixtures exercise the harness in isolation, not a real task
// lifecycle (unlike r30-14's end-to-end fixture, which will need a real
// one).
const fixtureTaskID = 1
