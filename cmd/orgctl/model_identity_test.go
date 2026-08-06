package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeIdentityCommand(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity-key.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDecodeIdentityKeyCommandIsStrictAndPublicOnly(t *testing.T) {
	path := writeIdentityCommand(t, `{"organization_id":"explorarte","execution_principal_key":"oracle-01/model-runtime-01","public_key_base64":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","secret_ref":"file://model-execution/oracle-01/key-0001","idempotency_key":"key-1"}`)
	command, err := decodeIdentityKeyCommand(path)
	if err != nil {
		t.Fatal(err)
	}
	if command.ExecutionPrincipalKey == "" || command.PublicKeyBase64 == "" || command.SecretRef == "" {
		t.Fatalf("unexpected command: %+v", command)
	}
}

func TestDecodeIdentityKeyCommandRejectsPrivateMaterialAndTrailingJSON(t *testing.T) {
	cases := []string{
		`{"organization_id":"explorarte","execution_principal_key":"p","public_key_base64":"x","secret_ref":"file://x","idempotency_key":"k","private_key":"secret"}`,
		`{"organization_id":"explorarte","execution_principal_key":"p","public_key_base64":"x","secret_ref":"file://x","idempotency_key":"k"}{}`,
	}
	for _, body := range cases {
		if _, err := decodeIdentityKeyCommand(writeIdentityCommand(t, body)); err == nil {
			t.Fatalf("expected strict decode failure for %s", body)
		}
	}
}
