package skillregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
)

func TestLegacyManifestHashUnchanged(t *testing.T) {
	manifest := Manifest{
		Name:                 "test-skill",
		Description:          "A skill for testing legacy manifest stability across schema changes.",
		Department:           "ingenieria_ia",
		OwnerRoleID:          "ingenieria_ia/lider_arquitectura",
		MemoryDomain:         "ingenieria_ia",
		BaseProtocol:         "none",
		VerifierRef:          "internal/verifier:v1",
		RequiredCapabilities: []string{"cap-b", "cap-a"},
	}

	hash1, err := HashManifest(manifest)
	if err != nil {
		t.Fatalf("HashManifest failed: %v", err)
	}

	// Manifest with empty/omitted v2 fields should produce exact same hash
	manifestV2Empty := manifest
	manifestV2Empty.ManifestSchemaVersion = ""
	manifestV2Empty.EvaluationSuiteRef = ""
	manifestV2Empty.CanaryPolicyRef = ""
	manifestV2Empty.ExecutionProfileRef = ""
	manifestV2Empty.RequiredTools = nil

	hash2, err := HashManifest(manifestV2Empty)
	if err != nil {
		t.Fatalf("HashManifest with empty v2 failed: %v", err)
	}

	if hash1 != hash2 {
		t.Fatalf("manifest hash drifted: %s != %s", hash1, hash2)
	}
}

func TestLegacyCanonicalHashUnchanged(t *testing.T) {
	source := SourceRecord{
		Path:           "test/SKILL.md",
		SHA256:         "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Origin:         OriginInternal,
		LegacyImported: true,
		RecordedBy:     "empresa/human",
		RecordRef:      "rec-1",
	}

	manifestHash := "1111111111111111111111111111111111111111111111111111111111111111"
	hash1, err := HashVersionIdentity("test-skill", "explorarte", 1, manifestHash, source)
	if err != nil {
		t.Fatalf("HashVersionIdentity failed: %v", err)
	}

	// Adding NormalizedSHA256 = "" (omitempty) must produce exact same canonical hash
	sourceWithEmptyNorm := source
	sourceWithEmptyNorm.NormalizedSHA256 = ""
	hash2, err := HashVersionIdentity("test-skill", "explorarte", 1, manifestHash, sourceWithEmptyNorm)
	if err != nil {
		t.Fatalf("HashVersionIdentity with empty norm failed: %v", err)
	}

	if hash1 != hash2 {
		t.Fatalf("canonical version hash drifted: %s != %s", hash1, hash2)
	}
}

func TestRawHashAndNormalizedHashDistinct(t *testing.T) {
	rawBytes := []byte("Title\r\n\r\nLine 1\r\nLine 2\r\n")
	normBytes, err := document.NormalizeText(rawBytes)
	if err != nil {
		t.Fatalf("NormalizeText failed: %v", err)
	}

	rawSum := sha256.Sum256(rawBytes)
	normSum := sha256.Sum256(normBytes)

	rawHex := hex.EncodeToString(rawSum[:])
	normHex := hex.EncodeToString(normSum[:])

	if rawHex == normHex {
		t.Fatalf("expected raw and normalized hashes to be distinct for CRLF input")
	}
}

func TestRuntimeVersionString(t *testing.T) {
	legacyVersion := SkillVersion{
		Version: 1,
		Source: SourceRecord{
			LegacyImported: true,
		},
	}
	if got := RuntimeVersionString(legacyVersion); got != "canonical-import-v1" {
		t.Fatalf("expected canonical-import-v1, got %q", got)
	}

	nativeV1 := SkillVersion{
		Version: 1,
		Source: SourceRecord{
			LegacyImported: false,
		},
	}
	if got := RuntimeVersionString(nativeV1); got != "registry-v1" {
		t.Fatalf("expected registry-v1, got %q", got)
	}

	nativeV42 := SkillVersion{
		Version: 42,
		Source: SourceRecord{
			LegacyImported: false,
		},
	}
	if got := RuntimeVersionString(nativeV42); got != "registry-v42" {
		t.Fatalf("expected registry-v42, got %q", got)
	}
}
