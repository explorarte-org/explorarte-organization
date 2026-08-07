package skillregistry

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type Service struct {
	clock Clock
}

func NewService(clock Clock) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{clock: clock}
}

type CreateDraftCommand struct {
	SkillID           string
	VersionID         string
	OrganizationID    string
	Version           int64
	CreatedByRole     string
	Manifest          Manifest
	Source            SourceRecord
	ContentHash       string
	SupersedesVersion string
}

func (s *Service) CreateDraft(command CreateDraftCommand) (Skill, SkillVersion, error) {
	if s == nil {
		return Skill{}, SkillVersion{}, errors.New("skill registry service is nil")
	}
	now := s.clock.Now().UTC()
	command.Manifest.RequiredCapabilities = NormalizeCapabilities(command.Manifest.RequiredCapabilities)
	manifestHash, err := HashManifest(command.Manifest)
	if err != nil {
		return Skill{}, SkillVersion{}, err
	}
	canonicalHash, err := HashVersionIdentity(command.SkillID, command.OrganizationID, command.Version, manifestHash, command.Source)
	if err != nil {
		return Skill{}, SkillVersion{}, err
	}
	skill := Skill{
		ID:             strings.TrimSpace(command.SkillID),
		OrganizationID: strings.TrimSpace(command.OrganizationID),
		CreatedByRole:  strings.TrimSpace(command.CreatedByRole),
		CreatedAt:      now,
	}
	version := SkillVersion{
		ID:                strings.TrimSpace(command.VersionID),
		SkillID:           strings.TrimSpace(command.SkillID),
		OrganizationID:    strings.TrimSpace(command.OrganizationID),
		Version:           command.Version,
		Lifecycle:         LifecycleDraft,
		Manifest:          command.Manifest,
		Source:            command.Source,
		ContentHash:       strings.TrimSpace(command.ContentHash),
		ManifestHash:      manifestHash,
		CanonicalHash:     canonicalHash,
		SupersedesVersion: strings.TrimSpace(command.SupersedesVersion),
		Revision:          1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := skill.Validate(); err != nil {
		return Skill{}, SkillVersion{}, err
	}
	if err := version.Validate(); err != nil {
		return Skill{}, SkillVersion{}, err
	}
	return skill, version, nil
}

func (s *Service) HumanApprove(version SkillVersion, approval ApprovalEvidence) (SkillVersion, error) {
	return s.transition(version, LifecycleHumanApproved, func(v *SkillVersion) error {
		v.OwnerApproval = &approval
		return validateApproval(v.OwnerApproval)
	})
}

func (s *Service) QualifyCandidate(version SkillVersion, evidence ValidationEvidence) (SkillVersion, error) {
	return s.transition(version, LifecycleCandidate, func(v *SkillVersion) error {
		v.Validation = &evidence
		return validateValidation(v.Validation, v.Source.RecordRef)
	})
}

func (s *Service) Activate(version SkillVersion, approval ApprovalEvidence) (SkillVersion, error) {
	return s.transition(version, LifecycleActive, func(v *SkillVersion) error {
		if err := validateApproval(v.OwnerApproval); err != nil {
			return err
		}
		if err := validateValidation(v.Validation, v.Source.RecordRef); err != nil {
			return err
		}
		v.ActivationApproval = &approval
		return validateApproval(v.ActivationApproval)
	})
}

func (s *Service) Suspend(version SkillVersion) (SkillVersion, error) {
	return s.transition(version, LifecycleSuspended, nil)
}

func (s *Service) Retire(version SkillVersion) (SkillVersion, error) {
	return s.transition(version, LifecycleRetired, nil)
}

func (s *Service) transition(version SkillVersion, to Lifecycle, mutate func(*SkillVersion) error) (SkillVersion, error) {
	if s == nil {
		return SkillVersion{}, errors.New("skill registry service is nil")
	}
	if err := version.Validate(); err != nil {
		return SkillVersion{}, err
	}
	if err := ValidateTransition(version.Lifecycle, to); err != nil {
		return SkillVersion{}, err
	}
	if mutate != nil {
		if err := mutate(&version); err != nil {
			return SkillVersion{}, err
		}
	}
	version.Lifecycle = to
	version.Revision++
	version.UpdatedAt = s.clock.Now().UTC()
	if version.UpdatedAt.Before(version.CreatedAt) {
		return SkillVersion{}, fmt.Errorf("%w: clock moved backwards", ErrInvalidVersion)
	}
	if err := version.Validate(); err != nil {
		return SkillVersion{}, err
	}
	return version, nil
}

type AssignCommand struct {
	AssignmentID          string
	OrganizationID        string
	RoleID                string
	AssignedBy            string
	AssignmentDecisionRef string
	CapabilityReviewRef   string
}

func (s *Service) Assign(version SkillVersion, command AssignCommand) (SkillAssignment, error) {
	if s == nil {
		return SkillAssignment{}, errors.New("skill registry service is nil")
	}
	if err := version.Validate(); err != nil {
		return SkillAssignment{}, err
	}
	if version.Lifecycle != LifecycleActive {
		return SkillAssignment{}, ErrVersionNotActive
	}
	now := s.clock.Now().UTC()
	assignment := SkillAssignment{
		ID:                    strings.TrimSpace(command.AssignmentID),
		OrganizationID:        strings.TrimSpace(command.OrganizationID),
		RoleID:                strings.TrimSpace(command.RoleID),
		SkillID:               version.SkillID,
		SkillVersionID:        version.ID,
		Status:                AssignmentActive,
		CapabilityReviewRef:   strings.TrimSpace(command.CapabilityReviewRef),
		AssignedBy:            strings.TrimSpace(command.AssignedBy),
		AssignmentDecisionRef: strings.TrimSpace(command.AssignmentDecisionRef),
		Revision:              1,
		AssignedAt:            now,
		UpdatedAt:             now,
	}
	if assignment.OrganizationID != version.OrganizationID {
		return SkillAssignment{}, fmt.Errorf("%w: assignment and version organizations differ", ErrInvalidAssignment)
	}
	if err := assignment.Validate(); err != nil {
		return SkillAssignment{}, err
	}
	return assignment, nil
}

func (s *Service) RevokeAssignment(assignment SkillAssignment, reason string) (SkillAssignment, error) {
	if s == nil {
		return SkillAssignment{}, errors.New("skill registry service is nil")
	}
	if err := assignment.Validate(); err != nil {
		return SkillAssignment{}, err
	}
	if assignment.Status != AssignmentActive {
		return SkillAssignment{}, ErrAssignmentNotActive
	}
	now := s.clock.Now().UTC()
	if strings.TrimSpace(reason) == "" || len(strings.TrimSpace(reason)) > 240 {
		return SkillAssignment{}, fmt.Errorf("%w: revoke reason is required", ErrInvalidAssignment)
	}
	assignment.Status = AssignmentRevoked
	assignment.Revision++
	assignment.UpdatedAt = now
	assignment.RevokedAt = &now
	assignment.RevokeReason = strings.TrimSpace(reason)
	if err := assignment.Validate(); err != nil {
		return SkillAssignment{}, err
	}
	return assignment, nil
}
