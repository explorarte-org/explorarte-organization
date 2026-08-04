package staging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type FileRepositoryCatalog struct {
	entries map[string]RepositoryConfig
	hash    string
	git     GitBackend
}

func LoadRepositoryCatalog(path string, git GitBackend) (*FileRepositoryCatalog, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return nil, errors.New("repository catalog path must be absolute")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read repository catalog: %w", err)
	}
	var document RepositoryFile
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("parse repository catalog: %w", err)
	}
	if document.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported repository catalog schema version %d", document.SchemaVersion)
	}
	if len(document.Repositories) == 0 {
		return nil, errors.New("repository catalog is empty")
	}
	entries := make(map[string]RepositoryConfig, len(document.Repositories))
	normalized := make([]RepositoryConfig, 0, len(document.Repositories))
	for _, entry := range document.Repositories {
		entry.ID = strings.TrimSpace(entry.ID)
		if err := ValidateRepositoryID(entry.ID); err != nil {
			return nil, err
		}
		if _, exists := entries[entry.ID]; exists {
			return nil, fmt.Errorf("duplicate repository ID %q", entry.ID)
		}
		if strings.Contains(entry.Path, "://") || !filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path || strings.ContainsRune(entry.Path, 0) {
			return nil, fmt.Errorf("repository %q has invalid local path", entry.ID)
		}
		info, err := os.Lstat(entry.Path)
		if err != nil {
			return nil, fmt.Errorf("stat repository %q: %w", entry.ID, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("%w: repository %q root must be a real directory", ErrUnsafeRepository, entry.ID)
		}
		resolved, err := filepath.EvalSymlinks(entry.Path)
		if err != nil || resolved != entry.Path {
			return nil, fmt.Errorf("%w: repository %q path is not canonical", ErrUnsafeRepository, entry.ID)
		}
		if len(entry.AllowedTargetRefs) == 0 {
			return nil, fmt.Errorf("repository %q has no allowed target refs", entry.ID)
		}
		seenRefs := map[string]struct{}{}
		for index, ref := range entry.AllowedTargetRefs {
			ref = strings.TrimSpace(ref)
			if err := ValidateTargetRef(ref); err != nil {
				return nil, fmt.Errorf("repository %q: %w", entry.ID, err)
			}
			if _, exists := seenRefs[ref]; exists {
				return nil, fmt.Errorf("repository %q repeats target ref %q", entry.ID, ref)
			}
			seenRefs[ref] = struct{}{}
			entry.AllowedTargetRefs[index] = ref
		}
		sort.Strings(entry.AllowedTargetRefs)
		entries[entry.ID] = entry
		normalized = append(normalized, entry)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	hashBody, err := json.Marshal(RepositoryFile{SchemaVersion: 1, Repositories: normalized})
	if err != nil {
		return nil, fmt.Errorf("hash repository catalog: %w", err)
	}
	digest := sha256.Sum256(hashBody)
	catalog := &FileRepositoryCatalog{entries: entries, hash: hex.EncodeToString(digest[:]), git: git}
	return catalog, nil
}

func (c *FileRepositoryCatalog) List(context.Context) []RepositoryView {
	result := make([]RepositoryView, 0, len(c.entries))
	for _, entry := range c.entries {
		result = append(result, RepositoryView{ID: entry.ID, Enabled: entry.Enabled, AllowedTargetRefs: append([]string(nil), entry.AllowedTargetRefs...), ConfigHash: c.hash})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (c *FileRepositoryCatalog) Get(_ context.Context, id string) (RepositoryConfig, string, error) {
	entry, ok := c.entries[id]
	if !ok || !entry.Enabled {
		return RepositoryConfig{}, "", ErrRepositoryDenied
	}
	entry.AllowedTargetRefs = append([]string(nil), entry.AllowedTargetRefs...)
	return entry, c.hash, nil
}

func (c *FileRepositoryCatalog) Validate(ctx context.Context, id string) error {
	entry, _, err := c.Get(ctx, id)
	if err != nil {
		return err
	}
	if c.git == nil {
		return errors.New("repository catalog has no Git backend")
	}
	return c.git.ValidateRepository(ctx, entry)
}

func (c *FileRepositoryCatalog) ValidateRootSeparation(roots ...string) error {
	for _, entry := range c.entries {
		paths := append([]string{entry.Path}, roots...)
		if err := ValidateSeparateRoots(paths...); err != nil {
			return fmt.Errorf("repository %q overlaps staging storage: %w", entry.ID, err)
		}
	}
	return nil
}

func TargetAllowed(repository RepositoryConfig, targetRef string) bool {
	if !repository.Enabled {
		return false
	}
	for _, allowed := range repository.AllowedTargetRefs {
		if allowed == targetRef {
			return true
		}
	}
	return false
}
