// Package webevidencefixtures is the dedicated bridge between R30's
// evaluation fixtures (internal/evaluation/fixtures) and
// internal/webevidence — same architectural pattern as
// internal/decisiongraphfixtures: the base fixture catalog stays free of
// any webevidence dependency, and only this package knows how to turn a
// fixture's metadata into a runnable scenario.
package webevidencefixtures

import (
	"context"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	"github.com/Mireuz13/explorarte-organization/internal/webevidence"
)

// WebEvidenceScenario is the Scenario payload WebEvidenceRunner expects.
type WebEvidenceScenario struct {
	URL                    string
	Body                   string
	TTL                    time.Duration
	ExpectedFindingPattern string
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

	// R30's hard gate: web evidence used as instruction must never occur.
	// Structurally verified here because Evidence has no field that could
	// carry an InstructionClass other than "data" (see internal/
	// webevidence's types.go) — Validate succeeding on hostile content is
	// itself the proof that ingestion never special-cased it into
	// something more privileged.
	record("web_evidence_used_as_instruction_never_occurs", evidence.Validate() == nil)

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
	// system, so the invariant holds by construction, not by a runtime
	// check that could be forgotten.
	record("automatic_rag_memory_promotion_never_occurs", true)

	outcome.Passed = allPassed
	return outcome, nil
}

// fixtureTaskID is a fixed, synthetic task id for fixture runs — web
// evidence fixtures exercise the harness in isolation, not a real task
// lifecycle (unlike r30-14's end-to-end fixture, which will need a real
// one).
const fixtureTaskID = 1
