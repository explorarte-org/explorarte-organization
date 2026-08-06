package modelegress

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, PolicyFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func validPolicyBody() string {
	return `schema_version: 0.1.0
document_status: branch_09_candidate
policy_id: model-egress
policy_version: 1
default_action: deny
hard_denies:
- data_classification: clinical
  reason_code: clinical_egress_forbidden
- data_classification: secret
  reason_code: secret_egress_forbidden
rules:
- provider_id: test.fake
  data_classification: organizational
  effect: deny
  reason_code: organizational_egress_not_approved
`
}

func TestLoadCanonicalPolicy(t *testing.T) {
	policy, err := LoadCanonicalPolicy(writePolicy(t, validPolicyBody()), LoadOptions{KnownProviders: []string{"test.fake"}})
	if err != nil {
		t.Fatal(err)
	}
	if policy.PolicyVersion != 1 || len(policy.CanonicalHash) != 64 || len(policy.Rules) != 1 {
		t.Fatalf("unexpected policy: %#v", policy)
	}
}

func TestCanonicalPolicyRejectsUnsafeYAMLAndUnknownFields(t *testing.T) {
	cases := []string{
		strings.Replace(validPolicyBody(), "schema_version: 0.1.0", "schema_version: 0.1.0\nschema_version: 0.2.0", 1),
		validPolicyBody() + "unknown_field: true\n",
		strings.Replace(validPolicyBody(), "reason_code: organizational_egress_not_approved", "reason_code: one\n  reason_code: two", 1),
		strings.Replace(validPolicyBody(), "hard_denies:", "hard_denies: &denies", 1),
		strings.Replace(validPolicyBody(), "rules:", "rules: *denies", 1),
		strings.Replace(validPolicyBody(), "schema_version: 0.1.0", "defaults: &defaults\n  schema_version: 0.1.0\n<<: *defaults", 1),
		strings.Replace(validPolicyBody(), "rules:", "rules: []", 1),
		strings.Replace(validPolicyBody(), "document_status: branch_09_candidate", "document_status: |\n  branch_09_candidate", 1),
		strings.Replace(validPolicyBody(), "policy_id:", "policy_id:\t", 1),
	}
	for index, body := range cases {
		if _, err := LoadCanonicalPolicy(writePolicy(t, body), LoadOptions{KnownProviders: []string{"test.fake"}}); err == nil {
			t.Fatalf("case %d unexpectedly accepted", index)
		}
	}
}

func TestCanonicalPolicyValidation(t *testing.T) {
	cases := []string{
		strings.Replace(validPolicyBody(), "policy_version: 1", "policy_version: 0", 1),
		strings.Replace(validPolicyBody(), "policy_version: 1", "policy_version: -1", 1),
		strings.Replace(validPolicyBody(), "default_action: deny", "default_action: allow", 1),
		strings.Replace(validPolicyBody(), "provider_id: test.fake", "provider_id: missing", 1),
		strings.Replace(validPolicyBody(), "data_classification: organizational", "data_classification: mystery", 1),
		strings.Replace(validPolicyBody(), "effect: deny", "effect: maybe", 1),
		strings.Replace(validPolicyBody(), "effect: deny", "effect: allow", 1),
		strings.Replace(validPolicyBody(), "data_classification: organizational", "data_classification: secret", 1),
		strings.Replace(validPolicyBody(), "reason_code: secret_egress_forbidden", "effect: allow\n  reason_code: secret_egress_forbidden", 1),
		validPolicyBody() + "- provider_id: test.fake\n  data_classification: organizational\n  effect: deny\n  reason_code: duplicate\n",
		strings.Replace(validPolicyBody(), "- data_classification: secret\n  reason_code: secret_egress_forbidden\n", "", 1),
		strings.Replace(validPolicyBody(), "- data_classification: clinical\n  reason_code: clinical_egress_forbidden\n", "", 1),
	}
	for index, body := range cases {
		_, err := LoadCanonicalPolicy(writePolicy(t, body), LoadOptions{KnownProviders: []string{"test.fake"}})
		if !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("case %d error=%v", index, err)
		}
	}
}

func TestFixturePolicyAllowsExplicitAllow(t *testing.T) {
	body := strings.Replace(validPolicyBody(), "effect: deny", "effect: allow", 1)
	policy, err := LoadCanonicalPolicy(writePolicy(t, body), LoadOptions{KnownProviders: []string{"test.fake"}, AllowExplicitAllows: true})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Rules[0].Effect != EffectAllow {
		t.Fatal("fixture allow was not preserved")
	}
}

func TestPolicyHashIgnoresRuleOrder(t *testing.T) {
	firstBody := validPolicyBody() + `- provider_id: test.fake
  data_classification: public
  effect: deny
  reason_code: public_egress_not_approved
`
	secondBody := strings.Replace(firstBody, `- provider_id: test.fake
  data_classification: organizational
  effect: deny
  reason_code: organizational_egress_not_approved
- provider_id: test.fake
  data_classification: public
  effect: deny
  reason_code: public_egress_not_approved
`, `- provider_id: test.fake
  data_classification: public
  effect: deny
  reason_code: public_egress_not_approved
- provider_id: test.fake
  data_classification: organizational
  effect: deny
  reason_code: organizational_egress_not_approved
`, 1)
	first, err := LoadCanonicalPolicy(writePolicy(t, firstBody), LoadOptions{KnownProviders: []string{"test.fake"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadCanonicalPolicy(writePolicy(t, secondBody), LoadOptions{KnownProviders: []string{"test.fake"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalHash != second.CanonicalHash {
		t.Fatalf("hash changed: %s != %s", first.CanonicalHash, second.CanonicalHash)
	}
}

func TestProductivePolicyAllowsOnlyCompiledOpenAICompatibleClasses(t *testing.T) {
	body := strings.Replace(validPolicyBody(), "provider_id: test.fake", "provider_id: openai_compatible", 1)
	body = strings.Replace(body, "data_classification: organizational", "data_classification: public", 1)
	body = strings.Replace(body, "effect: deny", "effect: allow", 1)
	body = strings.Replace(body, "reason_code: organizational_egress_not_approved", "reason_code: public_egress_approved_for_openai_compatible_v2", 1)
	options := ProductiveLoadOptions([]string{"openai_compatible", "deepseek"})
	policy, err := LoadCanonicalPolicy(writePolicy(t, body), options)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Rules[0].Effect != EffectAllow {
		t.Fatalf("rule=%+v", policy.Rules[0])
	}
	for name, candidate := range map[string]string{
		"organizational": strings.Replace(body, "data_classification: public", "data_classification: organizational", 1),
		"other provider": strings.Replace(body, "provider_id: openai_compatible", "provider_id: deepseek", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadCanonicalPolicy(writePolicy(t, candidate), options); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
