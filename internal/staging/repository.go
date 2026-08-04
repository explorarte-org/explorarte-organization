package staging

import (
	"context"
	"time"
)

type PreparedWorkspace struct {
	OrganizationID         string
	OrganizationRevisionID int64
	TaskID                 int64
	AttemptID              int64
	RepositoryID           string
	RepositoryConfigHash   string
	WorkspaceKey           string
	BaseCommit             string
	TargetRef              string
	HolderID               string
	ActorRoleID            string
	ArtifactRequirementID  int64
}

type SealPersistenceCommand struct {
	WorkspaceID      int64
	ExpectedVersion  int64
	CandidateCommit  string
	CandidateTree    string
	Manifest         StoredArtifact
	Patch            StoredArtifact
	ChangedFileCount int
	ActorRoleID      string
}

type PromotionApplyContext struct {
	Promotion         Promotion
	Workspace         Workspace
	ManifestReference string
	PatchReference    string
}

type ReconcileWorkspace struct {
	Workspace     Workspace
	WorkspacePath string
}

type Persistence interface {
	WorkspaceReader
	CreateProvisioning(context.Context, PreparedWorkspace) (Workspace, error)
	ActivateWorkspace(context.Context, int64, int64, string, string) (Workspace, error)
	FailWorkspace(context.Context, int64, string, string, string) (Workspace, error)
	SealWorkspace(context.Context, SealPersistenceCommand) (Workspace, error)
	AbandonWorkspace(context.Context, AbandonWorkspaceCommand) (Workspace, error)
	BeginCleanup(context.Context, int64, string) (Workspace, error)
	CompleteCleanup(context.Context, int64, string) (Workspace, error)
	RecordCheck(context.Context, RecordCheckCommand) (Check, error)
	RequestPromotion(context.Context, RequestPromotionCommand) (Promotion, error)
	SubmitReview(context.Context, SubmitReviewCommand, bool) (Promotion, error)
	GetPromotionApplyContext(context.Context, int64) (PromotionApplyContext, error)
	MarkPromotionApplied(context.Context, int64, string) (Promotion, error)
	MarkPromotionConflicted(context.Context, int64, string, string) (Promotion, error)
	MarkPromotionFailed(context.Context, int64, string, string) (Promotion, error)
	CancelPromotion(context.Context, CancelPromotionCommand) (Promotion, error)
	ListStaleWorkspaces(context.Context, time.Duration, int) ([]Workspace, error)
	ListWorkspaceKeys(context.Context) (map[string]struct{}, error)
	ListRecoverablePromotions(context.Context, int) ([]PromotionApplyContext, error)
	ListCleanupPending(context.Context, int) ([]Workspace, error)
}
