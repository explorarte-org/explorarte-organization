package staging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type ServiceConfig struct {
	OrganizationID   string
	WorkspaceRoot    string
	QuarantineRoot   string
	MaxArtifactBytes int64
	MaxChangedFiles  int
	StaleAfter       time.Duration
}

type Service struct {
	cfg         ServiceConfig
	persistence Persistence
	leases      tasks.ExecutionLeaseVerifier
	catalog     RepositoryCatalog
	authorizer  CapabilityAuthorizer
	registry    registry.Reader
	artifacts   ArtifactStore
	git         GitBackend
}

func NewService(cfg ServiceConfig, persistence Persistence, leases tasks.ExecutionLeaseVerifier, catalog RepositoryCatalog, authorizer CapabilityAuthorizer, reader registry.Reader, artifacts ArtifactStore, git GitBackend) (*Service, error) {
	if strings.TrimSpace(cfg.OrganizationID) == "" || cfg.MaxArtifactBytes <= 0 || cfg.MaxChangedFiles <= 0 || cfg.StaleAfter <= 0 {
		return nil, errors.New("invalid staging service configuration")
	}
	if persistence == nil || leases == nil || catalog == nil || authorizer == nil || reader == nil || artifacts == nil || git == nil {
		return nil, errors.New("staging service dependencies cannot be nil")
	}
	workspaceRoot, err := CanonicalRoot(cfg.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	quarantineRoot, err := CanonicalRoot(cfg.QuarantineRoot)
	if err != nil {
		return nil, err
	}
	cfg.WorkspaceRoot = workspaceRoot
	cfg.QuarantineRoot = quarantineRoot
	return &Service{cfg: cfg, persistence: persistence, leases: leases, catalog: catalog, authorizer: authorizer, registry: reader, artifacts: artifacts, git: git}, nil
}

func PrepareRoots(paths ...string) error {
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%w: staging root must be absolute", ErrInvalidInput)
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
	}
	return ValidateSeparateRoots(paths...)
}

