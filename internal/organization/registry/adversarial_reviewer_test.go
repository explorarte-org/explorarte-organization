package registry

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

const reviewerRoleID = "investigacion/revisor_adversarial"

// These tests read the canonical documents directly rather than a parsed
// projection of them. The authority boundary is a claim about what the
// organization has WRITTEN DOWN, and asserting it against the source is what
// makes the claim auditable by someone reading the same file.

func canonicalDocument(t *testing.T, name string, target any) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(canonicalDirForTest(t), name))
	if err != nil {
		t.Fatal(err)
	}
	if err = yaml.Unmarshal(body, target); err != nil {
		t.Fatal(err)
	}
}

func TestAdversarialReviewerIsAnIndependentTransversalRole(t *testing.T) {
	snapshot := loadTestSnapshot(t, canonicalDirForTest(t))
	reviewer := findRole(snapshot, reviewerRoleID)
	if reviewer == nil {
		t.Fatalf("%s is missing from the canonical catalog", reviewerRoleID)
	}
	if reviewer.UnitID != "investigacion" {
		t.Fatalf("reviewer belongs to %q", reviewer.UnitID)
	}
	if reviewer.AuthorityClass != "transversal_audit" {
		t.Fatalf("authority_class=%q", reviewer.AuthorityClass)
	}
	if reviewer.ModelPolicy == nil || *reviewer.ModelPolicy != "research.adversarial_review" {
		t.Fatalf("model_policy=%v", reviewer.ModelPolicy)
	}
	if reviewer.CanonicalLeader {
		t.Fatal("the reviewer is marked as a canonical leader")
	}
	// Activated: the owner resolved the model id, provisioned the credential
	// and confirmed the account exposes it. Enabled and executable is now the
	// asserted state, and the remaining fail-closed layers are the adapter's
	// own configuration, the egress scope and the pricing row.
	if !reviewer.Enabled || !reviewer.Executable {
		t.Fatal("the reviewer role is not dispatchable")
	}

	// Independence from the design author is structural, not a convention.
	author := findRole(snapshot, "ingenieria_ia/orquestador")
	if author == nil {
		t.Fatal("design author role is missing")
	}
	if reviewer.UnitID == author.UnitID {
		t.Fatalf("reviewer and design author share unit %q", author.UnitID)
	}
	if reviewer.ID == author.ID {
		t.Fatal("reviewer and design author are the same role")
	}
	// It also is not the existing brain auditor: two different jobs must stay
	// durably distinguishable without inferring it from the provider.
	if auditor := findRole(snapshot, "investigacion/auditor_cerebro_empresa"); auditor != nil {
		if auditor.ModelPolicy != nil && reviewer.ModelPolicy != nil && *auditor.ModelPolicy == *reviewer.ModelPolicy {
			t.Fatal("adversarial review and brain audit share a model policy")
		}
	}
}

type capabilityMatrix struct {
	Grants     map[string][]string `yaml:"grants"`
	HardDenies map[string][]string `yaml:"hard_denies"`
}

