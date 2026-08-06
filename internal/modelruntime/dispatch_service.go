package modelruntime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

type DispatchService struct {
	config          RuntimeConfig
	catalog         OrganizationCatalog
	tasks           TaskAttemptReader
	contexts        ContextReader
	evaluator       CapabilityEvaluator
	policyCatalog   EgressPolicyCatalog
	egressEvaluator EgressDecisionEvaluator
	egressStore     EgressEvaluationStore
	store           Store
	adapters        AdapterRegistry
	normalizer      Normalizer
	clock           Clock
}

func NewDispatchService(config RuntimeConfig, catalog OrganizationCatalog, tasks TaskAttemptReader, contexts ContextReader, evaluator CapabilityEvaluator, policyCatalog EgressPolicyCatalog, egressEvaluator EgressDecisionEvaluator, egressStore EgressEvaluationStore, store Store, adapters AdapterRegistry, clock Clock) (*DispatchService, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if catalog == nil || tasks == nil || contexts == nil || evaluator == nil || policyCatalog == nil || egressEvaluator == nil || egressStore == nil || store == nil || adapters == nil {
		return nil, fmt.Errorf("dispatch service dependencies are incomplete")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &DispatchService{
		config: config, catalog: catalog, tasks: tasks, contexts: contexts,
		evaluator: evaluator, policyCatalog: policyCatalog, egressEvaluator: egressEvaluator,
		egressStore: egressStore, store: store, adapters: adapters,
		normalizer: Normalizer{MaxResponseBytes: config.MaxResponseBytes, MaxToolIntents: config.MaxToolIntents},
		clock:      clock,
	}, nil
}

func (s *DispatchService) Dispatch(ctx context.Context, invocationID int64, claimedBy string) (DispatchResult, error) {
	if !s.config.Enabled {
		return DispatchResult{}, ErrDisabled
	}
	claimedBy = strings.TrimSpace(claimedBy)
	if invocationID <= 0 || claimedBy == "" || len(claimedBy) > 200 {
		return DispatchResult{}, fmt.Errorf("%w: invocation ID and claimant are required", ErrInvalidRequest)
	}
	claimed, err := s.store.ClaimInvocation(ctx, ClaimCommand{InvocationID: invocationID, ClaimedBy: claimedBy}, s.config)
	if err != nil {
		return DispatchResult{}, err
	}
	invocation := claimed.Invocation
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
		AttemptID: invocation.AttemptID, DispatchActorRoleID: invocation.DispatchActorRoleID,
		SubjectRoleID: invocation.SubjectRoleID, ContextSnapshotID: invocation.ContextSnapshotID,
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
	actionDigest, err := ActionDigest(invocation)
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

	providerAdapter, ok := s.adapters.Get(invocation.ProviderID)
	if !binding.Version.DispatchEnabled || binding.Version.AdapterStatus != AdapterAvailable || !binding.Provider.DispatchEnabled || binding.Provider.AdapterStatus != AdapterAvailable || !ok || providerAdapter == nil || providerAdapter.ProviderID() != invocation.ProviderID {
		// An allow decision becomes durable only together with send_started. The
		// adapter gate is still before rendering and before that atomic barrier.
		return failBeforeSend("adapter_unavailable", ErrProviderUnavailable, AuditInvocationFailed)
	}
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
	renderedContext, err := s.contexts.RenderContextSnapshot(ctx, invocation.ContextSnapshotID)
	if err != nil {
		return failBeforeSend("context_render_failed", err, AuditInvocationFailed)
	}
	renderedHash := SHA256Bytes(renderedContext)
	if renderedHash != snapshot.RenderedHash {
		return failBeforeSend("context_render_hash_mismatch", ErrContextRejected, AuditInvocationFailed)
	}
	allowEvaluation.DecisionHash = modelegress.DecisionHash(allowEvaluation)
	providerIdempotencyKeyHash := SHA256Bytes([]byte(fmt.Sprintf("%s:%d:%d", invocation.OrganizationID, invocation.ID, dispatchAttemptID)))
	if err = s.egressStore.PersistPreSendAllowAndMarkSendStarted(persistCtx, modelegress.PersistAllowCommand{
		Evaluation: allowEvaluation, ClaimToken: claimed.ClaimToken,
		ProviderIdempotencyKeyHash: providerIdempotencyKeyHash, Deadline: invocation.Deadline,
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
	rawResponse, adapterErr := providerAdapter.Dispatch(dispatchCtx, CanonicalRequest{
		InvocationID:           invocation.ID,
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
	})
	cancelDispatch()
	<-watchDone

	if adapterErr != nil {
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
				}, s.config.OutboxMaxAttempts)
				if persistErr != nil {
					return DispatchResult{}, errors.Join(adapterErr, persistErr)
				}
				return DispatchResult{Invocation: cancelled}, ErrCancellationRequested
			}
		}
		errorCode := "adapter_error_after_send"
		eventType := AuditInvocationAmbiguous
		if errors.Is(adapterErr, context.DeadlineExceeded) {
			errorCode = "provider_timeout_after_send"
			eventType = AuditInvocationTimedOut
		}
		ambiguous, persistErr := s.store.MarkAmbiguous(persistCtx, FailureCommand{
			InvocationID:          invocation.ID,
			DispatchAttemptID:     dispatchAttemptID,
			ClaimToken:            claimed.ClaimToken,
			ErrorCode:             errorCode,
			OutcomeClassification: "ambiguous_external_outcome",
		}, eventType, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, errors.Join(adapterErr, persistErr)
		}
		return DispatchResult{Invocation: ambiguous}, fmt.Errorf("%w: %v", ErrAmbiguousOutcome, adapterErr)
	}

	invocation, err = s.store.MarkResponseReceived(persistCtx, invocation.ID, dispatchAttemptID, claimed.ClaimToken, rawResponse.ProviderRequestID)
	if err != nil {
		// The provider returned, so this process must never issue another call. A later
		// reconcile classifies the durable send_started state without dispatching.
		return DispatchResult{}, err
	}
	normalized, err := s.normalizer.Normalize(invocation, dispatchAttemptID, rawResponse)
	if err != nil {
		failed, persistErr := s.store.FailAfterResponse(persistCtx, FailureCommand{
			InvocationID:          invocation.ID,
			DispatchAttemptID:     dispatchAttemptID,
			ClaimToken:            claimed.ClaimToken,
			ErrorCode:             "response_normalization_failed",
			OutcomeClassification: "response_received_rejected",
		}, s.config.OutboxMaxAttempts)
		if persistErr != nil {
			return DispatchResult{}, errors.Join(err, persistErr)
		}
		return DispatchResult{Invocation: failed}, err
	}
	if normalized.CancellationConfirmed {
		requested, requestErr := s.store.CancellationRequested(persistCtx, invocation.ID)
		if requestErr != nil {
			return DispatchResult{}, requestErr
		}
		if requested {
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
	return s.store.CompleteInvocation(persistCtx, CompletionCommand{
		InvocationID:      invocation.ID,
		DispatchAttemptID: dispatchAttemptID,
		ClaimToken:        claimed.ClaimToken,
		Response:          normalized,
	}, s.config.OutboxMaxAttempts)
}
