package authorization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ServiceConfig struct {
	OrganizationID    string
	DefaultTTL        time.Duration
	MaxTTL            time.Duration
	ExpireBatchSize   int
	OutboxMaxAttempts int
}

type Service struct {
	cfg    ServiceConfig
	policy *Authorizer
	store  Store
	clock  Clock
}

func NewService(cfg ServiceConfig, policy *Authorizer, store Store, clock Clock) (*Service, error) {
	if strings.TrimSpace(cfg.OrganizationID) == "" || cfg.DefaultTTL <= 0 || cfg.MaxTTL < cfg.DefaultTTL || cfg.ExpireBatchSize < 1 || cfg.ExpireBatchSize > 1000 || cfg.OutboxMaxAttempts < 1 || cfg.OutboxMaxAttempts > 100 {
		return nil, errors.New("invalid authorization service configuration")
	}
	if policy == nil || store == nil {
		return nil, errors.New("authorization service dependencies cannot be nil")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &Service{cfg: cfg, policy: policy, store: store, clock: clock}, nil
}

func (s *Service) Evaluate(ctx context.Context, request EvaluationRequest) (Evaluation, error) {
	base, err := s.policy.Evaluate(ctx, request)
	if err != nil || base.Effect != EffectApprovalRequired {
		return base, err
	}
	if request.ApprovalRequestID == nil {
		return base, nil
	}
	result, err := s.ConsumeApproval(ctx, ConsumeApprovalCommand{
		RequestID:              *request.ApprovalRequestID,
		OrganizationID:         request.OrganizationID,
		OrganizationRevisionID: request.OrganizationRevisionID,
		CapabilityMatrixHash:   base.MatrixHash,
		ActorRoleID:            request.ActorRoleID,
		CapabilityID:           request.CapabilityID,
		ResourceType:           request.ResourceType,
		ResourceID:             request.ResourceID,
		ApprovalMode:           base.ApprovalMode,
		ActionDigest:           request.ActionDigest,
	})
	if err != nil {
		if errors.Is(err, ErrRequestNotFound) {
			base.Effect = EffectApprovalRequired
			base.ReasonCode = ReasonApprovalMissing
			return base, nil
		}
		if errors.Is(err, ErrApprovalPending) || errors.Is(err, ErrApprovalRejected) || errors.Is(err, ErrApprovalExpired) || errors.Is(err, ErrApprovalConsumed) || errors.Is(err, ErrApprovalCancelled) || errors.Is(err, ErrApprovalScopeMismatch) || errors.Is(err, ErrApprovalPolicyDrift) || errors.Is(err, ErrCapabilityDenied) || errors.Is(err, ErrRoleNotFound) || errors.Is(err, ErrRoleDisabled) || errors.Is(err, ErrRoleRetired) || errors.Is(err, ErrRoleNotExecutable) {
			base.ReasonCode = result.ReasonCode
			if base.ReasonCode == "" {
				base.ReasonCode = reasonFromError(err)
			}
			if errors.Is(err, ErrApprovalPending) {
				base.Effect = EffectApprovalRequired
			} else {
				base.Effect = EffectDeny
			}
			return base, nil
		}
		return Evaluation{}, err
	}
	base.Effect, base.ReasonCode = EffectAllow, ReasonAllowedByApproval
	base.ApprovalRequestID = &result.Request.ID
	return base, nil
}

func (s *Service) RequestApproval(ctx context.Context, command RequestApprovalCommand) (RequestApprovalResult, error) {
	ttl, err := validateRequestCommand(command, s.cfg.DefaultTTL, s.cfg.MaxTTL)
	if err != nil {
		return RequestApprovalResult{}, err
	}
	organization, err := s.store.GetOrganization(ctx, s.cfg.OrganizationID)
	if err != nil {
		return RequestApprovalResult{}, err
	}
	if organization.CurrentRevision <= 0 {
		return RequestApprovalResult{}, ErrPolicyRevisionMismatch
	}
	evaluation, err := s.policy.Evaluate(ctx, EvaluationRequest{
		OrganizationID: s.cfg.OrganizationID, OrganizationRevisionID: organization.CurrentRevision,
		ActorRoleID: command.ActorRoleID, CapabilityID: command.CapabilityID,
		ResourceType: command.ResourceType, ResourceID: command.ResourceID, ActionDigest: command.ActionDigest,
	})
	if err != nil {
		return RequestApprovalResult{}, err
	}
	if evaluation.Effect == EffectDeny {
		return RequestApprovalResult{}, legacyEvaluationError(evaluation)
	}
	if evaluation.Effect != EffectApprovalRequired || evaluation.ApprovalMode == "" {
		return RequestApprovalResult{}, ErrApprovalNotRequired
	}
	now := s.clock.Now().UTC()
	expires := now.Add(ttl)
	request := ApprovalRequest{
		OrganizationID: s.cfg.OrganizationID, OrganizationRevisionID: organization.CurrentRevision,
		CapabilityMatrixHash: evaluation.MatrixHash, RequesterRoleID: strings.TrimSpace(command.ActorRoleID),
		CapabilityID: evaluation.CapabilityID, Risk: evaluation.Risk, ApprovalMode: evaluation.ApprovalMode,
		ResourceType: strings.TrimSpace(command.ResourceType), ResourceID: strings.TrimSpace(command.ResourceID),
		ActionDigest: command.ActionDigest, IdempotencyKey: strings.TrimSpace(command.IdempotencyKey),
		Status: RequestPending, Reason: strings.TrimSpace(command.Reason), Version: 1,
		CorrelationID: normalizeOptional(command.CorrelationID), CausationID: normalizeOptional(command.CausationID),
		CreatedAt: now, UpdatedAt: now, ExpiresAt: expires,
	}
	request.RequestHash = RequestHash(request.OrganizationID, request.OrganizationRevisionID, request.CapabilityMatrixHash, request.RequesterRoleID, request.CapabilityID, request.ApprovalMode, request.ResourceType, request.ResourceID, request.ActionDigest, ttl, request.Reason)
	return s.store.CreateRequest(ctx, request, s.cfg.OutboxMaxAttempts)
}

func (s *Service) DecideRequest(ctx context.Context, command DecideRequestCommand) (ApprovalRequest, error) {
	if command.RequestID <= 0 || !command.Decision.Valid() || !roleIDPattern.MatchString(strings.TrimSpace(command.ActorRoleID)) || strings.TrimSpace(command.Reason) == "" || len(command.Reason) > 2000 || strings.ContainsRune(command.Reason, 0) {
		return ApprovalRequest{}, ErrInvalidInput
	}
	if command.Digest != "" {
		if err := ValidateDigest(command.Digest); err != nil {
			return ApprovalRequest{}, err
		}
	}
	if len(command.Reference) > 500 || strings.ContainsRune(command.Reference, 0) {
		return ApprovalRequest{}, ErrInvalidInput
	}
	command.OrganizationID = s.cfg.OrganizationID
	return s.store.DecideRequest(ctx, command, s.clock.Now().UTC(), s.validateDecision, s.cfg.OutboxMaxAttempts)
}

func (s *Service) validateDecision(value ApprovalValidationContext) ReasonCode {
	request := value.Request
	if value.Organization.ID != request.OrganizationID || value.Organization.CurrentRevision != request.OrganizationRevisionID || value.Revision.ID != request.OrganizationRevisionID {
		return ReasonApprovalPolicyDrift
	}
	if value.Revision.DocumentHashes["capability-matrix.yaml"] != request.CapabilityMatrixHash || request.CapabilityMatrixHash != s.policy.MatrixHash() {
		return ReasonApprovalPolicyDrift
	}
	if value.Role.ID == "" {
		return ReasonRoleNotFound
	}
	if value.Role.RetiredAt != nil {
		return ReasonRoleRetired
	}
	if !value.Role.Enabled {
		return ReasonRoleDisabled
	}
	if !value.Role.Executable && value.Role.ID != value.Organization.OwnerRoleID {
		return ReasonRoleNotExecutable
	}
	if !s.policy.authorityKnown(value.Role.AuthorityClass) {
		return ReasonUnknownAuthorityClass
	}
	capability, ok := s.policy.Capability(request.CapabilityID)
	if !ok {
		return ReasonApprovalPolicyDrift
	}
	if s.policy.hardDenied(value.Role.AuthorityClass, request.CapabilityID) {
		return ReasonHardDeny
	}
	if capability.Approval == "" || capability.Approval != request.ApprovalMode {
		return ReasonApprovalPolicyDrift
	}
	return ""
}

func (s *Service) ConsumeApproval(ctx context.Context, command ConsumeApprovalCommand) (ConsumeApprovalResult, error) {
	if command.RequestID <= 0 || !roleIDPattern.MatchString(strings.TrimSpace(command.ActorRoleID)) {
		return ConsumeApprovalResult{}, ErrInvalidInput
	}
	if err := ValidateDigest(command.ActionDigest); err != nil {
		return ConsumeApprovalResult{}, err
	}
	for _, value := range []string{command.CorrelationID, command.CausationID} {
		if len(value) > 200 || strings.ContainsRune(value, 0) {
			return ConsumeApprovalResult{}, ErrInvalidInput
		}
	}
	if command.OrganizationID != "" && command.OrganizationID != s.cfg.OrganizationID {
		return ConsumeApprovalResult{}, ErrOrganizationMismatch
	}
	command.OrganizationID = s.cfg.OrganizationID
	result, err := s.store.ConsumeApproval(ctx, command, s.clock.Now().UTC(), s.validateConsumption, s.cfg.OutboxMaxAttempts)
	if err != nil {
		return result, err
	}
	if result.ReasonCode != "" {
		return result, errorForReason(result.ReasonCode)
	}
	return result, nil
}

func (s *Service) validateConsumption(value ApprovalValidationContext, command ConsumeApprovalCommand) ReasonCode {
	request := value.Request
	if value.Organization.ID != request.OrganizationID || value.Organization.CurrentRevision != request.OrganizationRevisionID || value.Revision.ID != request.OrganizationRevisionID {
		return ReasonApprovalPolicyDrift
	}
	if value.Revision.DocumentHashes["capability-matrix.yaml"] != request.CapabilityMatrixHash || request.CapabilityMatrixHash != s.policy.MatrixHash() {
		return ReasonApprovalPolicyDrift
	}
	if value.Role.ID == "" {
		return ReasonRoleNotFound
	}
	if value.Role.RetiredAt != nil {
		return ReasonRoleRetired
	}
	if !value.Role.Enabled {
		return ReasonRoleDisabled
	}
	if !value.Role.Executable && value.Role.ID != value.Organization.OwnerRoleID {
		return ReasonRoleNotExecutable
	}
	if !s.policy.authorityKnown(value.Role.AuthorityClass) {
		return ReasonUnknownAuthorityClass
	}
	capability, ok := s.policy.Capability(request.CapabilityID)
	if !ok {
		return ReasonApprovalPolicyDrift
	}
	if s.policy.hardDenied(value.Role.AuthorityClass, request.CapabilityID) {
		return ReasonHardDeny
	}
	if capability.Approval == "" || capability.Approval != request.ApprovalMode {
		return ReasonApprovalPolicyDrift
	}
	if command.ActorRoleID != request.RequesterRoleID || value.Role.ID != request.RequesterRoleID || command.ActionDigest != request.ActionDigest {
		return ReasonApprovalScopeMismatch
	}
	if command.OrganizationID != "" && command.OrganizationID != request.OrganizationID {
		return ReasonApprovalScopeMismatch
	}
	if command.OrganizationRevisionID != 0 && command.OrganizationRevisionID != request.OrganizationRevisionID {
		return ReasonApprovalScopeMismatch
	}
	if command.CapabilityMatrixHash != "" && command.CapabilityMatrixHash != request.CapabilityMatrixHash {
		return ReasonApprovalScopeMismatch
	}
	if command.CapabilityID != "" && command.CapabilityID != request.CapabilityID {
		return ReasonApprovalScopeMismatch
	}
	if command.ResourceType != "" && command.ResourceType != request.ResourceType {
		return ReasonApprovalScopeMismatch
	}
	if command.ResourceID != "" && command.ResourceID != request.ResourceID {
		return ReasonApprovalScopeMismatch
	}
	if command.ApprovalMode != "" && command.ApprovalMode != request.ApprovalMode {
		return ReasonApprovalScopeMismatch
	}
	return ""
}

func (s *Service) CancelRequest(ctx context.Context, command CancelRequestCommand) (ApprovalRequest, error) {
	if command.RequestID <= 0 || !roleIDPattern.MatchString(strings.TrimSpace(command.ActorRoleID)) || strings.TrimSpace(command.Reason) == "" || len(command.Reason) > 2000 || strings.ContainsRune(command.Reason, 0) {
		return ApprovalRequest{}, ErrInvalidInput
	}
	command.OrganizationID = s.cfg.OrganizationID
	return s.store.CancelRequest(ctx, command, s.clock.Now().UTC(), s.cfg.OutboxMaxAttempts)
}

func (s *Service) ExpireRequests(ctx context.Context, batch int) (ExpireResult, error) {
	if batch == 0 {
		batch = s.cfg.ExpireBatchSize
	}
	if batch < 1 || batch > 1000 {
		return ExpireResult{}, ErrInvalidInput
	}
	return s.store.ExpireRequests(ctx, s.cfg.OrganizationID, s.clock.Now().UTC(), batch, s.cfg.OutboxMaxAttempts)
}

func (s *Service) GetRequest(ctx context.Context, id int64) (ApprovalRequest, error) {
	if id <= 0 {
		return ApprovalRequest{}, ErrInvalidInput
	}
	value, err := s.store.GetRequest(ctx, id)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if value.OrganizationID != s.cfg.OrganizationID {
		return ApprovalRequest{}, ErrOrganizationMismatch
	}
	return value, nil
}

func (s *Service) ListRequests(ctx context.Context, filter ListRequestsFilter) ([]ApprovalRequest, error) {
	if filter.Status != "" && !filter.Status.Valid() {
		return nil, ErrInvalidInput
	}
	if filter.RequesterRoleID != "" && !roleIDPattern.MatchString(strings.TrimSpace(filter.RequesterRoleID)) {
		return nil, ErrInvalidInput
	}
	if len(filter.CapabilityID) > 160 || strings.ContainsRune(filter.CapabilityID, 0) {
		return nil, ErrInvalidInput
	}
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	if filter.Limit < 1 || filter.Limit > 1000 {
		return nil, ErrInvalidInput
	}
	filter.OrganizationID = s.cfg.OrganizationID
	return s.store.ListRequests(ctx, filter)
}

func (s *Service) OrganizationID() string { return s.cfg.OrganizationID }

func (s *Service) CurrentRevision(ctx context.Context) (int64, error) {
	organization, err := s.store.GetOrganization(ctx, s.cfg.OrganizationID)
	if err != nil {
		return 0, err
	}
	if organization.CurrentRevision <= 0 {
		return 0, ErrPolicyRevisionMismatch
	}
	return organization.CurrentRevision, nil
}

func reasonFromError(err error) ReasonCode {
	switch {
	case errors.Is(err, ErrApprovalPending):
		return ReasonApprovalPending
	case errors.Is(err, ErrApprovalRejected):
		return ReasonApprovalRejected
	case errors.Is(err, ErrApprovalExpired):
		return ReasonApprovalExpired
	case errors.Is(err, ErrApprovalConsumed):
		return ReasonApprovalConsumed
	case errors.Is(err, ErrApprovalCancelled):
		return ReasonApprovalCancelled
	case errors.Is(err, ErrApprovalScopeMismatch):
		return ReasonApprovalScopeMismatch
	case errors.Is(err, ErrApprovalPolicyDrift):
		return ReasonApprovalPolicyDrift
	case errors.Is(err, ErrCapabilityDenied):
		return ReasonHardDeny
	default:
		return ""
	}
}

func errorForReason(reason ReasonCode) error {
	switch reason {
	case ReasonApprovalPending:
		return ErrApprovalPending
	case ReasonApprovalRejected:
		return ErrApprovalRejected
	case ReasonApprovalExpired:
		return ErrApprovalExpired
	case ReasonApprovalConsumed:
		return ErrApprovalConsumed
	case ReasonApprovalCancelled:
		return ErrApprovalCancelled
	case ReasonApprovalScopeMismatch:
		return ErrApprovalScopeMismatch
	case ReasonApprovalPolicyDrift, ReasonRevisionMismatch, ReasonMatrixHashMismatch:
		return ErrApprovalPolicyDrift
	case ReasonRoleNotFound:
		return ErrRoleNotFound
	case ReasonRoleDisabled:
		return ErrRoleDisabled
	case ReasonRoleRetired:
		return ErrRoleRetired
	case ReasonRoleNotExecutable:
		return ErrRoleNotExecutable
	case ReasonUnknownAuthorityClass:
		return ErrUnknownAuthorityClass
	case ReasonHardDeny:
		return fmt.Errorf("%w: %s", ErrCapabilityDenied, reason)
	default:
		return fmt.Errorf("%w: %s", ErrCapabilityDenied, reason)
	}
}
