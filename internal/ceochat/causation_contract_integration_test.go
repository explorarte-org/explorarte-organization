//go:build integration

// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_FINAL_CLOSURE_V1 item 2's
// integration half: prove the full chain -- Send -> CreateTask ->
// StartAttempt -> AuthorizedAttemptProvisioner -> resolveTrustedRoot --
// actually succeeds, against real Postgres, using an idempotency key that
// would have failed modeldispatch's trusted-root causation pattern before
// canonicalOwnerCausation existed.
package ceochat_test

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
)

func TestCEOChatPreviouslyProblematicIdempotencyKeyReachesTrustedRootCausation(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	service := f.withScriptedModel(t, &scriptedModel{})
	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}

	// Colon and internal whitespace: both valid under SendRequest's own
	// contract (any 1..120 bytes), both rejected by
	// modeldispatch.validOwnerRootCausation's principal-key-shaped
	// pattern if forwarded raw -- exactly the previously-problematic shape
	// TestAcceptedCEOChatIdempotencyKeysAlwaysProduceTrustedRootCausation
	// proves canonicalOwnerCausation now handles.
	const problematicKey = "2026-01-01T00:00:00Z retry attempt #1"

	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: problematicKey, Content: "list findings please",
	})
	// A pre-canonicalOwnerCausation binary would fail here with
	// "provision ceochat turn dispatch authority: ... task N has
	// unsupported causation" -- resolveTrustedRoot rejecting the raw
	// "owner:2026-01-01T00:00:00Z retry attempt #1" causation outright,
	// before any model or tool call. Reaching Completed IS the proof the
	// whole chain -- CreateTask, StartAttempt,
	// EnsureAuthorizedAssignmentForRunningAttempt, resolveTrustedRoot --
	// accepted this key's derived causation.
	if err != nil {
		t.Fatalf("send with previously-problematic idempotency key %q: %v", problematicKey, err)
	}
	if result.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("outcome=%v, want Completed", result.Outcome)
	}
	if result.AssistantMessage == nil {
		t.Fatal("no assistant message despite a Completed outcome")
	}
}
