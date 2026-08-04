package staging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	repositoryIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	workspaceKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	commitPattern       = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
	digestPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	refPattern          = regexp.MustCompile(`^refs/heads/[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

func ValidateRepositoryID(value string) error {
	if !repositoryIDPattern.MatchString(value) {
		return fmt.Errorf("%w: invalid repository ID", ErrInvalidInput)
	}
	return nil
}

func ValidateWorkspaceKey(value string) error {
	if !workspaceKeyPattern.MatchString(value) {
		return fmt.Errorf("%w: invalid workspace key", ErrInvalidInput)
	}
	return nil
}

func ValidateCommit(value string) error {
	if !commitPattern.MatchString(value) {
		return fmt.Errorf("%w: invalid commit ID", ErrInvalidInput)
	}
	return nil
}

func ValidateDigest(value string) error {
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%w: invalid SHA-256 digest", ErrInvalidInput)
	}
	return nil
}

func ValidateTargetRef(value string) error {
	if !refPattern.MatchString(value) || strings.ContainsAny(value, " ~^:@{}\\\t\r\n") || strings.Contains(value, "..") || strings.Contains(value, "//") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".lock") {
		return fmt.Errorf("%w: invalid target ref", ErrInvalidInput)
	}
	return nil
}

func ValidateOpaqueReference(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: invalid evidence reference", ErrInvalidInput)
	}
	return nil
}

func CanonicalRoot(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || filepath.Clean(path) != path {
		return "", fmt.Errorf("%w: root path must be absolute and clean", ErrInvalidInput)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("stat root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: root cannot be a symlink", ErrUnsafeRepository)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if resolved != path {
		return "", fmt.Errorf("%w: root is not canonical", ErrUnsafeRepository)
	}
	return resolved, nil
}

func ValidateSeparateRoots(paths ...string) error {
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		resolved, err := CanonicalRoot(path)
		if err != nil {
			return err
		}
		canonical = append(canonical, resolved)
	}
	for i := range canonical {
		for j := i + 1; j < len(canonical); j++ {
			if pathContains(canonical[i], canonical[j]) || pathContains(canonical[j], canonical[i]) {
				return fmt.Errorf("%w: configured roots overlap", ErrInvalidInput)
			}
		}
	}
	return nil
}

func PathWithin(root, path string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	if !pathContains(root, path) {
		return fmt.Errorf("%w: path escapes configured root", ErrUnsafeRepository)
	}
	return nil
}

func pathContains(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func IsNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
