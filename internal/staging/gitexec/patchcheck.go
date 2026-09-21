package gitexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// maxPatchCheckDetail bounds the git diagnostic handed back to a planner.
const maxPatchCheckDetail = 1000

// CheckPatch asks git whether patch applies cleanly to the tree of commit, and says
// why when it does not.
//
// It is isolated by construction. The commit's tree is loaded into a PRIVATE
// temporary index file (GIT_INDEX_FILE) and the patch is checked against that index
// (`git apply --check --cached`), so nothing here reads or writes the repository's
// own index, its working tree, its refs or its objects: no other component using the
// operational checkout can observe or be disturbed by it. The temporary index lives
// under the backend's control directory and is removed before returning.
//
// A patch git rejects is a VERDICT (Applies false, with git's own diagnostic), not
// an error: the artifact is wrong, and the caller's remedy is to regenerate it. An
// error means the check itself could not run (repository, commit, git, timeout),
// which says nothing about the patch.
func (b *Backend) CheckPatch(ctx context.Context, repository staging.RepositoryConfig, commit, patch string) (staging.PatchCheck, error) {
	if !commitPattern.MatchString(commit) {
		return staging.PatchCheck{}, fmt.Errorf("%w: patch check needs a full 40-character commit", staging.ErrInvalidInput)
	}
	if strings.TrimSpace(patch) == "" {
		return staging.PatchCheck{}, fmt.Errorf("%w: patch is empty", staging.ErrInvalidInput)
	}
	if err := b.ValidateRepository(ctx, repository); err != nil {
		return staging.PatchCheck{}, err
	}
	if _, err := b.output(ctx, repository.Path, "cat-file", "-e", commit+"^{commit}"); err != nil {
		return staging.PatchCheck{}, fmt.Errorf("patch check: commit %s is not in the repository: %w", commit, err)
	}
	scratch, err := os.MkdirTemp(filepath.Join(b.workspaceRoot, ".control"), "patchcheck-")
	if err != nil {
		return staging.PatchCheck{}, fmt.Errorf("patch check: create scratch directory: %w", err)
	}
	defer os.RemoveAll(scratch)
	indexEnv := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err := b.runIsolated(ctx, repository.Path, indexEnv, nil, "read-tree", commit); err != nil {
		return staging.PatchCheck{}, fmt.Errorf("patch check: load tree of %s: %w", commit, err)
	}
	_, err = b.runIsolated(ctx, repository.Path, indexEnv, []byte(patch), "apply", "--check", "--cached", "--whitespace=nowarn", "-")
	if err == nil {
		return staging.PatchCheck{Applies: true}, nil
	}
	var rejected *patchRejected
	if errors.As(err, &rejected) {
		return staging.PatchCheck{Applies: false, Detail: rejected.detail}, nil
	}
	return staging.PatchCheck{}, fmt.Errorf("patch check: %w", err)
}

// ReadFile returns the exact content of path at commit, bounded by limit. It reads
// the object database only.
func (b *Backend) ReadFile(ctx context.Context, repository staging.RepositoryConfig, commit, path string, limit int64) ([]byte, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("%w: reading a file needs a full 40-character commit", staging.ErrInvalidInput)
	}
	if err := validateReadPath(path); err != nil {
		return nil, err
	}
	if err := b.ValidateRepository(ctx, repository); err != nil {
		return nil, err
	}
	return b.outputBytesLimited(ctx, repository.Path, limit, "cat-file", "blob", commit+":"+path)
}

func validateReadPath(path string) error {
	if path == "" || len(path) > 400 || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "-") ||
		strings.ContainsAny(path, "\x00\n\r:\\") {
		return fmt.Errorf("%w: %q is not a repository-relative path", staging.ErrInvalidInput, path)
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%w: %q is not a clean repository-relative path", staging.ErrInvalidInput, path)
		}
	}
	return nil
}

// patchRejected is git's verdict that a patch does not apply (exit status 1),
// as opposed to the check failing to run.
type patchRejected struct{ detail string }

func (p *patchRejected) Error() string { return "patch rejected: " + p.detail }

// runIsolated runs a hardened git command with the given extra environment (a
// private index) and optional stdin. Unlike run, it may set variables safeEnv would
// drop, and it reports git's exit status 1 as a verdict.
func (b *Backend) runIsolated(ctx context.Context, directory string, extraEnv []string, stdin []byte, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, b.binary, b.safeArgs(args...)...)
	command.Dir = directory
	command.Env = append(b.safeEnv(nil), extraEnv...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if commandCtx.Err() != nil {
			return nil, commandCtx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(args) > 0 && args[0] == "apply" && isPatchDiagnostic(exitErr.ExitCode(), message) {
			return nil, &patchRejected{detail: boundedDiagnostic(message)}
		}
		return nil, fmt.Errorf("git command failed: %w: %s", err, boundedDiagnostic(message))
	}
	return stdout.Bytes(), nil
}

// isPatchDiagnostic reports whether a failed `git apply` is a verdict about the
// PATCH rather than a failure of the check. git does not encode that in the exit
// status: a patch that does not apply exits 1 ("error: patch failed",
// "does not apply"), but a corrupt one exits 128 ("error: corrupt patch at
// <stdin>:4"), the same status as a broken repository ("fatal: not a git
// repository"). What separates them is git's own diagnostic: it reports the patch's
// faults as "error:" and its own failures as "fatal:".
func isPatchDiagnostic(exitCode int, message string) bool {
	if exitCode != 1 && exitCode != 128 {
		return false
	}
	return strings.HasPrefix(message, "error:") && !strings.Contains(message, "fatal:")
}

func boundedDiagnostic(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > maxPatchCheckDetail {
		message = message[:maxPatchCheckDetail]
	}
	return message
}
