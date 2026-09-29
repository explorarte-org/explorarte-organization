package coderunner

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GO_TEST runs a mission's own code, so it runs outside the controller.
//
// External audit A1 (2026-09-27, step B): a Go test a mission proposes ran as a subprocess of the
// controller, with its uid, its mounts and its network: it could reach PostgreSQL, write the base
// repository and every other workspace. Of the runner's operations only GO_TEST executes mission code
// (GO_BUILD and GO_VET compile and analyse; FITNESS runs host scripts a mission cannot change; git,
// gofmt and search are trusted tools), so only GO_TEST moves.
//
// The controller copies the attempt's workspace (without .git) into a job under an exchange directory
// shared with a separate executor container that has no network, no secrets, no database settings and
// no other mount. The executor runs exactly one job and exits; its container restart kills anything the
// test left running and empties its build cache. The job carries a secret nonce the executor reads and
// deletes before the test starts (its own memory is non-dumpable); the result is authenticated with an
// HMAC under that nonce, so a test that writes a result file of its own is refused.

const (
	isolatedJobFile     = "job.json"
	isolatedClaimFile   = "claimed"
	isolatedResultFile  = "result.json"
	isolatedSourceDir   = "src"
	isolatedJobsDir     = "jobs"
	isolatedPollEvery   = 200 * time.Millisecond
	isolatedClaimWindow = 30 * time.Second
)

// ErrIsolatedResultForged is a result whose authentication does not verify: something other than the
// executor wrote it. It is treated as indeterminate.
var ErrIsolatedResultForged = errors.New("isolated test result failed authentication")

type isolatedJob struct {
	Nonce     string   `json:"nonce"`
	Args      []string `json:"args"`
	TimeoutMS int64    `json:"timeout_ms"`
	HeadMax   int      `json:"head_max"`
	TailMax   int      `json:"tail_max"`
}

type isolatedResult struct {
	ExitCode      int    `json:"exit_code"`
	Success       bool   `json:"success"`
	Indeterminate bool   `json:"indeterminate"`
	Output        string `json:"output"`
	BytesProduced int64  `json:"bytes_produced"`
	OutputDigest  string `json:"output_digest"`
	Truncated     bool   `json:"truncated"`
	MAC           string `json:"mac"`
}

func (r isolatedResult) mac(nonce string) string {
	unsigned := r
	unsigned.MAC = ""
	body, _ := json.Marshal(unsigned)
	h := hmac.New(sha256.New, []byte(nonce))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// IsolatedTests submits GO_TEST runs to the executor through Root, the exchange directory.
type IsolatedTests struct {
	Root string
	// Grace is how long past the operation timeout the controller waits for a claimed job's result.
	Grace time.Duration
}

// RunGoTest runs `go test args...` against a copy of workspace in the executor.
func (s IsolatedTests) RunGoTest(ctx context.Context, workspace string, args []string, timeout time.Duration, headMax, tailMax int) (Result, error) {
	command := append([]string{"go", "test"}, args...)
	id, err := randomHex(16)
	if err != nil {
		return Result{}, err
	}
	nonce, err := randomHex(32)
	if err != nil {
		return Result{}, err
	}
	jobDir := filepath.Join(s.Root, isolatedJobsDir, id)
	defer os.RemoveAll(jobDir)
	if err := copyWorkspace(workspace, filepath.Join(jobDir, isolatedSourceDir)); err != nil {
		return Result{}, fmt.Errorf("stage isolated test source: %w", err)
	}
	job := isolatedJob{Nonce: nonce, Args: args, TimeoutMS: timeout.Milliseconds(), HeadMax: headMax, TailMax: tailMax}
	if err := writeFileAtomic(filepath.Join(jobDir, isolatedJobFile), job); err != nil {
		return Result{}, err
	}

	grace := s.Grace
	if grace <= 0 {
		grace = 30 * time.Second
	}
	claimDeadline := time.Now().Add(isolatedClaimWindow)
	resultDeadline := time.Time{}
	for {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("%s: %w", GoTest, ErrIndeterminateExecution)
		}
		if raw, readErr := os.ReadFile(filepath.Join(jobDir, isolatedResultFile)); readErr == nil {
			var result isolatedResult
			if json.Unmarshal(raw, &result) != nil || !hmac.Equal([]byte(result.MAC), []byte(result.mac(nonce))) {
				return Result{}, fmt.Errorf("%s: %w: %w", GoTest, ErrIsolatedResultForged, ErrIndeterminateExecution)
			}
			if result.Indeterminate {
				return Result{}, fmt.Errorf("%s: %w", GoTest, ErrIndeterminateExecution)
			}
			return Result{
				Type: GoTest, Success: result.Success, ExitCode: result.ExitCode, Output: result.Output,
				BytesProduced: result.BytesProduced, OutputDigest: result.OutputDigest, Truncated: result.Truncated,
				Command: command,
			}, nil
		}
		_, claimErr := os.Stat(filepath.Join(jobDir, isolatedClaimFile))
		claimed := claimErr == nil
		switch {
		case !claimed && time.Now().After(claimDeadline):
			// Never started: nothing ran, so this is an unavailable executor, not an indeterminate run.
			return Result{}, fmt.Errorf("isolated test executor did not take the job within %s", isolatedClaimWindow)
		case claimed && resultDeadline.IsZero():
			resultDeadline = time.Now().Add(timeout + grace)
		case claimed && time.Now().After(resultDeadline):
			return Result{}, fmt.Errorf("%s: no result from the isolated executor: %w", GoTest, ErrIndeterminateExecution)
		}
		time.Sleep(isolatedPollEvery)
	}
}