func TestReviewerAuthorityIsReadAndPublishOnly(t *testing.T) {
	var matrix capabilityMatrix
	canonicalDocument(t, "capability-matrix.yaml", &matrix)

	grants := matrix.Grants["transversal_audit"]
	denies := matrix.HardDenies["transversal_audit"]
	has := func(list []string, target string) bool {
		for _, value := range list {
			if value == target {
				return true
			}
		}
		return false
	}

	// What the reviewer may do.
	for _, capability := range []string{
		"organization.read_registry",
		"audit.read_sanitized_evidence",
		"audit.publish_finding",
	} {
		if !has(grants, capability) {
			t.Errorf("reviewer authority class lacks %q", capability)
		}
	}

	// What it may never do. task.review is on this list by an explicit
	// decision: the reviewer publishes findings and does not close the
	// epistemic question. Provider and dispatch selection are here for the
	// same reason -- a reviewer that could choose its own model could choose
	// the design author's.
	for _, capability := range []string{
		"task.assign_worker",
		"task.review",
		"memory.approve",
		"code.stage_write",
		"code.commit",
		"deployment.request",
		"model.dispatch_assignment.create",
		"model.dispatch_assignment.revoke",
	} {
		if !has(denies, capability) {
			t.Errorf("%q is not a hard deny for transversal_audit", capability)
		}
		if has(grants, capability) {
			t.Errorf("%q is granted to transversal_audit", capability)
		}
	}

	// Absent-by-omission under default-deny, and asserted so a future grant
	// list cannot quietly acquire them.
	for _, capability := range []string{
		"model.invoke",
		"project.create",
		"project.delegate_department",
		"organization.activate_skill",
		"task.approve_terminal",
		"rag.publish_approved",
		"model.execution_principal.register",
		"model.execution_identity_key.register",
	} {
		if has(grants, capability) {
			t.Errorf("transversal_audit acquired %q", capability)
		}
	}
}

type egressPolicyDocument struct {
	PolicyVersion int    `yaml:"policy_version"`
	DefaultAction string `yaml:"default_action"`
	HardDenies    []struct {
		DataClassification string `yaml:"data_classification"`
	} `yaml:"hard_denies"`
	Rules []struct {
		ProviderID         string `yaml:"provider_id"`
		DataClassification string `yaml:"data_classification"`
		Effect             string `yaml:"effect"`
	} `yaml:"rules"`
}

func TestXAIEgressSurfaceIsSanitizedAndPublicOnly(t *testing.T) {
	var document egressPolicyDocument
	canonicalDocument(t, "model-egress-policy.yaml", &document)
	if document.DefaultAction != "deny" {
		t.Fatalf("default_action=%q", document.DefaultAction)
	}
	allowed := map[string]struct{}{}
	for _, rule := range document.Rules {
		if rule.ProviderID != "xai" {
			continue
		}
		if rule.Effect != "allow" {
			continue
		}
		allowed[rule.DataClassification] = struct{}{}
	}
	if len(allowed) != 2 {
		t.Fatalf("xai allow surface=%v", allowed)
	}
	for _, classification := range []string{"public", "sanitized"} {
		if _, ok := allowed[classification]; !ok {
			t.Errorf("xai is missing an allow for %q", classification)
		}
	}
	// The one that matters: no rule may grant xai raw organizational data.
	if _, ok := allowed["organizational"]; ok {
		t.Fatal("xai is allowed to receive raw organizational data")
	}
	// clinical and secret stay globally hard-denied.
	hard := map[string]struct{}{}
	for _, deny := range document.HardDenies {
		hard[deny.DataClassification] = struct{}{}
	}
	for _, classification := range []string{"clinical", "secret"} {
		if _, ok := hard[classification]; !ok {
			t.Errorf("%q is not a global hard deny", classification)
		}
	}
}

// The compiled allowlist and the canonical policy must agree, or a rule could
// validate against one and be refused by the other.
func TestCompiledXAISurfaceMatchesTheCanonicalPolicy(t *testing.T) {
	classifications, ok := productiveEgressAllowRules["xai"]
	if !ok {
		t.Fatal("xai is absent from the compiled productive allowlist")
	}
	if len(classifications) != 2 {
		t.Fatalf("compiled xai surface=%v", classifications)
	}
	for _, classification := range []string{"public", "sanitized"} {
		if _, allowed := classifications[classification]; !allowed {
			t.Errorf("compiled surface lacks %q", classification)
		}
	}
	if _, allowed := classifications["organizational"]; allowed {
		t.Fatal("compiled surface allows xai/organizational")
	}
	if !productiveEgressAllowApproved("xai", "sanitized") {
		t.Fatal("xai/sanitized is not approved")
	}
	if productiveEgressAllowApproved("xai", "organizational") {
		t.Fatal("xai/organizational is approved")
	}
}
