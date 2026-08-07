package shadowverifier

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CapabilityFact mirrors one capability-matrix.yaml capabilities entry. It is
// the shadow's own type — internal/authorization's Capability is deliberately
// not reused, since the shadow's re-derivation must stay independent of the
// engine it verifies.
type CapabilityFact struct {
	ID       string `yaml:"id" json:"id"`
	Risk     string `yaml:"risk" json:"risk"`
	Approval string `yaml:"approval,omitempty" json:"approval,omitempty"`
}

// matrixSkillLifecycle and matrixImportedSkill exactly mirror
// internal/organization/registry's unexported skillLifecycleDefinition and
// importedSkillDefinition (field names, order and json tags) — not because
// the shadow needs their content, but because MatrixIndex.Hash must be
// computed the same way registry.hashDocuments computes
// DocumentHashes["capability-matrix.yaml"] (sha256 of the canonical
// json.Marshal of the parsed document, not the raw file bytes) for the two
// to ever agree. A map[string]any here would marshal with alphabetically
// sorted keys instead of the struct's declared field order and silently
// desync the hash forever.
type matrixSkillLifecycle struct {
	States             []string `yaml:"states" json:"states"`
	ActivationRequires []string `yaml:"activation_requires" json:"activation_requires"`
}

type matrixImportedSkill struct {
	ID           string  `yaml:"id" json:"id"`
	OwnerRoleID  string  `yaml:"owner_role_id" json:"owner_role_id"`
	MemoryDomain string  `yaml:"memory_domain" json:"memory_domain"`
	Origin       string  `yaml:"origin" json:"origin"`
	BaseProtocol string  `yaml:"base_protocol" json:"base_protocol"`
	Verifier     *string `yaml:"verifier" json:"verifier"`
	Status       string  `yaml:"status" json:"status"`
	SourceFile   string  `yaml:"source_file" json:"source_file"`
	SourceSHA256 string  `yaml:"source_sha256" json:"source_sha256"`
	Description  string  `yaml:"description" json:"description"`
}

// matrixFile mirrors the capability-matrix.yaml document shape, field-for-field
// identical to registry's own capabilityMatrixDocument (see the Hash doc
// comment on MatrixIndex for why that identity matters).
type matrixFile struct {
	SchemaVersion  string                `yaml:"schema_version" json:"schema_version"`
	DocumentStatus string                `yaml:"document_status" json:"document_status"`
	DefaultPolicy  string                `yaml:"default_policy" json:"default_policy"`
	Capabilities   []CapabilityFact      `yaml:"capabilities" json:"capabilities"`
	Grants         map[string][]string   `yaml:"grants" json:"grants"`
	HardDenies     map[string][]string   `yaml:"hard_denies" json:"hard_denies"`
	SkillLifecycle matrixSkillLifecycle  `yaml:"skill_lifecycle" json:"skill_lifecycle"`
	ImportedSkills []matrixImportedSkill `yaml:"imported_skills" json:"imported_skills"`
}

// MatrixIndex is the validated, indexed form of capability-matrix.yaml used by
// the shadow capability derivation. Field names mirror the canonical document;
// the indexing mirrors what internal/authorization's newAuthorizer builds,
// re-derived independently.
type MatrixIndex struct {
	// Hash is sha256(json.Marshal(parsed matrixFile)) — deliberately NOT a
	// hash of the raw file bytes. It must equal registry's own
	// DocumentHashes["capability-matrix.yaml"] (registry.hashDocuments hashes
	// json.Marshal of its own parsed struct, never the raw file) for the
	// policy-drift comparison in checkCapabilityFacts to mean anything; two
	// different hash algorithms over "the same" file would always disagree
	// and every capability check would silently report uncomparable forever.
	Hash string
	// DefaultPolicy must be "deny"; loading fails otherwise.
	DefaultPolicy string
	Capabilities  map[string]CapabilityFact
	Grants        map[string][]string
	HardDenies    map[string][]string
	Authorities   map[string]struct{}
}

