package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
)

func TestAuthorizationExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "deny", err: authorization.ErrCapabilityDenied, want: exitDenied},
		{name: "approval required", err: authorization.ErrApprovalRequired, want: exitApprovalRequired},
		{name: "pending", err: authorization.ErrApprovalPending, want: exitApprovalRequired},
		{name: "database", err: authorization.ErrDatabaseUnavailable, want: exitDatabase},
		{name: "invalid input", err: authorization.ErrInvalidInput, want: exitUsage},
		{name: "idempotency conflict", err: authorization.ErrIdempotencyConflict, want: exitDrift},
		{name: "missing request", err: authorization.ErrRequestNotFound, want: exitInvalid},
		{name: "operational", err: errors.New("unexpected"), want: exitInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := authorizationError(&stderr, test.err); got != test.want {
				t.Fatalf("exit=%d want=%d stderr=%q", got, test.want, stderr.String())
			}
		})
	}
}

func TestRootUsageIncludesAuthorization(t *testing.T) {
	var output bytes.Buffer
	printUsage(&output)
	if !bytes.Contains(output.Bytes(), []byte("authorization <evaluate|request|get|list|decide|consume|cancel|expire>")) {
		t.Fatalf("usage=%q", output.String())
	}
}
