package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadBearerToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider-token")
	if err := os.WriteFile(path, []byte("test-provider-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The umask clears bits from WriteFile's mode, so the mode this test
	// depends on has to be set explicitly. See file_resolver_test.go.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := LoadBearerToken(path)
	if err != nil {
		t.Fatal(err)
	}
	defer Zero(token)
	if string(token) != "test-provider-token" {
		t.Fatalf("token=%q", token)
	}
}

func TestLoadBearerTokenRejectsUnsafePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	path := filepath.Join(t.TempDir(), "provider-token")
	if err := os.WriteFile(path, []byte("test-provider-token"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The umask clears bits from WriteFile's mode, so the mode this test
	// depends on has to be set explicitly. See file_resolver_test.go.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBearerToken(path); !errors.Is(err, ErrUnsafeCredentialFile) {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadBearerTokenRejectsEmbeddedWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider-token")
	if err := os.WriteFile(path, []byte("test token"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The umask clears bits from WriteFile's mode, so the mode this test
	// depends on has to be set explicitly. See file_resolver_test.go.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBearerToken(path); !errors.Is(err, ErrUnsafeCredentialFile) {
		t.Fatalf("error=%v", err)
	}
}
