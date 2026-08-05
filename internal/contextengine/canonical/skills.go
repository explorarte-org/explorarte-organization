package canonical

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	"gopkg.in/yaml.v3"
)

type SkillProvider struct {
	canonicalRoot string
	byID          map[string]contextengine.SkillRecord
}

type capabilityFile struct {
	SkillLifecycle struct {
		States []string `yaml:"states"`
	} `yaml:"skill_lifecycle"`
	Imported []struct {
		ID           string `yaml:"id"`
		OwnerRoleID  string `yaml:"owner_role_id"`
		MemoryDomain string `yaml:"memory_domain"`
		SourceFile   string `yaml:"source_file"`
		SourceSHA256 string `yaml:"source_sha256"`
		Status       string `yaml:"status"`
	} `yaml:"imported_skills"`
}

func NewSkillProvider(canonicalRoot string) (*SkillProvider, error) {
	path := filepath.Join(canonicalRoot, "capability-matrix.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read capability matrix for skills: %w", err)
	}
	node, err := document.ParseStrictYAML(raw)
	if err != nil {
		return nil, err
	}
	var parsed capabilityFile
	if err = node.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode capability skill catalog: %w", err)
	}
	provider := &SkillProvider{canonicalRoot: canonicalRoot, byID: map[string]contextengine.SkillRecord{}}
	for _, item := range parsed.Imported {
		life := normalizeLifecycle(item.Status)
		department, _, ok := strings.Cut(item.OwnerRoleID, "/")
		if !ok || strings.TrimSpace(department) == "" {
			return nil, fmt.Errorf("imported skill %q has invalid owner_role_id %q", item.ID, item.OwnerRoleID)
		}
		provider.byID[item.ID] = contextengine.SkillRecord{
			ID: item.ID, RoleID: item.OwnerRoleID, Department: department, MemoryDomain: item.MemoryDomain,
			Lifecycle: life, Assigned: item.OwnerRoleID != "", Path: sourceFileName(item.SourceFile), Version: "canonical-import-v1", SourceHash: strings.TrimSpace(item.SourceSHA256),
		}
	}
	return provider, nil
}

func normalizeLifecycle(value string) contextengine.SkillLifecycle {
	switch value {
	case "draft", "imported_draft_requires_owner_approval":
		return contextengine.SkillDraft
	case "human_approved":
		return contextengine.SkillHumanApproved
	case "candidate":
		return contextengine.SkillCandidate
	case "active":
		return contextengine.SkillActive
	case "suspended":
		return contextengine.SkillSuspended
	case "retired":
		return contextengine.SkillRetired
	default:
		return contextengine.SkillDraft
	}
}

func (p *SkillProvider) ListActiveForRole(_ context.Context, organizationID, roleID string) ([]contextengine.SkillRecord, error) {
	if organizationID == "" {
		return nil, contextengine.Reject(contextengine.ReasonOrganizationMismatch, organizationID, "organization ID is required")
	}
	values := []contextengine.SkillRecord{}
	for _, skill := range p.byID {
		if skill.RoleID == roleID && skill.Lifecycle == contextengine.SkillActive && skill.Assigned {
			values = append(values, skill)
		}
	}
	return values, nil
}

func (p *SkillProvider) GetActiveForRole(_ context.Context, organizationID, roleID, skillID string) (contextengine.SkillRecord, error) {
	if organizationID == "" {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonOrganizationMismatch, organizationID, "organization ID is required")
	}
	record, exists := p.byID[skillID]
	if !exists {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotFound, skillID, "skill is not in canonical catalog")
	}
	if record.Lifecycle != contextengine.SkillActive {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotActive, skillID, "skill is not active")
	}
	if !record.Assigned || record.RoleID != roleID {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotAssigned, skillID, "skill is not assigned to actor role")
	}
	return record, nil
}

func (p *SkillProvider) ValidateVersion(_ context.Context, expected contextengine.SkillRecord) error {
	current, exists := p.byID[expected.ID]
	if !exists {
		return contextengine.Reject(contextengine.ReasonSkillStateDrift, expected.ID, "skill disappeared from canonical catalog")
	}
	if current.Lifecycle != contextengine.SkillActive || !current.Assigned || current.RoleID != expected.RoleID {
		return contextengine.Reject(contextengine.ReasonSkillStateDrift, expected.ID, "skill is no longer active and assigned")
	}
	if current.SourceHash != expected.SourceHash || current.Version != expected.Version || current.Path != expected.Path {
		return contextengine.Reject(contextengine.ReasonSkillSourceDrift, expected.ID, "skill source version changed")
	}
	return nil
}

func (p *SkillProvider) Record(skillID string) (contextengine.SkillRecord, bool) {
	value, ok := p.byID[skillID]
	return value, ok
}

func validateSkillCatalogNode(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return errors.New("skill catalog root must be a mapping")
	}
	return nil
}

func sourceFileName(value string) string { return strings.TrimSpace(value) }
