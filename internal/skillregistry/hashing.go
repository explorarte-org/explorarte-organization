package skillregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func HashManifest(value Manifest) (string, error) {
	value.RequiredCapabilities = NormalizeCapabilities(value.RequiredCapabilities)
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("hash skill manifest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func HashVersionIdentity(skillID, organizationID string, version int64, manifestHash string, source SourceRecord) (string, error) {
	body := struct {
		SchemaVersion  string       `json:"schema_version"`
		SkillID        string       `json:"skill_id"`
		OrganizationID string       `json:"organization_id"`
		Version        int64        `json:"version"`
		ManifestHash   string       `json:"manifest_hash"`
		Source         SourceRecord `json:"source"`
	}{"skill-registry.v1", skillID, organizationID, version, manifestHash, source}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("hash skill version: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
