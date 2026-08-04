package staging

import (
	"context"
	"io"
	"time"
)

type WorkspaceStatus string

const (
	WorkspaceProvisioning   WorkspaceStatus = "provisioning"
	WorkspaceActive         WorkspaceStatus = "active"
	WorkspaceSealed         WorkspaceStatus = "sealed"
	WorkspaceAbandoned      WorkspaceStatus = "abandoned"
	WorkspaceCleanupPending WorkspaceStatus = "cleanup_pending"
	WorkspaceCleaned        WorkspaceStatus = "cleaned"
	WorkspaceFailed         WorkspaceStatus = "failed"
)

func (s WorkspaceStatus) Valid() bool {
	switch s {
	case WorkspaceProvisioning, WorkspaceActive, WorkspaceSealed, WorkspaceAbandoned, WorkspaceCleanupPending, WorkspaceCleaned, WorkspaceFailed:
		return true
	default:
		return false
	}
}

func (s WorkspaceStatus) Terminal() bool {
	return s == WorkspaceCleaned
}

type PromotionStatus string

const (
	PromotionRequested     PromotionStatus = "requested"
	PromotionAwaitingGates PromotionStatus = "awaiting_gates"
	PromotionApproved      PromotionStatus = "approved"
	PromotionApplied       PromotionStatus = "applied"
	PromotionRejected      PromotionStatus = "rejected"
	PromotionConflicted    PromotionStatus = "conflicted"
	PromotionCancelled     PromotionStatus = "cancelled"
	PromotionFailed        PromotionStatus = "failed"
)

func (s PromotionStatus) Valid() bool {
	switch s {
	case PromotionRequested, PromotionAwaitingGates, PromotionApproved, PromotionApplied, PromotionRejected, PromotionConflicted, PromotionCancelled, PromotionFailed:
		return true
	default:
		return false
	}
}

func (s PromotionStatus) Terminal() bool {
	switch s {
	case PromotionApplied, PromotionRejected, PromotionConflicted, PromotionCancelled, PromotionFailed:
		return true
	default:
		return false
	}
}

type CheckStatus string

const (
	CheckPassed CheckStatus = "passed"
	CheckFailed CheckStatus = "failed"
)

type ReviewDecision string

const (
	ReviewApprove ReviewDecision = "approve"
	ReviewReject  ReviewDecision = "reject"
)

type ArtifactKind string

const (
	ArtifactManifest     ArtifactKind = "manifest"
	ArtifactBinaryPatch  ArtifactKind = "binary_patch"
	ArtifactCheckReport  ArtifactKind = "check_report"
	ArtifactReviewReport ArtifactKind = "review_report"
)

type RepositoryConfig struct {
	ID                string   `yaml:"id" json:"id"`
	Path              string   `yaml:"path" json:"-"`
	Enabled           bool     `yaml:"enabled" json:"enabled"`
	AllowedTargetRefs []string `yaml:"allowed_target_refs" json:"allowed_target_refs"`
}

type RepositoryFile struct {
	SchemaVersion int                `yaml:"schema_version"`
	Repositories  []RepositoryConfig `yaml:"repositories"`
}

type RepositoryView struct {
	ID                string   `json:"id"`
	Enabled           bool     `json:"enabled"`
	AllowedTargetRefs []string `json:"allowed_target_refs"`
	ConfigHash        string   `json:"config_hash"`
}

