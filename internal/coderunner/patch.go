package coderunner

import (
	"fmt"
	"strings"
)

// extractPatchPaths returns every workspace-relative path a unified diff
// touches, read from its "diff --git a/X b/Y" and "--- "/"+++ " headers.
// git apply's own path handling is not trusted as the sole guard: this is
// evaluated independently, before git ever sees the patch, against the same
// workspace-containment and structural-denylist rules every other typed
// operation uses.
func extractPatchPaths(patch string) ([]string, error) {
	seen := map[string]struct{}{}
	var paths []string
	add := func(raw string) error {
		p := stripDiffPrefix(raw)
		if p == "" || p == "/dev/null" {
			return nil
		}
		if _, ok := seen[p]; ok {
			return nil
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
		return nil
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			a, b, ok := splitGitDiffHeader(line)
			if !ok {
				return nil, fmt.Errorf("malformed diff --git header")
			}
			if err := add(a); err != nil {
				return nil, err
			}
			if err := add(b); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "--- "):
			if err := add(strings.TrimSpace(strings.TrimPrefix(line, "--- "))); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "+++ "):
			if err := add(strings.TrimSpace(strings.TrimPrefix(line, "+++ "))); err != nil {
				return nil, err
			}
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("patch touches no recognizable path")
	}
	return paths, nil
}

// ExtractPatchPaths exposes the canonical unified-diff path parser to
// higher-level policy layers. It deliberately returns the same normalized
// paths validated by CodeRunner; callers must still apply their own policy.
func ExtractPatchPaths(patch string) ([]string, error) { return extractPatchPaths(patch) }

// splitGitDiffHeader splits `diff --git a/<path> b/<path>` into its two
// halves. It is intentionally strict about the "a/"/"b/" prefix pair rather
// than trying to guess a split point in the general case, since that is
// exactly the header shape git itself always emits and the only shape
// CodeRunner accepts.
func splitGitDiffHeader(line string) (a, b string, ok bool) {
	rest := strings.TrimPrefix(line, "diff --git ")
	idx := strings.Index(rest, " b/")
	if !strings.HasPrefix(rest, "a/") || idx < 0 {
		return "", "", false
	}
	return rest[2:idx], rest[idx+3:], true
}

func stripDiffPrefix(p string) string {
	p = strings.TrimSpace(p)
	// Trailing metadata such as timestamps on --- / +++ lines.
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	switch {
	case strings.HasPrefix(p, "a/"):
		return p[2:]
	case strings.HasPrefix(p, "b/"):
		return p[2:]
	default:
		return p
	}
}

// validatePatchPaths rejects a patch touching any path outside the workspace
// or any structurally denied path (.git internals, go.mod/go.sum) before the
// patch is ever handed to git.
func validatePatchPaths(patch string) error {
	paths, err := extractPatchPaths(patch)
	if err != nil {
		return fmt.Errorf("patch validation: %w", err)
	}
	for _, p := range paths {
		if err := SafePath(p); err != nil {
			return fmt.Errorf("patch touches unsafe path %q: %w", p, err)
		}
		if structurallyDenied(p, true) {
			return fmt.Errorf("patch touches structurally denied path %q", p)
		}
	}
	return nil
}
