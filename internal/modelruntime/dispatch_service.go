package modelruntime

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	"github.com/Mireuz13/explorarte-organization/internal/secrets"
)

type DispatchService struct {
	organizationID   string
	config           RuntimeConfig
	catalog          OrganizationCatalog
	tasks            TaskAttemptReader
	contexts         ContextReader
	evaluator        CapabilityEvaluator
	policyCatalog    EgressPolicyCatalog
	egressEvaluator  EgressDecisionEvaluator
	egressStore      EgressEvaluationStore
	principals       modeldispatch.ExecutionPrincipalResolver
	assignments      modeldispatch.AssignmentResolver
	identity         ExecutionIdentityService
	privateKeyLoader ExecutionPrivateKeyLoader
	store            Store
	adapters         AdapterRegistry
	normalizer       Normalizer
	clock            Clock
	costGate         CostBudgetGate
}

// DispatchServiceOption configures optional DispatchService behavior that
// most callers (in particular every existing test) don't need to set up.
type DispatchServiceOption func(*DispatchService)

// WithCostBudgetGate wires real-money and multidimensional-budget
// reservation into every dispatch. Without this option, DispatchService
// behaves exactly as it did before CostBudgetGate existed — no cost or
// budget tracking, no new failure mode.
func WithCostBudgetGate(gate CostBudgetGate) DispatchServiceOption {
	return func(s *DispatchService) { s.costGate = gate }
}

type fileExecutionPrivateKeyLoader struct{}

func (fileExecutionPrivateKeyLoader) LoadExecutionPrivateKey(path string) (ed25519.PrivateKey, error) {
	return secrets.LoadEd25519PrivateKey(path)
}

