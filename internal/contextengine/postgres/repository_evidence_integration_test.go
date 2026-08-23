//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// PR #107 made repository_evidence productive without extending the
// persistence contract, and every test that mattered ran against the
// in-memory engine. The first campaign to observe its own repository failed
// at the first durable write. These tests are the seam that was missing: they
// speak to PostgreSQL, because PostgreSQL is what refused.

// The classification a repository fragment carries must survive the round
// trip. A snapshot that persists but comes back reclassified would be worse
// than one that fails: the boundary is enforced on what is read back.
func TestARepositoryEvidenceSegmentSurvivesTheRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	platform := openStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetSchema(t, ctx, platform)
	syncCanonical(t, ctx, platform)

	const (
		reference = "repository://explorarte-organization/internal/executive/orchestrator.go#L1-L4"
		version   = "ce60b6609a4db482f397b8cca75888f01ecfa646"
		body      = "func (o *Orchestrator) Resume(ctx context.Context) (Run, error) {"
	)
	snapshotID := insertSegmentForTest(t, ctx, platform, segmentRow{
		kind: string(contextengine.SourceRepositoryEvidence), reference: reference, version: version,
		instruction: "data", trust: "untrusted", data: "organizational",
		mayGrant: false, content: body,
	})

	var gotKind, gotRef, gotVersion, gotInstruction, gotTrust, gotData string
	var gotMayGrant bool
	var gotContent []byte
	if err = platform.Pool().QueryRow(ctx, `
		SELECT source_kind, source_reference, source_version,
		       instruction_class, trust_class, data_class,
		       may_grant_capabilities, content
		FROM context_segments WHERE snapshot_id=$1`, snapshotID).
		Scan(&gotKind, &gotRef, &gotVersion, &gotInstruction, &gotTrust, &gotData, &gotMayGrant, &gotContent); err != nil {
		t.Fatalf("the segment did not come back: %v", err)
	}
	if gotKind != "repository_evidence" || gotRef != reference || gotVersion != version {
		t.Fatalf("identity changed in the store: kind=%q ref=%q version=%q", gotKind, gotRef, gotVersion)
	}
	if gotInstruction != "data" || gotTrust != "untrusted" || gotData != "organizational" || gotMayGrant {
		t.Fatalf("classification changed in the store: instruction=%q trust=%q data=%q mayGrant=%v",
			gotInstruction, gotTrust, gotData, gotMayGrant)
	}
	if string(gotContent) != body {
		t.Fatalf("content changed in the store: %q", string(gotContent))
	}
}

// The four invariants must be refused by the database itself. An assembler
// that rejects them first proves the assembler, not the contract -- and the
// assembler is one refactor away from being bypassed by a new writer.
func TestTheDatabaseRefusesMisclassifiedRepositoryEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	platform := openStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetSchema(t, ctx, platform)
	syncCanonical(t, ctx, platform)

	valid := segmentRow{
		kind: "repository_evidence", reference: "repository://x/y.go#L1-L2", version: "ce60b66",
		instruction: "data", trust: "untrusted", data: "organizational", mayGrant: false,
		content: "package y",
	}
	// The same row must be accepted, or the negatives below would pass for
	// the wrong reason -- any rejected insert looks alike.
	if _, err := tryInsertSegment(ctx, platform, valid); err != nil {
		t.Fatalf("the well-formed fragment was refused, so the negatives prove nothing: %v", err)
	}

	for _, tc := range []struct {
		name  string
		alter func(*segmentRow)
	}{
		{"granting capabilities", func(r *segmentRow) { r.mayGrant = true }},
		{"not data", func(r *segmentRow) { r.instruction = "role_instruction" }},
		{"not untrusted", func(r *segmentRow) { r.trust = "approved" }},
		{"not organizational", func(r *segmentRow) { r.data = "sanitized" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := valid
			tc.alter(&row)
			_, insertErr := tryInsertSegment(ctx, platform, row)
			if insertErr == nil {
				t.Fatalf("PostgreSQL accepted repository evidence %s", tc.name)
			}
			if !strings.Contains(insertErr.Error(), "23514") &&
				!strings.Contains(strings.ToLower(insertErr.Error()), "constraint") {
				t.Fatalf("refused for the wrong reason, not by a check constraint: %v", insertErr)
			}
		})
	}
}
