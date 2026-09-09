package modelruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRoutingDoc(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, routingFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const poolPolicyDoc = `schema_version: 1
document_status: test
policies:
  research.worker:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    capabilities:
      - structured.output
    candidates:
      - provider: cloudflare_workers_ai
        model: "@cf/zai-org/glm-4.7-flash"
        transport: http_adapter
        capacity_class: free_daily
        priority: 10
      - provider: mistral
        model: "ministral-8b-2512"
        transport: http_adapter
        capacity_class: credit_monthly
        priority: 20
routing_invariants:
  - roles do not choose models
`

// TestBuildRegistryPlanPoolMaterializesDistinctCandidateProfiles: Section
// 12.M's prerequisite -- each candidate gets its OWN Provider/Profile/
// Version/CapabilitySnapshot, a RoutingPolicy row is emitted, and the
// role bound to this pool policy gets NO RoleBinding (that is resolved
// dynamically, never through role_model_bindings).
func TestBuildRegistryPlanPoolMaterializesDistinctCandidateProfiles(t *testing.T) {
	dir := writeRoutingDoc(t, poolPolicyDoc)
	routing, err := loadCanonicalRoutingDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	roles := []RoleRef{{ID: "investigacion/research_worker_hourly", ModelPolicy: "research.worker", Enabled: true, Executable: true}}
	plan, err := BuildRegistryPlan(roles, OrganizationRef{ID: "explorarte", RevisionID: 1}, routing)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RoutingPolicies) != 1 || plan.RoutingPolicies[0].PolicyID != "research.worker" || plan.RoutingPolicies[0].RoutingMode != RoutingModePool {
		t.Fatalf("expected one pool RoutingPolicy, got %#v", plan.RoutingPolicies)
	}
	if len(plan.RoutingCandidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(plan.RoutingCandidates))
	}
	if len(plan.Versions) != 2 || len(plan.Profiles) != 2 || len(plan.CapabilitySnapshots) != 2 {
		t.Fatalf("expected 2 independently materialized profile/version/capability rows, got versions=%d profiles=%d caps=%d", len(plan.Versions), len(plan.Profiles), len(plan.CapabilitySnapshots))
	}
	if plan.Versions[0].ProfileID == plan.Versions[1].ProfileID {
		t.Fatal("pool candidates must not share a profile")
	}
	seenProviders := map[string]bool{}
	for _, v := range plan.Versions {
		seenProviders[v.ProviderID] = true
	}
	if !seenProviders["cloudflare_workers_ai"] || !seenProviders["mistral"] {
		t.Fatalf("expected both candidate providers materialized: %#v", seenProviders)
	}
	if len(plan.Bindings) != 0 {
		t.Fatalf("pool-policy role must get NO RoleBinding, got %#v", plan.Bindings)
	}
	if len(plan.Providers) != 2 {
		t.Fatalf("expected 2 distinct providers registered, got %d", len(plan.Providers))
	}
}

// TestBuildRegistryPlanStaticUnaffectedByPoolPolicy proves a static
// policy sitting alongside a pool policy in the same document keeps
// producing exactly a RoleBinding, unaffected (invariant #16).
func TestBuildRegistryPlanStaticUnaffectedByPoolPolicy(t *testing.T) {
	doc := `schema_version: 1
document_status: test
policies:
  research.worker:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    capabilities:
      - structured.output
    candidates:
      - provider: cloudflare_workers_ai
        model: "@cf/zai-org/glm-4.7-flash"
        transport: http_adapter
        capacity_class: free_daily
        priority: 10
      - provider: mistral
        model: "ministral-8b-2512"
        transport: http_adapter
        capacity_class: credit_monthly
        priority: 20
  department.worker:
    provider: test.fake
    model: v1
    transport: fake_adapter
routing_invariants:
  - roles do not choose models
`
	dir := writeRoutingDoc(t, doc)
	routing, err := loadCanonicalRoutingDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	roles := []RoleRef{
		{ID: "investigacion/research_worker_hourly", ModelPolicy: "research.worker", Enabled: true, Executable: true},
		{ID: "ingenieria_ia/qa", ModelPolicy: "department.worker", Enabled: true, Executable: true},
	}
	plan, err := BuildRegistryPlan(roles, OrganizationRef{ID: "explorarte", RevisionID: 1}, routing)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Bindings) != 1 || plan.Bindings[0].RoleID != "ingenieria_ia/qa" {
		t.Fatalf("expected exactly the static role's binding, got %#v", plan.Bindings)
	}
}

