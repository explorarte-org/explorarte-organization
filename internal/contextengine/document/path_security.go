package document

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func resolveWithinRoot(root, relative string) (string, error) {
	if strings.TrimSpace(relative) == "" {
		return "", errors.New("relative path cannot be empty")
	}
	if filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) {
		return "", errors.New("absolute or NUL-containing paths are not allowed")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes configured root")
	}
	candidate := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	if err := ensureContained(root, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func ensureContained(root, candidate string) error {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return fmt.Errorf("compare path with root: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("resolved path escapes configured root")
	}
	return nil
}

func validateOpenedFile(root string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", info.Mode())
	}
	procPath := fmt.Sprintf("/proc/self/fd/%d", file.Fd())
	resolved, err := filepath.EvalSymlinks(procPath)
	if err == nil {
		if err = ensureContained(root, resolved); err != nil {
			return errors.New("opened descriptor escapes configured root")
		}
	}
	return nil
}
