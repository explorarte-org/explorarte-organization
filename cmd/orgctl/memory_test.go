package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

func TestDecodeMemoryStrictRejectsUnknownFields(t *testing.T) {
	var input memoryMutationInput
	err := decodeMemoryStrict([]byte(`{"entry_id":"mem-1","expected_revision":1,"actor_role_id":"empresa/human","reason":"review","unexpected":true}`), &input)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error=%v, want unknown field", err)
	}
}

func TestDecodeMemoryStrictRejectsMultipleTopLevelValues(t *testing.T) {
	var input memoryMutationInput
	err := decodeMemoryStrict([]byte(`{"entry_id":"mem-1","expected_revision":1,"actor_role_id":"empresa/human","reason":"review"} {}`), &input)
	if err == nil || !strings.Contains(err.Error(), "multiple top-level") {
		t.Fatalf("error=%v, want multiple top-level rejection", err)
	}
}

func TestDecodeMemoryStrictAcceptsOneWellFormedValue(t *testing.T) {
	var input memoryReviewInput
	err := decodeMemoryStrict([]byte(`{"entry_id":"mem-1","expected_revision":1,"actor_role_id":"empresa/human","reason":"reviewed evidence","outcome":"approve"}`), &input)
	if err != nil {
		t.Fatal(err)
	}
	if input.EntryID != "mem-1" || input.Outcome != memory.ReviewApprove {
		t.Fatalf("decoded input=%+v", input)
	}
}

func TestMemoryCommandErrorMapsAuthorizationAndDomainErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "denied", err: authorization.ErrCapabilityDenied, want: exitDenied},
		{name: "approval required", err: authorization.ErrApprovalRequired, want: exitApprovalRequired},
		{name: "revision", err: memory.ErrRevisionConflict, want: exitDrift},
		{name: "invalid admission", err: memory.ErrInvalidAdmission, want: exitInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := memoryCommandError(&stderr, test.err); got != test.want {
				t.Fatalf("exit=%d want %d stderr=%q", got, test.want, stderr.String())
			}
		})
	}
}

func TestMemoryCommandErrorPreservesWrappedSentinels(t *testing.T) {
	var stderr bytes.Buffer
	err := errors.Join(errors.New("context"), memory.ErrForbiddenDataClass)
	if got := memoryCommandError(&stderr, err); got != exitInvalid {
		t.Fatalf("exit=%d stderr=%q", got, stderr.String())
	}
}