// TestBuildRegistryPlanCanonicalHashDriftsWithPoolFields: Section 12.M.
// Changing priority, capacity_class, or adding/removing a candidate must
// change the canonical routing hash -- the whole-document hash already
// covers this (every byte of the YAML feeds it), and the per-candidate
// CandidateHash independently changes too.
func TestBuildRegistryPlanCanonicalHashDriftsWithPoolFields(t *testing.T) {
	dir := writeRoutingDoc(t, poolPolicyDoc)
	base, err := loadCanonicalRoutingDocument(dir)
	if err != nil {
		t.Fatal(err)
	}

	changedPriority := `schema_version: 1
document_status: test
policies:
  research.worker:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    capabilities:
      - structured.output
    candidates:
      - provider: cloudflare_workers_ai
        model: "@cf/zai-org/glm-4.7-flash"
        transport: http_adapter
        capacity_class: free_daily
        priority: 99
      - provider: mistral
        model: "ministral-8b-2512"
        transport: http_adapter
        capacity_class: credit_monthly
        priority: 20
routing_invariants:
  - roles do not choose models
`
	dir2 := writeRoutingDoc(t, changedPriority)
	changed, err := loadCanonicalRoutingDocument(dir2)
	if err != nil {
		t.Fatal(err)
	}
	if base.Hash == changed.Hash {
		t.Fatal("changing a candidate's priority must change the canonical routing hash")
	}

	roles := []RoleRef{{ID: "investigacion/research_worker_hourly", ModelPolicy: "research.worker", Enabled: true, Executable: true}}
	basePlan, err := BuildRegistryPlan(roles, OrganizationRef{ID: "explorarte", RevisionID: 1}, base)
	if err != nil {
		t.Fatal(err)
	}
	changedPlan, err := BuildRegistryPlan(roles, OrganizationRef{ID: "explorarte", RevisionID: 1}, changed)
	if err != nil {
		t.Fatal(err)
	}
	if basePlan.RoutingPolicies[0].CanonicalHash == changedPlan.RoutingPolicies[0].CanonicalHash {
		t.Fatal("RoutingPolicy.CanonicalHash must drift when a candidate's priority changes")
	}
}

func TestValidatePoolPolicyRejectsMissingSelector(t *testing.T) {
	doc := "schema_version: 1\ndocument_status: test\npolicies:\n  research.worker:\n    routing_mode: pool\n    candidates:\n      - provider: cloudflare_workers_ai\n        model: m\n        transport: http_adapter\n        capacity_class: free_daily\n        priority: 1\nrouting_invariants:\n  - x\n"
	dir := writeRoutingDoc(t, doc)
	if _, err := loadCanonicalRoutingDocument(dir); err == nil {
		t.Fatal("expected rejection: pool policy missing selector")
	}
}

func TestValidatePoolPolicyRejectsNoCandidates(t *testing.T) {
	doc := "schema_version: 1\ndocument_status: test\npolicies:\n  research.worker:\n    routing_mode: pool\n    selector: free_capacity_v1\nrouting_invariants:\n  - x\n"
	dir := writeRoutingDoc(t, doc)
	if _, err := loadCanonicalRoutingDocument(dir); err == nil {
		t.Fatal("expected rejection: pool policy with zero candidates")
	}
}

func TestValidatePoolPolicyRejectsDuplicateCandidate(t *testing.T) {
	doc := `schema_version: 1
document_status: test
policies:
  research.worker:
    routing_mode: pool
    selector: free_capacity_v1
    candidates:
      - provider: mistral
        model: ministral-8b-2512
        transport: http_adapter
        capacity_class: credit_monthly
        priority: 1
      - provider: mistral
        model: ministral-8b-2512
        transport: http_adapter
        capacity_class: credit_monthly
        priority: 2
routing_invariants:
  - x
`
	dir := writeRoutingDoc(t, doc)
	if _, err := loadCanonicalRoutingDocument(dir); err == nil {
		t.Fatal("expected rejection: duplicate candidate provider+model")
	}
}

func TestValidatePoolPolicyRejectsPaidCandidateWithoutAllowPaid(t *testing.T) {
	doc := `schema_version: 1
document_status: test
policies:
  research.worker:
    routing_mode: pool
    selector: free_capacity_v1
    candidates:
      - provider: openai_compatible
        model: gpt-5
        transport: http_adapter
        capacity_class: paid
        priority: 1
routing_invariants:
  - x
`
	dir := writeRoutingDoc(t, doc)
	if _, err := loadCanonicalRoutingDocument(dir); err == nil {
		t.Fatal("expected rejection: paid candidate without allow_paid: true")
	}
}

func TestValidateStaticPolicyRejectsPoolFields(t *testing.T) {
	doc := `schema_version: 1
document_status: test
policies:
  department.worker:
    provider: test.fake
    model: v1
    transport: fake_adapter
    allow_paid: true
routing_invariants:
  - x
`
	dir := writeRoutingDoc(t, doc)
	if _, err := loadCanonicalRoutingDocument(dir); err == nil {
		t.Fatal("expected rejection: static policy setting a pool-only field (allow_paid)")
	}
}

func TestValidatePoolPolicyRejectsStaticProviderField(t *testing.T) {
	doc := `schema_version: 1
document_status: test
policies:
  research.worker:
    routing_mode: pool
    provider: mistral
    selector: free_capacity_v1
    candidates:
      - provider: mistral
        model: ministral-8b-2512
        transport: http_adapter
        capacity_class: credit_monthly
        priority: 1
routing_invariants:
  - x
`
	dir := writeRoutingDoc(t, doc)
	if _, err := loadCanonicalRoutingDocument(dir); err == nil {
		t.Fatal("expected rejection: pool policy also setting a static provider")
	}
}

func TestValidateRoutingRejectsUnknownMode(t *testing.T) {
	doc := "schema_version: 1\ndocument_status: test\npolicies:\n  x:\n    routing_mode: quantum\n    provider: test.fake\n    model: v1\n    transport: fake_adapter\nrouting_invariants:\n  - x\n"
	dir := writeRoutingDoc(t, doc)
	if _, err := loadCanonicalRoutingDocument(dir); err == nil {
		t.Fatal("expected rejection: unknown routing_mode")
	}
}
