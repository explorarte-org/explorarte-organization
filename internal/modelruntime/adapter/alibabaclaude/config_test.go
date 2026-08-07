package alibabaclaude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigDisabledNeedsNoProviderArtifacts(t *testing.T) {
	cfg, err := LoadConfig(func(key string) (string, bool) {
		if key == "ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED" {
			return "false", true
		}
		return "", false
	}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("disabled provider unexpectedly enabled")
	}
	if cfg.MaxConcurrency != defaultMaxConcurrency || cfg.RequestTimeout != defaultRequestTimeout {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestConfigRejectsUnapprovedEndpointAndRelativeArtifacts(t *testing.T) {
	cfg := Config{
		Enabled:          true,
		Executable:       "claude",
		ExpectedVersion:  "2.1.224",
		ExecutableSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SettingsFile:     "settings.json",
		SettingsSHA256:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TokenPlanBaseURL: "https://example.invalid/apps/anthropic",
		WorkDir:          "work",
		RuntimePath:      "/usr/bin:/bin",
		RequestTimeout:   time.Minute,
		KillGrace:        time.Second,
		MaxStdoutBytes:   1 << 20,
		MaxStderrBytes:   64 << 10,
		MaxConcurrency:   1,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("relative provider artifacts and unapproved endpoint were accepted")
	}
}

func TestValidateInstallationDetectsExecutableHashAndVersionDrift(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"hasCompletedOnboarding":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "claude")
	body := []byte("#!/bin/sh\nprintf '2.1.225\\n'\n")
	if err := os.WriteFile(executable, body, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte("{\"env\":{\"ANTHROPIC_AUTH_TOKEN\":\"sk-test-token\",\"ANTHROPIC_BASE_URL\":\"" + SingaporeTokenPlanEndpoint + "\",\"ANTHROPIC_MODEL\":\"qwen3.6-plus\",\"ANTHROPIC_DEFAULT_HAIKU_MODEL\":\"qwen3.6-flash\",\"ANTHROPIC_DEFAULT_SONNET_MODEL\":\"qwen3.6-plus\",\"ANTHROPIC_DEFAULT_OPUS_MODEL\":\"qwen3.6-plus\",\"CLAUDE_CODE_SUBAGENT_MODEL\":\"qwen3.6-plus\"}}")
	settingsFile := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsFile, settings, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfigFromArtifacts(t, dir, executable, body, settingsFile, settings)
	cfg.ExecutableSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := validateInstallation(t.Context(), cfg); err == nil {
		t.Fatal("executable hash drift accepted")
	}

	cfg = testConfigFromArtifacts(t, dir, executable, body, settingsFile, settings)
	cfg.ExpectedVersion = "2.1.224"
	if err := validateInstallation(t.Context(), cfg); err == nil {
		t.Fatal("executable version drift accepted")
	}
}

func testConfigFromArtifacts(t *testing.T, dir, executable string, executableBody []byte, settingsFile string, settings []byte) Config {
	t.Helper()
	return Config{
		Enabled:          true,
		Executable:       executable,
		ExpectedVersion:  "2.1.225",
		ExecutableSHA256: sha256Hex(executableBody),
		SettingsFile:     settingsFile,
		SettingsSHA256:   sha256Hex(settings),
		TokenPlanBaseURL: SingaporeTokenPlanEndpoint,
		WorkDir:          dir,
		RuntimePath:      "/usr/bin:/bin",
		RequestTimeout:   time.Minute,
		KillGrace:        time.Second,
		MaxStdoutBytes:   1 << 20,
		MaxStderrBytes:   64 << 10,
		MaxConcurrency:   1,
	}
}
