// Package gitsource reads a repository at one exact commit.
//
// It is the authority half of repository evidence. Every operation names the
// commit explicitly and none of them can be asked about HEAD: a reader that
// could answer "the current version" would eventually be asked to, and the
// answer would be evidence about a repository nobody decided on.
package gitsource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Source reads one repository on disk.
type Source struct {
	// Directory is the repository working tree or bare repository.
	Directory string
	// Binary is the git executable, trusted deployment configuration.
	Binary string
	// MaxFileBytes refuses a file too large to excerpt sensibly, before it
	// is read into memory rather than after.
	MaxFileBytes int
}

func New(directory, binary string, maxFileBytes int) (*Source, error) {
	if strings.TrimSpace(directory) == "" || strings.TrimSpace(binary) == "" {
		return nil, fmt.Errorf("%w: git source needs a directory and a binary", repositoryevidence.ErrInvalidFragment)
	}
	if maxFileBytes < 1 {
		maxFileBytes = 2 << 20
	}
	return &Source{Directory: directory, Binary: binary, MaxFileBytes: maxFileBytes}, nil
}

// Search lists paths whose contents match a query at the given commit.
//
// This is discovery: it may miss, it may over-return, and nothing it produces
// is quoted to anyone. Its results are path candidates and nothing else.
func (s *Source) Search(ctx context.Context, baseSHA, query string, limit int) ([]repositoryevidence.Match, error) {
	if err := validate(baseSHA); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: empty search", repositoryevidence.ErrInvalidFragment)
	}
	if limit < 1 {
		limit = 10
	}
	// -n gives the line of each hit, -F treats the query as a literal so a
	// search string can never become a pattern with its own semantics, and
	// the commit is named explicitly rather than implied by a checkout.
	out, code, err := s.run(ctx, "grep", "-n", "-F", "-e", query, baseSHA)
	if err != nil {
		// git grep exits 1 for "nothing matched", which is an ANSWER.
		// Anything else -- an unreadable repository, a commit that does not
		// exist, a broken git -- is a sensor failure, and turning it into
		// "no code found" is how the design phase would go back to guessing
		// while everything looked healthy. Blindness must be loud.
		if code != 1 {
			return nil, fmt.Errorf("%w: searching %s at %s: %v", ErrSourceUnavailable, s.Directory, baseSHA, err)
		}
		return nil, nil
	}
	matches := make([]repositoryevidence.Match, 0, limit)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		// Each hit is "<commit>:<path>:<line>:<content>".
		rest := strings.TrimPrefix(strings.TrimSpace(line), baseSHA+":")
		path, after, ok := strings.Cut(rest, ":")
		if !ok || repositoryevidence.ValidatePath(path) != nil {
			continue
		}
		number, _, ok := strings.Cut(after, ":")
		if !ok {
			continue
		}
		at, convErr := strconv.Atoi(number)
		if convErr != nil || at < 1 {
			continue
		}
		// One location per file: a term appearing twenty times in one file
		// is one place to look, not twenty, and spending the range budget
		// on repetitions of the same neighbourhood buys nothing.
		if _, already := seen[path]; already {
			continue
		}
		seen[path] = struct{}{}
		matches = append(matches, repositoryevidence.Match{Path: path, Line: at})
		if len(matches) >= limit {
			break
		}
	}
	return matches, nil
}

// Lines reports a file's length at a commit.
func (s *Source) Lines(ctx context.Context, baseSHA, path string) (int, error) {
	content, err := s.file(ctx, baseSHA, path)
	if err != nil {
		// A path that is absent at this commit is an answer -- a stale
		// suggestion, which is exactly how discovery is allowed to fail. An
		// unreachable repository is not, and must not read as "empty file".
		if errors.Is(err, ErrSourceUnavailable) {
			return 0, err
		}
		return 0, nil
	}
	if content == "" {
		return 0, nil
	}
	return len(strings.Split(content, "\n")), nil
}

// ReadRange returns the exact lines of a file at a commit.
func (s *Source) ReadRange(ctx context.Context, baseSHA, path string, start, end int) (string, error) {
	content, err := s.file(ctx, baseSHA, path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(content, "\n")
	if start < 1 || start > len(lines) {
		return "", fmt.Errorf("%w: %s has %d lines at %s", repositoryevidence.ErrInvalidFragment, path, len(lines), baseSHA)
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n"), nil
}

func (s *Source) file(ctx context.Context, baseSHA, path string) (string, error) {
	if err := validate(baseSHA); err != nil {
		return "", err
	}
	if err := repositoryevidence.ValidatePath(path); err != nil {
		return "", err
	}
	out, code, err := s.run(ctx, "show", baseSHA+":"+path)
	if err != nil {
		// git show exits 128 both for "no such path in that commit" and for
		// "no such commit". The first is a stale candidate; the second means
		// the sensor is pointed at a repository that cannot answer, so the
		// commit is checked separately rather than guessed from the code.
		if code != 128 || !s.commitExists(ctx, baseSHA) {
			return "", fmt.Errorf("%w: reading %s at %s from %s: %v", ErrSourceUnavailable, path, baseSHA, s.Directory, err)
		}
		return "", fmt.Errorf("%w: %s does not exist at %s", repositoryevidence.ErrInvalidFragment, path, baseSHA)
	}
	if len(out) > s.MaxFileBytes {
		return "", fmt.Errorf("%w: %s is %d bytes at %s, too large to excerpt", repositoryevidence.ErrInvalidFragment, path, len(out), baseSHA)
	}
	return strings.TrimRight(out, "\n"), nil
}

func validate(baseSHA string) error {
	if !shaPattern.MatchString(baseSHA) {
		return fmt.Errorf("%w: base_sha must be a full 40-character commit id, got %q", repositoryevidence.ErrInvalidFragment, baseSHA)
	}
	return nil
}

// run executes git with a fixed argument vector and no shell.
//
// Arguments are never concatenated into a command line, the commit is always
// validated before it gets here, and the environment is not inherited: a
// repository read must not be able to become anything other than a read.
func (s *Source) run(ctx context.Context, args ...string) (string, int, error) {
	command := exec.CommandContext(ctx, s.Binary, append([]string{"-C", s.Directory}, args...)...)
	command.Env = []string{"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "HOME=/nonexistent"}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		// The exit code is returned rather than folded into the error,
		// because "nothing matched" and "the repository is unreachable" are
		// different facts and only the caller knows which one it can absorb.
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return "", code, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), 0, nil
}

// commitExists distinguishes a repository that cannot answer from a commit
// that simply does not contain a path.
func (s *Source) commitExists(ctx context.Context, baseSHA string) bool {
	_, _, err := s.run(ctx, "cat-file", "-e", baseSHA+"^{commit}")
	return err == nil
}

// ErrSourceUnavailable reports that the repository could not be read at all.
//
// It is deliberately distinct from an absent path: one is a fact about the
// code, the other is a fact about the sensor, and collapsing them is what
// turns a broken observer into a design that quietly starts guessing again.
var ErrSourceUnavailable = errors.New("gitsource: repository could not be read")

var _ repositoryevidence.Source = (*Source)(nil)
