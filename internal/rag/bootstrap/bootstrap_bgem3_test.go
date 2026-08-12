package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	testBGEM3ModelRevision  = "bge-m3-2024-06"
	testBGEM3ArtifactSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func setBGEM3Env(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("ORG_EMBEDDING_PROVIDER_BGE_M3_ENABLED", "true")
	t.Setenv("ORG_EMBEDDING_PROVIDER_BGE_M3_BASE_URL", baseURL)
	t.Setenv("ORG_EMBEDDING_PROVIDER_BGE_M3_MODEL_REVISION", testBGEM3ModelRevision)
	t.Setenv("ORG_EMBEDDING_PROVIDER_BGE_M3_ARTIFACT_SHA256", testBGEM3ArtifactSHA256)
	t.Setenv("ORG_EMBEDDING_PROVIDER_BGE_M3_TOKENIZER_REVISION", "bge-m3-tokenizer-2024-06")
	t.Setenv("ORG_EMBEDDING_PROVIDER_BGE_M3_REQUEST_TIMEOUT", "2s")
}

// TestOpenBGEM3SemanticSearchRequiresReadySidecarAtStartup is R30.1-6: the
// bootstrap of the BGE-M3 profile must call Adapter.Healthy before ever
// returning usable SemanticSearchDeps — a sidecar reporting the wrong
// weights (or simply not ready) must fail Open outright, not be
// discovered only on the first real Embed call.
func TestOpenBGEM3SemanticSearchRequiresReadySidecarAtStartup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ready", "model_revision": "a-completely-different-revision",
			"artifact_sha256": testBGEM3ArtifactSHA256, "dimension": 1024,
		})
	}))
	defer server.Close()
	setBGEM3Env(t, server.URL)

	deps, err := openBGEM3SemanticSearch(nil)
	if err == nil {
		t.Fatal("expected sidecar identity drift at startup to fail Open, not silently return usable deps")
	}
	if deps != nil {
		t.Fatalf("deps=%+v want nil on a failed readiness check", deps)
	}
}

// TestOpenBGEM3SemanticSearchSucceedsWhenSidecarIsReadyAndMatches proves the
// happy path actually works end to end: a sidecar reporting the exact
// pinned identity and status=ready lets Open succeed and return deps
// wired to that same adapter.
func TestOpenBGEM3SemanticSearchSucceedsWhenSidecarIsReadyAndMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ready", "model_revision": testBGEM3ModelRevision,
			"artifact_sha256": testBGEM3ArtifactSHA256, "tokenizer_revision": "bge-m3-tokenizer-2024-06", "normalization": "l2", "pooling": "cls", "dimension": 1024,
		})
	}))
	defer server.Close()
	setBGEM3Env(t, server.URL)

	deps, err := openBGEM3SemanticSearch(nil)
	if err != nil {
		t.Fatal(err)
	}
	if deps == nil || !deps.LocalComputeOnly || deps.OnlineAdapter == nil {
		t.Fatalf("deps=%+v", deps)
	}
	if deps.Identity.ModelRevision != testBGEM3ModelRevision || deps.Identity.ArtifactSHA256 != testBGEM3ArtifactSHA256 {
		t.Fatalf("identity=%+v", deps.Identity)
	}
}

// TestOpenBGEM3SemanticSearchFailsWhenSidecarUnreachable proves a down
// sidecar fails Open too — readiness is checked, not merely assumed from
// the fact that a config existed.
func TestOpenBGEM3SemanticSearchFailsWhenSidecarUnreachable(t *testing.T) {
	setBGEM3Env(t, "http://127.0.0.1:1")
	if _, err := openBGEM3SemanticSearch(nil); err == nil {
		t.Fatal("expected an unreachable sidecar to fail Open")
	}
}
