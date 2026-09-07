package contextprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

// PinnedCandidateSkillProvider is an ISOLATED provider strictly constructed
// by evaluation harnesses. It exposes exactly one candidate version for one allowed role.
// It is NOT available in production bootstrap and cannot be selected by arbitrary
// context BuildRequests or ExecutionPurpose overrides.
type PinnedCandidateSkillProvider struct {
	Baseline      contextengine.SkillProvider
	Candidate     skillregistry.SkillVersion
	AllowedRoleID string
}

func NewPinnedCandidateSkillProvider(baseline contextengine.SkillProvider, candidate skillregistry.SkillVersion, allowedRoleID string) (*PinnedCandidateSkillProvider, error) {
	if candidate.ID == "" || candidate.SkillID == "" {
		return nil, fmt.Errorf("candidate skill version is invalid")
	}
	if strings.TrimSpace(allowedRoleID) == "" {
		return nil, fmt.Errorf("allowed role ID is required")
	}
	return &PinnedCandidateSkillProvider{
		Baseline:      baseline,
		Candidate:     candidate,
		AllowedRoleID: strings.TrimSpace(allowedRoleID),
	}, nil
}

func (p *PinnedCandidateSkillProvider) ListActiveForRole(ctx context.Context, organizationID, roleID string) ([]contextengine.SkillRecord, error) {
	var records []contextengine.SkillRecord
	if p.Baseline != nil {
		baseRecords, err := p.Baseline.ListActiveForRole(ctx, organizationID, roleID)
		if err != nil {
			return nil, err
		}
		records = append(records, baseRecords...)
	}

	// If role matches the allowed role, include the pinned candidate
	if roleID == p.AllowedRoleID {
		candidateRec := p.candidateToRecord()
		// If already in list from baseline, replace with candidate
		replaced := false
		for i, r := range records {
			if r.ID == candidateRec.ID {
				records[i] = candidateRec
				replaced = true
				break
			}
		}
		if !replaced {
			records = append(records, candidateRec)
		}
	}

	return records, nil
}

func (p *PinnedCandidateSkillProvider) GetActiveForRole(ctx context.Context, organizationID, roleID, skillID string) (contextengine.SkillRecord, error) {
	if roleID == p.AllowedRoleID && skillID == p.Candidate.SkillID {
		return p.candidateToRecord(), nil
	}
	if p.Baseline != nil {
		return p.Baseline.GetActiveForRole(ctx, organizationID, roleID, skillID)
	}
	return contextengine.SkillRecord{}, contextengine.Reject(contextengine.ReasonSkillNotFound, skillID, "candidate provider cannot resolve skill")
}

func (p *PinnedCandidateSkillProvider) ValidateVersion(ctx context.Context, expected contextengine.SkillRecord) error {
	if expected.RoleID == p.AllowedRoleID && expected.ID == p.Candidate.SkillID {
		rec := p.candidateToRecord()
		if rec.SourceHash != expected.SourceHash || rec.Version != expected.Version || rec.Path != expected.Path {
			return contextengine.Reject(contextengine.ReasonSkillSourceDrift, expected.ID, "pinned candidate source drift")
		}
		return nil
	}
	if p.Baseline != nil {
		return p.Baseline.ValidateVersion(ctx, expected)
	}
	return contextengine.Reject(contextengine.ReasonSkillNotFound, expected.ID, "skill not found in baseline")
}

func (p *PinnedCandidateSkillProvider) candidateToRecord() contextengine.SkillRecord {
	sourceHash := p.Candidate.Source.NormalizedSHA256
	if sourceHash == "" {
		sourceHash = p.Candidate.Source.SHA256
	}
	department := p.Candidate.Manifest.Department
	if department == "" {
		dep, _, _ := strings.Cut(p.AllowedRoleID, "/")
		department = dep
	}

	return contextengine.SkillRecord{
		ID:           p.Candidate.SkillID,
		RoleID:       p.AllowedRoleID,
		Department:   department,
		MemoryDomain: p.Candidate.Manifest.MemoryDomain,
		Lifecycle:    contextengine.SkillCandidate,
		Assigned:     true,
		Path:         p.Candidate.Source.Path,
		Version:      skillregistry.RuntimeVersionString(p.Candidate),
		SourceHash:   sourceHash,
	}
}
