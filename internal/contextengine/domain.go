package contextengine

import "time"

type AuthorityTier string

const (
	TierImmutableSafety   AuthorityTier = "immutable_safety"
	TierOwnerDecisions    AuthorityTier = "owner_decisions"
	TierCanonicalPolicies AuthorityTier = "canonical_registry_and_policies"
	TierOrganizationAgent AuthorityTier = "organization_agent"
	TierDepartmentAgent   AuthorityTier = "department_agent"
	TierRoleProfile       AuthorityTier = "role_profile"
	TierApprovedMemory    AuthorityTier = "approved_memory"
	TierApprovedSkill     AuthorityTier = "matched_approved_skills"
	TierProject           AuthorityTier = "project_context"
	TierTask              AuthorityTier = "task_context"
	TierRAGEvidence       AuthorityTier = "rag_evidence"
)

type InstructionClass string

const (
	InstructionImmutableConstraint InstructionClass = "immutable_constraint"
	InstructionOwnerConstraint     InstructionClass = "owner_constraint"
	InstructionCanonicalPolicy     InstructionClass = "canonical_policy"
	InstructionOrganizational      InstructionClass = "organizational_instruction"
	InstructionRole                InstructionClass = "role_instruction"
	InstructionScoped              InstructionClass = "scoped_instruction"
	InstructionProcedure           InstructionClass = "approved_procedure"
	InstructionData                InstructionClass = "data"
)

type TrustClass string

const (
	TrustImmutable     TrustClass = "immutable"
	TrustAuthoritative TrustClass = "authoritative"
	TrustApproved      TrustClass = "approved"
	TrustScoped        TrustClass = "scoped"
	TrustUntrusted     TrustClass = "untrusted"
)

type DataClass string

const (
	DataPublic         DataClass = "public"
	DataOrganizational DataClass = "organizational"
	DataSanitized      DataClass = "sanitized"
	DataSecret         DataClass = "secret"
	DataClinical       DataClass = "clinical"
)

type SourceKind string

const (
	SourceCanonicalDocument SourceKind = "canonical_document"
	SourceOwnerConstraint   SourceKind = "owner_constraint"
	SourceOrganizationAgent SourceKind = "organization_agent"
	SourceDepartmentAgent   SourceKind = "department_agent"
	SourceRoleProfile       SourceKind = "role_profile"
	SourceApprovedMemory    SourceKind = "approved_memory"
	SourceApprovedSkill     SourceKind = "approved_skill"
	SourceProjectContext    SourceKind = "project_context"
	SourceTaskContext       SourceKind = "task_context"
	SourceRAGEvidence       SourceKind = "rag_evidence"
	// SourceWebEvidence is R30.1-4's kind for internal/webevidence.Evidence
	// rendered into a SourceRecord — see assembler.go's hard gate: exactly
	// like SourceApprovedMemory/SourceRAGEvidence, it may never carry
	// anything but InstructionData/TrustUntrusted/MayGrantCapabilities=
	// false. No productive provider wires this kind into Service yet
	// (internal/webevidencefixtures renders it directly for the
	// evaluation harness) — that wiring is future work, not part of this
	// fix.
	SourceWebEvidence SourceKind = "web_evidence"
)

type SnapshotStatus string

const (
	SnapshotReady       SnapshotStatus = "ready"
	SnapshotInvalidated SnapshotStatus = "invalidated"
)

type SkillLifecycle string

const (
	SkillDraft         SkillLifecycle = "draft"
	SkillHumanApproved SkillLifecycle = "human_approved"
	SkillCandidate     SkillLifecycle = "candidate"
	SkillActive        SkillLifecycle = "active"
	SkillSuspended     SkillLifecycle = "suspended"
	SkillRetired       SkillLifecycle = "retired"
)

type BuildRequest struct {
	OrganizationID         string   `json:"organization_id"`
	OrganizationRevisionID int64    `json:"organization_revision_id"`
	ActorRoleID            string   `json:"actor_role_id"`
	Purpose                string   `json:"purpose"`
	ProjectRef             string   `json:"project_ref,omitempty"`
	TaskRef                string   `json:"task_ref,omitempty"`
	RequestedSkillIDs      []string `json:"requested_skill_ids,omitempty"`
	IdempotencyKey         string   `json:"idempotency_key"`
	CorrelationID          string   `json:"correlation_id,omitempty"`
	CausationID            string   `json:"causation_id,omitempty"`
}

type SourceRecord struct {
	Kind                 SourceKind       `json:"kind"`
	Reference            string           `json:"reference"`
	Version              string           `json:"version"`
	AuthorityTier        AuthorityTier    `json:"authority_tier"`
	AuthorityPriority    int              `json:"authority_priority"`
	InstructionClass     InstructionClass `json:"instruction_class"`
	TrustClass           TrustClass       `json:"trust_class"`
	DataClass            DataClass        `json:"data_class"`
	MayGrantCapabilities bool             `json:"may_grant_capabilities"`
	Content              []byte           `json:"content,omitempty"`
	ContentHash          string           `json:"content_hash"`
	Included             bool             `json:"included"`
	OmissionReason       string           `json:"omission_reason,omitempty"`
	Relevance            int              `json:"relevance,omitempty"`
	ProviderPriority     int              `json:"provider_priority,omitempty"`
}

