package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

func TestContextExitCodesAreStable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "policy rejection", err: contextengine.Reject(contextengine.ReasonForbiddenDataClass, "source", "forbidden"), want: exitContextRejected},
		{name: "invalid input", err: contextengine.ErrInvalidRequest, want: exitContextRejected},
		{name: "idempotency conflict", err: contextengine.ErrIdempotencyConflict, want: exitContextRejected},
		{name: "invalidated", err: contextengine.ErrSnapshotInvalidated, want: exitContextStale},
		{name: "stale", err: contextengine.ErrSnapshotStale, want: exitContextStale},
		{name: "not found", err: contextengine.ErrSnapshotNotFound, want: exitInvalid},
		{name: "database", err: contextengine.ErrDatabaseUnavailable, want: exitDatabase},
		{name: "cancelled", err: context.Canceled, want: exitInternal},
		{name: "operational", err: errors.New("unexpected"), want: exitInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := contextError(&stderr, test.err); got != test.want {
				t.Fatalf("exit=%d want=%d stderr=%q", got, test.want, stderr.String())
			}
		})
	}
}

func TestRootUsageIncludesContextAndExitCodes(t *testing.T) {
	var output bytes.Buffer
	printUsage(&output)
	text := output.String()
	if !strings.Contains(text, "context <validate-source|build|get|list|render|validate|invalidate>") {
		t.Fatalf("usage=%q", text)
	}
	if !strings.Contains(text, "8 context rejected") || !strings.Contains(text, "9 context stale or invalidated") {
		t.Fatalf("missing context exit codes: %q", text)
	}
}

func TestRedactedSegmentsNeverExposeContent(t *testing.T) {
	values := []contextengine.Segment{{SourceReference: "profile", Content: []byte("private organizational text")}}
	got := redactedSegments(values)
	if len(got) != 1 || got[0].Content != nil {
		t.Fatalf("redacted=%+v", got)
	}
	if string(values[0].Content) != "private organizational text" {
		t.Fatal("input was mutated")
	}
}
