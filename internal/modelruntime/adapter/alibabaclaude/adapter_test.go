//go:build !windows

package alibabaclaude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

func TestAdapterDispatchUsesPinnedCLIAndStdin(t *testing.T) {
	now := time.Date(2026, 8, 7, 14, 0, 0, 0, time.UTC)
	cfg := testConfig(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '2.1.224\n'
  exit 0
fi
cat >/dev/null
printf '{"result":"ok","usage":{"input_tokens":12,"output_tokens":3}}\n'
`)
	adapter, err := newAdapter(cfg, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{ProviderID: ProviderID, ProviderModelID: "qwen3.6-flash", Deadline: now.Add(time.Minute)}); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	secretContext := []byte("private-context-never-in-argv")
	request := modelruntime.CanonicalRequest{
		ProviderID: ProviderID, ProviderModelID: "qwen3.6-flash",
		RenderedContext: secretContext, OutputMode: modelruntime.OutputText,
		MaxOutputTokens: 128, Deadline: now.Add(time.Minute),
	}
	for _, arg := range adapter.arguments(request) {
		if strings.Contains(arg, string(secretContext)) {
			t.Fatalf("rendered context leaked to argv: %q", arg)
		}
	}
	response, err := adapter.Dispatch(context.Background(), request)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if string(response.Content) != "ok" || response.InputTokens != 12 || response.OutputTokens != 3 {
		t.Fatalf("response=%+v", response)
	}
	if response.ProviderOutcome.Transport != modelruntime.TransportCLI || response.ProviderOutcome.ProcessExitCode == nil || *response.ProviderOutcome.ProcessExitCode != 0 {
		t.Fatalf("outcome=%+v", response.ProviderOutcome)
	}
}

func TestAdapterJSONResponseUsesStructuredOutput(t *testing.T) {
	now := time.Date(2026, 8, 7, 14, 0, 0, 0, time.UTC)
	cfg := testConfig(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '2.1.224\n'
  exit 0
fi
cat >/dev/null
printf '{"result":"ignored","structured_output":{"ok":true},"usage":{"input_tokens":4,"output_tokens":2}}\n'
`)
	adapter, err := newAdapter(cfg, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Dispatch(context.Background(), modelruntime.CanonicalRequest{
		ProviderID: ProviderID, ProviderModelID: "qwen3.6-flash", RenderedContext: []byte("input"),
		OutputMode: modelruntime.OutputJSON, OutputSchema: []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`),
		MaxOutputTokens: 64, Deadline: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(response.Content) != `{"ok":true}` {
		t.Fatalf("content=%s", response.Content)
	}
}

func TestAdapterStartFailureIsNotSent(t *testing.T) {
	now := time.Date(2026, 8, 7, 14, 0, 0, 0, time.UTC)
	cfg := testConfig(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '2.1.224\n'
  exit 0
fi
printf '{"result":"ok"}\n'
`)
	adapter, err := newAdapter(cfg, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	// Preflight has already proven the executable. Simulate replacement/removal
	// between the request barrier and process start; this remains provably not sent.
	if err = os.Remove(cfg.Executable); err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Dispatch(context.Background(), modelruntime.CanonicalRequest{
		ProviderID: ProviderID, ProviderModelID: "qwen3.6-flash", RenderedContext: []byte("input"),
		OutputMode: modelruntime.OutputText, MaxOutputTokens: 64, Deadline: now.Add(time.Minute),
	})
	var adapterErr *modelruntime.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Outcome.OutcomeClassification != modelruntime.ProviderOutcomeNotSent {
		t.Fatalf("err=%v", err)
	}
}

func TestAdapterRejectsMutableEndpointAndSettingsDrift(t *testing.T) {
	cfg := testConfig(t, `#!/bin/sh
printf '2.1.224\n'
`)
	cfg.TokenPlanBaseURL = "https://coding-intl.dashscope.aliyuncs.com/apps/anthropic"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Coding Plan endpoint accepted by Token Plan adapter")
	}
	cfg = testConfig(t, `#!/bin/sh
printf '2.1.224\n'
`)
	if err := os.WriteFile(cfg.SettingsFile, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-test-token","ANTHROPIC_BASE_URL":"https://evil.example","ANTHROPIC_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_HAIKU_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_SONNET_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_OPUS_MODEL":"qwen3.6-flash","CLAUDE_CODE_SUBAGENT_MODEL":"qwen3.6-flash"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateSettingsFile(cfg); err == nil {
		t.Fatal("settings drift accepted")
	}
}

func TestRunCLITimeoutIsPostStartAmbiguousPrimitive(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "slow")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, started, _, err := runCLI(context.Background(), cliRunRequest{
		Executable: script, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"}, Stdin: []byte("x"),
		MaxStdoutBytes: 4096, MaxStderrBytes: 4096, Timeout: 150 * time.Millisecond, KillGrace: 100 * time.Millisecond,
	})
	if !started || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("started=%v err=%v", started, err)
	}
}

func testConfig(t *testing.T, executableBody string) Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "claude")
	if err := os.WriteFile(executable, []byte(executableBody), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-test-token","ANTHROPIC_BASE_URL":"` + SingaporeTokenPlanEndpoint + `","ANTHROPIC_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_HAIKU_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_SONNET_MODEL":"qwen3.6-flash","ANTHROPIC_DEFAULT_OPUS_MODEL":"qwen3.6-flash","CLAUDE_CODE_SUBAGENT_MODEL":"qwen3.6-flash"}}`)
	settingsFile := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsFile, settings, 0o600); err != nil {
		t.Fatal(err)
	}
	executableHash := sha256.Sum256([]byte(executableBody))
	settingsHash := sha256.Sum256(settings)
	return Config{
		Enabled: true, Executable: executable, ExpectedVersion: "2.1.224",
		ExecutableSHA256: hex.EncodeToString(executableHash[:]), SettingsFile: settingsFile,
		SettingsSHA256: hex.EncodeToString(settingsHash[:]), TokenPlanBaseURL: SingaporeTokenPlanEndpoint,
		WorkDir: dir, RuntimePath: "/usr/bin:/bin", RequestTimeout: time.Second,
		KillGrace: 100 * time.Millisecond, MaxStdoutBytes: 1 << 20, MaxStderrBytes: 64 << 10, MaxConcurrency: 1,
	}
}
