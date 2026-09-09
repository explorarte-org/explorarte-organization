package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// (A) A pool policy (routing_mode: pool, selector, allow_paid, capabilities,
// candidates) must parse under this package's own strict KnownFields(true)
// decode -- this is exactly what Blocker 6 broke.
func TestStrictYAMLAcceptsPoolRoutingDocument(t *testing.T) {
	body := []byte(`schema_version: 0.1.0
document_status: branch_0_candidate
policies:
  test.pool_blocker6:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: true
    capabilities:
    - text_generation
    - vision
    candidates:
    - provider: test.fake
      model: model-a
      transport: http_adapter
      capacity_class: small
      priority: 1
    - provider: test.fake
      model: model-b
      transport: http_adapter
      capacity_class: medium
      priority: 2
routing_invariants: []
`)
	var document modelRoutingDocument
	if err := decodeStrictYAML(body, &document); err != nil {
		t.Fatalf("decodeStrictYAML rejected a pool routing document: %v", err)
	}
	policy, exists := document.Policies["test.pool_blocker6"]
	if !exists {
		t.Fatal("test.pool_blocker6 policy missing after decode")
	}
	if policy.RoutingMode != "pool" || policy.Selector != "free_capacity_v1" || !policy.AllowPaid {
		t.Fatalf("pool scalar fields not decoded: %+v", policy)
	}
	if len(policy.Capabilities) != 2 || len(policy.Candidates) != 2 {
		t.Fatalf("pool list fields not decoded: %+v", policy)
	}
	if policy.Candidates[0].Provider != "test.fake" || policy.Candidates[0].Model != "model-a" || policy.Candidates[0].Priority != 1 {
		t.Fatalf("candidate[0] not decoded: %+v", policy.Candidates[0])
	}
}

