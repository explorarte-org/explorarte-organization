package document

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

func TestLoaderRejectsPathTraversalAbsoluteDirectoryAndSpecialFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.md"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	loader, err := NewLoader(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../escape", "/etc/passwd", "."} {
		t.Run(path, func(t *testing.T) {
			_, loadErr := loader.Load(t.Context(), path, 0)
			if loadErr == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	fifo := filepath.Join(root, "fifo")
	if err = syscall.Mkfifo(fifo, 0o600); err == nil {
		_, loadErr := loader.Load(t.Context(), "fifo", 0)
		if loadErr == nil {
			t.Fatal("FIFO should be rejected")
		}
	}
}

func TestLoaderRejectsSymlinkEscapeAndAllowsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.md"), []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "escape.md")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(filepath.Join(root, "target.md"), filepath.Join(root, "internal.md")); err != nil {
		t.Fatal(err)
	}
	loader, err := NewLoader(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = loader.Load(t.Context(), "escape.md", 0); err == nil || !errors.Is(err, contextengine.ErrRejected) {
		t.Fatalf("escape error=%v", err)
	}
	value, err := loader.Load(t.Context(), "internal.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(value.Normalized) != "target" {
		t.Fatalf("content=%q", value.Normalized)
	}
}

func TestLoaderRejectsOversizeInvalidUTF8AndNUL(t *testing.T) {
	root := t.TempDir()
	loader, err := NewLoader(root, 8)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{"large": []byte("123456789"), "utf8": {0xff}, "nul": {'a', 0}}
	for name, data := range cases {
		if err = os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, loadErr := loader.Load(t.Context(), name, 0); loadErr == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestNewLoaderRejectsMissingRoot(t *testing.T) {
	if _, err := NewLoader(filepath.Join(t.TempDir(), "missing"), 1024); err == nil {
		t.Fatal("expected error")
	}
}