func NewDispatchService(organizationID string, config RuntimeConfig, catalog OrganizationCatalog, tasks TaskAttemptReader, contexts ContextReader, evaluator CapabilityEvaluator, policyCatalog EgressPolicyCatalog, egressEvaluator EgressDecisionEvaluator, egressStore EgressEvaluationStore, principals modeldispatch.ExecutionPrincipalResolver, assignments modeldispatch.AssignmentResolver, identity ExecutionIdentityService, store Store, adapters AdapterRegistry, clock Clock, opts ...DispatchServiceOption) (*DispatchService, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if catalog == nil || tasks == nil || contexts == nil || evaluator == nil || policyCatalog == nil || egressEvaluator == nil || egressStore == nil || principals == nil || assignments == nil || identity == nil || store == nil || adapters == nil {
		return nil, fmt.Errorf("dispatch service dependencies are incomplete")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	privateKeyLoader := ExecutionPrivateKeyLoader(fileExecutionPrivateKeyLoader{})
	if injected, ok := identity.(ExecutionPrivateKeyLoader); ok {
		privateKeyLoader = injected
	}
	service := &DispatchService{
		organizationID: organizationID,
		config:         config, catalog: catalog, tasks: tasks, contexts: contexts,
		evaluator: evaluator, policyCatalog: policyCatalog, egressEvaluator: egressEvaluator,
		egressStore: egressStore, principals: principals, assignments: assignments, identity: identity, privateKeyLoader: privateKeyLoader,
		store: store, adapters: adapters,
		normalizer: Normalizer{MaxResponseBytes: config.MaxResponseBytes, MaxToolIntents: config.MaxToolIntents},
		clock:      clock,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service, nil
}

func (s *DispatchService) Dispatch(ctx context.Context, invocationID int64) (DispatchResult, error) {
	if !s.config.Enabled {
		return DispatchResult{}, ErrDisabled
	}
	if invocationID <= 0 {
		return DispatchResult{}, fmt.Errorf("%w: invocation ID is required", ErrInvalidRequest)
	}
	principalKey := strings.TrimSpace(s.config.ExecutionPrincipalKey)
	if principalKey == "" {
		return DispatchResult{}, fmt.Errorf("%w: ORG_MODEL_EXECUTION_PRINCIPAL_KEY is not configured", ErrInvalidRequest)
	}
	principal, err := s.principals.ResolveByKey(ctx, s.organizationID, principalKey)
	if err != nil {
		return DispatchResult{}, err
	}
	if principal.Status != modeldispatch.PrincipalActive {
		return DispatchResult{}, ErrExecutionPrincipalDisabled
	}

	invocation, err := s.store.GetInvocation(ctx, invocationID)
	if err != nil {
		return DispatchResult{}, err
	}
	// Expand-and-contract legacy rows are allowed to claim only so that the
	// system can persist a terminal failed-before-send record. This is not a
	// fallback identity path: no challenge, render, egress evaluation or adapter
	// call is possible.
	if invocation.DispatcherAssignmentID == nil || invocation.ExecutionPrincipalID == nil {
		claimed, claimErr := s.store.ClaimInvocation(ctx, ClaimCommand{InvocationID: invocationID, ClaimedBy: principal.PrincipalKey, ExecutionPrincipalID: principal.ID}, s.config)
		if claimErr != nil {
			return DispatchResult{}, claimErr
		}
		persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), s.config.CommandTimeout)
		defer cancelPersist()
		failed, persistErr := s.store.FailBeforeSend(persistCtx, FailureCommand{
			InvocationID: claimed.Invocation.ID, DispatchAttemptID: claimed.DispatchAttempt.ID,
			ClaimToken: claimed.ClaimToken, ErrorCode: "dispatcher_assignment_unpinned",
			OutcomeClassification: "failed_before_send", EventType: AuditInvocationFailed,
		}, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, errors.Join(ErrDispatcherAssignmentUnpinned, persistErr)
		}
		return DispatchResult{Invocation: failed}, ErrDispatcherAssignmentUnpinned
	}
	if *invocation.ExecutionPrincipalID != principal.ID {
		return DispatchResult{}, ErrExecutionPrincipalMismatch
	}
	// Branch 09 legacy rows may have a dispatcher assignment and execution
	// principal pinned while still lacking the model egress policy pin. They
	// cannot produce an ActionDigest and therefore cannot enter the execution
	// identity challenge flow. Claim only to persist the terminal
	// failed-before-send result; this is an expand-and-contract cleanup path,
	// never an identity bypass and never reaches render, egress evaluation or an
	// adapter.
	if invocation.ModelEgressPolicyVersionID == nil || invocation.ModelEgressPolicyHash == "" {
		claimed, claimErr := s.store.ClaimInvocation(ctx, ClaimCommand{InvocationID: invocationID, ClaimedBy: principal.PrincipalKey, ExecutionPrincipalID: principal.ID}, s.config)
		if claimErr != nil {
			return DispatchResult{}, claimErr
		}
		persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), s.config.CommandTimeout)
		defer cancelPersist()
		failed, persistErr := s.store.FailBeforeSend(persistCtx, FailureCommand{
			InvocationID: claimed.Invocation.ID, DispatchAttemptID: claimed.DispatchAttempt.ID,
			ClaimToken: claimed.ClaimToken, ErrorCode: "egress_policy_unpinned",
			OutcomeClassification: "failed_before_send", EventType: AuditInvocationFailed,
		}, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, errors.Join(ErrEgressPolicyUnpinned, persistErr)
		}
		return DispatchResult{Invocation: failed}, ErrEgressPolicyUnpinned
	}
	if invocation.ExecutionIdentityPolicyVersionID == nil || invocation.ExecutionIdentityPolicyHash == "" {
		claimed, claimErr := s.store.ClaimInvocation(ctx, ClaimCommand{InvocationID: invocationID, ClaimedBy: principal.PrincipalKey, ExecutionPrincipalID: principal.ID}, s.config)
		if claimErr != nil {
			return DispatchResult{}, claimErr
		}
		persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), s.config.CommandTimeout)
		defer cancelPersist()
		failed, persistErr := s.store.FailBeforeSend(persistCtx, FailureCommand{
			InvocationID: claimed.Invocation.ID, DispatchAttemptID: claimed.DispatchAttempt.ID,
			ClaimToken: claimed.ClaimToken, ErrorCode: "execution_identity_unpinned",
			OutcomeClassification: "failed_before_send", EventType: AuditInvocationFailed,
		}, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, errors.Join(ErrExecutionIdentityUnpinned, persistErr)
		}
		return DispatchResult{Invocation: failed}, ErrExecutionIdentityUnpinned
	}
	if !s.config.ExecutionIdentityEnabled {
		return DispatchResult{}, fmt.Errorf("%w: ORG_MODEL_EXECUTION_IDENTITY_ENABLED is false", ErrExecutionIdentityDenied)
	}
	if strings.TrimSpace(s.config.ExecutionIdentityKeyFile) == "" {
		return DispatchResult{}, fmt.Errorf("%w: ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE is not configured", ErrInvalidRequest)
	}
	privateKey, err := s.privateKeyLoader.LoadExecutionPrivateKey(s.config.ExecutionIdentityKeyFile)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("%w: %v", ErrExecutionIdentityDenied, err)
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return DispatchResult{}, ErrExecutionIdentityDenied
	}
	fingerprint := modelidentity.PublicKeyFingerprint(publicKey)
	identityKey, err := s.identity.ResolveActiveKeyByFingerprint(ctx, invocation.OrganizationID, principal.ID, fingerprint)
	if err != nil {
		return DispatchResult{}, err
	}
	identityPolicy, err := s.identity.ResolvePolicyByID(ctx, invocation.OrganizationID, *invocation.ExecutionIdentityPolicyVersionID)
	if err != nil {
		return DispatchResult{}, err
	}
	if identityPolicy.Version.CanonicalHash != invocation.ExecutionIdentityPolicyHash {
		return DispatchResult{}, ErrExecutionIdentityDenied
	}
	actionDigest, err := ActionDigest(invocation)
	if err != nil {
		return DispatchResult{}, err
	}
	issued, err := s.identity.Issue(ctx, modelidentity.ChallengeScope{
		OrganizationID: invocation.OrganizationID, OrganizationRevisionID: invocation.OrganizationRevisionID,
		InvocationID: invocation.ID, DispatcherAssignmentID: *invocation.DispatcherAssignmentID,
		ExecutionPrincipalID: principal.ID, ExecutionIdentityPolicyVersionID: *invocation.ExecutionIdentityPolicyVersionID,
		ExecutionIdentityPolicyHash: invocation.ExecutionIdentityPolicyHash, ExecutionIdentityKeyID: identityKey.ID,
		ActionDigest: actionDigest, RequestHash: invocation.RequestHash,
	}, identityPolicy)
	if err != nil {
		return DispatchResult{}, err
	}
	signature := ed25519.Sign(privateKey, issued.Payload)
	_, err = s.identity.Verify(identityKey, issued, signature, identityPolicy)
	if err != nil {
		return DispatchResult{}, err
	}
	claimed, err := s.store.ClaimInvocationAuthenticated(ctx, AuthenticatedClaimCommand{
		InvocationID: invocationID, ClaimedBy: principal.PrincipalKey,
		ExecutionPrincipalID: principal.ID, IdentityKeyID: identityKey.ID,
		ChallengeID: issued.Challenge.ID, ChallengeNonce: issued.Nonce,
		Signature: append([]byte(nil), signature...),
	}, s.config)
	if err != nil {
		return DispatchResult{}, err
	}
	invocation = claimed.Invocation
	dispatchAttemptID := claimed.DispatchAttempt.ID
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), s.config.CommandTimeout)
	defer cancelPersist()

	failBeforeSend := func(code string, cause error, eventType string) (DispatchResult, error) {
		failed, persistErr := s.store.FailBeforeSend(persistCtx, FailureCommand{
			InvocationID: invocation.ID, DispatchAttemptID: dispatchAttemptID,
			ClaimToken: claimed.ClaimToken, ErrorCode: code,
			OutcomeClassification: "failed_before_send", EventType: eventType,
		}, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, errors.Join(cause, persistErr)
		}
		return DispatchResult{Invocation: failed}, cause
	}

	if !invocation.Deadline.After(s.clock.Now()) {
		return failBeforeSend("deadline_elapsed", context.DeadlineExceeded, AuditInvocationTimedOut)
	}
	taskAttempt, err := s.tasks.GetTaskAttempt(ctx, invocation.TaskID, invocation.AttemptID)
	if err != nil {
		return failBeforeSend("task_attempt_unavailable", err, AuditInvocationFailed)
	}
	commandScope := CreateInvocationCommand{
		OrganizationID: invocation.OrganizationID, TaskID: invocation.TaskID,
		AttemptID: invocation.AttemptID, SubjectRoleID: invocation.SubjectRoleID, ContextSnapshotID: invocation.ContextSnapshotID,
	}
	if err = validateTaskAttempt(taskAttempt, commandScope, s.clock.Now()); err != nil {
		return failBeforeSend("task_attempt_rejected", err, AuditInvocationFailed)
	}
	if taskAttempt.OrganizationRevisionID != invocation.OrganizationRevisionID {
		return failBeforeSend("task_revision_drift", ErrTaskAttemptRejected, AuditInvocationFailed)
	}
	organization, err := s.catalog.CurrentOrganization(ctx, invocation.OrganizationID)
	if err != nil {
		return failBeforeSend("organization_unavailable", err, AuditInvocationFailed)
	}
	if organization.RevisionID != invocation.OrganizationRevisionID {
		return failBeforeSend("organization_revision_drift", ErrTaskAttemptRejected, AuditInvocationFailed)
	}

	resolvedAssignment, err := s.assignments.GetByID(ctx, invocation.OrganizationID, *invocation.DispatcherAssignmentID)
	if err != nil {
		return failBeforeSend("dispatcher_assignment_unavailable", err, AuditInvocationFailed)
	}
	if resolvedAssignment.Principal.ID != *invocation.ExecutionPrincipalID || resolvedAssignment.Principal.ID != principal.ID {
		return failBeforeSend("dispatcher_principal_mismatch", ErrExecutionPrincipalMismatch, AuditInvocationFailed)
	}
	if resolvedAssignment.Principal.Status != modeldispatch.PrincipalActive {
		return failBeforeSend("dispatcher_principal_disabled", ErrExecutionPrincipalDisabled, AuditInvocationFailed)
	}
	assignment := resolvedAssignment.Assignment
	switch assignment.Status {
	case modeldispatch.AssignmentActive:
	case modeldispatch.AssignmentRevoked:
		return failBeforeSend("dispatcher_assignment_revoked", ErrAssignmentUnavailable, AuditInvocationFailed)
	case modeldispatch.AssignmentExhausted:
		return failBeforeSend("dispatcher_assignment_exhausted", ErrAssignmentQuotaExhausted, AuditInvocationFailed)
	case modeldispatch.AssignmentExpired:
		return failBeforeSend("dispatcher_assignment_expired", ErrAssignmentVigencyExpired, AuditInvocationFailed)
	default:
		return failBeforeSend("dispatcher_assignment_unavailable", ErrAssignmentUnavailable, AuditInvocationFailed)
	}
	now := s.clock.Now()
	if now.Before(assignment.ValidFrom) || !now.Before(assignment.ValidUntil) {
		return failBeforeSend("dispatcher_assignment_expired", ErrAssignmentVigencyExpired, AuditInvocationFailed)
	}
	if assignment.UsedInvocations >= assignment.MaxInvocations {
		return failBeforeSend("dispatcher_assignment_exhausted", ErrAssignmentQuotaExhausted, AuditInvocationFailed)
	}
	if assignment.OrganizationRevisionID != invocation.OrganizationRevisionID || assignment.SubjectRoleID != invocation.SubjectRoleID || assignment.DispatchActorRoleID != invocation.DispatchActorRoleID {
		return failBeforeSend("dispatcher_assignment_drift", ErrAssignmentRevisionDrift, AuditInvocationFailed)
	}

	binding, err := s.store.GetBinding(ctx, invocation.OrganizationID, invocation.OrganizationRevisionID, invocation.SubjectRoleID)
	if err != nil {
		return failBeforeSend("binding_unavailable", err, AuditInvocationFailed)
	}
	if binding.Version.ID != invocation.ModelProfileVersionID || binding.Profile.ID != invocation.ModelProfileID || binding.Version.ProviderID != invocation.ProviderID || binding.Version.ProviderModelID != invocation.ProviderModelID {
		return failBeforeSend("binding_drift", ErrBindingNotFound, AuditInvocationFailed)
	}
	if !binding.Binding.Active || !capabilitiesSatisfy(binding.Capabilities.Capabilities, invocation.RequiredCapabilities) {
		return failBeforeSend("capability_mismatch", ErrCapabilityMismatch, AuditInvocationFailed)
	}
	if invocation.ModelEgressPolicyVersionID == nil || invocation.ModelEgressPolicyHash == "" {
		return failBeforeSend("egress_policy_unpinned", ErrEgressPolicyUnpinned, AuditInvocationFailed)
	}
	policy, err := s.policyCatalog.ResolveForRevision(ctx, invocation.OrganizationID, invocation.OrganizationRevisionID)
	if err != nil {
		return failBeforeSend("egress_policy_unavailable", err, AuditInvocationFailed)
	}
	if err = validateResolvedEgressPolicy(policy, organization); err != nil || policy.Version.ID != *invocation.ModelEgressPolicyVersionID || policy.CanonicalHash != invocation.ModelEgressPolicyHash {
		return failBeforeSend("egress_policy_drift", ErrEgressPolicyUnpinned, AuditInvocationFailed)
	}

	snapshot, err := s.contexts.GetContextSnapshot(ctx, invocation.ContextSnapshotID)
	if err != nil {
		return failBeforeSend("context_unavailable", err, AuditInvocationFailed)
	}
	if err = validateContext(snapshot, commandScope, invocation.OrganizationRevisionID); err != nil {
		return failBeforeSend("context_rejected", err, AuditInvocationFailed)
	}
	if err = s.contexts.ValidateContextSnapshot(ctx, invocation.ContextSnapshotID); err != nil {
		return failBeforeSend("context_drift", fmt.Errorf("%w: %v", ErrContextRejected, err), AuditInvocationFailed)
	}
	classifications, classificationsHash := modelegress.NormalizeClassifications(snapshot.DataClasses)
	actionDigest, err = ActionDigest(invocation)
	if err != nil {
		return failBeforeSend("action_digest_failed", err, AuditInvocationFailed)
	}

	baseEvaluation := modelegress.PreSendEvaluation{
		InvocationID: invocation.ID, DispatchAttemptID: dispatchAttemptID,
		PolicyVersionID: policy.Version.ID, PolicyHash: policy.CanonicalHash,
		OrganizationID: invocation.OrganizationID, OrganizationRevisionID: invocation.OrganizationRevisionID,
		DispatchActorRoleID: invocation.DispatchActorRoleID, SubjectRoleID: invocation.SubjectRoleID,
		ModelProfileVersionID: invocation.ModelProfileVersionID, ProviderID: invocation.ProviderID,
		ProviderTransport: string(binding.Version.Transport), ActionDigest: actionDigest,
		CapabilityMatrixHash:   organization.CapabilityMatrixHash,
		ContextClassifications: classifications, ContextClassificationsHash: classificationsHash,
		CorrelationID: invocation.CorrelationID, CausationID: invocation.CausationID,
	}
	persistDecisionFailure := func(evaluation modelegress.PreSendEvaluation, code string, cause error) (DispatchResult, error) {
		evaluation.DecisionHash = modelegress.DecisionHash(evaluation)
		persistErr := s.egressStore.PersistPreSendDenyAndFail(persistCtx, modelegress.PersistDenyCommand{
			Evaluation: evaluation, ClaimToken: claimed.ClaimToken,
			ErrorCode: code, OutboxMaxAttempts: s.config.OutboxMaxAttempts,
		})
		if persistErr != nil {
			return DispatchResult{}, errors.Join(cause, persistErr)
		}
		failed, getErr := s.store.GetInvocation(persistCtx, invocation.ID)
		if getErr != nil {
			return DispatchResult{}, errors.Join(cause, getErr)
		}
		return DispatchResult{Invocation: failed}, cause
	}

	authorizationDecision, authorizationErr := s.evaluator.EvaluateDispatch(ctx, invocation.OrganizationID, invocation.OrganizationRevisionID, invocation.DispatchActorRoleID, strconv.FormatInt(invocation.ID, 10), actionDigest)
	if authorizationErr != nil {
		evaluation := baseEvaluation
		evaluation.AuthorizationEffect = modelegress.AuthorizationError
		evaluation.AuthorizationReasonCode = "authorization_operational_error"
		evaluation.EgressEffect = modelegress.EffectNotEvaluated
		return persistDecisionFailure(evaluation, "authorization_error", authorizationErr)
	}
	baseEvaluation.CapabilityMatrixHash = authorizationDecision.MatrixHash
	if baseEvaluation.CapabilityMatrixHash == "" {
		baseEvaluation.CapabilityMatrixHash = organization.CapabilityMatrixHash
	}
	if !authorizationDecision.Allowed {
		evaluation := baseEvaluation
		evaluation.AuthorizationEffect = modelegress.AuthorizationDeny
		evaluation.AuthorizationReasonCode = authorizationDecision.ReasonCode
		evaluation.EgressEffect = modelegress.EffectNotEvaluated
		return persistDecisionFailure(evaluation, "authorization_denied", fmt.Errorf("%w: %s", ErrAuthorizationDenied, authorizationDecision.ReasonCode))
	}
	baseEvaluation.AuthorizationEffect = modelegress.AuthorizationAllow
	baseEvaluation.AuthorizationReasonCode = authorizationDecision.ReasonCode

	egressDecision, egressErr := s.egressEvaluator.Evaluate(modelegress.EvaluationRequest{
		ProviderID: invocation.ProviderID, ProviderTransport: string(binding.Version.Transport),
		OrganizationRevisionID: invocation.OrganizationRevisionID, Policy: policy,
		ContextClassifications: classifications,
	})
	if egressErr != nil || egressDecision.Effect == modelegress.EffectError {
		evaluation := baseEvaluation
		evaluation.EgressEffect = modelegress.EffectError
		evaluation.EgressReasonCodes = egressDecision.ReasonCodes
		if len(evaluation.EgressReasonCodes) == 0 {
			evaluation.EgressReasonCodes = []string{"egress_operational_error"}
		}
		cause := ErrEgressEvaluation
		if egressErr != nil {
			cause = errors.Join(ErrEgressEvaluation, egressErr)
		}
		return persistDecisionFailure(evaluation, "egress_error", cause)
	}
	if egressDecision.Effect != modelegress.EffectAllow {
		evaluation := baseEvaluation
		evaluation.EgressEffect = egressDecision.Effect
		evaluation.EgressReasonCodes = egressDecision.ReasonCodes
		return persistDecisionFailure(evaluation, "egress_denied", fmt.Errorf("%w: %s", ErrEgressDenied, strings.Join(egressDecision.ReasonCodes, ",")))
	}

	allowEvaluation := baseEvaluation
	allowEvaluation.EgressEffect = modelegress.EffectAllow
	allowEvaluation.EgressReasonCodes = egressDecision.ReasonCodes

	for _, classification := range classifications {
		if classification == string(modelegress.ClassificationSecret) || classification == string(modelegress.ClassificationClinical) {
			// The evaluator must already have denied these classifications. If a
			// defective evaluator reaches this guard, persist a conservative deny
			// rather than a contradictory allow or an unaudited generic failure.
			evaluation := baseEvaluation
			evaluation.EgressEffect = modelegress.EffectDeny
			evaluation.EgressReasonCodes = []string{"egress_guard_rejected"}
			return persistDecisionFailure(evaluation, "egress_guard_rejected", ErrEgressDenied)
		}
	}

	providerAdapter, ok := s.adapters.Get(invocation.ProviderID)
	if !binding.Version.DispatchEnabled || binding.Version.AdapterStatus != AdapterAvailable || !binding.Provider.DispatchEnabled || binding.Provider.AdapterStatus != AdapterAvailable || !ok || providerAdapter == nil || providerAdapter.ProviderID() != invocation.ProviderID {
		// An allow decision becomes durable only together with send_started. The
		// adapter gate is still before rendering and before that atomic barrier.
		return failBeforeSend("adapter_unavailable", ErrProviderUnavailable, AuditInvocationFailed)
	}
	descriptor := providerAdapter.Descriptor()
	if err = descriptor.Validate(); err != nil || descriptor.ProviderID != invocation.ProviderID || descriptor.Transport != binding.Version.Transport {
		return failBeforeSend("adapter_descriptor_invalid", ErrProviderUnavailable, AuditInvocationFailed)
	}
	if err = providerAdapter.Preflight(ctx, ProviderPreflightRequest{
		ProviderID: invocation.ProviderID, ProviderModelID: invocation.ProviderModelID, Deadline: invocation.Deadline,
	}); err != nil {
		code := "adapter_preflight_failed"
		if classified, ok := AsAdapterError(err); ok && classified.Outcome.ErrorCode != "" {
			code = classified.Outcome.ErrorCode
		}
		return failBeforeSend(code, errors.Join(ErrProviderUnavailable, err), AuditInvocationFailed)
	}
	renderedContext, err := s.contexts.RenderContextSnapshot(ctx, invocation.ContextSnapshotID)
	if err != nil {
		return failBeforeSend("context_render_failed", err, AuditInvocationFailed)
	}
	renderedHash := SHA256Bytes(renderedContext)
	if renderedHash != snapshot.RenderedHash {
		return failBeforeSend("context_render_hash_mismatch", ErrContextRejected, AuditInvocationFailed)
	}
	// R10.4: best-effort ProviderRender v1 telemetry (StablePrefix/
	// DynamicSuffix hashes/bytes) -- observability only, never a dispatch
	// gate. Both s.contexts and s.store are OPTIONAL capabilities here
	// (ProviderRenderTelemetryReader/Recorder); an implementation that
	// doesn't support either (any test double, or any deployment that
	// hasn't upgraded) leaves this whole block a no-op, exactly as before
	// R10.4 existed.
	if reader, ok := s.contexts.(ProviderRenderTelemetryReader); ok {
		if telemetry, telemetryErr := reader.GetProviderRenderTelemetry(ctx, invocation.ContextSnapshotID); telemetryErr == nil {
			if recorder, ok := s.store.(ProviderRenderTelemetryRecorder); ok {
				_ = recorder.RecordProviderRenderTelemetry(persistCtx, invocation.ID, telemetry)
			}
		}
	}

	var costReservation CostReservation
	reservationApplied, reservationSettled := false, false
	if s.costGate != nil {
		estimatedInputTokens := estimateTokenCount(renderedContext)
		reserved, reserveErr := s.costGate.Reserve(ctx, CostReservationRequest{
			OrganizationID: invocation.OrganizationID, TaskID: invocation.TaskID, InvocationID: invocation.ID,
			ProviderID: invocation.ProviderID, ProviderModelID: invocation.ProviderModelID,
			EstimatedInputTokens: estimatedInputTokens, MaxOutputTokens: int64(invocation.MaxOutputTokens),
		}, s.clock.Now())
		if reserveErr != nil {
			return failBeforeSend("budget_exceeded", reserveErr, AuditInvocationFailed)
		}
		costReservation, reservationApplied = reserved, true
		defer func() {
			if reservationApplied && !reservationSettled {
				_ = s.costGate.Release(context.WithoutCancel(ctx), costReservation, s.clock.Now())
			}
		}()
	}

	allowEvaluation.DecisionHash = modelegress.DecisionHash(allowEvaluation)
	providerIdempotencyKeyHash := SHA256Bytes([]byte(fmt.Sprintf("%s:%d:%d", invocation.OrganizationID, invocation.ID, dispatchAttemptID)))
	canonicalRequest := CanonicalRequest{
		InvocationID:           invocation.ID,
		DispatchAttemptID:      dispatchAttemptID,
		OrganizationID:         invocation.OrganizationID,
		OrganizationRevisionID: invocation.OrganizationRevisionID,
		TaskID:                 invocation.TaskID,
		AttemptID:              invocation.AttemptID,
		DispatchActorRoleID:    invocation.DispatchActorRoleID,
		SubjectRoleID:          invocation.SubjectRoleID,
		ModelProfileID:         invocation.ModelProfileID,
		ModelProfileVersionID:  invocation.ModelProfileVersionID,
		ProviderID:             invocation.ProviderID,
		ProviderModelID:        invocation.ProviderModelID,
		ProviderIdempotencyKey: providerIdempotencyKeyHash,
		ReasoningEffort:        binding.Version.ReasoningEffort,
		ContextSnapshotID:      invocation.ContextSnapshotID,
		ContextRenderedHash:    renderedHash,
		RenderedContext:        renderedContext,
		RequiredCapabilities:   invocation.RequiredCapabilities,
		OutputMode:             invocation.OutputMode,
		OutputSchema:           invocation.OutputSchema,
		MaxOutputTokens:        invocation.MaxOutputTokens,
		Temperature:            invocation.Temperature,
		ThinkingMode:           invocation.ThinkingMode,
		Deadline:               invocation.Deadline,
	}
	providerRequest, err := BuildProviderRequestEvidence(canonicalRequest, descriptor)
	if err != nil {
		return failBeforeSend("provider_request_evidence_invalid", err, AuditInvocationFailed)
	}
	if err = s.egressStore.PersistPreSendAllowAndMarkSendStarted(persistCtx, modelegress.PersistAllowCommand{
		Evaluation: allowEvaluation, ClaimToken: claimed.ClaimToken,
		ProviderIdempotencyKeyHash: providerIdempotencyKeyHash, ProviderRequest: providerRequest,
		Deadline: invocation.Deadline,
	}); err != nil {
		return DispatchResult{}, err
	}
	invocation, err = s.store.GetInvocation(persistCtx, invocation.ID)
	if err != nil {
		return DispatchResult{}, err
	}

	dispatchCtx, cancelDispatch := context.WithDeadline(ctx, invocation.Deadline)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		if watchErr := s.store.WatchCancellation(dispatchCtx, invocation.ID); watchErr == nil {
			cancelDispatch()
		}
	}()
	rawResponse, adapterErr := providerAdapter.Dispatch(dispatchCtx, canonicalRequest)
	cancelDispatch()
	<-watchDone

	if adapterErr != nil {
		if classified, ok := AsAdapterError(adapterErr); ok {
			command := FailureCommand{
				InvocationID: invocation.ID, DispatchAttemptID: dispatchAttemptID,
				ClaimToken: claimed.ClaimToken, ErrorCode: classified.Outcome.ErrorCode,
				OutcomeClassification: classified.Outcome.OutcomeClassification,
				ProviderOutcome:       &classified.Outcome,
			}
			switch classified.Phase {
			case AdapterFailureBeforeRequest:
				failed, persistErr := s.store.FailCommittedBeforeRequest(persistCtx, command, classified.Outcome, s.config.OutboxMaxAttempts)
				if persistErr != nil {
					return DispatchResult{}, errors.Join(adapterErr, persistErr)
				}
				return DispatchResult{Invocation: failed}, adapterErr
			case AdapterFailureResponseReceived:
				// Proof-positive the provider's HTTP response was received —
				// the call may already be billed. This is the exact phase the
				// original bug got backwards: reservationSettled was never
				// set here, so the deferred Release below fired
				// unconditionally and every one of these calls was recorded
				// as financially free. When the adapter could recover real
				// usage from the response envelope even while erroring
				// (recoveredUsage), commit that as the actual cost; the
				// invocation is still recorded as `failed` below — financial
				// settlement and task/business success are independent.
				usage := recoveredUsage(invocation.ID, dispatchAttemptID, rawResponse)
				command.Usage = usage
				failed, persistErr := s.store.RejectProviderResponse(persistCtx, command, classified.Outcome, s.config.OutboxMaxAttempts)
				if persistErr != nil {
					return DispatchResult{}, errors.Join(adapterErr, persistErr)
				}
				s.settleNonSuccessReservation(ctx, &reservationSettled, reservationApplied, costReservation, usage)
				return DispatchResult{Invocation: failed}, adapterErr
			case AdapterFailureAmbiguous:
				// The provider may or may not have processed (and billed)
				// this call — releasing the reservation here would hand
				// back money that was possibly already spent. Leave it
				// reserved for reconciliation rather than releasing it.
				s.settleNonSuccessReservation(ctx, &reservationSettled, reservationApplied, costReservation, nil)
				eventType := AuditInvocationAmbiguous
				if errors.Is(adapterErr, context.DeadlineExceeded) {
					eventType = AuditInvocationTimedOut
				}
				ambiguous, persistErr := s.store.MarkAmbiguous(persistCtx, command, eventType, s.config.OutboxMaxAttempts)
				if persistErr != nil {
					return DispatchResult{}, errors.Join(adapterErr, persistErr)
				}
				return DispatchResult{Invocation: ambiguous}, fmt.Errorf("%w: %v", ErrAmbiguousOutcome, adapterErr)
			}
		}
		if errors.Is(adapterErr, context.Canceled) && rawResponse.CancellationConfirmed {
			requested, requestErr := s.store.CancellationRequested(persistCtx, invocation.ID)
			if requestErr != nil {
				return DispatchResult{}, errors.Join(adapterErr, requestErr)
			}
			if requested {
				cancelled, persistErr := s.store.MarkCancelled(persistCtx, FailureCommand{
					InvocationID:          invocation.ID,
					DispatchAttemptID:     dispatchAttemptID,
					ClaimToken:            claimed.ClaimToken,
					ErrorCode:             "adapter_cancelled",
					OutcomeClassification: "cancelled_confirmed",
					ProviderOutcome: &ProviderOutcome{
						OutcomeClassification: ProviderOutcomeCancelled,
						ProviderRequestID:     rawResponse.ProviderRequestID,
						ResponseHash:          SHA256Bytes(rawResponse.Content),
						ResponseSchemaVersion: descriptor.ResponseSchemaVersion,
						CancellationConfirmed: true,
					},
				}, s.config.OutboxMaxAttempts)
				if persistErr != nil {
					return DispatchResult{}, errors.Join(adapterErr, persistErr)
				}
				return DispatchResult{Invocation: cancelled}, ErrCancellationRequested
			}
		}
		// Same reasoning as the AdapterFailureAmbiguous case above: the
		// request reached the provider through some path this classifier
		// doesn't recognize as definitively unsent, so the reservation
		// must not be auto-released.
		s.settleNonSuccessReservation(ctx, &reservationSettled, reservationApplied, costReservation, nil)
		errorCode := "adapter_error_after_send"
		eventType := AuditInvocationAmbiguous
		if errors.Is(adapterErr, context.DeadlineExceeded) {
			errorCode = "provider_timeout_after_send"
			eventType = AuditInvocationTimedOut
		}
		outcome := ProviderOutcome{
			OutcomeClassification: ProviderOutcomeAmbiguous,
			ErrorClass:            "adapter", ErrorCode: errorCode, Retryable: true,
			ResponseSchemaVersion: descriptor.ResponseSchemaVersion,
		}
		ambiguous, persistErr := s.store.MarkAmbiguous(persistCtx, FailureCommand{
			InvocationID:          invocation.ID,
			DispatchAttemptID:     dispatchAttemptID,
			ClaimToken:            claimed.ClaimToken,
			ErrorCode:             errorCode,
			OutcomeClassification: "ambiguous_external_outcome",
			ProviderOutcome:       &outcome,
		}, eventType, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, errors.Join(adapterErr, persistErr)
		}
		return DispatchResult{Invocation: ambiguous}, fmt.Errorf("%w: %v", ErrAmbiguousOutcome, adapterErr)
	}

	providerOutcome := rawResponse.ProviderOutcome
	if providerOutcome.OutcomeClassification == "" {
		providerOutcome = ProviderOutcome{
			OutcomeClassification: ProviderOutcomeResponseReceived,
			ProviderRequestID:     rawResponse.ProviderRequestID, HTTPStatus: 200,
			ResponseHash: SHA256Bytes(rawResponse.Content), ResponseSchemaVersion: descriptor.ResponseSchemaVersion,
		}
	}
	invocation, err = s.store.MarkResponseReceived(persistCtx, invocation.ID, dispatchAttemptID, claimed.ClaimToken, providerOutcome)
	if err != nil {
		// The provider returned, so this process must never issue another call. A later
		// reconcile classifies the durable send_started state without dispatching.
		// The provider is now known to have processed this call, so the
		// reservation must not be released even though persisting that
		// fact failed. Whether the persistence failure itself means this
		// specific write landed is unknown, so prefer the conservative
		// "commit what we can prove" behavior: if the adapter already
		// recovered real usage, commit it as actual; otherwise leave the
		// reservation parked pending reconciliation rather than releasing
		// it.
		s.settleNonSuccessReservation(ctx, &reservationSettled, reservationApplied, costReservation, recoveredUsage(invocation.ID, dispatchAttemptID, rawResponse))
		return DispatchResult{}, err
	}
	normalized, err := s.normalizer.Normalize(invocation, dispatchAttemptID, rawResponse)
	if err != nil {
		// The provider already responded (and normalization is the only
		// thing that failed — our own business JSON parse, not the
		// adapter's decode of the outer envelope), so the call is almost
		// always billable. The adapter already decoded the envelope fine
		// here (adapterErr was nil to reach this point), so real usage is
		// threaded through as FailureCommand.Usage and committed as actual
		// rather than left stuck reserved forever.
		usage := recoveredUsage(invocation.ID, dispatchAttemptID, rawResponse)
		failed, persistErr := s.store.FailAfterResponse(persistCtx, FailureCommand{
			InvocationID:          invocation.ID,
			DispatchAttemptID:     dispatchAttemptID,
			ClaimToken:            claimed.ClaimToken,
			ErrorCode:             "response_normalization_failed",
			OutcomeClassification: "response_received_rejected",
			Usage:                 usage,
		}, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, errors.Join(err, persistErr)
		}
		s.settleNonSuccessReservation(ctx, &reservationSettled, reservationApplied, costReservation, usage)
		return DispatchResult{Invocation: failed}, err
	}
	if normalized.CancellationConfirmed {
		requested, requestErr := s.store.CancellationRequested(persistCtx, invocation.ID)
		if requestErr != nil {
			return DispatchResult{}, requestErr
		}
		if requested {
			// MarkResponseReceived already persisted the single immutable provider
			// outcome for this attempt. Cancellation now changes only the runtime
			// terminal state; inserting a second outcome would violate the one-call,
			// one-outcome invariant.
			cancelled, persistErr := s.store.MarkCancelled(persistCtx, FailureCommand{
				InvocationID:          invocation.ID,
				DispatchAttemptID:     dispatchAttemptID,
				ClaimToken:            claimed.ClaimToken,
				ErrorCode:             "adapter_cancelled",
				OutcomeClassification: "cancelled_confirmed",
			}, s.config.OutboxMaxAttempts)
			if persistErr != nil {
				return DispatchResult{}, persistErr
			}
			return DispatchResult{Invocation: cancelled}, ErrCancellationRequested
		}
		failed, persistErr := s.store.FailAfterResponse(persistCtx, FailureCommand{
			InvocationID:          invocation.ID,
			DispatchAttemptID:     dispatchAttemptID,
			ClaimToken:            claimed.ClaimToken,
			ErrorCode:             "provider_cancelled_without_request",
			OutcomeClassification: "provider_cancelled_without_request",
		}, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, persistErr
		}
		return DispatchResult{Invocation: failed}, ErrResponseRejected
	}
	if reservationApplied {
		// The provider already answered successfully; a reconciliation
		// failure must never turn a successful call into a reported
		// failure. Mark it settled either way so the deferred Release
		// above never fires after a reconciliation attempt — retrying
		// reconciliation, not releasing, is the correct recovery for a
		// call that did happen.
		if costReservation.Subscription {
			// Subscription/token-plan billing (e.g. mimo): no real per-call
			// USD amount to reconcile against a wallet reservation that was
			// never made (see CostReservation.Subscription's doc comment).
			// Record the real, observable fact -- the call succeeded and
			// consumed plan resources -- via the optional
			// SubscriptionSettler capability, never via Reconcile (which
			// would try to resolve a PriceTier that does not and should not
			// exist for this provider).
			if settler, ok := s.costGate.(SubscriptionSettler); ok {
				if err := settler.RecordSubscriptionConsumption(persistCtx, costReservation, s.clock.Now()); err != nil {
					slog.Default().Error("cost gate subscription consumption recording failed after a successful provider call",
						"invocation_id", costReservation.InvocationID, "provider_id", costReservation.ProviderID,
						"provider_model_id", costReservation.ProviderModelID, "error", err)
				}
			}
		} else if reconcileErr := s.costGate.Reconcile(persistCtx, costReservation, normalized.Usage.InputTokens, 0, normalized.Usage.OutputTokens, s.clock.Now()); reconcileErr != nil {
			slog.Default().Error("cost gate reconciliation failed after a successful provider call",
				"invocation_id", costReservation.InvocationID, "provider_id", costReservation.ProviderID,
				"provider_model_id", costReservation.ProviderModelID, "error", reconcileErr)
		}
		reservationSettled = true
	}
	return s.store.CompleteInvocation(persistCtx, CompletionCommand{
		InvocationID:      invocation.ID,
		DispatchAttemptID: dispatchAttemptID,
		ClaimToken:        claimed.ClaimToken,
		Response:          normalized,
	}, s.config.OutboxMaxAttempts)
}

