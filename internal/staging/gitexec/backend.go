package gitexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

type Backend struct {
	binary        string
	workspaceRoot string
	timeout       time.Duration
	globalConfig  string
	hooksDir      string
}

func New(binary, workspaceRoot string, timeout time.Duration) (*Backend, error) {
	binary = strings.TrimSpace(binary)
	if binary == "" || strings.ContainsAny(binary, " \t\r\n") {
		return nil, errors.New("Git binary must not contain arguments")
	}
	if timeout <= 0 {
		return nil, errors.New("Git timeout must be positive")
	}
	root, err := staging.CanonicalRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	control := filepath.Join(root, ".control")
	hooks := filepath.Join(control, "empty-hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		return nil, fmt.Errorf("create empty hooks directory: %w", err)
	}
	globalConfig := filepath.Join(control, "empty-gitconfig")
	if err := os.WriteFile(globalConfig, []byte(""), 0o600); err != nil {
		return nil, fmt.Errorf("create empty Git config: %w", err)
	}
	return &Backend{binary: binary, workspaceRoot: root, timeout: timeout, globalConfig: globalConfig, hooksDir: hooks}, nil
}

func (b *Backend) ValidateRepository(ctx context.Context, repository staging.RepositoryConfig) error {
	if err := staging.ValidateRepositoryID(repository.ID); err != nil {
		return err
	}
	info, err := os.Lstat(repository.Path)
	if err != nil {
		return fmt.Errorf("stat repository: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return staging.ErrUnsafeRepository
	}
	resolved, err := filepath.EvalSymlinks(repository.Path)
	if err != nil || resolved != repository.Path {
		return staging.ErrUnsafeRepository
	}
	top, err := b.output(ctx, repository.Path, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(top) != repository.Path {
		return fmt.Errorf("%w: repository root mismatch", staging.ErrUnsafeRepository)
	}
	bare, err := b.output(ctx, repository.Path, "rev-parse", "--is-bare-repository")
	if err != nil || strings.TrimSpace(bare) != "false" {
		return fmt.Errorf("%w: bare repository is not supported", staging.ErrUnsafeRepository)
	}
	if _, err := os.Lstat(filepath.Join(repository.Path, ".gitmodules")); err == nil {
		return fmt.Errorf("%w: submodules are not supported", staging.ErrUnsafeRepository)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staged, err := b.output(ctx, repository.Path, "ls-files", "--stage")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(staged, "\n") {
		if strings.HasPrefix(line, "160000 ") {
			return fmt.Errorf("%w: submodule entry detected", staging.ErrUnsafeRepository)
		}
	}
	filters, filterErr := b.outputAllowExitOne(ctx, repository.Path, "config", "--local", "--get-regexp", "^filter\\.")
	if filterErr != nil {
		return filterErr
	}
	if strings.TrimSpace(filters) != "" {
		return fmt.Errorf("%w: Git filters are configured", staging.ErrUnsafeRepository)
	}
	if err := inspectAttributes(repository.Path); err != nil {
		return err
	}
	return rejectNestedRepositories(repository.Path)
}

func (b *Backend) ResolveCommit(ctx context.Context, repository staging.RepositoryConfig, revision string) (string, error) {
	if err := staging.ValidateCommit(revision); err != nil {
		return "", err
	}
	value, err := b.output(ctx, repository.Path, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve commit: %w", err)
	}
	value = strings.TrimSpace(value)
	if err := staging.ValidateCommit(value); err != nil {
		return "", err
	}
	return value, nil
}

func (b *Backend) CreateWorktree(ctx context.Context, request staging.CreateWorktreeRequest) error {
	if err := b.ValidateRepository(ctx, request.Repository); err != nil {
		return err
	}
	if err := staging.ValidateCommit(request.BaseCommit); err != nil {
		return err
	}
	if err := b.validateWorkspaceRef(request.Workspace); err != nil {
		return err
	}
	if _, err := os.Lstat(request.Workspace.WorkspacePath); err == nil {
		return staging.ErrWorkspaceExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(request.Workspace.WorkspacePath), 0o700); err != nil {
		return err
	}
	if _, err := b.output(ctx, request.Repository.Path, "worktree", "add", "--detach", request.Workspace.WorkspacePath, request.BaseCommit); err != nil {
		return fmt.Errorf("create Git worktree: %w", err)
	}
	if err := os.Chmod(request.Workspace.WorkspacePath, 0o700); err != nil {
		return fmt.Errorf("restrict worktree permissions: %w", err)
	}
	return nil
}

func (b *Backend) InspectWorktree(ctx context.Context, workspace staging.WorkspaceRef) (staging.WorkspaceInspection, error) {
	if err := b.validateWorkspaceRef(workspace); err != nil {
		return staging.WorkspaceInspection{}, err
	}
	info, err := os.Lstat(workspace.WorkspacePath)
	if errors.Is(err, os.ErrNotExist) {
		return staging.WorkspaceInspection{Exists: false}, nil
	}
	if err != nil {
		return staging.WorkspaceInspection{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return staging.WorkspaceInspection{}, staging.ErrUnsafeRepository
	}
	head, err := b.output(ctx, workspace.WorkspacePath, "rev-parse", "HEAD")
	if err != nil {
		return staging.WorkspaceInspection{}, err
	}
	tree, err := b.output(ctx, workspace.WorkspacePath, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return staging.WorkspaceInspection{}, err
	}
	status, err := b.outputBytes(ctx, workspace.WorkspacePath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return staging.WorkspaceInspection{}, err
	}
	return staging.WorkspaceInspection{Exists: true, Clean: len(status) == 0, HeadCommit: strings.TrimSpace(head), HeadTree: strings.TrimSpace(tree)}, nil
}

func (b *Backend) SealWorktree(ctx context.Context, request staging.SealRequest) (staging.SealedRevision, error) {
	if err := b.ValidateRepository(ctx, request.Repository); err != nil {
		return staging.SealedRevision{}, err
	}
	if err := b.validateWorkspaceRef(request.Workspace); err != nil {
		return staging.SealedRevision{}, err
	}
	if err := rejectNestedRepositories(request.Workspace.WorkspacePath); err != nil {
		return staging.SealedRevision{}, err
	}
	if err := inspectAttributes(request.Workspace.WorkspacePath); err != nil {
		return staging.SealedRevision{}, err
	}
	status, err := b.outputBytes(ctx, request.Workspace.WorkspacePath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return staging.SealedRevision{}, err
	}
	headBefore, err := b.output(ctx, request.Workspace.WorkspacePath, "rev-parse", "HEAD")
	if err != nil {
		return staging.SealedRevision{}, err
	}
	headBefore = strings.TrimSpace(headBefore)
	var changed []staging.ChangedFile
	if len(status) > 0 {
		if headBefore != request.BaseCommit {
			return staging.SealedRevision{}, fmt.Errorf("%w: dirty workspace already contains a candidate commit", staging.ErrGitConflict)
		}
		if _, err := b.output(ctx, request.Workspace.WorkspacePath, "add", "-A", "--", "."); err != nil {
			return staging.SealedRevision{}, fmt.Errorf("stage workspace changes: %w", err)
		}
		changed, err = b.changedFilesCached(ctx, request.Workspace.WorkspacePath, request.BaseCommit)
		if err != nil {
			return staging.SealedRevision{}, err
		}
		if len(changed) == 0 {
			return staging.SealedRevision{}, staging.ErrNoChanges
		}
		if request.MaxChangedFiles > 0 && len(changed) > request.MaxChangedFiles {
			return staging.SealedRevision{}, fmt.Errorf("%w: changed file limit exceeded", staging.ErrInvalidInput)
		}
		if err := validateChangedSymlinks(request.Workspace.WorkspacePath, changed); err != nil {
			return staging.SealedRevision{}, err
		}
		message := fmt.Sprintf("staging task %d attempt %d", request.TaskID, request.AttemptID)
		env := []string{
			"GIT_AUTHOR_NAME=Explorarte Staging",
			"GIT_AUTHOR_EMAIL=staging@invalid.local",
			"GIT_COMMITTER_NAME=Explorarte Staging",
			"GIT_COMMITTER_EMAIL=staging@invalid.local",
			"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
			"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
		}
		if _, err := b.outputEnv(ctx, request.Workspace.WorkspacePath, env, "commit", "--no-gpg-sign", "--no-verify", "-m", message); err != nil {
			return staging.SealedRevision{}, fmt.Errorf("create candidate commit: %w", err)
		}
	} else if headBefore == request.BaseCommit {
		return staging.SealedRevision{}, staging.ErrNoChanges
	}
	candidate, err := b.output(ctx, request.Workspace.WorkspacePath, "rev-parse", "HEAD")
	if err != nil {
		return staging.SealedRevision{}, err
	}
	candidate = strings.TrimSpace(candidate)
	parent, err := b.output(ctx, request.Workspace.WorkspacePath, "rev-parse", "HEAD^")
	if err != nil || strings.TrimSpace(parent) != request.BaseCommit {
		return staging.SealedRevision{}, fmt.Errorf("%w: candidate parent differs from base", staging.ErrGitConflict)
	}
	tree, err := b.output(ctx, request.Workspace.WorkspacePath, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return staging.SealedRevision{}, err
	}
	changed, err = b.changedFiles(ctx, request.Workspace.WorkspacePath, request.BaseCommit, candidate)
	if err != nil {
		return staging.SealedRevision{}, err
	}
	manifest := staging.Manifest{SchemaVersion: 1, RepositoryID: request.Repository.ID, WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, AttemptID: request.AttemptID, BaseCommit: request.BaseCommit, CandidateCommit: candidate, CandidateTree: strings.TrimSpace(tree), TargetRef: request.TargetRef, ChangedFiles: changed}
	manifestBytes, err := marshalManifest(manifest)
	if err != nil {
		return staging.SealedRevision{}, err
	}
	patch, err := b.outputBytesLimited(ctx, request.Repository.Path, request.MaxArtifactBytes, "diff", "--binary", "--no-ext-diff", request.BaseCommit, candidate, "--")
	if err != nil {
		return staging.SealedRevision{}, fmt.Errorf("generate binary patch: %w", err)
	}
	workspaceRef := "refs/explorarte/workspaces/" + strconv.FormatInt(request.WorkspaceID, 10)
	if _, err := b.output(ctx, request.Repository.Path, "update-ref", workspaceRef, candidate); err != nil {
		return staging.SealedRevision{}, fmt.Errorf("create internal workspace ref: %w", err)
	}
	return staging.SealedRevision{CandidateCommit: candidate, CandidateTree: strings.TrimSpace(tree), Manifest: manifestBytes, Patch: patch, ChangedFiles: changed}, nil
}

func (b *Backend) RemoveWorktree(ctx context.Context, workspace staging.WorkspaceRef) error {
	if err := b.validateWorkspaceRef(workspace); err != nil {
		return err
	}
	// Repository path is not stored in WorkspaceRef by design. Resolve the common
	// administrative directory from the worktree itself.
	common, err := b.output(ctx, workspace.WorkspacePath, "rev-parse", "--git-common-dir")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return staging.ErrWorkspaceMissing
		}
		return err
	}
	commonPath := strings.TrimSpace(common)
	if !filepath.IsAbs(commonPath) {
		commonPath = filepath.Clean(filepath.Join(workspace.WorkspacePath, commonPath))
	}
	repositoryPath := filepath.Dir(commonPath)
	if _, err := b.output(ctx, repositoryPath, "worktree", "remove", "--force", workspace.WorkspacePath); err != nil {
		return fmt.Errorf("remove Git worktree: %w", err)
	}
	return nil
}

func (b *Backend) ReadTarget(ctx context.Context, repository staging.RepositoryConfig, targetRef string) (string, error) {
	if err := staging.ValidateTargetRef(targetRef); err != nil {
		return "", err
	}
	value, err := b.output(ctx, repository.Path, "rev-parse", "--verify", targetRef+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func (b *Backend) PromoteRef(ctx context.Context, request staging.PromotionRefRequest) (staging.PromotionRefResult, error) {
	if err := b.ValidateRepository(ctx, request.Repository); err != nil {
		return staging.PromotionRefResult{}, err
	}
	if !staging.TargetAllowed(request.Repository, request.TargetRef) {
		return staging.PromotionRefResult{}, staging.ErrTargetRefDenied
	}
	if err := b.ensureTargetNotCheckedOut(ctx, request.Repository.Path, request.TargetRef); err != nil {
		return staging.PromotionRefResult{}, err
	}
	current, err := b.ReadTarget(ctx, request.Repository, request.TargetRef)
	if err != nil {
		return staging.PromotionRefResult{}, err
	}
	if current == request.CandidateCommit {
		return staging.PromotionRefResult{Applied: true, Current: current}, nil
	}
	if current != request.ExpectedBaseCommit {
		return staging.PromotionRefResult{Current: current}, staging.ErrTargetMoved
	}
	if _, err := b.output(ctx, request.Repository.Path, "update-ref", request.TargetRef, request.CandidateCommit, request.ExpectedBaseCommit); err != nil {
		after, readErr := b.ReadTarget(ctx, request.Repository, request.TargetRef)
		if readErr == nil && after == request.CandidateCommit {
			return staging.PromotionRefResult{Applied: true, Current: after}, nil
		}
		if readErr == nil && after != request.ExpectedBaseCommit {
			return staging.PromotionRefResult{Current: after}, staging.ErrTargetMoved
		}
		return staging.PromotionRefResult{}, fmt.Errorf("compare-and-swap target ref: %w", err)
	}
	return staging.PromotionRefResult{Applied: true, Current: request.CandidateCommit}, nil
}

func (b *Backend) validateWorkspaceRef(workspace staging.WorkspaceRef) error {
	if workspace.ID <= 0 || staging.ValidateWorkspaceKey(workspace.WorkspaceKey) != nil || !filepath.IsAbs(workspace.WorkspacePath) {
		return staging.ErrInvalidInput
	}
	if err := staging.PathWithin(b.workspaceRoot, workspace.WorkspacePath); err != nil {
		return err
	}
	if filepath.Base(workspace.WorkspacePath) != workspace.WorkspaceKey {
		return fmt.Errorf("%w: workspace path does not match key", staging.ErrInvalidInput)
	}
	return nil
}

func (b *Backend) ensureTargetNotCheckedOut(ctx context.Context, repositoryPath, targetRef string) error {
	body, err := b.output(ctx, repositoryPath, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "branch "+targetRef {
			return staging.ErrUnsafeRepository
		}
	}
	return nil
}

func (b *Backend) changedFiles(ctx context.Context, directory, base, candidate string) ([]staging.ChangedFile, error) {
	body, err := b.outputBytes(ctx, directory, "diff-tree", "--no-commit-id", "--raw", "-r", "--no-renames", "--no-abbrev", "-z", base, candidate)
	if err != nil {
		return nil, err
	}
	return parseRawDiff(body)
}

func (b *Backend) changedFilesCached(ctx context.Context, directory, base string) ([]staging.ChangedFile, error) {
	body, err := b.outputBytes(ctx, directory, "diff", "--cached", "--raw", "--no-renames", "--no-abbrev", "-z", base, "--")
	if err != nil {
		return nil, err
	}
	return parseRawDiff(body)
}

func parseRawDiff(body []byte) ([]staging.ChangedFile, error) {
	parts := bytes.Split(body, []byte{0})
	result := make([]staging.ChangedFile, 0, len(parts)/2)
	for index := 0; index < len(parts); {
		if len(parts[index]) == 0 {
			index++
			continue
		}
		header := string(parts[index])
		index++
		fields := strings.Fields(header)
		if len(fields) != 5 || !strings.HasPrefix(fields[0], ":") || index >= len(parts) {
			return nil, fmt.Errorf("parse Git raw diff header %q", header)
		}
		path := string(parts[index])
		index++
		change := fields[4]
		switch change[0] {
		case 'A':
			change = "added"
		case 'D':
			change = "deleted"
		case 'M':
			change = "modified"
		case 'T':
			change = "type_changed"
		default:
			return nil, fmt.Errorf("unsupported Git change %q", fields[4])
		}
		result = append(result, staging.ChangedFile{Path: path, Change: change, OldMode: strings.TrimPrefix(fields[0], ":"), NewMode: fields[1], OldBlob: fields[2], NewBlob: fields[3]})
	}
	return result, nil
}

func marshalManifest(manifest staging.Manifest) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := newCanonicalJSONEncoder(&buffer)
	if err := encoder(manifest); err != nil {
		return nil, fmt.Errorf("encode staging manifest: %w", err)
	}
	return buffer.Bytes(), nil
}

func newCanonicalJSONEncoder(buffer *bytes.Buffer) func(any) error {
	return func(value any) error {
		body, err := jsonMarshal(value)
		if err != nil {
			return err
		}
		buffer.Write(body)
		buffer.WriteByte('\n')
		return nil
	}
}

var jsonMarshal = func(value any) ([]byte, error) {
	// encoding/json preserves struct field order and sorts map keys. The manifest
	// contains no maps, making this representation deterministic.
	return json.Marshal(value)
}

func validateChangedSymlinks(root string, files []staging.ChangedFile) error {
	for _, changed := range files {
		path := filepath.Join(root, filepath.FromSlash(changed.Path))
		if err := staging.PathWithin(root, path); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && changed.Change == "deleted" {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil || staging.PathWithin(root, target) != nil {
				return fmt.Errorf("%w: changed symlink escapes workspace", staging.ErrUnsafeRepository)
			}
		}
	}
	return nil
}

func inspectAttributes(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != ".gitattributes" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, "filter=") || strings.Contains(lower, "working-tree-encoding") {
			return fmt.Errorf("%w: unsafe .gitattributes directive", staging.ErrUnsafeRepository)
		}
		return nil
	})
}

