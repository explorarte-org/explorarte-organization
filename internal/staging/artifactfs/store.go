package artifactfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

type Store struct {
	root     string
	maxBytes int64
}

func New(root string, maxBytes int64) (*Store, error) {
	if maxBytes <= 0 {
		return nil, errors.New("artifact max bytes must be positive")
	}
	canonical, err := staging.CanonicalRoot(root)
	if err != nil {
		return nil, err
	}
	return &Store{root: canonical, maxBytes: maxBytes}, nil
}

func (s *Store) Put(ctx context.Context, reader io.Reader, metadata staging.ArtifactMetadata) (staging.StoredArtifact, error) {
	if reader == nil {
		return staging.StoredArtifact{}, errors.New("artifact reader cannot be nil")
	}
	limit := s.maxBytes
	if metadata.MaxBytes > 0 && metadata.MaxBytes < limit {
		limit = metadata.MaxBytes
	}
	mediaType := strings.TrimSpace(metadata.MediaType)
	if mediaType == "" || len(mediaType) > 255 {
		return staging.StoredArtifact{}, fmt.Errorf("%w: invalid artifact media type", staging.ErrInvalidInput)
	}
	tmpDir := filepath.Join(s.root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return staging.StoredArtifact{}, fmt.Errorf("create artifact temp directory: %w", err)
	}
	tmp, err := os.CreateTemp(tmpDir, "artifact-*")
	if err != nil {
		return staging.StoredArtifact{}, fmt.Errorf("create artifact temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return staging.StoredArtifact{}, fmt.Errorf("restrict artifact temp permissions: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	hash := sha256.New()
	limited := &contextReader{ctx: ctx, reader: io.LimitReader(reader, limit+1)}
	written, err := io.Copy(io.MultiWriter(tmp, hash), limited)
	if err != nil {
		return staging.StoredArtifact{}, fmt.Errorf("write artifact: %w", err)
	}
	if written > limit {
		return staging.StoredArtifact{}, fmt.Errorf("%w: artifact exceeds %d bytes", staging.ErrInvalidInput, limit)
	}
	if err := tmp.Sync(); err != nil {
		return staging.StoredArtifact{}, fmt.Errorf("sync artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return staging.StoredArtifact{}, fmt.Errorf("close artifact: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	finalDir := filepath.Join(s.root, "sha256", digest[0:2], digest[2:4])
	finalPath := filepath.Join(finalDir, digest)
	if err := os.MkdirAll(finalDir, 0o700); err != nil {
		return staging.StoredArtifact{}, fmt.Errorf("create artifact directory: %w", err)
	}
	if info, err := os.Lstat(finalPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != written {
			return staging.StoredArtifact{}, staging.ErrArtifactCorrupt
		}
		if err := verifyFile(finalPath, digest); err != nil {
			return staging.StoredArtifact{}, err
		}
		return stored(digest, written, mediaType), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return staging.StoredArtifact{}, fmt.Errorf("stat artifact destination: %w", err)
	}
	// Hard-link publication is atomic and, unlike os.Rename on Unix, never
	// replaces an existing digest. The temporary file lives below the same
	// artifact root, so it is guaranteed to be on the same filesystem.
	if err := os.Link(tmpName, finalPath); err != nil {
		if info, statErr := os.Lstat(finalPath); statErr == nil && info.Mode().IsRegular() && info.Size() == written {
			if verifyErr := verifyFile(finalPath, digest); verifyErr == nil {
				return stored(digest, written, mediaType), nil
			}
		}
		return staging.StoredArtifact{}, fmt.Errorf("publish artifact without overwrite: %w", err)
	}
	if err := os.Chmod(finalPath, 0o600); err != nil {
		return staging.StoredArtifact{}, fmt.Errorf("restrict artifact permissions: %w", err)
	}
	if err := syncDirectory(finalDir); err != nil {
		return staging.StoredArtifact{}, err
	}
	return stored(digest, written, mediaType), nil
}

func (s *Store) Stat(_ context.Context, reference string) (staging.StoredArtifact, error) {
	digest, path, err := s.resolve(reference)
	if err != nil {
		return staging.StoredArtifact{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return staging.StoredArtifact{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return staging.StoredArtifact{}, staging.ErrArtifactCorrupt
	}
	return stored(digest, info.Size(), "application/octet-stream"), nil
}

func (s *Store) Open(ctx context.Context, reference string) (io.ReadCloser, error) {
	digest, path, err := s.resolve(reference)
	if err != nil {
		return nil, err
	}
	if err := verifyFile(path, digest); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		_ = file.Close()
		return nil, ctx.Err()
	default:
		return file, nil
	}
}

func (s *Store) Verify(_ context.Context, reference string) error {
	digest, path, err := s.resolve(reference)
	if err != nil {
		return err
	}
	return verifyFile(path, digest)
}

func (s *Store) resolve(reference string) (string, string, error) {
	const prefix = "artifact://sha256/"
	if !strings.HasPrefix(reference, prefix) {
		return "", "", fmt.Errorf("%w: invalid artifact reference", staging.ErrInvalidInput)
	}
	digest := strings.TrimPrefix(reference, prefix)
	if err := staging.ValidateDigest(digest); err != nil {
		return "", "", err
	}
	path := filepath.Join(s.root, "sha256", digest[0:2], digest[2:4], digest)
	if err := staging.PathWithin(s.root, path); err != nil {
		return "", "", err
	}
	return digest, path, nil
}

func stored(digest string, size int64, mediaType string) staging.StoredArtifact {
	return staging.StoredArtifact{Digest: digest, SizeBytes: size, MediaType: mediaType, StorageKey: "artifact://sha256/" + digest}
}

func verifyFile(path, expected string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return staging.ErrArtifactCorrupt
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return staging.ErrArtifactCorrupt
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}

var _ staging.ArtifactStore = (*Store)(nil)