// settleNonSuccessReservation resolves the cost reservation for a call that
// is being recorded as anything other than the ordinary success path
// (which reconciles via normalized.Usage further down in Dispatch). It
// implements the P0 financial state machine fix: a call whose real,
// provider-reported token usage was recovered gets that cost committed as
// actual — financial_outcome=actual — even though the invocation itself is
// being recorded as failed/ambiguous/cancelled, because task success and
// financial settlement are independent facts (this is the behavior the
// original bug got backwards: AdapterFailureResponseReceived, proof the
// provider's HTTP response was received, was instead auto-released as if
// free). When no usage could be recovered, the reservation is left in
// place — never released — and annotated as
// financial_outcome=estimated_pending_reconciliation, using the
// already-computed conservative estimate from Reserve, because the
// provider may already have billed a call this process cannot prove it
// didn't.
//
// Sets *reservationSettled=true whenever it takes any action, so the
// deferred Release in Dispatch never also fires for the same reservation.
// A nil costGate or an unapplied reservation is a no-op, matching every
// other reservation-settling call site in Dispatch.
func (s *DispatchService) settleNonSuccessReservation(ctx context.Context, reservationSettled *bool, reservationApplied bool, reservation CostReservation, usage *Usage) {
	if !reservationApplied || s.costGate == nil {
		return
	}
	*reservationSettled = true
	settleCtx := context.WithoutCancel(ctx)
	now := s.clock.Now()
	if reservation.Subscription {
		// Same reasoning as the success path above: a subscription-billed
		// call that reached the provider (response received, transport
		// ambiguous, etc.) consumed real plan resources regardless of its
		// business outcome, but has no USD amount to reconcile/park at an
		// estimate -- there never was a wallet reservation for it.
		if settler, ok := s.costGate.(SubscriptionSettler); ok {
			if err := settler.RecordSubscriptionConsumption(settleCtx, reservation, now); err != nil {
				slog.Default().Error("cost gate subscription consumption recording failed for a non-success outcome",
					"invocation_id", reservation.InvocationID, "provider_id", reservation.ProviderID,
					"provider_model_id", reservation.ProviderModelID, "error", err)
			}
		}
		return
	}
	if usage != nil {
		if err := s.costGate.Reconcile(settleCtx, reservation, usage.InputTokens, 0, usage.OutputTokens, now); err != nil {
			slog.Default().Error("cost gate reconciliation failed for a call with recovered provider usage",
				"invocation_id", reservation.InvocationID, "provider_id", reservation.ProviderID,
				"provider_model_id", reservation.ProviderModelID, "error", err)
		}
		return
	}
	if err := s.costGate.MarkPendingReconciliation(settleCtx, reservation, now); err != nil {
		slog.Default().Error("cost gate could not mark reservation pending reconciliation",
			"invocation_id", reservation.InvocationID, "provider_id", reservation.ProviderID,
			"provider_model_id", reservation.ProviderModelID, "error", err)
	}
}

