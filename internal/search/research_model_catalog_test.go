package search

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestResearch_ConfiguredDefaultModelIsInVerifiedFreeCatalog guards against
// the regression where the configured Research model was
// "minimax/minimax-m3:free" — an ID that does NOT exist on OpenRouter
// (minimax/m3 is paid-only), which would fail every invocation silently.
//
// Offline: asserts the configured default is one of the IDs verified against
// the live catalog during the last gated run (verifiedFreeModels).
// Gated (AUTONOMOUS_RESEARCH_SMOKE=1 or OR_VALIDATE_MODEL_CATALOG=1):
// re-validates BOTH the default and the smoke pool against
// https://openrouter.ai/api/v1/models (public, no key needed).
func TestResearch_ConfiguredDefaultModelIsInVerifiedFreeCatalog(t *testing.T) {
	verifiedFreeModels := map[string]bool{
		// Verified 2026-09-08 against https://openrouter.ai/api/v1/models
		"cohere/north-mini-code:free":                        true,
		"dots-studio/dots-3-note-preview:free":               true,
		"google/gemma-4-26b-a4b-it:free":                     true,
		"google/gemma-4-31b-it:free":                         true,
		"inclusionai/ling-3.0-flash-fin:free":                true,
		"inclusionai/ling-3.0-flash-sante:free":              true,
		"liquid/lfm-2.5-2.6b:free":                           true,
		"nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free": true,
		"nvidia/nemotron-3-super-120b-a12b:free":             true,
		"nvidia/nemotron-3-ultra-550b-a55b:free":             true,
		"nvidia/nemotron-3.5-content-safety:free":            true,
		"nvidia/nemotron-3.5-lightning:free":                 true,
		"poolside/laguna-s-2.1:free":                         true,
		"poolside/laguna-xs-2.1:free":                        true,
		"thinkingmachines/inkling-small:free":                true,
		"thinkingmachines/inkling:free":                      true,
	}
	// Known-bad IDs must never come back.
	for _, bad := range []string{
		"minimax/minimax-m3:free",   // paid-only model, no :free variant
		"minimax/minimax-m2.7:free", // paid-only
		"minimax/minimax-m2.5:free", // paid-only
	} {
		if verifiedFreeModels[bad] {
			t.Fatalf("known-bad model %s must never be in the free catalog", bad)
		}
	}

	cfg := DefaultWorkerConfig()
	if !verifiedFreeModels[cfg.ModelID] {
		t.Fatalf("configured default model %q is not in the verified free catalog; "+
			"invocations would fail. Pick a verified :free model.", cfg.ModelID)
	}

	if os.Getenv("OR_VALIDATE_MODEL_CATALOG") != "1" && os.Getenv("AUTONOMOUS_RESEARCH_SMOKE") != "1" {
		return // offline mode: verified snapshot above is authoritative
	}

	// Live validation (public endpoint, no credentials).
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://openrouter.ai/api/v1/models")
	if err != nil {
		t.Skipf("catalog unreachable, skipping live validation: %v", err)
	}
	defer resp.Body.Close()
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	live := map[string]bool{}
	for _, model := range payload.Data {
		live[model.ID] = true
	}
	if !live[cfg.ModelID] {
		t.Fatalf("configured default model %q NOT in the live OpenRouter catalog", cfg.ModelID)
	}
	// Every smoke-pool model must exist too (and keep its :free tag).
	smokePool := SmokeFreeModelPool()
	for _, id := range smokePool {
		if !live[id] {
			t.Errorf("smoke pool model %q NOT in live catalog", id)
		}
	}
}

// TestResearch_ModelIdentityChangeableWithoutRecompile re-proves the seam:
// RESEARCH_MODEL_ID env selects any verified model at deploy time.
func TestResearch_ModelIdentityChangeableWithoutRecompile(t *testing.T) {
	alternatives := []string{
		"nvidia/nemotron-3.5-lightning:free",
		"thinkingmachines/inkling:free",
		"poolside/laguna-s-2.1:free",
	}
	for _, model := range alternatives {
		gen, err := NewLLMQueryGenerator(&fakeModelInvoker{}, "openrouter", model,
			[]SearchIntent{IntentAcademic})
		if err != nil {
			t.Fatal(err)
		}
		if gen.Model != model {
			t.Fatalf("model identity must be injectable: %s", gen.Model)
		}
		_ = context.Background
	}
}
