package artifactfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

func TestStoreContentAddressedAndVerify(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Put(context.Background(), bytes.NewBufferString("hello"), staging.ArtifactMetadata{MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if stored.StorageKey != "artifact://sha256/"+stored.Digest {
		t.Fatalf("unexpected key %s", stored.StorageKey)
	}
	if err := store.Verify(context.Background(), stored.StorageKey); err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(context.Background(), stored.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(body) != "hello" {
		t.Fatalf("got %q", body)
	}
	second, err := store.Put(context.Background(), bytes.NewBufferString("hello"), staging.ArtifactMetadata{MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest != stored.Digest {
		t.Fatal("digest changed")
	}
}

func TestStoreRejectsLimitAndCorruption(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := New(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), bytes.NewBufferString("12345"), staging.ArtifactMetadata{MediaType: "text/plain"}); err == nil {
		t.Fatal("oversize accepted")
	}
	store, err = New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Put(context.Background(), bytes.NewBufferString("hello"), staging.ArtifactMetadata{MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sha256", stored.Digest[:2], stored.Digest[2:4], stored.Digest)
	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(context.Background(), stored.StorageKey); !errors.Is(err, staging.ErrArtifactCorrupt) {
		t.Fatalf("got %v", err)
	}
}
