package coderunner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// structurallyDenied returns true for repository-control or dependency-manifest
// paths that CodeRunner typed operations must never mutate directly, regardless
// of workspace confinement. Reads are allowed for the non-.git entries (GIT_DIFF
// and GIT_STATUS already cover the host-owned git-metadata view); mutation is
// never allowed for any of them through CodeRunner.
func structurallyDenied(rel string, mutating bool) bool {
	clean := filepath.ToSlash(filepath.Clean(rel))
	segments := strings.Split(clean, "/")
	for _, s := range segments {
		if s == ".git" {
			return true
		}
	}
	if mutating && (clean == "go.mod" || clean == "go.sum") {
		return true
	}
	return false
}

// realPath resolves rel against root and proves the result stays inside root,
// even when the final path component does not yet exist or is itself a
// symlink pointing outside root. It never trusts string-prefix comparison on
// unresolved paths: every existing ancestor is resolved through
// filepath.EvalSymlinks before containment is checked.
func realPath(root, rel string) (string, error) {
	if err := SafePath(rel); err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	requested := filepath.Join(realRoot, filepath.FromSlash(rel))

	// Walk from the requested path up to the root, resolving symlinks at the
	// deepest existing ancestor. This protects the final path component (a
	// symlink at workspace/foo.go -> /outside/file) as well as any
	// intermediate directory symlink, and still works when the final
	// component (or several trailing components) do not exist yet.
	resolvedAncestor := requested
	var trailing []string
	for {
		info, statErr := os.Lstat(resolvedAncestor)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				target, evalErr := filepath.EvalSymlinks(resolvedAncestor)
				if evalErr != nil {
					return "", fmt.Errorf("resolve symlink: %w", evalErr)
				}
				resolvedAncestor = target
			}
			break
		}
		if resolvedAncestor == realRoot || resolvedAncestor == string(filepath.Separator) || resolvedAncestor == "." {
			break
		}
		parent := filepath.Dir(resolvedAncestor)
		if parent == resolvedAncestor {
			break
		}
		trailing = append([]string{filepath.Base(resolvedAncestor)}, trailing...)
		resolvedAncestor = parent
	}
	// resolvedAncestor now points at the deepest existing, fully
	// symlink-resolved path. Re-resolve it directly in case it is itself a
	// directory reached via a resolved symlink chain.
	finalAncestor, err := filepath.EvalSymlinks(resolvedAncestor)
	if err != nil {
		return "", fmt.Errorf("resolve ancestor: %w", err)
	}
	real := filepath.Join(append([]string{finalAncestor}, trailing...)...)

	if !within(realRoot, real) {
		return "", fmt.Errorf("workspace escape")
	}
	return real, nil
}

// within reports whether target is inside root (root itself counts).
// It uses filepath.Rel instead of string-prefix matching so that sibling
// directories sharing a prefix (workspace vs workspace-evil) are never
// confused for containment.
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// withinGitMetadata reports whether real (an already-resolved, absolute
// path) falls inside root's .git directory. This is checked after symlink
// resolution so an innocently named symlink cannot be used to reach git
// control state that structurallyDenied's textual check on the requested
// path would otherwise catch.
func withinGitMetadata(root, real string) bool {
	gitDir, err := filepath.EvalSymlinks(filepath.Join(root, ".git"))
	if err != nil {
		// .git does not exist (or is not yet resolvable) at all: nothing to
		// protect against.
		return false
	}
	return real == gitDir || within(gitDir, real)
}