func (s *Service) CreateWorkspace(ctx context.Context, command CreateWorkspaceCommand) (Workspace, error) {
	if command.TaskID <= 0 || command.AttemptID <= 0 || command.ArtifactRequirementID <= 0 {
		return Workspace{}, ErrInvalidInput
	}
	for _, value := range []string{command.RepositoryID, command.BaseCommit, command.TargetRef, command.HolderID, command.ActorRoleID} {
		if strings.TrimSpace(value) == "" {
			return Workspace{}, ErrInvalidInput
		}
	}
	if strings.TrimSpace(command.LeaseToken) == "" {
		return Workspace{}, ErrLeaseRequired
	}
	if err := ValidateRepositoryID(command.RepositoryID); err != nil {
		return Workspace{}, err
	}
	if err := ValidateCommit(command.BaseCommit); err != nil {
		return Workspace{}, err
	}
	if err := ValidateTargetRef(command.TargetRef); err != nil {
		return Workspace{}, err
	}
	lease, err := s.leases.VerifyActiveExecutionLease(ctx, tasks.VerifyExecutionLeaseCommand{TaskID: command.TaskID, AttemptID: command.AttemptID, HolderID: command.HolderID, LeaseToken: command.LeaseToken})
	command.LeaseToken = ""
	if err != nil {
		return Workspace{}, fmt.Errorf("%w: %v", ErrLeaseInvalid, err)
	}
	if lease.OrganizationID != s.cfg.OrganizationID || lease.AssignedRoleID != command.ActorRoleID {
		return Workspace{}, ErrLeaseInvalid
	}
	if err := s.authorize(ctx, lease.OrganizationID, lease.OrganizationRevisionID, command.ActorRoleID, "code.stage_write"); err != nil {
		return Workspace{}, err
	}
	repository, configHash, err := s.catalog.Get(ctx, command.RepositoryID)
	if err != nil {
		return Workspace{}, err
	}
	if !TargetAllowed(repository, command.TargetRef) {
		return Workspace{}, ErrTargetRefDenied
	}
	if err := s.git.ValidateRepository(ctx, repository); err != nil {
		return Workspace{}, err
	}
	resolved, err := s.git.ResolveCommit(ctx, repository, command.BaseCommit)
	if err != nil {
		return Workspace{}, err
	}
	if resolved != command.BaseCommit {
		return Workspace{}, ErrGitConflict
	}
	target, err := s.git.ReadTarget(ctx, repository, command.TargetRef)
	if err != nil {
		return Workspace{}, err
	}
	if target != command.BaseCommit {
		return Workspace{}, ErrTargetMoved
	}
	key := fmt.Sprintf("task-%d-attempt-%d", command.TaskID, command.AttemptID)
	if err := ValidateWorkspaceKey(key); err != nil {
		return Workspace{}, err
	}
	workspace, err := s.persistence.CreateProvisioning(ctx, PreparedWorkspace{OrganizationID: lease.OrganizationID, OrganizationRevisionID: lease.OrganizationRevisionID, TaskID: command.TaskID, AttemptID: command.AttemptID, RepositoryID: command.RepositoryID, RepositoryConfigHash: configHash, WorkspaceKey: key, BaseCommit: command.BaseCommit, TargetRef: command.TargetRef, HolderID: command.HolderID, ActorRoleID: command.ActorRoleID, ArtifactRequirementID: command.ArtifactRequirementID})
	if err != nil {
		return Workspace{}, err
	}
	ref := s.workspaceRef(workspace)
	if err := s.git.CreateWorktree(ctx, CreateWorktreeRequest{Repository: repository, Workspace: ref, BaseCommit: workspace.BaseCommit}); err != nil {
		_, _ = s.persistence.FailWorkspace(ctx, workspace.ID, command.ActorRoleID, "worktree_create_failed", safeReason(err))
		return Workspace{}, err
	}
	workspace, err = s.persistence.ActivateWorkspace(ctx, workspace.ID, workspace.Version, workspace.WorkspaceRef, command.ActorRoleID)
	if err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func (s *Service) InspectWorkspace(ctx context.Context, id int64) (WorkspaceInspection, error) {
	workspace, err := s.persistence.GetWorkspace(ctx, id)
	if err != nil {
		return WorkspaceInspection{}, err
	}
	return s.git.InspectWorktree(ctx, s.workspaceRef(workspace))
}

func (s *Service) SealWorkspace(ctx context.Context, command SealWorkspaceCommand) (Workspace, error) {
	workspace, err := s.persistence.GetWorkspace(ctx, command.WorkspaceID)
	if err != nil {
		return Workspace{}, err
	}
	if workspace.Status == WorkspaceSealed {
		return workspace, nil
	}
	if workspace.Status != WorkspaceActive {
		return Workspace{}, ErrWorkspaceNotActive
	}
	if workspace.HolderID != command.HolderID || workspace.ActorRoleID != command.ActorRoleID {
		return Workspace{}, ErrLeaseInvalid
	}
	if strings.TrimSpace(command.LeaseToken) == "" {
		return Workspace{}, ErrLeaseRequired
	}
	lease, err := s.leases.VerifyActiveExecutionLease(ctx, tasks.VerifyExecutionLeaseCommand{TaskID: workspace.TaskID, AttemptID: workspace.AttemptID, HolderID: command.HolderID, LeaseToken: command.LeaseToken})
	command.LeaseToken = ""
	if err != nil {
		return Workspace{}, fmt.Errorf("%w: %v", ErrLeaseInvalid, err)
	}
	if lease.OrganizationRevisionID != workspace.OrganizationRevisionID || lease.AssignedRoleID != command.ActorRoleID {
		return Workspace{}, ErrLeaseInvalid
	}
	if err := s.authorize(ctx, workspace.OrganizationID, workspace.OrganizationRevisionID, command.ActorRoleID, "code.stage_write"); err != nil {
		return Workspace{}, err
	}
	repository, hash, err := s.catalog.Get(ctx, workspace.RepositoryID)
	if err != nil {
		return Workspace{}, err
	}
	if hash != workspace.RepositoryConfigHash {
		return Workspace{}, ErrPolicyRevisionMismatch
	}
	sealed, err := s.git.SealWorktree(ctx, SealRequest{Repository: repository, Workspace: s.workspaceRef(workspace), WorkspaceID: workspace.ID, TaskID: workspace.TaskID, AttemptID: workspace.AttemptID, BaseCommit: workspace.BaseCommit, TargetRef: workspace.TargetRef, MaxChangedFiles: s.cfg.MaxChangedFiles, MaxArtifactBytes: s.cfg.MaxArtifactBytes})
	if err != nil {
		return Workspace{}, err
	}
	manifest, err := s.artifacts.Put(ctx, bytes.NewReader(sealed.Manifest), ArtifactMetadata{MediaType: "application/vnd.explorarte.staging-manifest+json", MaxBytes: s.cfg.MaxArtifactBytes})
	if err != nil {
		return Workspace{}, err
	}
	patch, err := s.artifacts.Put(ctx, bytes.NewReader(sealed.Patch), ArtifactMetadata{MediaType: "application/vnd.git.binary-patch", MaxBytes: s.cfg.MaxArtifactBytes})
	if err != nil {
		return Workspace{}, err
	}
	return s.persistence.SealWorkspace(ctx, SealPersistenceCommand{WorkspaceID: workspace.ID, ExpectedVersion: workspace.Version, CandidateCommit: sealed.CandidateCommit, CandidateTree: sealed.CandidateTree, Manifest: manifest, Patch: patch, ChangedFileCount: len(sealed.ChangedFiles), ActorRoleID: command.ActorRoleID})
}

func (s *Service) AbandonWorkspace(ctx context.Context, command AbandonWorkspaceCommand) (Workspace, error) {
	workspace, err := s.persistence.GetWorkspace(ctx, command.WorkspaceID)
	if err != nil {
		return Workspace{}, err
	}
	if err := s.authorize(ctx, workspace.OrganizationID, workspace.OrganizationRevisionID, command.ActorRoleID, "code.stage_write"); err != nil {
		return Workspace{}, err
	}
	if strings.TrimSpace(command.ReasonCode) == "" || strings.TrimSpace(command.Reason) == "" {
		return Workspace{}, ErrInvalidInput
	}
	return s.persistence.AbandonWorkspace(ctx, command)
}
func (s *Service) CleanupWorkspace(ctx context.Context, command CleanupWorkspaceCommand) (Workspace, error) {
	workspace, err := s.persistence.GetWorkspace(ctx, command.WorkspaceID)
	if err != nil {
		return Workspace{}, err
	}
	if err := s.authorize(ctx, workspace.OrganizationID, workspace.OrganizationRevisionID, command.ActorRoleID, "code.stage_write"); err != nil {
		return Workspace{}, err
	}
	workspace, err = s.persistence.BeginCleanup(ctx, workspace.ID, command.ActorRoleID)
	if err != nil {
		return Workspace{}, err
	}
	ref := s.workspaceRef(workspace)
	inspection, inspectErr := s.git.InspectWorktree(ctx, ref)
	if inspectErr != nil {
		return Workspace{}, inspectErr
	}
	if inspection.Exists {
		if err := s.git.RemoveWorktree(ctx, ref); err != nil {
			return Workspace{}, err
		}
	}
	return s.persistence.CompleteCleanup(ctx, workspace.ID, command.ActorRoleID)
}
func (s *Service) RecordCheck(ctx context.Context, command RecordCheckCommand) (Check, error) {
	if command.WorkspaceID <= 0 || command.RequirementID <= 0 || strings.TrimSpace(command.ActorRoleID) == "" {
		return Check{}, ErrInvalidInput
	}
	workspace, err := s.persistence.GetWorkspace(ctx, command.WorkspaceID)
	if err != nil {
		return Check{}, err
	}
	if err := s.authorize(ctx, workspace.OrganizationID, workspace.OrganizationRevisionID, command.ActorRoleID, "code.run_tests"); err != nil {
		return Check{}, err
	}
	if strings.TrimSpace(command.Name) == "" || ValidateOpaqueReference(command.Reference) != nil {
		return Check{}, ErrInvalidInput
	}
	if command.Digest != "" {
		if err := ValidateDigest(command.Digest); err != nil {
			return Check{}, err
		}
	}
	return s.persistence.RecordCheck(ctx, command)
}
func (s *Service) RequestPromotion(ctx context.Context, command RequestPromotionCommand) (Promotion, error) {
	if command.WorkspaceID <= 0 || strings.TrimSpace(command.ActorRoleID) == "" {
		return Promotion{}, ErrInvalidInput
	}
	workspace, err := s.persistence.GetWorkspace(ctx, command.WorkspaceID)
	if err != nil {
		return Promotion{}, err
	}
	if err := s.authorize(ctx, workspace.OrganizationID, workspace.OrganizationRevisionID, command.ActorRoleID, "code.commit"); err != nil {
		return Promotion{}, err
	}
	return s.persistence.RequestPromotion(ctx, command)
}
func (s *Service) SubmitReview(ctx context.Context, command SubmitReviewCommand) (Promotion, error) {
	if command.PromotionID <= 0 || command.RequirementID <= 0 || strings.TrimSpace(command.ActorRoleID) == "" {
		return Promotion{}, ErrInvalidInput
	}
	promotion, err := s.persistence.GetPromotion(ctx, command.PromotionID)
	if err != nil {
		return Promotion{}, err
	}
	workspace, err := s.persistence.GetWorkspace(ctx, promotion.WorkspaceID)
	if err != nil {
		return Promotion{}, err
	}
	if err := s.authorize(ctx, workspace.OrganizationID, workspace.OrganizationRevisionID, command.ActorRoleID, "task.review"); err != nil {
		return Promotion{}, err
	}
	if strings.TrimSpace(command.Reason) == "" || ValidateOpaqueReference(command.Reference) != nil {
		return Promotion{}, ErrInvalidInput
	}
	if command.Digest != "" {
		if err := ValidateDigest(command.Digest); err != nil {
			return Promotion{}, err
		}
	}
	role, err := s.registry.GetRole(ctx, workspace.OrganizationID, command.ActorRoleID)
	if err != nil {
		return Promotion{}, err
	}
	return s.persistence.SubmitReview(ctx, command, role.AuthorityClass == "owner")
}
func (s *Service) ApplyPromotion(ctx context.Context, command ApplyPromotionCommand) (Promotion, error) {
	if command.PromotionID <= 0 || strings.TrimSpace(command.ActorRoleID) == "" {
		return Promotion{}, ErrInvalidInput
	}
	promotion, err := s.persistence.GetPromotion(ctx, command.PromotionID)
	if err != nil {
		return Promotion{}, err
	}
	workspace, err := s.persistence.GetWorkspace(ctx, promotion.WorkspaceID)
	if err != nil {
		return Promotion{}, err
	}
	if err := s.authorize(ctx, workspace.OrganizationID, workspace.OrganizationRevisionID, command.ActorRoleID, "code.commit"); err != nil {
		return Promotion{}, err
	}
	if promotion.Status == PromotionApplied {
		return promotion, nil
	}
	if promotion.Status.Terminal() {
		return Promotion{}, ErrInvalidTransition
	}
	apply, err := s.persistence.GetPromotionApplyContext(ctx, command.PromotionID)
	if err != nil {
		return Promotion{}, err
	}
	repository, hash, err := s.catalog.Get(ctx, apply.Promotion.RepositoryID)
	if err != nil {
		return Promotion{}, err
	}
	if hash != apply.Workspace.RepositoryConfigHash {
		return Promotion{}, ErrPolicyRevisionMismatch
	}
	if err := s.verifyPromotionArtifacts(ctx, apply); err != nil {
		return Promotion{}, err
	}
	if apply.Workspace.CandidateTree == nil {
		return Promotion{}, ErrGitConflict
	}
	if err := s.git.VerifySealedRevision(ctx, repository, s.workspaceRef(apply.Workspace), apply.Workspace.BaseCommit, apply.Promotion.CandidateCommit, *apply.Workspace.CandidateTree); err != nil {
		return Promotion{}, err
	}
	result, err := s.git.PromoteRef(ctx, PromotionRefRequest{Repository: repository, TargetRef: apply.Promotion.TargetRef, CandidateCommit: apply.Promotion.CandidateCommit, ExpectedBaseCommit: apply.Promotion.ExpectedBaseCommit})
	if errors.Is(err, ErrTargetMoved) {
		_, _ = s.persistence.MarkPromotionConflicted(ctx, command.PromotionID, "target_moved", "target ref no longer equals expected base")
		return Promotion{}, err
	}
	if err != nil {
		return Promotion{}, err
	}
	if !result.Applied {
		return Promotion{}, ErrGitConflict
	}
	return s.persistence.MarkPromotionApplied(ctx, command.PromotionID, command.ActorRoleID)
}

func (s *Service) verifyPromotionArtifacts(ctx context.Context, apply PromotionApplyContext) error {
	workspace := apply.Workspace
	if workspace.ManifestDigest == nil || workspace.PatchDigest == nil {
		return ErrArtifactCorrupt
	}
	expectedManifestRef := "artifact://sha256/" + *workspace.ManifestDigest
	expectedPatchRef := "artifact://sha256/" + *workspace.PatchDigest
	if apply.ManifestReference != expectedManifestRef || apply.PatchReference != expectedPatchRef {
		return ErrArtifactCorrupt
	}
	if err := s.artifacts.Verify(ctx, apply.PatchReference); err != nil {
		return err
	}
	reader, err := s.artifacts.Open(ctx, apply.ManifestReference)
	if err != nil {
		return err
	}
	defer reader.Close()
	decoder := json.NewDecoder(io.LimitReader(reader, s.cfg.MaxArtifactBytes+1))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("%w: decode staging manifest", ErrArtifactCorrupt)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: staging manifest contains trailing data", ErrArtifactCorrupt)
	}
	if manifest.SchemaVersion != 1 ||
		manifest.RepositoryID != workspace.RepositoryID ||
		manifest.WorkspaceID != workspace.ID ||
		manifest.TaskID != workspace.TaskID ||
		manifest.AttemptID != workspace.AttemptID ||
		manifest.BaseCommit != workspace.BaseCommit ||
		manifest.CandidateCommit != apply.Promotion.CandidateCommit ||
		workspace.CandidateTree == nil || manifest.CandidateTree != *workspace.CandidateTree ||
		manifest.TargetRef != apply.Promotion.TargetRef ||
		workspace.ChangedFileCount == nil || len(manifest.ChangedFiles) != *workspace.ChangedFileCount {
		return fmt.Errorf("%w: staging manifest does not match persisted candidate", ErrArtifactCorrupt)
	}
	for _, changed := range manifest.ChangedFiles {
		if strings.TrimSpace(changed.Path) == "" || filepath.IsAbs(changed.Path) || strings.ContainsRune(changed.Path, 0) {
			return fmt.Errorf("%w: invalid changed-file path", ErrArtifactCorrupt)
		}
		clean := filepath.Clean(changed.Path)
		if clean != changed.Path || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: changed-file path escapes repository", ErrArtifactCorrupt)
		}
	}
	return nil
}

