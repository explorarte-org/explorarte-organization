package modelruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

type InvocationService struct {
	organizationID         string
	catalog                OrganizationCatalog
	tasks                  TaskAttemptReader
	contexts               ContextReader
	store                  Store
	egress                 EgressPolicyCatalog
	identity               ExecutionIdentityPolicyCatalog
	assignments            modeldispatch.AssignmentResolver
	clock                  Clock
	outboxMaxAttempts      int
	singleProviderTestMode bool
}

func NewInvocationService(organizationID string, catalog OrganizationCatalog, tasks TaskAttemptReader, contexts ContextReader, store Store, egress EgressPolicyCatalog, identity ExecutionIdentityPolicyCatalog, assignments modeldispatch.AssignmentResolver, clock Clock, outboxMaxAttempts int, singleProviderTestMode bool) (*InvocationService, error) {
	if catalog == nil || tasks == nil || contexts == nil || store == nil || egress == nil || identity == nil || assignments == nil {
		return nil, fmt.Errorf("invocation service dependencies are incomplete")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &InvocationService{organizationID: organizationID, catalog: catalog, tasks: tasks, contexts: contexts, store: store, egress: egress, identity: identity, assignments: assignments, clock: clock, outboxMaxAttempts: outboxMaxAttempts, singleProviderTestMode: singleProviderTestMode}, nil
}

func (s *InvocationService) Create(ctx context.Context, command CreateInvocationCommand) (CreateInvocationResult, error) {
	if command.OrganizationID == "" {
		command.OrganizationID = s.organizationID
	}
	prepared, caps, schema, err := PrepareCreateCommand(command, s.clock.Now())
	if err != nil {
		return CreateInvocationResult{}, err
	}
	org, err := s.catalog.CurrentOrganization(ctx, prepared.OrganizationID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	task, err := s.tasks.GetTaskAttempt(ctx, prepared.TaskID, prepared.AttemptID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	if err = validateTaskAttempt(task, prepared, s.clock.Now()); err != nil {
		return CreateInvocationResult{}, err
	}
	if task.OrganizationRevisionID != org.RevisionID {
		return CreateInvocationResult{}, fmt.Errorf("%w: task organization revision drift", ErrTaskAttemptRejected)
	}
	resolved, err := s.assignments.ResolveActive(ctx, prepared.OrganizationID, prepared.TaskID, prepared.AttemptID, prepared.SubjectRoleID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	if err = validateAssignmentForCreation(resolved, org.RevisionID, s.clock.Now()); err != nil {
		return CreateInvocationResult{}, err
	}
	dispatchActorRole, err := s.catalog.GetRole(ctx, prepared.OrganizationID, resolved.Principal.DispatchActorRoleID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	if !dispatchActorRole.Enabled || !dispatchActorRole.Executable || dispatchActorRole.AuthorityClass != "execution_service" {
		return CreateInvocationResult{}, fmt.Errorf("%w: dispatch actor role is not an eligible execution service", ErrTaskAttemptRejected)
	}
	subject, err := s.catalog.GetRole(ctx, prepared.OrganizationID, prepared.SubjectRoleID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	if !subject.Enabled || !subject.Executable {
		return CreateInvocationResult{}, fmt.Errorf("%w: subject role is not executable", ErrTaskAttemptRejected)
	}
	snapshot, err := s.contexts.GetContextSnapshot(ctx, prepared.ContextSnapshotID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	if err = validateContext(snapshot, prepared, org.RevisionID); err != nil {
		return CreateInvocationResult{}, err
	}
	if err = s.contexts.ValidateContextSnapshot(ctx, prepared.ContextSnapshotID); err != nil {
		return CreateInvocationResult{}, fmt.Errorf("%w: %v", ErrContextRejected, err)
	}
	binding, err := s.store.GetBinding(ctx, prepared.OrganizationID, org.RevisionID, prepared.SubjectRoleID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	if !capabilitiesSatisfy(binding.Capabilities.Capabilities, caps) {
		return CreateInvocationResult{}, fmt.Errorf("%w: profile lacks requested capabilities", ErrCapabilityMismatch)
	}
	if reason, allowed := modelegress.ValidateExecutiveScope(
		binding.Version.ProviderID,
		string(binding.Version.Transport),
		snapshot.DataClasses,
		snapshot.ExecutiveScope,
		s.singleProviderTestMode,
	); !allowed {
		return CreateInvocationResult{}, fmt.Errorf("%w: %s", ErrEgressDenied, reason)
	}
	policy, err := s.egress.ResolveForRevision(ctx, prepared.OrganizationID, org.RevisionID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	if err = validateResolvedEgressPolicy(policy, org); err != nil {
		return CreateInvocationResult{}, err
	}
	identityPolicy, err := s.identity.ResolveActive(ctx, prepared.OrganizationID)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	if identityPolicy.Version.Status != "active" {
		return CreateInvocationResult{}, ErrExecutionIdentityUnpinned
	}
	hash, err := invocationRequestHash(prepared, org.RevisionID, binding, caps, schema, policy.Version.ID, policy.CanonicalHash, identityPolicy.Version.ID, identityPolicy.Version.CanonicalHash, resolved)
	if err != nil {
		return CreateInvocationResult{}, err
	}
	return s.store.CreateInvocation(ctx, PreparedInvocation{Command: prepared, OrganizationRevisionID: org.RevisionID, Binding: binding, RequestHash: hash, RequiredCapabilities: caps, OutputSchema: schema, EgressPolicy: policy, IdentityPolicy: identityPolicy, Assignment: resolved}, s.outboxMaxAttempts)
}

func (s *InvocationService) Get(ctx context.Context, id int64) (Invocation, error) {
	if id <= 0 {
		return Invocation{}, fmt.Errorf("%w: invalid invocation ID", ErrInvalidRequest)
	}
	return s.store.GetInvocation(ctx, id)
}

func (s *InvocationService) List(ctx context.Context, limit int) ([]Invocation, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.store.ListInvocations(ctx, s.organizationID, limit)
}

func (s *InvocationService) Cancel(ctx context.Context, id int64, actor, reason string) (CancelResult, error) {
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if id <= 0 || !roleIDPattern.MatchString(actor) {
		return CancelResult{}, fmt.Errorf("%w: invocation ID and actor role are required", ErrInvalidRequest)
	}
	if len(reason) > 4000 {
		return CancelResult{}, fmt.Errorf("%w: cancellation reason is too long", ErrInvalidRequest)
	}
	invocation, err := s.store.GetInvocation(ctx, id)
	if err != nil {
		return CancelResult{}, err
	}
	if invocation.OrganizationID != s.organizationID || invocation.DispatchActorRoleID != actor {
		return CancelResult{}, fmt.Errorf("%w: actor cannot cancel this invocation", ErrAuthorizationDenied)
	}
	return s.store.RequestCancellation(ctx, id, actor, reason, s.outboxMaxAttempts)
}

func (s *InvocationService) Reconcile(ctx context.Context, batch int) (ReconcileResult, error) {
	if batch <= 0 {
		batch = 100
	}
	return s.store.Reconcile(ctx, s.organizationID, batch, s.outboxMaxAttempts)
}