type Workspace struct {
	ID                     int64           `json:"id"`
	OrganizationID         string          `json:"organization_id"`
	OrganizationRevisionID int64           `json:"organization_revision_id"`
	TaskID                 int64           `json:"task_id"`
	AttemptID              int64           `json:"attempt_id"`
	RepositoryID           string          `json:"repository_id"`
	RepositoryConfigHash   string          `json:"repository_config_hash"`
	WorkspaceKey           string          `json:"workspace_key"`
	WorkspaceRef           string          `json:"workspace_ref"`
	BaseCommit             string          `json:"base_commit"`
	TargetRef              string          `json:"target_ref"`
	Status                 WorkspaceStatus `json:"status"`
	HolderID               string          `json:"holder_id"`
	ActorRoleID            string          `json:"actor_role_id"`
	ArtifactRequirementID  *int64          `json:"artifact_requirement_id,omitempty"`
	CandidateCommit        *string         `json:"candidate_commit,omitempty"`
	CandidateTree          *string         `json:"candidate_tree,omitempty"`
	ManifestDigest         *string         `json:"manifest_digest,omitempty"`
	PatchDigest            *string         `json:"patch_digest,omitempty"`
	ChangedFileCount       *int            `json:"changed_file_count,omitempty"`
	Version                int64           `json:"version"`
	StatusReasonCode       *string         `json:"status_reason_code,omitempty"`
	StatusReason           *string         `json:"status_reason,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	SealedAt               *time.Time      `json:"sealed_at,omitempty"`
	AbandonedAt            *time.Time      `json:"abandoned_at,omitempty"`
	CleanedAt              *time.Time      `json:"cleaned_at,omitempty"`
}

type Artifact struct {
	ID         int64     `json:"id"`
	Digest     string    `json:"digest"`
	SizeBytes  int64     `json:"size_bytes"`
	MediaType  string    `json:"media_type"`
	StorageKey string    `json:"storage_key"`
	CreatedAt  time.Time `json:"created_at"`
}

type Check struct {
	ID            int64       `json:"id"`
	WorkspaceID   int64       `json:"workspace_id"`
	TaskID        int64       `json:"task_id"`
	RequirementID int64       `json:"requirement_id"`
	Name          string      `json:"name"`
	Status        CheckStatus `json:"status"`
	Reference     string      `json:"reference"`
	Digest        *string     `json:"digest,omitempty"`
	ActorRoleID   string      `json:"actor_role_id"`
	CreatedAt     time.Time   `json:"created_at"`
}

type Promotion struct {
	ID                 int64           `json:"id"`
	WorkspaceID        int64           `json:"workspace_id"`
	TaskID             int64           `json:"task_id"`
	RepositoryID       string          `json:"repository_id"`
	TargetRef          string          `json:"target_ref"`
	ExpectedBaseCommit string          `json:"expected_base_commit"`
	CandidateCommit    string          `json:"candidate_commit"`
	Status             PromotionStatus `json:"status"`
	RequestedByRoleID  string          `json:"requested_by_role_id"`
	ApprovedByRoleID   *string         `json:"approved_by_role_id,omitempty"`
	StatusReasonCode   *string         `json:"status_reason_code,omitempty"`
	StatusReason       *string         `json:"status_reason,omitempty"`
	Version            int64           `json:"version"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	ApprovedAt         *time.Time      `json:"approved_at,omitempty"`
	AppliedAt          *time.Time      `json:"applied_at,omitempty"`
	RejectedAt         *time.Time      `json:"rejected_at,omitempty"`
	CancelledAt        *time.Time      `json:"cancelled_at,omitempty"`
}

