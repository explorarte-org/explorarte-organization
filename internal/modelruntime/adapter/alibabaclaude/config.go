package alibabaclaude

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ProviderID            = "alibaba_token_plan_via_claude_code"
	AdapterID             = "alibaba_claude_code_print"
	AdapterVersion        = 2
	RequestSchemaVersion  = "claude.code.print.request.v1"
	ResponseSchemaVersion = "claude.code.print.response.v1"

	SingaporeTokenPlanEndpoint = "https://token-plan.ap-southeast-1.maas.aliyuncs.com/apps/anthropic"

	defaultRequestTimeout = 2 * time.Minute
	defaultKillGrace      = 3 * time.Second
	defaultMaxStderrBytes = 64 << 10
	defaultMaxConcurrency = 2
	maxClaudeStdinBytes   = 10 << 20
)

type LookupEnv func(string) (string, bool)

type Config struct {
	Enabled          bool
	Executable       string
	ExpectedVersion  string
	ExecutableSHA256 string
	SettingsFile     string
	SettingsSHA256   string
	TokenPlanBaseURL string
	WorkDir          string
	RuntimePath      string
	RequestTimeout   time.Duration
	KillGrace        time.Duration
	MaxStdoutBytes   int
	MaxStderrBytes   int
	MaxConcurrency   int
}

func LoadConfig(lookup LookupEnv, maxResponseBytes int) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("Alibaba Claude Code environment lookup is nil")
	}
	cfg := Config{
		RequestTimeout: defaultRequestTimeout,
		KillGrace:      defaultKillGrace,
		MaxStdoutBytes: maxResponseBytes,
		MaxStderrBytes: defaultMaxStderrBytes,
		MaxConcurrency: defaultMaxConcurrency,
		RuntimePath:    "/usr/local/bin:/usr/bin:/bin",
	}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_ENABLED", false); err != nil {
		return Config{}, err
	}
	for key, target := range map[string]*string{
		"ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXECUTABLE":          &cfg.Executable,
		"ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXPECTED_VERSION":    &cfg.ExpectedVersion,
		"ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_EXECUTABLE_SHA256":   &cfg.ExecutableSHA256,
		"ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_SETTINGS_FILE":       &cfg.SettingsFile,
		"ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_SETTINGS_SHA256":     &cfg.SettingsSHA256,
		"ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_TOKEN_PLAN_BASE_URL": &cfg.TokenPlanBaseURL,
		"ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_WORK_DIR":            &cfg.WorkDir,
		"ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_RUNTIME_PATH":        &cfg.RuntimePath,
	} {
		if raw, ok := lookup(key); ok {
			*target = strings.TrimSpace(raw)
		}
	}
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_REQUEST_TIMEOUT", defaultRequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.KillGrace, err = envDuration(lookup, "ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_KILL_GRACE", defaultKillGrace); err != nil {
		return Config{}, err
	}
	if cfg.MaxStderrBytes, err = envInt(lookup, "ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_MAX_STDERR_BYTES", defaultMaxStderrBytes); err != nil {
		return Config{}, err
	}
	if cfg.MaxConcurrency, err = envInt(lookup, "ORG_MODEL_PROVIDER_ALIBABA_CLAUDE_MAX_CONCURRENCY", defaultMaxConcurrency); err != nil {
		return Config{}, err
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.MaxStdoutBytes < 1024 || c.MaxStdoutBytes > 16<<20 {
		return errors.New("Alibaba Claude Code maximum stdout bytes outside allowed range")
	}
	if c.MaxStderrBytes < 1024 || c.MaxStderrBytes > 1<<20 {
		return errors.New("Alibaba Claude Code maximum stderr bytes outside allowed range")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > 30*time.Minute {
		return errors.New("Alibaba Claude Code request timeout outside allowed range")
	}
	if c.KillGrace < 100*time.Millisecond || c.KillGrace > 30*time.Second {
		return errors.New("Alibaba Claude Code kill grace outside allowed range")
	}
	if c.MaxConcurrency < 1 || c.MaxConcurrency > 64 {
		return errors.New("Alibaba Claude Code concurrency outside allowed range")
	}
	if err := validateRuntimePath(c.RuntimePath); err != nil {
		return err
	}
	if !c.Enabled {
		return nil
	}
	for name, value := range map[string]string{
		"executable": c.Executable, "expected version": c.ExpectedVersion,
		"executable sha256": c.ExecutableSHA256, "settings file": c.SettingsFile,
		"settings sha256": c.SettingsSHA256, "token plan base URL": c.TokenPlanBaseURL,
		"work directory": c.WorkDir,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("Alibaba Claude Code %s is required when enabled", name)
		}
	}
	if !filepath.IsAbs(filepath.Clean(c.Executable)) || !filepath.IsAbs(filepath.Clean(c.SettingsFile)) || !filepath.IsAbs(filepath.Clean(c.WorkDir)) {
		return errors.New("Alibaba Claude Code executable, settings file, and work directory must be absolute paths")
	}
	if !validSHA256(c.ExecutableSHA256) || !validSHA256(c.SettingsSHA256) {
		return errors.New("Alibaba Claude Code executable/settings pins must be lowercase SHA-256")
	}
	if c.TokenPlanBaseURL != SingaporeTokenPlanEndpoint {
		return fmt.Errorf("Alibaba Claude Code endpoint is not an approved Token Plan endpoint: %s", c.TokenPlanBaseURL)
	}
	if strings.ContainsAny(c.ExpectedVersion, "\r\n\x00") || len(c.ExpectedVersion) > 120 {
		return errors.New("Alibaba Claude Code expected version is invalid")
	}
	return nil
}

func validateRuntimePath(value string) error {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) == 0 || len(parts) > 16 {
		return errors.New("Alibaba Claude Code runtime PATH is invalid")
	}
	for _, part := range parts {
		clean := filepath.Clean(strings.TrimSpace(part))
		if clean == "." || !filepath.IsAbs(clean) {
			return errors.New("Alibaba Claude Code runtime PATH may contain only absolute directories")
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func envBool(lookup LookupEnv, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func envInt(lookup LookupEnv, key string, fallback int) (int, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func envDuration(lookup LookupEnv, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}
