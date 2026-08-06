package secrets

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

var ErrUnsafeKeyFile = errors.New("unsafe execution identity key file")

const maxExecutionIdentityKeyFileBytes int64 = 16 << 10

func LoadEd25519PrivateKey(path string) (ed25519.PrivateKey, error) {
	clean := filepath.Clean(path)
	if clean == "." || !filepath.IsAbs(clean) {
		return nil, fmt.Errorf("%w: absolute key path is required", ErrUnsafeKeyFile)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxExecutionIdentityKeyFileBytes {
		return nil, fmt.Errorf("%w: key path is not a bounded regular file", ErrUnsafeKeyFile)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: permissions must not grant group or other access", ErrUnsafeKeyFile)
	}
	body, err := os.ReadFile(clean)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(body)
	if block == nil || len(rest) != 0 || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%w: expected one PKCS#8 PRIVATE KEY PEM block", ErrUnsafeKeyFile)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: key is not Ed25519", ErrUnsafeKeyFile)
	}
	return append(ed25519.PrivateKey(nil), key...), nil
}
