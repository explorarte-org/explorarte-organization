package ceochat

import (
	"regexp"
	"strings"
	"testing"
)

// localValidOwnerRootCausationPattern is an INDEPENDENT reimplementation
// of the exact suffix shape internal/modeldispatch's own
// validOwnerRootCausation/principalKeyPattern require after "owner:" --
// unexported in that different package, so it cannot be imported here.
// Kept as a literal copy, never refactored to share code with
// canonicalOwnerCausation in service.go, so this test cannot become
// tautological by construction.
var localValidOwnerRootCausationPattern = regexp.MustCompile(`^[a-zA-Z0-9]+(?:[._/-][a-zA-Z0-9]+)*$`)

func acceptedByModeldispatchValidOwnerRootCausation(value string) bool {
	const prefix = "owner:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(value, prefix)
	return len(suffix) >= 1 && len(value) <= 200 && localValidOwnerRootCausationPattern.MatchString(suffix)
}

// TestAcceptedCEOChatIdempotencyKeysAlwaysProduceTrustedRootCausation is
// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_FINAL_CLOSURE_V1 item 2:
// SendRequest.IdempotencyKey and modeldispatch's trusted-root causation
// pattern are two independent contracts (1..maxIdempotencyKeyLen bytes of
// ANY content vs. a strict principal-key-shaped suffix) that must never
// be allowed to diverge into "Send validation = PASS, task creation =
// PASS, StartAttempt = PASS, AuthorizedAttemptProvisioner = FAIL" purely
// from a syntax mismatch between the two domains. Every key this test
// documents as Send-acceptable must derive a causation
// acceptedByModeldispatchValidOwnerRootCausation accepts.
func TestAcceptedCEOChatIdempotencyKeysAlwaysProduceTrustedRootCausation(t *testing.T) {
	keys := map[string]string{
		"lowercase-simple":      "turn-1",
		"uuid-like":             "550e8400-e29b-41d4-a716-446655440000",
		"uppercase":             "TURN-KEY-UPPERCASE",
		"colon":                 "2026-01-01T00:00:00Z",
		"internal whitespace":   "turn key with spaces",
		"leading/trailing ws":   "  turn-with-padding  ",
		"unicode/emoji":         "turno-🎉-clave",
		"slashes":               "campaign/turn/1",
		"dots":                  "v1.turn.1",
		"mixed punctuation":     "turn_1:retry#2 (final)",
		"single character":      "a",
		"max length (120)":      strings.Repeat("k", maxIdempotencyKeyLen),
		"already principal-ish": "already-safe.principal_key/shape-1",
	}
	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			req := SendRequest{ConversationID: 1, ActorRoleID: "empresa/human", IdempotencyKey: key, Content: "hola"}
			if err := validateSendRequest(req); err != nil {
				t.Fatalf("Send rejects idempotency key %q, which this test documents as an accepted case: %v", key, err)
			}
			causation := canonicalOwnerCausation(1, key)
			if !acceptedByModeldispatchValidOwnerRootCausation(causation) {
				t.Fatalf("idempotency key %q -> causation %q is NOT a valid trusted-root causation under modeldispatch's own pattern", key, causation)
			}
		})
	}
}

// TestCanonicalOwnerCausationIsDeterministicAndConversationScoped proves
// the derivation is stable (the same input always derives the same
// causation -- required for idempotent task creation to keep working) and
// that two different conversations reusing the identical idempotency key
// text derive DIFFERENT causations, so nothing about this scheme
// introduces a false collision across conversations.
func TestCanonicalOwnerCausationIsDeterministicAndConversationScoped(t *testing.T) {
	first := canonicalOwnerCausation(1, "same-key")
	second := canonicalOwnerCausation(1, "same-key")
	if first != second {
		t.Fatalf("canonicalOwnerCausation is not deterministic: %q != %q", first, second)
	}
	other := canonicalOwnerCausation(2, "same-key")
	if first == other {
		t.Fatal("canonicalOwnerCausation collided across two different conversations reusing the same idempotency key text")
	}
}
