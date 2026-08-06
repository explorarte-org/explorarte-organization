package modelidentity

import (
	"errors"
	"os"
	"path/filepath"
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

func TestLoadCanonicalPolicyStrictAndDeterministic(t *testing.T) {
	dir := writePolicy(t, "schema_version: \"1\"\ndocument_status: active\npolicy_id: model-execution-identity\npolicy_version: 1\ndefault_action: deny\nalgorithm: ed25519\nchallenge_ttl_seconds: 120\nclock_skew_seconds: 15\n")
	first, err := LoadCanonicalPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadCanonicalPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalHash == "" || first.CanonicalHash != second.CanonicalHash || first.Algorithm != AlgorithmEd25519 || first.DefaultAction != "deny" {
		t.Fatalf("unexpected policy: %#v", first)
	}
}

func TestLoadCanonicalPolicyRejectsUnsafeDocuments(t *testing.T) {
	cases := map[string]string{
		"unknown field":     "schema_version: \"1\"\ndocument_status: active\npolicy_id: model-execution-identity\npolicy_version: 1\ndefault_action: deny\nalgorithm: ed25519\nchallenge_ttl_seconds: 120\nclock_skew_seconds: 15\nextra: true\n",
		"duplicate key":     "schema_version: \"1\"\nschema_version: \"1\"\n",
		"allow default":     "schema_version: \"1\"\ndocument_status: active\npolicy_id: model-execution-identity\npolicy_version: 1\ndefault_action: allow\nalgorithm: ed25519\nchallenge_ttl_seconds: 120\nclock_skew_seconds: 15\n",
		"unknown algorithm": "schema_version: \"1\"\ndocument_status: active\npolicy_id: model-execution-identity\npolicy_version: 1\ndefault_action: deny\nalgorithm: rsa\nchallenge_ttl_seconds: 120\nclock_skew_seconds: 15\n",
		"alias":             "schema_version: &v \"1\"\ndocument_status: *v\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadCanonicalPolicy(writePolicy(t, body))
			if !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