func rejectNestedRepositories(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.Name() == ".git" {
			if filepath.Dir(path) == root {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return fmt.Errorf("%w: nested repository detected", staging.ErrUnsafeRepository)
		}
		return nil
	})
}

func (b *Backend) output(ctx context.Context, directory string, args ...string) (string, error) {
	body, err := b.outputBytes(ctx, directory, args...)
	return string(body), err
}

func (b *Backend) outputEnv(ctx context.Context, directory string, extraEnv []string, args ...string) (string, error) {
	body, err := b.run(ctx, directory, extraEnv, args...)
	return string(body), err
}

func (b *Backend) outputBytes(ctx context.Context, directory string, args ...string) ([]byte, error) {
	return b.run(ctx, directory, nil, args...)
}

func (b *Backend) outputBytesLimited(ctx context.Context, directory string, limit int64, args ...string) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: output limit must be positive", staging.ErrInvalidInput)
	}
	return b.runLimited(ctx, directory, nil, limit, args...)
}

func (b *Backend) outputAllowExitOne(ctx context.Context, directory string, args ...string) (string, error) {
	body, err := b.run(ctx, directory, nil, args...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
	}
	return string(body), err
}

func (b *Backend) run(ctx context.Context, directory string, extraEnv []string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	fullArgs := b.safeArgs(args...)
	command := exec.CommandContext(commandCtx, b.binary, fullArgs...)
	command.Dir = directory
	command.Env = b.safeEnv(extraEnv)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if commandCtx.Err() != nil {
			return nil, commandCtx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if len(message) > 1000 {
			message = message[:1000]
		}
		return nil, fmt.Errorf("git command failed: %w: %s", err, message)
	}
	return stdout.Bytes(), nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int64
	over   bool
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	if w.over {
		return 0, errOutputLimit
	}
	remaining := w.limit - int64(w.buffer.Len())
	if remaining <= 0 {
		w.over = true
		return 0, errOutputLimit
	}
	if int64(len(p)) > remaining {
		_, _ = w.buffer.Write(p[:remaining])
		w.over = true
		return int(remaining), errOutputLimit
	}
	return w.buffer.Write(p)
}

