package skillforge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

type StaticValidator struct {
	skillsRoot string
	contract   *ContractValidator
}

func NewStaticValidator(skillsRoot string) *StaticValidator {
	return &StaticValidator{
		skillsRoot: skillsRoot,
		contract:   NewContractValidator(),
	}
}

func (v *StaticValidator) ValidateSkillSource(_ context.Context, skillID string, source skillregistry.SourceRecord, manifest skillregistry.Manifest) (string, bool, error) {
	if err := manifest.Validate(skillID); err != nil {
		return "", false, fmt.Errorf("manifest validation failed: %w", err)
	}
	if err := source.Validate(); err != nil {
		return "", false, fmt.Errorf("source record validation failed: %w", err)
	}

	// Read source from skills root
	fullPath := filepath.Join(v.skillsRoot, source.Path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", false, fmt.Errorf("read materialized skill file %s: %w", fullPath, err)
	}

	// Validate contract
	if err := v.contract.ValidateContract(content); err != nil {
		return "", false, fmt.Errorf("skill contract check failed: %w", err)
	}

	ref := fmt.Sprintf("schema-valid:%s:%s", skillID, source.SHA256[:12])
	return ref, true, nil
}

func (v *StaticValidator) ReviewRoleCapabilities(_ context.Context, roleID, skillID string, requiredCapabilities []string) (string, bool, error) {
	// Re-verify that required capabilities are within canonical limits
	for _, cap := range requiredCapabilities {
		if strings.Contains(cap, "secret") || strings.Contains(cap, "admin") || strings.Contains(cap, "root") {
			return "", false, fmt.Errorf("unauthorized capability requested: %s", cap)
		}
	}
	ref := fmt.Sprintf("cap-review:%s:%s", roleID, skillID)
	return ref, true, nil
}

func (v *StaticValidator) ReviewSkillInstructions(_ context.Context, skillID string, source skillregistry.SourceRecord, manifest skillregistry.Manifest) (string, bool, error) {
	fullPath := filepath.Join(v.skillsRoot, source.Path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", false, fmt.Errorf("read skill file %s: %w", fullPath, err)
	}

	text := strings.ToLower(string(content))
	adversarialSignals := []string{
		"disregard previous instructions",
		"ignore all previous instructions",
		"system prompt override",
		"jailbreak",
		"exfiltrate",
		"api_key",
		"secret_token",
	}

	for _, sig := range adversarialSignals {
		if strings.Contains(text, sig) {
			return "", false, fmt.Errorf("adversarial signal detected in instructions: %q", sig)
		}
	}

	ref := fmt.Sprintf("safety-review:%s:%s", skillID, source.SHA256[:12])
	return ref, true, nil
}