func (s *Service) CancelPromotion(ctx context.Context, command CancelPromotionCommand) (Promotion, error) {
	if command.PromotionID <= 0 || strings.TrimSpace(command.ActorRoleID) == "" {
		return Promotion{}, ErrInvalidInput
	}
	promotion, err := s.persistence.GetPromotion(ctx, command.PromotionID)
	if err != nil {
		return Promotion{}, err
	}
	workspace, err := s.persistence.GetWorkspace(ctx, promotion.WorkspaceID)
	if err != nil {
		return Promotion{}, err
	}
	if err := s.authorize(ctx, workspace.OrganizationID, workspace.OrganizationRevisionID, command.ActorRoleID, "code.commit"); err != nil {
		return Promotion{}, err
	}
	if strings.TrimSpace(command.ReasonCode) == "" || strings.TrimSpace(command.Reason) == "" {
		return Promotion{}, ErrInvalidInput
	}
	return s.persistence.CancelPromotion(ctx, command)
}
func (s *Service) VerifyArtifact(ctx context.Context, reference string) error {
	if ValidateOpaqueReference(reference) != nil {
		return ErrInvalidInput
	}
	return s.artifacts.Verify(ctx, reference)
}

func (s *Service) GetWorkspace(ctx context.Context, id int64) (Workspace, error) {
	return s.persistence.GetWorkspace(ctx, id)
}
func (s *Service) ListWorkspaces(ctx context.Context, filter WorkspaceFilter) ([]Workspace, error) {
	return s.persistence.ListWorkspaces(ctx, filter)
}
func (s *Service) GetPromotion(ctx context.Context, id int64) (Promotion, error) {
	return s.persistence.GetPromotion(ctx, id)
}
func (s *Service) ListPromotions(ctx context.Context, filter PromotionFilter) ([]Promotion, error) {
	return s.persistence.ListPromotions(ctx, filter)
}
func (s *Service) workspaceRef(workspace Workspace) WorkspaceRef {
	return WorkspaceRef{ID: workspace.ID, RepositoryID: workspace.RepositoryID, WorkspaceKey: workspace.WorkspaceKey, WorkspacePath: filepath.Join(s.cfg.WorkspaceRoot, workspace.WorkspaceKey)}
}
func (s *Service) authorize(ctx context.Context, organization string, revision int64, role, capability string) error {
	if err := s.authorizer.Authorize(ctx, organization, revision, role, capability); err != nil {
		if errors.Is(err, ErrPolicyRevisionMismatch) {
			return ErrPolicyRevisionMismatch
		}
		return fmt.Errorf("%w: %v", ErrCapabilityDenied, err)
	}
	return nil
}
func safeReason(err error) string {
	value := strings.TrimSpace(err.Error())
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

var _ WorkspaceService = (*Service)(nil)
var _ PromotionService = (*Service)(nil)