type Review struct {
	ID            int64          `json:"id"`
	PromotionID   int64          `json:"promotion_id"`
	RequirementID int64          `json:"requirement_id"`
	Decision      ReviewDecision `json:"decision"`
	ActorRoleID   string         `json:"actor_role_id"`
	Reason        string         `json:"reason"`
	Reference     string         `json:"reference"`
	Digest        *string        `json:"digest,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

type ChangedFile struct {
	Path    string `json:"path"`
	Change  string `json:"change"`
	OldMode string `json:"old_mode"`
	NewMode string `json:"new_mode"`
	OldBlob string `json:"old_blob"`
	NewBlob string `json:"new_blob"`
}

type Manifest struct {
	SchemaVersion   int           `json:"schema_version"`
	RepositoryID    string        `json:"repository_id"`
	WorkspaceID     int64         `json:"workspace_id"`
	TaskID          int64         `json:"task_id"`
	AttemptID       int64         `json:"attempt_id"`
	BaseCommit      string        `json:"base_commit"`
	CandidateCommit string        `json:"candidate_commit"`
	CandidateTree   string        `json:"candidate_tree"`
	TargetRef       string        `json:"target_ref"`
	ChangedFiles    []ChangedFile `json:"changed_files"`
}

type ArtifactMetadata struct {
	MediaType string
	MaxBytes  int64
}

type StoredArtifact struct {
	Digest     string `json:"digest"`
	SizeBytes  int64  `json:"size_bytes"`
	MediaType  string `json:"media_type"`
	StorageKey string `json:"storage_key"`
}

type CreateWorkspaceCommand struct {
	TaskID                int64
	AttemptID             int64
	RepositoryID          string
	BaseCommit            string
	TargetRef             string
	HolderID              string
	ActorRoleID           string
	ArtifactRequirementID int64
	LeaseToken            string
}

type SealWorkspaceCommand struct {
	WorkspaceID int64
	HolderID    string
	ActorRoleID string
	LeaseToken  string
}

type AbandonWorkspaceCommand struct {
	WorkspaceID int64
	ActorRoleID string
	ReasonCode  string
	Reason      string
}

type CleanupWorkspaceCommand struct {
	WorkspaceID int64
	ActorRoleID string
}

type RecordCheckCommand struct {
	WorkspaceID   int64
	RequirementID int64
	Name          string
	Status        CheckStatus
	Reference     string
	Digest        string
	ActorRoleID   string
}

type RequestPromotionCommand struct {
	WorkspaceID int64
	ActorRoleID string
}

type SubmitReviewCommand struct {
	PromotionID   int64
	RequirementID int64
	Decision      ReviewDecision
	ActorRoleID   string
	Reason        string
	Reference     string
	Digest        string
}

type ApplyPromotionCommand struct {
	PromotionID int64
	ActorRoleID string
}

type CancelPromotionCommand struct {
	PromotionID int64
	ActorRoleID string
	ReasonCode  string
	Reason      string
}

type WorkspaceFilter struct {
	Status WorkspaceStatus
	Limit  int
}

type PromotionFilter struct {
	Status PromotionStatus
	Limit  int
}

type WorkspaceInspection struct {
	Exists       bool
	Clean        bool
	HeadCommit   string
	HeadTree     string
	ChangedFiles []ChangedFile
}

type WorkspaceRef struct {
	ID            int64
	RepositoryID  string
	WorkspaceKey  string
	WorkspacePath string
}

type CreateWorktreeRequest struct {
	Repository RepositoryConfig
	Workspace  WorkspaceRef
	BaseCommit string
}

type SealRequest struct {
	Repository       RepositoryConfig
	Workspace        WorkspaceRef
	WorkspaceID      int64
	TaskID           int64
	AttemptID        int64
	BaseCommit       string
	TargetRef        string
	MaxChangedFiles  int
	MaxArtifactBytes int64
}

type SealedRevision struct {
	CandidateCommit string
	CandidateTree   string
	Manifest        []byte
	Patch           []byte
	ChangedFiles    []ChangedFile
}

type PromotionRefRequest struct {
	Repository         RepositoryConfig
	TargetRef          string
	CandidateCommit    string
	ExpectedBaseCommit string
}

type PromotionRefResult struct {
	Applied bool
	Current string
}

type ReconcileResult struct {
	RecoveredWorkspaces    int `json:"recovered_workspaces"`
	MissingWorkspaces      int `json:"missing_workspaces"`
	QuarantinedDirectories int `json:"quarantined_directories"`
	RecoveredPromotions    int `json:"recovered_promotions"`
	ConflictedPromotions   int `json:"conflicted_promotions"`
	RecoveredCleanup       int `json:"recovered_cleanup"`
}

type RepositoryCatalog interface {
	List(context.Context) []RepositoryView
	Get(context.Context, string) (RepositoryConfig, string, error)
	Validate(context.Context, string) error
}

type CapabilityAuthorizer interface {
	Authorize(context.Context, string, int64, string, string) error
}

type WorkspaceReader interface {
	GetWorkspace(context.Context, int64) (Workspace, error)
	ListWorkspaces(context.Context, WorkspaceFilter) ([]Workspace, error)
	GetPromotion(context.Context, int64) (Promotion, error)
	ListPromotions(context.Context, PromotionFilter) ([]Promotion, error)
}

type WorkspaceService interface {
	CreateWorkspace(context.Context, CreateWorkspaceCommand) (Workspace, error)
	InspectWorkspace(context.Context, int64) (WorkspaceInspection, error)
	SealWorkspace(context.Context, SealWorkspaceCommand) (Workspace, error)
	AbandonWorkspace(context.Context, AbandonWorkspaceCommand) (Workspace, error)
	CleanupWorkspace(context.Context, CleanupWorkspaceCommand) (Workspace, error)
	RecordCheck(context.Context, RecordCheckCommand) (Check, error)
}

type ArtifactStore interface {
	Put(context.Context, io.Reader, ArtifactMetadata) (StoredArtifact, error)
	Stat(context.Context, string) (StoredArtifact, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Verify(context.Context, string) error
}

type GitBackend interface {
	ValidateRepository(context.Context, RepositoryConfig) error
	ResolveCommit(context.Context, RepositoryConfig, string) (string, error)
	CreateWorktree(context.Context, CreateWorktreeRequest) error
	InspectWorktree(context.Context, WorkspaceRef) (WorkspaceInspection, error)
	SealWorktree(context.Context, SealRequest) (SealedRevision, error)
	RemoveWorktree(context.Context, WorkspaceRef) error
	ReadTarget(context.Context, RepositoryConfig, string) (string, error)
	PromoteRef(context.Context, PromotionRefRequest) (PromotionRefResult, error)
	VerifySealedRevision(context.Context, RepositoryConfig, WorkspaceRef, string, string, string) error
}

type PromotionService interface {
	RequestPromotion(context.Context, RequestPromotionCommand) (Promotion, error)
	SubmitReview(context.Context, SubmitReviewCommand) (Promotion, error)
	ApplyPromotion(context.Context, ApplyPromotionCommand) (Promotion, error)
	CancelPromotion(context.Context, CancelPromotionCommand) (Promotion, error)
}

type StagingReconciler interface {
	Reconcile(context.Context, int) (ReconcileResult, error)
}