// recoveredUsage builds the Usage the provider's response envelope actually
// reported, when the adapter was able to recover one even while returning
// an error (see RawResponse.ProviderReported — the deepseek adapter now
// sets this, with real InputTokens/OutputTokens, on every
// AdapterFailureResponseReceived branch that fires after its own
// json.Unmarshal of the response body already succeeded, e.g.
// response_choice_count_invalid, response_content_invalid,
// tool_call_name_missing, response_content_filtered,
// response_truncated_empty — as opposed to response_read_failed/
// response_json_invalid/HTTP-error-status branches, where no usage object
// was ever decoded and ProviderReported stays false). Returns nil when
// nothing recoverable is known.
func recoveredUsage(invocationID, dispatchAttemptID int64, rawResponse RawResponse) *Usage {
	if !rawResponse.ProviderReported {
		return nil
	}
	return &Usage{
		InvocationID: invocationID, DispatchAttemptID: dispatchAttemptID,
		InputTokens: rawResponse.InputTokens, OutputTokens: rawResponse.OutputTokens,
		TotalTokens: rawResponse.InputTokens + rawResponse.OutputTokens, ProviderReported: true,
		PromptCacheHitTokens: rawResponse.PromptCacheHitTokens, PromptCacheMissTokens: rawResponse.PromptCacheMissTokens,
	}
}

// estimateTokenCount is a deliberately conservative, provider-agnostic
// approximation used only to size a pre-send worst-case cost reservation —
// it is never used to compute the real, billable cost, which comes from
// the provider's own reported token usage after the call completes. Three
// bytes per token overestimates token count for most real text, which
// overestimates the reservation, which can only reject a call the wallet
// or budget couldn't truly afford, never let one through it couldn't.
func estimateTokenCount(renderedContext []byte) int64 {
	const conservativeBytesPerToken = 3
	return int64(len(renderedContext))/conservativeBytesPerToken + 1
}
