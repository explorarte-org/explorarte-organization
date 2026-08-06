package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unicode"
)

var ErrUnsafeCredentialFile = errors.New("unsafe provider credential file")

const maxProviderCredentialFileBytes int64 = 16 << 10

func LoadBearerToken(path string) ([]byte, error) {
	clean := filepath.Clean(path)
	if clean == "." || !filepath.IsAbs(clean) {
		return nil, fmt.Errorf("%w: absolute credential path is required", ErrUnsafeCredentialFile)
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxProviderCredentialFileBytes {
		return nil, fmt.Errorf("%w: credential path is not a bounded regular file", ErrUnsafeCredentialFile)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: permissions must not grant group or other access", ErrUnsafeCredentialFile)
	}
	body, err := os.ReadFile(clean)
	if err != nil {
		return nil, err
	}
	body = bytes.TrimSuffix(body, []byte("\n"))
	body = bytes.TrimSuffix(body, []byte("\r"))
	if len(body) < 8 || len(body) > 8192 {
		zero(body)
		return nil, fmt.Errorf("%w: credential length is outside allowed range", ErrUnsafeCredentialFile)
	}
	for _, r := range string(body) {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			zero(body)
			return nil, fmt.Errorf("%w: credential contains whitespace or control characters", ErrUnsafeCredentialFile)
		}
	}
	return body, nil
}

func Zero(value []byte) { zero(value) }

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
