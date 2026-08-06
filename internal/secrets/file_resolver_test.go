package secrets

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writePrivateKey(t *testing.T, mode os.FileMode) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity-key.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadEd25519PrivateKey(t *testing.T) {
	path := writePrivateKey(t, 0o600)
	key, err := LoadEd25519PrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != ed25519.PrivateKeySize {
		t.Fatalf("private key length=%d", len(key))
	}
}

func TestLoadEd25519PrivateKeyRejectsUnsafePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	path := writePrivateKey(t, 0o644)
	if _, err := LoadEd25519PrivateKey(path); !errors.Is(err, ErrUnsafeKeyFile) {
		t.Fatalf("error=%v", err)
	}
}