var errOutputLimit = errors.New("Git output exceeds configured limit")

func (b *Backend) runLimited(ctx context.Context, directory string, extraEnv []string, limit int64, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	fullArgs := b.safeArgs(args...)
	command := exec.CommandContext(commandCtx, b.binary, fullArgs...)
	command.Dir = directory
	command.Env = b.safeEnv(extraEnv)
	stdout := &boundedBuffer{limit: limit}
	stderr := &boundedBuffer{limit: 4096}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if stdout.over {
		return nil, fmt.Errorf("%w: generated Git artifact exceeds %d bytes", staging.ErrInvalidInput, limit)
	}
	if err != nil {
		if commandCtx.Err() != nil {
			return nil, commandCtx.Err()
		}
		message := strings.TrimSpace(stderr.buffer.String())
		return nil, fmt.Errorf("git command failed: %w: %s", err, message)
	}
	return stdout.buffer.Bytes(), nil
}

func (b *Backend) safeArgs(args ...string) []string {
	base := []string{
		"-c", "core.hooksPath=" + b.hooksDir,
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-c", "core.attributesFile=/dev/null",
	}
	return append(base, args...)
}

func (b *Backend) safeEnv(extra []string) []string {
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Dir(b.globalConfig),
		"TMPDIR=" + os.TempDir(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + b.globalConfig,
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
	allowedExtra := map[string]struct{}{
		"GIT_AUTHOR_NAME": {}, "GIT_AUTHOR_EMAIL": {}, "GIT_AUTHOR_DATE": {},
		"GIT_COMMITTER_NAME": {}, "GIT_COMMITTER_EMAIL": {}, "GIT_COMMITTER_DATE": {},
	}
	for _, value := range extra {
		name, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		if _, allowed := allowedExtra[name]; !allowed {
			continue
		}
		environment = append(environment, value)
	}
	return environment
}

var _ staging.GitBackend = (*Backend)(nil)

func (b *Backend) VerifySealedRevision(ctx context.Context, repository staging.RepositoryConfig, workspace staging.WorkspaceRef, baseCommit, candidateCommit, candidateTree string) error {
	if err := b.ValidateRepository(ctx, repository); err != nil {
		return err
	}
	if err := b.validateWorkspaceRef(workspace); err != nil {
		return err
	}
	status, err := b.outputBytes(ctx, workspace.WorkspacePath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if len(status) != 0 {
		return staging.ErrWorkspaceDirty
	}
	head, err := b.output(ctx, workspace.WorkspacePath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != candidateCommit {
		return fmt.Errorf("%w: worktree head differs from candidate", staging.ErrGitConflict)
	}
	parent, err := b.output(ctx, workspace.WorkspacePath, "rev-parse", "HEAD^")
	if err != nil || strings.TrimSpace(parent) != baseCommit {
		return fmt.Errorf("%w: candidate parent differs from base", staging.ErrGitConflict)
	}
	tree, err := b.output(ctx, workspace.WorkspacePath, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(tree) != candidateTree {
		return fmt.Errorf("%w: candidate tree differs", staging.ErrGitConflict)
	}
	return nil
}
