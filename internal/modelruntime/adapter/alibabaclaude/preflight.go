package alibabaclaude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxExecutableBytes int64 = 256 << 20

func validateInstallation(ctx context.Context, config Config) error {
	if err := validateWorkDir(config.WorkDir); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(config.Executable))
	if err != nil {
		return fmt.Errorf("resolve Claude Code executable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat Claude Code executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxExecutableBytes {
		return errors.New("Claude Code executable must be a bounded regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return errors.New("Claude Code executable is not executable")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(file, maxExecutableBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != config.ExecutableSHA256 {
		return errors.New("Claude Code executable hash does not match configured pin")
	}
	if _, err = validateSettingsFile(config); err != nil {
		return err
	}
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(versionCtx, config.Executable, "--version")
	cmd.Dir = config.WorkDir
	cmd.Env = childEnvironment(config)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("execute Claude Code --version: %w", err)
	}
	if len(output) > 4096 {
		return errors.New("Claude Code version output exceeds limit")
	}
	if strings.TrimSpace(string(output)) != strings.TrimSpace(config.ExpectedVersion) {
		return errors.New("Claude Code version does not match configured pin")
	}
	return nil
}

func validateWorkDir(path string) error {
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Alibaba Claude Code work directory must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return errors.New("Alibaba Claude Code work directory must not be group/other writable")
	}
	return nil
}

func childEnvironment(config Config) []string {
	return []string{
		"HOME=" + filepath.Clean(config.WorkDir),
		"PATH=" + config.RuntimePath,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TZ=UTC",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
		"CLAUDE_CODE_SKIP_PROMPT_HISTORY=1",
	}
}
