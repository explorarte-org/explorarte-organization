package skillregistry

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	skillIDPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	roleIDPattern        = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*/[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	canonicalIDPattern   = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	digestPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	capabilityPattern    = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	githubPinnedPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+@[0-9a-f]{40}$`)
	legacySkillFilePattern = regexp.MustCompile(`^SKILL(?:\([1-9][0-9]*\))?\.md$`)
)

func (s Skill) Validate() error {
	if !skillIDPattern.MatchString(s.ID) {
		return fmt.Errorf("%w: invalid id %q", ErrInvalidSkill, s.ID)
	}
	if !canonicalIDPattern.MatchString(s.OrganizationID) || !roleIDPattern.MatchString(s.CreatedByRole) {
		return fmt.Errorf("%w: invalid organization or creator", ErrInvalidSkill)
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidSkill)
	}
	return nil
}

func (m Manifest) Validate(skillID string) error {
	if m.Name != skillID || !skillIDPattern.MatchString(m.Name) {
		return fmt.Errorf("%w: manifest name must equal skill id", ErrInvalidVersion)
	}
	if n := len(strings.TrimSpace(m.Description)); n < 10 || n > 2000 {
		return fmt.Errorf("%w: description must contain 10 to 2000 bytes", ErrInvalidVersion)
	}
	if !canonicalIDPattern.MatchString(m.Department) || !roleIDPattern.MatchString(m.OwnerRoleID) {
		return fmt.Errorf("%w: invalid department or owner role", ErrInvalidVersion)
	}
	department, _, ok := strings.Cut(m.OwnerRoleID, "/")
	if !ok || department != m.Department {
		return fmt.Errorf("%w: owner role does not belong to manifest department", ErrInvalidVersion)
	}
	if !canonicalIDPattern.MatchString(m.MemoryDomain) {
		return fmt.Errorf("%w: invalid memory domain", ErrInvalidVersion)
	}
	switch m.BaseProtocol {
	case "verificacion_estado", "protocolo_verificacion", "none":
	default:
		return fmt.Errorf("%w: unsupported base protocol", ErrInvalidVersion)
	}
	if len(m.VerifierRef) > 240 || strings.ContainsRune(m.VerifierRef, 0) {
		return fmt.Errorf("%w: invalid verifier ref", ErrInvalidVersion)
	}
	seen := map[string]struct{}{}
	for _, capability := range m.RequiredCapabilities {
		capability = strings.TrimSpace(capability)
		if !capabilityPattern.MatchString(capability) {
			return fmt.Errorf("%w: invalid required capability %q", ErrInvalidVersion, capability)
		}
		if _, exists := seen[capability]; exists {
			return fmt.Errorf("%w: duplicate required capability %q", ErrInvalidVersion, capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func (s SourceRecord) Validate() error {
	path := strings.TrimSpace(s.Path)
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return fmt.Errorf("%w: source path must be relative", ErrInvalidVersion)
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: source path escapes repository", ErrInvalidVersion)
	}
	base := filepath.Base(clean)
	if s.LegacyImported {
		if !legacySkillFilePattern.MatchString(base) {
			return fmt.Errorf("%w: legacy source filename is not recognized", ErrInvalidVersion)
		}
	} else if base != "SKILL.md" {
		return fmt.Errorf("%w: new skill source must point to SKILL.md", ErrInvalidVersion)
	}
	if !digestPattern.MatchString(strings.TrimSpace(s.SHA256)) {
		return fmt.Errorf("%w: source sha256 is invalid", ErrInvalidVersion)
	}
	if !s.Origin.Valid() {
		return fmt.Errorf("%w: invalid origin", ErrInvalidVersion)
	}
	if s.Origin == OriginGitHub && !githubPinnedPattern.MatchString(strings.TrimSpace(s.OriginRef)) {
		return fmt.Errorf("%w: github origin must be pinned as owner/repo@40-char-commit-sha", ErrInvalidVersion)
	}
	if s.Origin == OriginInternal && strings.TrimSpace(s.OriginRef) != "" {
		return fmt.Errorf("%w: internal origin must not carry an external origin ref", ErrInvalidVersion)
	}
	if !roleIDPattern.MatchString(s.RecordedBy) || strings.TrimSpace(s.RecordRef) == "" || len(s.RecordRef) > 240 {
		return fmt.Errorf("%w: invalid source record provenance", ErrInvalidVersion)
	}
	return nil
}

func (v SkillVersion) Validate() error {
	if strings.TrimSpace(v.ID) == "" || !skillIDPattern.MatchString(v.SkillID) || !canonicalIDPattern.MatchString(v.OrganizationID) {
		return fmt.Errorf("%w: invalid identity", ErrInvalidVersion)
	}
	if v.Version <= 0 || !v.Lifecycle.Valid() || v.Revision <= 0 {
		return fmt.Errorf("%w: invalid version, lifecycle, or revision", ErrInvalidVersion)
	}
	if err := v.Manifest.Validate(v.SkillID); err != nil {
		return err
	}
	if err := v.Source.Validate(); err != nil {
		return err
	}
	if v.ContentHash != v.Source.SHA256 {
		return fmt.Errorf("%w: content hash must equal registered source sha256", ErrInvalidVersion)
	}
	for _, value := range []string{v.ContentHash, v.ManifestHash, v.CanonicalHash} {
		if !digestPattern.MatchString(value) {
			return fmt.Errorf("%w: invalid canonical digest", ErrInvalidVersion)
		}
	}
	expectedManifestHash, err := HashManifest(v.Manifest)
	if err != nil {
		return err
	}
	if expectedManifestHash != v.ManifestHash {
		return fmt.Errorf("%w: manifest hash mismatch", ErrSourceDrift)
	}
	expectedCanonicalHash, err := HashVersionIdentity(v.SkillID, v.OrganizationID, v.Version, v.ManifestHash, v.Source)
	if err != nil {
		return err
	}
	if expectedCanonicalHash != v.CanonicalHash {
		return fmt.Errorf("%w: canonical version hash mismatch", ErrSourceDrift)
	}
	if v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) {
		return fmt.Errorf("%w: invalid timestamps", ErrInvalidVersion)
	}
	if v.SupersedesVersion == v.ID && v.SupersedesVersion != "" {
		return fmt.Errorf("%w: version cannot supersede itself", ErrInvalidVersion)
	}
	switch v.Lifecycle {
	case LifecycleDraft:
		if v.OwnerApproval != nil || v.Validation != nil || v.ActivationApproval != nil {
			return fmt.Errorf("%w: draft cannot contain approval or activation evidence", ErrInvalidVersion)
		}
	case LifecycleHumanApproved:
		if err := validateApproval(v.OwnerApproval); err != nil {
			return err
		}
		if v.Validation != nil || v.ActivationApproval != nil {
			return fmt.Errorf("%w: human_approved cannot contain later-stage evidence", ErrInvalidVersion)
		}
	case LifecycleCandidate:
		if err := validateApproval(v.OwnerApproval); err != nil {
			return err
		}
		if err := validateValidation(v.Validation, v.Source.RecordRef); err != nil {
			return err
		}
		if v.ActivationApproval != nil {
			return fmt.Errorf("%w: candidate cannot already be activated", ErrInvalidVersion)
		}
	case LifecycleActive, LifecycleSuspended:
		if err := validateApproval(v.OwnerApproval); err != nil {
			return err
		}
		if err := validateValidation(v.Validation, v.Source.RecordRef); err != nil {
			return err
		}
		if err := validateApproval(v.ActivationApproval); err != nil {
			return err
		}
	case LifecycleRetired:
		// Historical evidence remains immutable; retirement may occur from any state explicitly listed in transitions.go.
	}
	return nil
}

func validateApproval(value *ApprovalEvidence) error {
	if value == nil || strings.TrimSpace(value.DecisionRef) == "" || !roleIDPattern.MatchString(value.ApprovedBy) || value.ApprovedAt.IsZero() {
		return fmt.Errorf("%w: owner approval evidence is incomplete", ErrMissingActivationProof)
	}
	return nil
}

func validateValidation(value *ValidationEvidence, sourceRef string) error {
	if value == nil || strings.TrimSpace(value.SchemaValidationRef) == "" || strings.TrimSpace(value.CapabilityReviewRef) == "" || strings.TrimSpace(value.InstructionSafetyRef) == "" || strings.TrimSpace(value.SourceRecordRef) == "" || !roleIDPattern.MatchString(value.ValidatedBy) || value.ValidatedAt.IsZero() {
		return fmt.Errorf("%w: validation evidence is incomplete", ErrMissingActivationProof)
	}
	if value.SourceRecordRef != sourceRef {
		return fmt.Errorf("%w: validation evidence does not reference the registered source", ErrMissingActivationProof)
	}
	if !value.CapabilitiesPass {
		return ErrCapabilityReviewFailed
	}
	if !value.InstructionSafetyPass {
		return fmt.Errorf("%w: instruction safety review failed", ErrMissingActivationProof)
	}
	return nil
}

func (a SkillAssignment) Validate() error {
	if strings.TrimSpace(a.ID) == "" || !canonicalIDPattern.MatchString(a.OrganizationID) || !roleIDPattern.MatchString(a.RoleID) || !skillIDPattern.MatchString(a.SkillID) || strings.TrimSpace(a.SkillVersionID) == "" {
		return fmt.Errorf("%w: invalid identity", ErrInvalidAssignment)
	}
	if !a.Status.Valid() || a.Revision <= 0 || !roleIDPattern.MatchString(a.AssignedBy) || strings.TrimSpace(a.CapabilityReviewRef) == "" || strings.TrimSpace(a.AssignmentDecisionRef) == "" {
		return fmt.Errorf("%w: invalid assignment governance", ErrInvalidAssignment)
	}
	if a.AssignedAt.IsZero() || a.UpdatedAt.IsZero() || a.UpdatedAt.Before(a.AssignedAt) {
		return fmt.Errorf("%w: invalid timestamps", ErrInvalidAssignment)
	}
	if a.Status == AssignmentActive {
		if a.RevokedAt != nil || strings.TrimSpace(a.RevokeReason) != "" {
			return fmt.Errorf("%w: active assignment cannot contain revocation metadata", ErrInvalidAssignment)
		}
	} else {
		if a.RevokedAt == nil || a.RevokedAt.IsZero() || strings.TrimSpace(a.RevokeReason) == "" {
			return fmt.Errorf("%w: revoked assignment requires timestamp and reason", ErrInvalidAssignment)
		}
	}
	return nil
}

func NormalizeCapabilities(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.TrimSpace(value))
	}
	sort.Strings(out)
	return out
}
