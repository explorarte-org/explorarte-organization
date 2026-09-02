package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCheckCredentialReadable (G2-002): a credential prepared for future
// use but not readable by this process's UID must be caught here, at
// startup, the same failure mode a root:root-owned secret file produced
// silently for three weeks before this session found it.
func TestCheckCredentialReadable(t *testing.T) {
	if err := checkCredentialReadable(""); err != nil {
		t.Fatalf("an unconfigured (empty) credential path must not be treated as a defect, got %v", err)
	}
	if err := checkCredentialReadable("   "); err != nil {
		t.Fatalf("a whitespace-only path must be treated the same as empty, got %v", err)
	}

	readable := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(readable, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkCredentialReadable(readable); err != nil {
		t.Fatalf("a real, readable file must pass, got %v", err)
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	err := checkCredentialReadable(missing)
	if err == nil {
		t.Fatal("expected an error for a credential file that does not exist")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the underlying os.ErrNotExist to be wrapped, got %v", err)
	}

	unreadable := filepath.Join(t.TempDir(), "unreadable")
	if err := os.WriteFile(unreadable, []byte("secret"), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: a 0000-permission file is still readable by root, so this case cannot be exercised here")
	}
	if err := checkCredentialReadable(unreadable); err == nil {
		t.Fatal("expected an error for a credential file this process cannot read")
	}
}