// (B) Extending modelPolicyDoc with the pool fields must not relax
// KnownFields(true): a field this package genuinely does not know about --
// whether beside the pool fields on the policy, or inside a candidate --
// must still be rejected.
func TestStrictYAMLStillRejectsUnknownFieldsBesidePoolFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "unknown field on pool policy",
			body: `schema_version: 0.1.0
document_status: branch_0_candidate
policies:
  test.pool:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    capabilities: [text_generation]
    candidates:
    - provider: test.fake
      model: model-a
      transport: http_adapter
      capacity_class: small
      priority: 1
    totally_unexpected_field: true
routing_invariants: []
`,
		},
		{
			name: "unknown field on candidate",
			body: `schema_version: 0.1.0
document_status: branch_0_candidate
policies:
  test.pool:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    capabilities: [text_generation]
    candidates:
    - provider: test.fake
      model: model-a
      transport: http_adapter
      capacity_class: small
      priority: 1
      totally_unexpected_field: true
routing_invariants: []
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var document modelRoutingDocument
			if err := decodeStrictYAML([]byte(tc.body), &document); err == nil {
				t.Fatalf("decodeStrictYAML accepted an unknown field (%s)", tc.name)
			}
		})
	}
}

const poolBlocker6PolicyYAML = `  test.pool_blocker6:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    capabilities:
    - text_generation
    candidates:
    - provider: test.fake
      model: model-a
      transport: http_adapter
      capacity_class: small
      priority: 1
    - provider: test.fake
      model: model-b
      transport: http_adapter
      capacity_class: small
      priority: 2
`

// insertBeforeMarker inserts insertion immediately before the first line of
// path that equals marker exactly, and rewrites the file in place.
func insertBeforeMarker(t *testing.T, path, marker, insertion string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(body), "\n")
	index := -1
	for i, line := range lines {
		if line == marker {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("marker %q not found in %s", marker, path)
	}
	rebuilt := append([]string{}, lines[:index]...)
	rebuilt = append(rebuilt, strings.TrimRight(insertion, "\n"))
	rebuilt = append(rebuilt, lines[index:]...)
	if err := os.WriteFile(path, []byte(strings.Join(rebuilt, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendEgressRule(t *testing.T, path, providerID string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// effect: deny (not allow) deliberately: this helper exists to exercise
	// validateModelEgressPolicy's provider-existence check in isolation.
	// An "allow" rule additionally requires the provider/classification pair
	// to be productiveEgressAllowApproved -- an orthogonal, out-of-scope
	// compiled-adapter allowlist this fix must not touch or depend on.
	rule := "- provider_id: " + providerID + "\n  data_classification: public\n  effect: deny\n  reason_code: blocker6_pool_candidate_test\n"
	if err := os.WriteFile(path, append(body, []byte(rule)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// (C) A pool policy has no top-level Provider -- its providers live only in
// Candidates[*].Provider. validateModelEgressPolicy must accept an egress
// rule for such a candidate-only provider, and must still reject a rule for
// a provider that is genuinely absent from both Policies[*].Provider and
// Policies[*].Candidates[*].Provider.
func TestModelEgressAcceptsCandidateOnlyProviderAndRejectsUnknownProvider(t *testing.T) {
	dir := copyCanonical(t)
	insertBeforeMarker(t, filepath.Join(dir, "model-routing.yaml"), "routing_invariants:", poolBlocker6PolicyYAML)
	appendEgressRule(t, filepath.Join(dir, "model-egress-policy.yaml"), "test.fake")

	if _, report, err := mustLoader(t, dir).Load(); err != nil {
		t.Fatalf("load rejected a candidate-only provider egress rule: %v (%+v)", err, report)
	}

	unknownDir := copyCanonical(t)
	insertBeforeMarker(t, filepath.Join(unknownDir, "model-routing.yaml"), "routing_invariants:", poolBlocker6PolicyYAML)
	appendEgressRule(t, filepath.Join(unknownDir, "model-egress-policy.yaml"), "totally.unknown.provider")

	_, report, err := mustLoader(t, unknownDir).Load()
	if err == nil {
		t.Fatal("load accepted an egress rule for a provider absent from model-routing.yaml")
	}
	found := false
	for _, issue := range report.Errors {
		if issue.Code == "model_egress.provider_unknown" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected model_egress.provider_unknown, got %+v", report.Errors)
	}
}

// (D) Adding pool support must not change how a static-only canonical
// directory (the real docs/canonical today) parses and validates. This is a
// direct regression guard for the static path alongside the pool-specific
// tests above.
func TestStaticOnlyCanonicalDirectoryStillLoadsUnchanged(t *testing.T) {
	if _, report, err := mustLoader(t, canonicalDirForTest(t)).Load(); err != nil {
		t.Fatalf("load: %v (%+v)", err, report)
	}
	loader := mustLoader(t, canonicalDirForTest(t))
	documents, _, err := loader.readDocuments()
	if err != nil {
		t.Fatal(err)
	}
	normalizeDocuments(&documents)
	for id, policy := range documents.ModelRouting.Policies {
		if policy.RoutingMode != "" || policy.Selector != "" || policy.AllowPaid || len(policy.Capabilities) != 0 || len(policy.Candidates) != 0 {
			t.Fatalf("real canonical policy %q unexpectedly carries pool fields: %+v", id, policy)
		}
		if policy.Provider == "" {
			t.Fatalf("real canonical policy %q lost its static provider", id)
		}
	}
}

// (E) Semantic hashing: order, duplicate whitespace, and casing-irrelevant
// differences in capabilities/candidates that carry no semantic meaning must
// hash identically; genuine differences (candidate provider/model,
// capacity_class, priority, selector, allow_paid) must hash differently.
func TestPoolPolicySemanticHashIgnoresInsignificantDifferencesOnly(t *testing.T) {
	base := func() parsedDocuments {
		return parsedDocuments{
			ModelRouting: modelRoutingDocument{
				Policies: map[string]modelPolicyDoc{
					"test.pool": {
						RoutingMode:  "pool",
						Selector:     "free_capacity_v1",
						AllowPaid:    false,
						Capabilities: []string{"text_generation", "vision"},
						Candidates: []modelPolicyCandidateDoc{
							{Provider: "test.fake", Model: "model-a", Transport: "http_adapter", CapacityClass: "small", Priority: 1},
							{Provider: "test.fake", Model: "model-b", Transport: "http_adapter", CapacityClass: "small", Priority: 2},
						},
					},
				},
			},
		}
	}
	hashOf := func(docs parsedDocuments) string {
		t.Helper()
		normalizeDocuments(&docs)
		hashes, _, err := hashDocuments(docs)
		if err != nil {
			t.Fatal(err)
		}
		return hashes["model-routing.yaml"]
	}

	baseline := hashOf(base())

	insignificant := base()
	p := insignificant.ModelRouting.Policies["test.pool"]
	p.Capabilities = []string{" vision", "text_generation", "vision ", "text_generation"}
	p.Candidates = []modelPolicyCandidateDoc{
		{Provider: "test.fake", Model: " model-b", Transport: "http_adapter", CapacityClass: "small", Priority: 2},
		{Provider: "test.fake ", Model: "model-a", Transport: "http_adapter ", CapacityClass: "small", Priority: 1},
	}
	insignificant.ModelRouting.Policies["test.pool"] = p
	if got := hashOf(insignificant); got != baseline {
		t.Fatalf("insignificant differences changed the hash: baseline=%s got=%s", baseline, got)
	}

	type mutation struct {
		name  string
		apply func(*modelPolicyDoc)
	}
	mutations := []mutation{
		{"candidate provider", func(p *modelPolicyDoc) { p.Candidates[0].Provider = "test.other" }},
		{"candidate model", func(p *modelPolicyDoc) { p.Candidates[0].Model = "model-z" }},
		{"candidate capacity_class", func(p *modelPolicyDoc) { p.Candidates[0].CapacityClass = "large" }},
		{"candidate priority", func(p *modelPolicyDoc) { p.Candidates[0].Priority = 99 }},
		{"selector", func(p *modelPolicyDoc) { p.Selector = "other_selector_v1" }},
		{"allow_paid", func(p *modelPolicyDoc) { p.AllowPaid = true }},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			changed := base()
			cp := changed.ModelRouting.Policies["test.pool"]
			m.apply(&cp)
			changed.ModelRouting.Policies["test.pool"] = cp
			if got := hashOf(changed); got == baseline {
				t.Fatalf("mutation %q did not change the hash (baseline=%s)", m.name, baseline)
			}
		})
	}
}

// (F) A static policy (the overwhelming majority today) must not gain any
// zero-value pool JSON keys merely because modelPolicyDoc grew fields --
// its semantic hash must stay byte-for-byte what it always was.
func TestStaticPolicyJSONCarriesNoZeroValuePoolKeys(t *testing.T) {
	policy := modelPolicyDoc{
		Provider:  "deepseek",
		Model:     "deepseek-v4-pro",
		Transport: "http_adapter",
	}
	bytes, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bytes)
	for _, forbidden := range []string{"routing_mode", "selector", "allow_paid", "capabilities", "candidates"} {
		if strings.Contains(body, `"`+forbidden+`"`) {
			t.Fatalf("static policy JSON unexpectedly contains pool key %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{"provider", "model", "transport"} {
		if !strings.Contains(body, `"`+required+`"`) {
			t.Fatalf("static policy JSON lost required key %q: %s", required, body)
		}
	}
}