// IsolatedTestExecutor is the executor side: it takes one job from Root, runs it, and returns.
type IsolatedTestExecutor struct {
	Root string
}

// RunOne waits for a job, runs it, writes its authenticated result, and returns. The caller exits
// after it, so the container restart ends every process the test started.
func (x IsolatedTestExecutor) RunOne(ctx context.Context) error {
	jobsRoot := filepath.Join(x.Root, isolatedJobsDir)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, _ := os.ReadDir(jobsRoot)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			jobDir := filepath.Join(jobsRoot, entry.Name())
			job, ok := claimJob(jobDir)
			if !ok {
				continue
			}
			return x.run(ctx, jobDir, job)
		}
		time.Sleep(isolatedPollEvery)
	}
}

// claimJob takes a job by creating its claim marker exclusively, reads it into memory and deletes it
// before anything runs, so the test never sees the nonce.
func claimJob(jobDir string) (isolatedJob, bool) {
	jobPath := filepath.Join(jobDir, isolatedJobFile)
	if _, err := os.Stat(jobPath); err != nil {
		return isolatedJob{}, false
	}
	marker, err := os.OpenFile(filepath.Join(jobDir, isolatedClaimFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return isolatedJob{}, false
	}
	marker.Close()
	raw, err := os.ReadFile(jobPath)
	removeErr := os.Remove(jobPath)
	var job isolatedJob
	if err != nil || removeErr != nil || json.Unmarshal(raw, &job) != nil || job.Nonce == "" {
		return isolatedJob{}, false
	}
	return job, true
}

func (x IsolatedTestExecutor) run(ctx context.Context, jobDir string, job isolatedJob) error {
	result := isolatedResult{}
	if err := validateIsolatedArgs(job.Args); err != nil {
		result = isolatedResult{ExitCode: -1, Output: "refused: " + err.Error()}
	} else {
		timeout := time.Duration(job.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = defaultOperationTimeout
		}
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		capture := newBoundedOutput(job.HeadMax, job.TailMax, nil)
		code, runErr := runSupervisedEnv(runCtx, filepath.Join(jobDir, isolatedSourceDir), "", capture, isolatedTestEnv(os.Environ()), "go", append([]string{"test"}, job.Args...)...)
		cancel()
		if errors.Is(runErr, ErrIndeterminateExecution) {
			result.Indeterminate = true
		} else {
			br := capture.Result()
			result = isolatedResult{ExitCode: code, Success: runErr == nil, Output: br.String(), BytesProduced: br.TotalBytes, OutputDigest: br.DigestSHA256, Truncated: br.Truncated}
		}
	}
	result.MAC = result.mac(job.Nonce)
	return writeFileAtomic(filepath.Join(jobDir, isolatedResultFile), result)
}

// validateIsolatedArgs admits exactly what the controller sends for GO_TEST: package patterns and -race.
func validateIsolatedArgs(args []string) error {
	for _, arg := range args {
		if arg == "-race" {
			continue
		}
		if err := validatePackage(arg); err != nil {
			return err
		}
	}
	return nil
}

// isolatedTestEnv is the executor's subprocess environment: the toolchain allowlist, with test result
// caching off, so a result cached by an earlier job can never stand for this one.
func isolatedTestEnv(environ []string) []string {
	env := subprocessEnv(environ)
	flags := "-count=1"
	for i, kv := range env {
		if value, ok := strings.CutPrefix(kv, "GOFLAGS="); ok {
			env[i] = "GOFLAGS=" + strings.TrimSpace(value+" "+flags)
			return env
		}
	}
	return append(env, "GOFLAGS="+flags)
}

// copyWorkspace copies the workspace tree without its .git, preserving file modes and symlinks.
func copyWorkspace(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, 0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyRegularFile(path, target, info.Mode().Perm())
		}
		return nil
	})
}

func copyRegularFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeFileAtomic(path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