// LoadMatrix reads and validates capability-matrix.yaml from the canonical
// directory with the shadow's own parser. It fails closed on any structural
// problem, mirroring the real engine's strictness: a shadow that cannot load
// the policy must compare nothing, not guess.
func LoadMatrix(canonicalDir string) (MatrixIndex, error) {
	path := strings.TrimRight(canonicalDir, "/") + "/capability-matrix.yaml"
	body, err := os.ReadFile(path)
	if err != nil {
		return MatrixIndex{}, fmt.Errorf("%w: %v", ErrMatrixUnavailable, err)
	}
	var file matrixFile
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return MatrixIndex{}, fmt.Errorf("%w: parse capability matrix: %v", ErrMatrixInvalid, err)
	}
	if file.DefaultPolicy != "deny" {
		return MatrixIndex{}, fmt.Errorf("%w: capability matrix must use default deny", ErrMatrixInvalid)
	}
	index := MatrixIndex{
		DefaultPolicy: file.DefaultPolicy,
		Capabilities:  make(map[string]CapabilityFact, len(file.Capabilities)),
		Grants:        file.Grants,
		HardDenies:    file.HardDenies,
		Authorities:   make(map[string]struct{}, len(file.Grants)),
	}
	for _, capability := range file.Capabilities {
		capability.ID = strings.TrimSpace(capability.ID)
		capability.Risk = strings.TrimSpace(capability.Risk)
		capability.Approval = strings.TrimSpace(capability.Approval)
		if capability.ID == "" || capability.Risk == "" {
			return MatrixIndex{}, fmt.Errorf("%w: capability matrix contains incomplete capability", ErrMatrixInvalid)
		}
		if _, exists := index.Capabilities[capability.ID]; exists {
			return MatrixIndex{}, fmt.Errorf("%w: duplicate capability %q", ErrMatrixInvalid, capability.ID)
		}
		switch capability.Approval {
		case "", "owner", "policy_or_human", "owner_or_cell_policy":
		default:
			return MatrixIndex{}, fmt.Errorf("%w: capability %q uses unsupported approval mode %q", ErrMatrixInvalid, capability.ID, capability.Approval)
		}
		index.Capabilities[capability.ID] = capability
	}
	for authority, grants := range file.Grants {
		authority = strings.TrimSpace(authority)
		if authority == "" {
			return MatrixIndex{}, fmt.Errorf("%w: capability matrix contains empty authority class", ErrMatrixInvalid)
		}
		index.Authorities[authority] = struct{}{}
		for _, grant := range grants {
			if grant != "*" {
				if _, ok := index.Capabilities[grant]; !ok {
					return MatrixIndex{}, fmt.Errorf("%w: authority %q grants unknown capability %q", ErrMatrixInvalid, authority, grant)
				}
			}
		}
	}
	for authority, denied := range file.HardDenies {
		if authority != "*" {
			if _, ok := index.Authorities[authority]; !ok {
				return MatrixIndex{}, fmt.Errorf("%w: hard deny references unknown authority %q", ErrMatrixInvalid, authority)
			}
		}
		for _, capability := range denied {
			if _, ok := index.Capabilities[capability]; !ok {
				return MatrixIndex{}, fmt.Errorf("%w: hard deny references unknown capability %q", ErrMatrixInvalid, capability)
			}
		}
	}
	canonical, err := json.Marshal(file)
	if err != nil {
		return MatrixIndex{}, fmt.Errorf("%w: marshal parsed capability matrix: %v", ErrMatrixInvalid, err)
	}
	sum := sha256.Sum256(canonical)
	index.Hash = hex.EncodeToString(sum[:])
	return index, nil
}

// CapabilityIDs returns the matrix's capability IDs in sorted order so
// exhaustive enumeration is deterministic.
func (m MatrixIndex) CapabilityIDs() []string {
	ids := make([]string, 0, len(m.Capabilities))
	for id := range m.Capabilities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
