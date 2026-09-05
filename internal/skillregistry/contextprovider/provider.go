package contextprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

// Provider implements contextengine.SkillProvider on top of skillregistry.Repository.
// It enforces fail-closed semantics: any repository or unexpected error is returned directly,
// preventing silent fallback or swallowing of operational failures.
type Provider struct {
	repo           skillregistry.Repository
	organizationID string
}

func New(repo skillregistry.Repository, organizationID string) (*Provider, error) {
	if repo == nil {
		return nil, errors.New("skill registry repository is required")
	}
	if strings.TrimSpace(organizationID) == "" {
		return nil, errors.New("organization ID is required")
	}
	return &Provider{
		repo:           repo,
		organizationID: strings.TrimSpace(organizationID),
	}, nil
}

func (p *Provider) ListActiveForRole(ctx context.Context, organizationID, roleID string) ([]contextengine.SkillRecord, error) {
	if organizationID == "" || organizationID != p.organizationID {
		return nil, contextengine.Reject(contextengine.ReasonOrganizationMismatch, organizationID, "organization ID mismatch")
	}
	if strings.TrimSpace(roleID) == "" {
		return nil, contextengine.Reject(contextengine.ReasonRoleRetired, roleID, "role ID is required")
	}

	assignments, err := p.repo.ListActiveAssignmentsForRole(ctx, organizationID, roleID)
	if err != nil {
		// FAIL CLOSED: do NOT convert to ErrSourceProviderUnavailable!
		return nil, fmt.Errorf("list active assignments for role %s: %w", roleID, err)
	}

	records := make([]contextengine.SkillRecord, 0, len(assignments))
	for _, assignment := range assignments {
		if assignment.Status != skillregistry.AssignmentActive || assignment.RoleID != roleID {
			continue
		}

		version, err := p.repo.GetVersion(ctx, organizationID, assignment.SkillVersionID)
		if err != nil {
			if errors.Is(err, skillregistry.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("get assigned skill version %s: %w", assignment.SkillVersionID, err)
		}

		// A skill is ONLY runtime-visible if:
		// 1. Version Lifecycle is active
		// 2. Assignment is active
		// 3. Assignment version matches version ID
		// 4. Role matches
		// 5. Organization matches
		if version.Lifecycle != skillregistry.LifecycleActive {
			continue
		}
		if version.SkillID != assignment.SkillID || version.OrganizationID != organizationID {
			continue
		}

		sourceHash := version.Source.NormalizedSHA256
		if sourceHash == "" && version.Source.LegacyImported {
			sourceHash = version.Source.SHA256
		}

		department := version.Manifest.Department
		if department == "" {
			dep, _, _ := strings.Cut(roleID, "/")
			department = dep
		}

		records = append(records, contextengine.SkillRecord{
			ID:           version.SkillID,
			RoleID:       roleID,
			Department:   department,
			MemoryDomain: version.Manifest.MemoryDomain,
			Lifecycle:    contextengine.SkillActive,
			Assigned:     true,
			Path:         version.Source.Path,
			Version:      skillregistry.RuntimeVersionString(version),
			SourceHash:   sourceHash,
		})
	}

	return records, nil
}

func (p *Provider) GetActiveForRole(ctx context.Context, organizationID, roleID, skillID string) (contextengine.SkillRecord, error) {
	if organizationID == "" || organizationID != p.organizationID {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonOrganizationMismatch, organizationID, "organization ID mismatch")
	}
	if strings.TrimSpace(roleID) == "" {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonRoleRetired, roleID, "role ID is required")
	}
	if strings.TrimSpace(skillID) == "" {
		return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotFound, skillID, "skill ID is required")
	}

	active, err := p.ListActiveForRole(ctx, organizationID, roleID)
	if err != nil {
		return contextengine.SkillRecord{}, err
	}

	for _, rec := range active {
		if rec.ID == skillID {
			return rec, nil
		}
	}

	// Determine specific rejection reason:
	skill, err := p.repo.GetSkill(ctx, organizationID, skillID)
	if err != nil {
		if errors.Is(err, skillregistry.ErrNotFound) {
			return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotFound, skillID, "skill is not registered")
		}
		return contextengine.SkillRecord{}, fmt.Errorf("check skill existence %s: %w", skillID, err)
	}
	_ = skill

	versions, err := p.repo.ListVersions(ctx, organizationID, skillID)
	if err != nil && !errors.Is(err, skillregistry.ErrNotFound) {
		return contextengine.SkillRecord{}, fmt.Errorf("list versions for skill %s: %w", skillID, err)
	}
	for _, v := range versions {
		if v.Lifecycle != skillregistry.LifecycleActive {
			return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotActive, skillID, fmt.Sprintf("skill version %s is %s, not active", v.ID, v.Lifecycle))
		}
	}

	return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotAssigned, skillID, "skill is not assigned to actor role")
}

func (p *Provider) ValidateVersion(ctx context.Context, expected contextengine.SkillRecord) error {
	current, err := p.GetActiveForRole(ctx, p.organizationID, expected.RoleID, expected.ID)
	if err != nil {
		return contextengine.Reject(contextengine.ReasonSkillStateDrift, expected.ID, fmt.Sprintf("skill validation failed: %v", err))
	}
	if current.Lifecycle != contextengine.SkillActive || !current.Assigned || current.RoleID != expected.RoleID {
		return contextengine.Reject(contextengine.ReasonSkillStateDrift, expected.ID, "skill is no longer active and assigned")
	}
	if current.SourceHash != expected.SourceHash || current.Version != expected.Version || current.Path != expected.Path {
		return contextengine.Reject(contextengine.ReasonSkillSourceDrift, expected.ID, fmt.Sprintf("skill source version changed: expected hash %s version %s path %s, got hash %s version %s path %s",
			expected.SourceHash, expected.Version, expected.Path, current.SourceHash, current.Version, current.Path))
	}
	return nil
}
