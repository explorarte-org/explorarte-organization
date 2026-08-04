package authorization

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"gopkg.in/yaml.v3"
)

var (
	ErrCapabilityDenied       = staging.ErrCapabilityDenied
	ErrUnknownCapability      = errors.New("unknown capability")
	ErrUnknownAuthorityClass  = errors.New("unknown authority class")
	ErrPolicyRevisionMismatch = staging.ErrPolicyRevisionMismatch
)

type CapabilityAuthorizer interface {
	Authorize(context.Context, string, int64, string, string) error
}

type Matrix struct {
	SchemaVersion  string              `yaml:"schema_version"`
	DocumentStatus string              `yaml:"document_status"`
	DefaultPolicy  string              `yaml:"default_policy"`
	Capabilities   []Capability        `yaml:"capabilities"`
	Grants         map[string][]string `yaml:"grants"`
	HardDenies     map[string][]string `yaml:"hard_denies"`
	SkillLifecycle map[string]any      `yaml:"skill_lifecycle,omitempty"`
	ImportedSkills []map[string]any    `yaml:"imported_skills,omitempty"`
}

type Capability struct {
	ID       string `yaml:"id"`
	Risk     string `yaml:"risk"`
	Approval string `yaml:"approval,omitempty"`
}

type Authorizer struct {
	reader       registry.Reader
	organization string
	matrix       Matrix
	matrixHash   string
	known        map[string]struct{}
	authorities  map[string]struct{}
}

func New(reader registry.Reader, organizationID, canonicalDir string) (*Authorizer, error) {
	if reader == nil {
		return nil, errors.New("capability authorizer requires registry reader")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("capability authorizer requires organization ID")
	}
	body, err := os.ReadFile(strings.TrimRight(canonicalDir, "/") + "/capability-matrix.yaml")
	if err != nil {
		return nil, fmt.Errorf("read capability matrix: %w", err)
	}
	var matrix Matrix
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&matrix); err != nil {
		return nil, fmt.Errorf("parse capability matrix: %w", err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		return nil, fmt.Errorf("create canonical loader: %w", err)
	}
	snapshot, _, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("load canonical documents: %w", err)
	}
	var matrixHash string
	for _, document := range snapshot.Documents {
		if document.Path == "capability-matrix.yaml" {
			matrixHash = document.SemanticHash
			break
		}
	}
	if matrixHash == "" {
		return nil, errors.New("canonical capability matrix hash is missing")
	}
	return newAuthorizer(reader, organizationID, matrix, matrixHash)
}

func newAuthorizer(reader registry.Reader, organizationID string, matrix Matrix, matrixHash string) (*Authorizer, error) {
	if matrix.DefaultPolicy != "deny" {
		return nil, errors.New("capability matrix must use default deny")
	}
	known := make(map[string]struct{}, len(matrix.Capabilities))
	for _, capability := range matrix.Capabilities {
		id := strings.TrimSpace(capability.ID)
		if id == "" {
			return nil, errors.New("capability matrix contains empty capability")
		}
		if _, exists := known[id]; exists {
			return nil, fmt.Errorf("duplicate capability %q", id)
		}
		known[id] = struct{}{}
	}
	authorities := make(map[string]struct{}, len(matrix.Grants))
	for authority, grants := range matrix.Grants {
		authority = strings.TrimSpace(authority)
		if authority == "" {
			return nil, errors.New("capability matrix contains empty authority class")
		}
		authorities[authority] = struct{}{}
		for _, grant := range grants {
			if grant != "*" {
				if _, ok := known[grant]; !ok {
					return nil, fmt.Errorf("authority %q grants unknown capability %q", authority, grant)
				}
			}
		}
	}
	for authority, denied := range matrix.HardDenies {
		if authority != "*" {
			if _, ok := authorities[authority]; !ok {
				return nil, fmt.Errorf("hard deny references unknown authority %q", authority)
			}
		}
		for _, capability := range denied {
			if _, ok := known[capability]; !ok {
				return nil, fmt.Errorf("hard deny references unknown capability %q", capability)
			}
		}
	}
	return &Authorizer{reader: reader, organization: organizationID, matrix: matrix, matrixHash: matrixHash, known: known, authorities: authorities}, nil
}

func (a *Authorizer) Authorize(ctx context.Context, organizationID string, revisionID int64, roleID, capability string) error {
	if a == nil || a.reader == nil {
		return fmt.Errorf("%w: authorizer unavailable", ErrCapabilityDenied)
	}
	if strings.TrimSpace(organizationID) == "" || organizationID != a.organization || revisionID <= 0 {
		return fmt.Errorf("%w: invalid organization or revision", ErrCapabilityDenied)
	}
	capability = strings.TrimSpace(capability)
	if _, ok := a.known[capability]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownCapability, capability)
	}
	revision, err := a.reader.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return fmt.Errorf("read organization revision: %w", err)
	}
	if revision == nil || revision.ID != revisionID || revision.DocumentHashes["capability-matrix.yaml"] != a.matrixHash {
		return ErrPolicyRevisionMismatch
	}
	role, err := a.reader.GetRole(ctx, organizationID, strings.TrimSpace(roleID))
	if err != nil {
		return fmt.Errorf("%w: role lookup failed: %v", ErrCapabilityDenied, err)
	}
	if !role.Enabled || role.RetiredAt != nil {
		return fmt.Errorf("%w: role is not enabled", ErrCapabilityDenied)
	}
	authority := strings.TrimSpace(role.AuthorityClass)
	if !role.Executable && authority != "owner" {
		return fmt.Errorf("%w: non-owner role is not executable", ErrCapabilityDenied)
	}
	if _, ok := a.authorities[authority]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAuthorityClass, authority)
	}
	if contains(a.matrix.HardDenies["*"], capability) || contains(a.matrix.HardDenies[authority], capability) {
		return fmt.Errorf("%w: hard deny for %s", ErrCapabilityDenied, capability)
	}
	grants := a.matrix.Grants[authority]
	if contains(grants, "*") || contains(grants, capability) {
		return nil
	}
	return fmt.Errorf("%w: %s lacks %s", ErrCapabilityDenied, roleID, capability)
}

func (a *Authorizer) MatrixHash() string { return a.matrixHash }

func (a *Authorizer) KnownCapabilities() []string {
	result := make([]string, 0, len(a.known))
	for capability := range a.known {
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
