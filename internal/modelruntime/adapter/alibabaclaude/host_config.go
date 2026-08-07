package alibabaclaude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const maxClaudeGlobalConfigBytes int64 = 4 << 10

type claudeGlobalConfig struct {
	HasCompletedOnboarding bool `json:"hasCompletedOnboarding"`
}

// validateClaudeGlobalConfig makes the HOME-level Claude config an explicit
// part of the provider boundary. Alibaba documents hasCompletedOnboarding=true
// as required to avoid Claude Code attempting Anthropic login verification on
// startup. No other ~/.claude.json keys are accepted in the isolated workdir.
func validateClaudeGlobalConfig(workDir string) error {
	path := filepath.Join(filepath.Clean(workDir), ".claude.json")
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat isolated Claude global config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxClaudeGlobalConfigBytes {
		return errors.New("isolated Claude global config must be a bounded regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("isolated Claude global config must not grant group/other permissions")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var cfg claudeGlobalConfig
	if err := decoder.Decode(&cfg); err != nil {
		return fmt.Errorf("parse isolated Claude global config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("isolated Claude global config contains multiple JSON values")
		}
		return fmt.Errorf("parse isolated Claude global config trailer: %w", err)
	}
	if !cfg.HasCompletedOnboarding {
		return errors.New("isolated Claude global config must set hasCompletedOnboarding=true")
	}
	return nil
}
