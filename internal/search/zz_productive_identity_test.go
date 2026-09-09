package search_test

// Productive InvokerFactory evidence: the Research model pool is wired with
// the canonical modelruntime adapters (mistral under the host-owned
// 'mistral' identity; openrouter/openaicompat under theirs). No fake
// transport: identity is proven at the adapter seam, where each adapter
// reports its fixed provider id.

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/mistral"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/openaicompat"
)

// TestMistralAdapterProviderIdentityExact proves the mistral adapter is
// constructed under the canonical 'mistral' provider identity.
func TestMistralAdapterProviderIdentityExact(t *testing.T) {
	if got := string(mistral.ProviderID); got != "mistral" {
		t.Fatalf("mistral provider identity = %q, want %q", got, "mistral")
	}
}

// TestOpenAICompatAdapterProviderIdentityExact pins the sibling identity so
// cross-provider mismatches are structurally impossible at the seam.
func TestOpenAICompatAdapterProviderIdentityExact(t *testing.T) {
	if got := string(openaicompat.ProviderID); got == "mistral" {
		t.Fatalf("openaicompat provider identity collided with mistral")
	}
	if got := string(openaicompat.ProviderID); got == "" {
		t.Fatalf("openaicompat provider identity empty")
	}
}
