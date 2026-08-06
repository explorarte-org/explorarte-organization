package modelruntime

import (
	"os"
	"path/filepath"
	"testing"

	organizationregistry "github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

func TestLoadCanonicalRoutingAndPlan(t *testing.T) {
	routing, err := LoadCanonicalRouting(filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	if routing.Hash == "" || len(routing.Policies) != 6 {
		t.Fatalf("unexpected routing: %#v", routing)
	}
	roles := []RoleRef{{ID: "empresa/ceo", ModelPolicy: "executive.ceo", Enabled: true, Executable: true}, {ID: "ingenieria_ia/orquestador", ModelPolicy: "department.leader", Enabled: true, Executable: true}, {ID: "ingenieria_ia/qa", ModelPolicy: "department.worker", Enabled: true, Executable: true}}
	plan, err := BuildRegistryPlan(roles, OrganizationRef{ID: "explorarte", RevisionID: 7}, routing)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Bindings) != 3 || len(plan.Versions) != 6 {
		t.Fatalf("unexpected plan sizes: %#v", plan)
	}
	for _, version := range plan.Versions {
		if version.ProviderID == "openai_compatible" {
			if !version.DispatchEnabled || version.AdapterStatus != AdapterAvailable || version.Transport != TransportHTTP {
				t.Fatalf("compiled openai-compatible adapter was not materialized: %#v", version)
			}
			continue
		}
		if version.DispatchEnabled || version.AdapterStatus != AdapterUnavailable {
			t.Fatalf("unimplemented provider unexpectedly enabled: %#v", version)
		}
	}
}
func TestLoadCanonicalRoutingRejectsDuplicateKey(t *testing.T) {
	dir := t.TempDir()
	body := []byte("schema_version: 1\ndocument_status: test\npolicies:\n  x:\n    provider: a\n    provider: b\n    model: m\n    transport: fake_adapter\nrouting_invariants:\n- bad\n")
	if err := os.WriteFile(filepath.Join(dir, routingFileName), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCanonicalRouting(dir); err == nil {
		t.Fatal("expected duplicate key rejection")
	}
}
func TestFakeCanonicalRouteCanDispatch(t *testing.T) {
	dir := t.TempDir()
	body := []byte("schema_version: 1\ndocument_status: test\npolicies:\n  department.worker:\n    provider: test.fake\n    model: deterministic-v1\n    transport: fake_adapter\n    capabilities:\n      - structured.output\nrouting_invariants:\n  - roles do not choose models\n")
	if err := os.WriteFile(filepath.Join(dir, routingFileName), body, 0o600); err != nil {
		t.Fatal(err)
	}
	routing, err := loadCanonicalRoutingDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildRegistryPlan([]RoleRef{{ID: "x/y", ModelPolicy: "department.worker", Enabled: true, Executable: true}}, OrganizationRef{ID: "explorarte", RevisionID: 1}, routing)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Versions[0].DispatchEnabled || plan.Versions[0].AdapterStatus != AdapterAvailable {
		t.Fatalf("fake route not enabled: %#v", plan.Versions[0])
	}
}

func TestCanonicalRoutingHashMatchesOrganizationRegistryDigest(t *testing.T) {
	canonicalDir := filepath.Join("..", "..", "docs", "canonical")
	routing, err := LoadCanonicalRouting(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := organizationregistry.NewLoader(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	var expected string
	for _, document := range snapshot.Documents {
		if document.Path == routingFileName {
			expected = document.SemanticHash
			break
		}
	}
	if expected == "" {
		t.Fatal("model-routing.yaml digest is absent from organization registry snapshot")
	}
	if routing.Hash != expected {
		t.Fatalf("routing hash drift: got %s want %s", routing.Hash, expected)
	}
}

func TestRegistryPlanBindsRevisionAndDisablesInactiveRoles(t *testing.T) {
	routing, err := LoadCanonicalRouting(filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	role := RoleRef{ID: "ingenieria_ia/qa", ModelPolicy: "department.worker", Enabled: false, Executable: false}
	first, err := BuildRegistryPlan([]RoleRef{role}, OrganizationRef{ID: "explorarte", RevisionID: 7, ModelRoutingHash: routing.Hash}, routing)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildRegistryPlan([]RoleRef{role}, OrganizationRef{ID: "explorarte", RevisionID: 8, ModelRoutingHash: routing.Hash}, routing)
	if err != nil {
		t.Fatal(err)
	}
	if first.Bindings[0].Active {
		t.Fatal("disabled role received active binding")
	}
	if first.Versions[0].VersionHash == second.Versions[0].VersionHash {
		t.Fatal("profile version hash did not bind organization revision")
	}
	if _, err = BuildRegistryPlan([]RoleRef{role}, OrganizationRef{ID: "explorarte", RevisionID: 7, ModelRoutingHash: "wrong"}, routing); err == nil {
		t.Fatal("expected canonical revision hash mismatch")
	}
}

func TestCanonicalRoutingRejectsUnknownAndAliasSyntax(t *testing.T) {
	for name, body := range map[string]string{
		"unknown": "schema_version: 1\ndocument_status: test\nunknown: value\npolicies:\n  x:\n    provider: test.fake\n    model: v1\n    transport: fake_adapter\nrouting_invariants:\n- safe\n",
		"alias":   "schema_version: 1\ndocument_status: test\npolicies:\n  x: &base\n    provider: test.fake\n    model: v1\n    transport: fake_adapter\nrouting_invariants:\n- safe\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, routingFileName), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadCanonicalRouting(dir); err == nil {
				t.Fatal("expected strict YAML rejection")
			}
		})
	}
}
