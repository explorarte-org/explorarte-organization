package alibabaclaude

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestValidateSettingsRejectsUnknownFieldsAndUnsafeTokenEncoding(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		body string
	}{
		{
			name: "unknown top level customization",
			body: `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-test-token","ANTHROPIC_BASE_URL":"https://token-plan.ap-southeast-1.maas.aliyuncs.com/apps/anthropic","ANTHROPIC_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_HAIKU_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_SONNET_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_OPUS_MODEL":"qwen3.6-flash","CLAUDE_CODE_SUBAGENT_MODEL":"qwen3.6-flash"},"permissions":{"allow":["Bash(*)"]}}`,
		},
		{
			name: "token encoded with whitespace",
			body: `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk test token","ANTHROPIC_BASE_URL":"https://token-plan.ap-southeast-1.maas.aliyuncs.com/apps/anthropic","ANTHROPIC_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_HAIKU_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_SONNET_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_OPUS_MODEL":"qwen3.6-flash","CLAUDE_CODE_SUBAGENT_MODEL":"qwen3.6-flash"}}`,
		},
		{
			name: "token with JSON escape",
			body: `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-test\\ntoken","ANTHROPIC_BASE_URL":"https://token-plan.ap-southeast-1.maas.aliyuncs.com/apps/anthropic","ANTHROPIC_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_HAIKU_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_SONNET_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_OPUS_MODEL":"qwen3.6-flash","CLAUDE_CODE_SUBAGENT_MODEL":"qwen3.6-flash"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".json")
			body := []byte(tc.body)
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := settingsOnlyConfig(path, body)
			if _, err := validateSettingsFile(cfg); err == nil {
				t.Fatalf("unsafe settings accepted: %s", tc.name)
			}
		})
	}
}

func TestValidateSettingsRejectsGroupReadableSecretFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics")
	}
	dir := t.TempDir()
	body := validSettingsBody()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, body, 0o640); err != nil {
		t.Fatal(err)
	}
	cfg := settingsOnlyConfig(path, body)
	if _, err := validateSettingsFile(cfg); err == nil {
		t.Fatal("group-readable settings file accepted")
	}
}

func TestValidateSettingsAcceptsDedicatedMinimalFile(t *testing.T) {
	dir := t.TempDir()
	body := validSettingsBody()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := settingsOnlyConfig(path, body)
	validated, err := validateSettingsFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if validated.BaseURL != SingaporeTokenPlanEndpoint {
		t.Fatalf("base URL=%q", validated.BaseURL)
	}
}

func validSettingsBody() []byte {
	return []byte("{\"env\":{\"ANTHROPIC_AUTH_TOKEN\":\"sk-test-token\",\"ANTHROPIC_BASE_URL\":\"" + SingaporeTokenPlanEndpoint + "\",\"ANTHROPIC_MODEL\":\"qwen3.6-flash\",\"ANTHROPIC_DEFAULT_HAIKU_MODEL\":\"qwen3.6-flash\",\"ANTHROPIC_DEFAULT_SONNET_MODEL\":\"qwen3.6-flash\",\"ANTHROPIC_DEFAULT_OPUS_MODEL\":\"qwen3.6-flash\",\"CLAUDE_CODE_SUBAGENT_MODEL\":\"qwen3.6-flash\"}}")
}

func settingsOnlyConfig(path string, body []byte) Config {
	return Config{
		Enabled:          true,
		Executable:       "/usr/local/bin/claude",
		ExpectedVersion:  "2.1.224",
		ExecutableSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SettingsFile:     path,
		SettingsSHA256:   sha256Hex(body),
		TokenPlanBaseURL: SingaporeTokenPlanEndpoint,
		WorkDir:          "/var/lib/explorarte/alibaba-claude",
		RuntimePath:      "/usr/bin:/bin",
		RequestTimeout:   time.Minute,
		KillGrace:        time.Second,
		MaxStdoutBytes:   1 << 20,
		MaxStderrBytes:   64 << 10,
		MaxConcurrency:   1,
	}
}
