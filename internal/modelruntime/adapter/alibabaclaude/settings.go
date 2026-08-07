package alibabaclaude

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/secrets"
)

const maxSettingsBytes int64 = 64 << 10

type settingsEnvelope struct {
	Env settingsEnv `json:"env"`
}

type settingsEnv struct {
	AnthropicAuthToken         json.RawMessage `json:"ANTHROPIC_AUTH_TOKEN"`
	AnthropicBaseURL           string          `json:"ANTHROPIC_BASE_URL"`
	AnthropicModel             string          `json:"ANTHROPIC_MODEL"`
	AnthropicDefaultHaikuModel string          `json:"ANTHROPIC_DEFAULT_HAIKU_MODEL"`
	AnthropicDefaultSonnetModel string         `json:"ANTHROPIC_DEFAULT_SONNET_MODEL"`
	AnthropicDefaultOpusModel   string         `json:"ANTHROPIC_DEFAULT_OPUS_MODEL"`
	ClaudeCodeSubagentModel     string         `json:"CLAUDE_CODE_SUBAGENT_MODEL"`
}

type validatedSettings struct {
	BaseURL string
}

func validateSettingsFile(config Config) (validatedSettings, error) {
	clean := filepath.Clean(config.SettingsFile)
	info, err := os.Lstat(clean)
	if err != nil {
		return validatedSettings{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSettingsBytes {
		return validatedSettings{}, errors.New("Alibaba Claude Code settings must be a bounded regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return validatedSettings{}, errors.New("Alibaba Claude Code settings must not grant group/other permissions")
	}
	body, err := os.ReadFile(clean)
	if err != nil {
		return validatedSettings{}, err
	}
	defer secrets.Zero(body)
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != config.SettingsSHA256 {
		return validatedSettings{}, errors.New("Alibaba Claude Code settings hash does not match configured pin")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var envelope settingsEnvelope
	if err = decoder.Decode(&envelope); err != nil {
		return validatedSettings{}, fmt.Errorf("parse Alibaba Claude Code settings: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return validatedSettings{}, errors.New("Alibaba Claude Code settings contain multiple JSON values")
		}
		return validatedSettings{}, fmt.Errorf("parse Alibaba Claude Code settings trailer: %w", err)
	}
	if len(envelope.Env.AnthropicAuthToken) < 10 || bytes.Equal(bytes.TrimSpace(envelope.Env.AnthropicAuthToken), []byte(`""`)) || bytes.Equal(bytes.TrimSpace(envelope.Env.AnthropicAuthToken), []byte("null")) {
		return validatedSettings{}, errors.New("Alibaba Claude Code settings require ANTHROPIC_AUTH_TOKEN")
	}
	if strings.TrimSpace(envelope.Env.AnthropicBaseURL) != config.TokenPlanBaseURL || envelope.Env.AnthropicBaseURL != config.TokenPlanBaseURL {
		return validatedSettings{}, errors.New("Alibaba Claude Code settings endpoint does not match configured Token Plan endpoint")
	}
	models := []string{
		envelope.Env.AnthropicModel,
		envelope.Env.AnthropicDefaultHaikuModel,
		envelope.Env.AnthropicDefaultSonnetModel,
		envelope.Env.AnthropicDefaultOpusModel,
		envelope.Env.ClaudeCodeSubagentModel,
	}
	for _, model := range models {
		if !validModelID(strings.TrimSpace(model)) {
			return validatedSettings{}, errors.New("Alibaba Claude Code settings contain invalid model mapping")
		}
	}
	return validatedSettings{BaseURL: envelope.Env.AnthropicBaseURL}, nil
}

func validModelID(value string) bool {
	if value == "" || len(value) > 120 {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' || ch == '[' || ch == ']' {
			continue
		}
		return false
	}
	return true
}