type Segment struct {
	Ordinal              int              `json:"ordinal"`
	RenderOrdinal        int              `json:"render_ordinal"`
	AuthorityPriority    int              `json:"authority_priority"`
	AuthorityTier        AuthorityTier    `json:"authority_tier"`
	SourceKind           SourceKind       `json:"source_kind"`
	SourceReference      string           `json:"source_reference"`
	SourceVersion        string           `json:"source_version"`
	InstructionClass     InstructionClass `json:"instruction_class"`
	TrustClass           TrustClass       `json:"trust_class"`
	DataClass            DataClass        `json:"data_class"`
	MayGrantCapabilities bool             `json:"may_grant_capabilities"`
	Included             bool             `json:"included"`
	OmissionReason       string           `json:"omission_reason,omitempty"`
	ContentHash          string           `json:"content_hash"`
	ByteCount            int              `json:"byte_count"`
	Content              []byte           `json:"content,omitempty"`
}

type Snapshot struct {
	ID                     int64          `json:"id"`
	OrganizationID         string         `json:"organization_id"`
	OrganizationRevisionID int64          `json:"organization_revision_id"`
	ActorRoleID            string         `json:"actor_role_id"`
	Purpose                string         `json:"purpose"`
	ProjectRef             string         `json:"project_ref,omitempty"`
	TaskRef                string         `json:"task_ref,omitempty"`
	IdempotencyKey         string         `json:"idempotency_key,omitempty"`
	Status                 SnapshotStatus `json:"status"`
	Version                int64          `json:"version"`
	RequestHash            string         `json:"request_hash"`
	PrecedenceHash         string         `json:"precedence_hash"`
	CanonicalBundleHash    string         `json:"canonical_bundle_hash"`
	RenderedHash           string         `json:"rendered_hash"`
	SegmentCount           int            `json:"segment_count"`
	IncludedSegmentCount   int            `json:"included_segment_count"`
	OmittedSegmentCount    int            `json:"omitted_segment_count"`
	TotalBytes             int64          `json:"total_bytes"`
	CorrelationID          string         `json:"correlation_id,omitempty"`
	CausationID            string         `json:"causation_id,omitempty"`
	Segments               []Segment      `json:"segments,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	InvalidatedAt          *time.Time     `json:"invalidated_at,omitempty"`
	InvalidationReason     string         `json:"invalidation_reason,omitempty"`
}

type DriftFinding struct {
	ReasonCode string     `json:"reason_code"`
	SourceKind SourceKind `json:"source_kind,omitempty"`
	Reference  string     `json:"reference,omitempty"`
	Expected   string     `json:"expected,omitempty"`
	Actual     string     `json:"actual,omitempty"`
}

type SnapshotValidation struct {
	Valid      bool           `json:"valid"`
	ReasonCode string         `json:"reason_code,omitempty"`
	Drift      []DriftFinding `json:"drift,omitempty"`
}

type Assembly struct {
	Segments             []Segment
	SegmentCount         int
	IncludedSegmentCount int
	OmittedSegmentCount  int
	TotalBytes           int64
}

type BuildResult struct {
	Snapshot Snapshot `json:"snapshot"`
	Reused   bool     `json:"reused"`
}

type ListFilter struct {
	OrganizationID string
	ActorRoleID    string
	Status         SnapshotStatus
	Limit          int
}

type InvalidateCommand struct {
	SnapshotID    int64
	ActorRoleID   string
	Reason        string
	CorrelationID string
	CausationID   string
}

type SourceValidation struct {
	Valid       bool       `json:"valid"`
	Kind        SourceKind `json:"kind"`
	Path        string     `json:"path"`
	ContentHash string     `json:"content_hash,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
}

func RenderRank(tier AuthorityTier) (int, bool) {
	switch tier {
	case TierImmutableSafety:
		return 1, true
	case TierOwnerDecisions:
		return 2, true
	case TierCanonicalPolicies:
		return 3, true
	case TierOrganizationAgent:
		return 4, true
	case TierDepartmentAgent:
		return 5, true
	case TierRoleProfile:
		return 6, true
	case TierApprovedMemory:
		return 7, true
	case TierApprovedSkill:
		return 8, true
	case TierProject:
		return 9, true
	case TierTask:
		return 10, true
	case TierRAGEvidence:
		return 11, true
	default:
		return 0, false
	}
}

func AuthorityPriority(tier AuthorityTier) (int, bool) {
	switch tier {
	case TierImmutableSafety:
		return 0, true
	case TierOwnerDecisions:
		return 1, true
	case TierCanonicalPolicies:
		return 2, true
	case TierOrganizationAgent, TierDepartmentAgent:
		return 3, true
	case TierRoleProfile, TierApprovedSkill:
		return 4, true
	case TierProject, TierTask:
		return 5, true
	case TierApprovedMemory, TierRAGEvidence:
		return 6, true
	default:
		return 0, false
	}
}
