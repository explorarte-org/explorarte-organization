package skillregistry

import "time"

type Lifecycle string

const (
	LifecycleDraft         Lifecycle = "draft"
	LifecycleHumanApproved Lifecycle = "human_approved"
	LifecycleCandidate     Lifecycle = "candidate"
	LifecycleActive        Lifecycle = "active"
	LifecycleSuspended     Lifecycle = "suspended"
	LifecycleRetired       Lifecycle = "retired"
)

func (v Lifecycle) Valid() bool {
	switch v {
	case LifecycleDraft, LifecycleHumanApproved, LifecycleCandidate, LifecycleActive, LifecycleSuspended, LifecycleRetired:
		return true
	default:
		return false
	}
}

type AssignmentStatus string

const (
	AssignmentActive  AssignmentStatus = "active"
	AssignmentRevoked AssignmentStatus = "revoked"
)

func (v AssignmentStatus) Valid() bool { return v == AssignmentActive || v == AssignmentRevoked }

type OriginKind string

const (
	OriginInternal OriginKind = "internal"
	OriginGitHub   OriginKind = "github"
)

func (v OriginKind) Valid() bool { return v == OriginInternal || v == OriginGitHub }

type Skill struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	CreatedByRole  string    `json:"created_by_role"`
	CreatedAt      time.Time `json:"created_at"`
}

type SourceRecord struct {
	Path           string     `json:"path"`
	SHA256         string     `json:"sha256"`
	Origin         OriginKind `json:"origin"`
	OriginRef      string     `json:"origin_ref,omitempty"`
	LegacyImported bool       `json:"legacy_imported,omitempty"`
	RecordedBy     string     `json:"recorded_by"`
	RecordRef      string     `json:"record_ref"`
}

type Manifest struct {
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	Department           string   `json:"department"`
	OwnerRoleID          string   `json:"owner_role_id"`
	MemoryDomain         string   `json:"memory_domain"`
	BaseProtocol         string   `json:"base_protocol"`
	VerifierRef          string   `json:"verifier_ref,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
}

type ApprovalEvidence struct {
	DecisionRef string    `json:"decision_ref"`
	ApprovedBy  string    `json:"approved_by"`
	ApprovedAt  time.Time `json:"approved_at"`
}

type ValidationEvidence struct {
	SchemaValidationRef     string    `json:"schema_validation_ref"`
	CapabilityReviewRef     string    `json:"capability_review_ref"`
	InstructionSafetyRef    string    `json:"instruction_safety_ref"`
	SourceRecordRef         string    `json:"source_record_ref"`
	ValidatedBy             string    `json:"validated_by"`
	ValidatedAt             time.Time `json:"validated_at"`
	CapabilitiesPass        bool      `json:"capabilities_pass"`
	InstructionSafetyPass   bool      `json:"instruction_safety_pass"`
}

type SkillVersion struct {
	ID                 string              `json:"id"`
	SkillID            string              `json:"skill_id"`
	OrganizationID     string              `json:"organization_id"`
	Version            int64               `json:"version"`
	Lifecycle          Lifecycle           `json:"lifecycle"`
	Manifest           Manifest            `json:"manifest"`
	Source             SourceRecord        `json:"source"`
	ContentHash        string              `json:"content_hash"`
	ManifestHash       string              `json:"manifest_hash"`
	CanonicalHash      string              `json:"canonical_hash"`
	OwnerApproval      *ApprovalEvidence   `json:"owner_approval,omitempty"`
	Validation         *ValidationEvidence `json:"validation,omitempty"`
	ActivationApproval *ApprovalEvidence   `json:"activation_approval,omitempty"`
	SupersedesVersion  string              `json:"supersedes_version,omitempty"`
	Revision           int64               `json:"revision"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

type SkillAssignment struct {
	ID                    string           `json:"id"`
	OrganizationID        string           `json:"organization_id"`
	RoleID                string           `json:"role_id"`
	SkillID               string           `json:"skill_id"`
	SkillVersionID        string           `json:"skill_version_id"`
	Status                AssignmentStatus `json:"status"`
	CapabilityReviewRef   string           `json:"capability_review_ref"`
	AssignedBy            string           `json:"assigned_by"`
	AssignmentDecisionRef string           `json:"assignment_decision_ref"`
	Revision              int64            `json:"revision"`
	AssignedAt            time.Time        `json:"assigned_at"`
	UpdatedAt             time.Time        `json:"updated_at"`
	RevokedAt             *time.Time       `json:"revoked_at,omitempty"`
	RevokeReason          string           `json:"revoke_reason,omitempty"`
}
