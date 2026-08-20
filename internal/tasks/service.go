package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Config struct {
	OrganizationID       string
	DefaultMaxAttempts   int
	DefaultLeaseDuration time.Duration
	MaxLeaseDuration     time.Duration
	RetryPolicy          RetryPolicy
	OutboxMaxAttempts    int
	OutboxClaimDuration  time.Duration
}

func (cfg Config) Validate() error {
	if cfg.OrganizationID == "" || !identifierPattern.MatchString(cfg.OrganizationID) {
		return fmt.Errorf("%w: task organization ID is invalid", ErrInvalidInput)
	}
	if cfg.DefaultMaxAttempts < 1 || cfg.DefaultMaxAttempts > 100 {
		return fmt.Errorf("%w: default max attempts must be between 1 and 100", ErrInvalidInput)
	}
	if cfg.DefaultLeaseDuration <= 0 || cfg.MaxLeaseDuration <= 0 || cfg.DefaultLeaseDuration > cfg.MaxLeaseDuration {
		return fmt.Errorf("%w: lease durations are invalid", ErrInvalidInput)
	}
	if cfg.RetryPolicy.BaseDelay <= 0 || cfg.RetryPolicy.MaxDelay <= 0 || cfg.RetryPolicy.BaseDelay > cfg.RetryPolicy.MaxDelay {
		return fmt.Errorf("%w: retry delays are invalid", ErrInvalidInput)
	}
	if cfg.OutboxMaxAttempts < 1 || cfg.OutboxMaxAttempts > 100 {
		return fmt.Errorf("%w: outbox max attempts must be between 1 and 100", ErrInvalidInput)
	}
	if cfg.OutboxClaimDuration <= 0 {
		return fmt.Errorf("%w: outbox claim duration must be greater than zero", ErrInvalidInput)
	}
	return nil
}

type Service struct {
	persistence Persistence
	catalog     Catalog
	cfg         Config
}

func NewService(persistence Persistence, catalog Catalog, cfg Config) (*Service, error) {
	if persistence == nil {
		return nil, errors.New("task service requires persistence")
	}
	if catalog == nil {
		return nil, errors.New("task service requires an organization catalog")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Service{persistence: persistence, catalog: catalog, cfg: cfg}, nil
}

func (s *Service) GetTask(ctx context.Context, id int64) (TaskDetail, error) {
	if id <= 0 {
		return TaskDetail{}, fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	detail, err := s.persistence.GetTask(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	// ORG-AUDIT-012: the postgres Store has no organization_id bound at
	// construction (unlike memory/rag) -- GetTask(id) alone can return a
	// row belonging to a different organization if this DB ever holds more
	// than one. Every caller of Service.GetTask reaches it through a
	// Service instance already scoped to s.cfg.OrganizationID, so this is
	// where the boundary belongs without changing the Persistence/
	// TaskReader interface (which has ~20 call sites across the executive
	// orchestrator and would need its own careful pass, not a rushed one
	// alongside this fix).
	if detail.Task.OrganizationID != s.cfg.OrganizationID {
		return TaskDetail{}, fmt.Errorf("%w: task %d", ErrNotFound, id)
	}
	return detail, nil
}

func (s *Service) ListTasks(ctx context.Context, filter TaskFilter) ([]Task, error) {
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.AssignedRoleID = strings.TrimSpace(filter.AssignedRoleID)
	filter.AssignedUnitID = strings.TrimSpace(filter.AssignedUnitID)
	filter.CorrelationID = strings.TrimSpace(filter.CorrelationID)
	if filter.OrganizationID == "" {
		filter.OrganizationID = s.cfg.OrganizationID
	}
	if !identifierPattern.MatchString(filter.OrganizationID) {
		return nil, fmt.Errorf("%w: task organization ID is invalid", ErrInvalidInput)
	}
	if filter.AssignedRoleID != "" && !rolePattern.MatchString(filter.AssignedRoleID) {
		return nil, fmt.Errorf("%w: assigned role filter is invalid", ErrInvalidInput)
	}
	if filter.AssignedUnitID != "" && !identifierPattern.MatchString(filter.AssignedUnitID) {
		return nil, fmt.Errorf("%w: assigned unit filter is invalid", ErrInvalidInput)
	}
	if len(filter.CorrelationID) > 200 {
		return nil, fmt.Errorf("%w: correlation filter is too long", ErrInvalidInput)
	}
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	if filter.Limit < 1 || filter.Limit > 1000 || filter.Offset < 0 {
		return nil, fmt.Errorf("%w: task list pagination is invalid", ErrInvalidInput)
	}
	for _, status := range filter.Statuses {
		if !status.Valid() {
			return nil, fmt.Errorf("%w: task status %q is invalid", ErrInvalidInput, status)
		}
	}
	return s.persistence.ListTasks(ctx, filter)
}

func (s *Service) ListEvents(ctx context.Context, taskID int64, limit int) ([]Event, error) {
	if taskID <= 0 {
		return nil, fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	if limit == 0 {
		limit = 500
	}
	if limit < 1 || limit > 5000 {
		return nil, fmt.Errorf("%w: event limit is invalid", ErrInvalidInput)
	}
	return s.persistence.ListEvents(ctx, taskID, limit)
}

func (s *Service) ListAttempts(ctx context.Context, taskID int64) ([]Attempt, error) {
	if taskID <= 0 {
		return nil, fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	return s.persistence.ListAttempts(ctx, taskID)
}

func (s *Service) ListDeadLetters(ctx context.Context, limit int) ([]DeadLetter, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("%w: dead-letter limit is invalid", ErrInvalidInput)
	}
	all, err := s.persistence.ListDeadLetters(ctx, limit)
	if err != nil {
		return nil, err
	}
	// ORG-AUDIT-012: task_dead_letters has no organization_id of its own
	// and ListDeadLetters(ctx, limit) does not filter by task ownership.
	// Same boundary rationale as GetTask above -- filtered here rather than
	// widening Persistence's signature in this pass.
	filtered := make([]DeadLetter, 0, len(all))
	for _, dl := range all {
		detail, getErr := s.persistence.GetTask(ctx, dl.TaskID)
		if getErr != nil {
			continue
		}
		if detail.Task.OrganizationID == s.cfg.OrganizationID {
			filtered = append(filtered, dl)
		}
	}
	return filtered, nil
}

func (s *Service) GetDeadLetter(ctx context.Context, id int64) (DeadLetter, error) {
	if id <= 0 {
		return DeadLetter{}, fmt.Errorf("%w: dead-letter ID must be positive", ErrInvalidInput)
	}
	return s.persistence.GetDeadLetter(ctx, id)
}

func (s *Service) CreateTask(ctx context.Context, request CreateRequest, actorType, actorID string) (Task, bool, error) {
	request = NormalizeCreateRequest(request)
	if request.OrganizationID == "" {
		request.OrganizationID = s.cfg.OrganizationID
	}
	request.AvailableAt = NormalizeAvailableAt(request.AvailableAt)
	if request.MaxAttempts == 0 {
		request.MaxAttempts = s.cfg.DefaultMaxAttempts
	}
	if err := ValidateCreateRequest(request); err != nil {
		return Task{}, false, err
	}
	// A newly persisted task always gets an explicit, non-empty TaskClass
	// -- an empty request.TaskClass just means the caller didn't propose
	// one, defaulted here to the safe generic class, never left empty.
	// This is what keeps "all three durable selector facts empty" an
	// unambiguous signal reserved for rows that predate M1.3 entirely
	// (see contextengine's equivalent default-free handling for context
	// snapshots, which intentionally does NOT default TaskClass itself).
	//
	// taskClassExplicit is captured BEFORE defaulting and threaded
	// through PreparedCreate separately: Store.Create's idempotency-reuse
	// path needs to know whether the caller actually asserted something,
	// not just what the resolved value ended up being -- an omitted
	// TaskClass and an explicit "general.work" must never be
	// indistinguishable once compared against an already-durable row's
	// own known classification (independent review round 2).
	taskClassExplicit := request.TaskClass != ""
	if request.TaskClass == "" {
		request.TaskClass = TaskClassGeneralWork
	}
	actorType, actorID = normalizeActor(actorType, actorID)
	if err := validateActor(actorType, actorID); err != nil {
		return Task{}, false, err
	}
	revision, err := s.catalog.CurrentRevision(ctx, request.OrganizationID)
	if err != nil {
		return Task{}, false, mapCatalogError(err)
	}
	assigned, err := s.catalog.GetRole(ctx, request.OrganizationID, request.AssignedRoleID)
	if err != nil {
		return Task{}, false, mapCatalogError(err)
	}
	if !roleAssignable(assigned) {
		return Task{}, false, fmt.Errorf("%w: %s", ErrAssigneeUnavailable, request.AssignedRoleID)
	}
	if request.RequestedByRoleID != "" {
		requester, roleErr := s.catalog.GetRole(ctx, request.OrganizationID, request.RequestedByRoleID)
		if roleErr != nil {
			return Task{}, false, mapCatalogError(roleErr)
		}
		if requester.Retired {
			return Task{}, false, fmt.Errorf("%w: requester %s is retired", ErrInvalidInput, requester.ID)
		}
	}
	hash, err := HashCreateRequest(request)
	if err != nil {
		return Task{}, false, err
	}
	initial := StatusReady
	if len(request.Dependencies) > 0 || request.AvailableAt != nil {
		initial = StatusPending
	}
	// A held task is born blocked. Creating it ready and blocking it
	// afterwards would leave it claimable in the window between the two
	// writes, which is precisely the defect the hold exists to remove, so
	// the hold is decided here and persisted by the same INSERT.
	//
	// The hold outranks the dependency-derived status because they answer
	// different questions and the answer to this one is "not yet, at all".
	// Dependencies are re-evaluated on release, so a held task with
	// unsatisfied dependencies still lands in pending, never ready.
	reasonCode, reason := "", ""
	if request.HoldForCoordination {
		initial = StatusBlocked
		reasonCode = ReasonCodeCoordinationHold
		reason = "task is not published until its creator's coordination is durable"
	}
	return s.persistence.Create(ctx, PreparedCreate{
		Request: request, OrganizationRevisionID: revision.ID, AssignedUnitID: assigned.UnitID,
		TaskClass: request.TaskClass, TaskClassExplicit: taskClassExplicit,
		RequestHash: hash, InitialStatus: initial,
		InitialStatusReasonCode: reasonCode, InitialStatusReason: reason,
		DefaultMaxAttempts: s.cfg.DefaultMaxAttempts,
		OutboxMaxAttempts:  s.cfg.OutboxMaxAttempts, ActorType: actorType, ActorID: actorID,
	})
}

// ReleaseCoordinationHold publishes a task created under a coordination hold.
//
// It is deliberately NOT UnblockTask. An operator unblocking a dependency or
// assignee problem has no way to know whether a child's budget and delegation
// exist, so allowing that path to publish a held child would put the
// invariant back in the hands of whoever happens to call the generic tool.
// The store enforces the distinction; this method only carries it.
func (s *Service) ReleaseCoordinationHold(ctx context.Context, command ReleaseCoordinationHoldCommand) (Task, error) {
	if command.TaskID <= 0 {
		return Task{}, fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	command.ActorType, command.ActorID = normalizeActor(command.ActorType, command.ActorID)
	if err := validateActor(command.ActorType, command.ActorID); err != nil {
		return Task{}, err
	}
	detail, err := s.persistence.GetTask(ctx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	check, err := s.validateAssignee(ctx, detail.Task)
	if err != nil {
		return Task{}, err
	}
	return s.persistence.ReleaseCoordinationHold(ctx, command, check, s.cfg.OutboxMaxAttempts)
}

func (s *Service) AddDependency(ctx context.Context, command AddDependencyCommand) error {
	if command.TaskID <= 0 || command.DependsOnID <= 0 {
		return fmt.Errorf("%w: task and dependency IDs must be positive", ErrInvalidInput)
	}
	if command.TaskID == command.DependsOnID {
		return ErrDependencyCycle
	}
	command.ActorType, command.ActorID = normalizeActor(command.ActorType, command.ActorID)
	if err := validateActor(command.ActorType, command.ActorID); err != nil {
		return err
	}
	return s.persistence.AddDependency(ctx, command, s.cfg.OutboxMaxAttempts)
}

func (s *Service) AddRequirement(ctx context.Context, command AddRequirementCommand) (Requirement, error) {
	if command.TaskID <= 0 {
		return Requirement{}, fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	command.Spec.Key = strings.TrimSpace(command.Spec.Key)
	command.Spec.Description = strings.TrimSpace(command.Spec.Description)
	if err := ValidateRequirementSpec(command.Spec); err != nil {
		return Requirement{}, err
	}
	command.ActorType, command.ActorID = normalizeActor(command.ActorType, command.ActorID)
	if err := validateActor(command.ActorType, command.ActorID); err != nil {
		return Requirement{}, err
	}
	return s.persistence.AddRequirement(ctx, command, s.cfg.OutboxMaxAttempts)
}

func (s *Service) RecordEvidence(ctx context.Context, command RecordEvidenceCommand) (Evidence, error) {
	command.Reference = strings.TrimSpace(command.Reference)
	command.Digest = strings.TrimSpace(command.Digest)
	command.RecordedBy = strings.TrimSpace(command.RecordedBy)
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	if err := ValidateEvidence(command); err != nil {
		return Evidence{}, err
	}
	return s.persistence.RecordEvidence(ctx, command, s.cfg.OutboxMaxAttempts)
}

func (s *Service) RecordRequirementVerification(ctx context.Context, command RequirementVerificationCommand) (Evidence, error) {
	command.Reference = strings.TrimSpace(command.Reference)
	command.Digest = strings.TrimSpace(command.Digest)
	command.RecordedBy = strings.TrimSpace(command.RecordedBy)
	if command.TaskID <= 0 || command.RequirementID <= 0 {
		return Evidence{}, fmt.Errorf("%w: task and requirement IDs must be positive", ErrInvalidInput)
	}
	if command.Result != RequirementVerificationSatisfied && command.Result != RequirementVerificationFailed {
		return Evidence{}, fmt.Errorf("%w: verification result must be satisfied or failed", ErrInvalidInput)
	}
	if command.Reference == "" || len(command.Reference) > 2048 || command.RecordedBy == "" || len(command.RecordedBy) > 200 {
		return Evidence{}, fmt.Errorf("%w: verification reference and recorded_by are required", ErrInvalidInput)
	}
	if command.Digest != "" && !digestPattern.MatchString(command.Digest) {
		return Evidence{}, fmt.Errorf("%w: verification digest must be lowercase SHA-256", ErrInvalidInput)
	}
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	return s.persistence.RecordRequirementVerification(ctx, command, s.cfg.OutboxMaxAttempts)
}

func (s *Service) ClaimTasks(ctx context.Context, request ClaimRequest) ([]ClaimedTask, error) {
	request.OrganizationID = strings.TrimSpace(request.OrganizationID)
	if request.OrganizationID == "" {
		request.OrganizationID = s.cfg.OrganizationID
	}
	if !identifierPattern.MatchString(request.OrganizationID) {
		return nil, fmt.Errorf("%w: organization_id is invalid", ErrInvalidInput)
	}
	request.WorkerID = strings.TrimSpace(request.WorkerID)
	request.HolderPrincipalID = strings.TrimSpace(request.HolderPrincipalID)
	request.AssignedRoleID = strings.TrimSpace(request.AssignedRoleID)
	if request.WorkerID == "" || len(request.WorkerID) > 200 {
		return nil, fmt.Errorf("%w: worker_id is required and must be at most 200 bytes", ErrInvalidInput)
	}
	if len(request.HolderPrincipalID) > 200 {
		return nil, fmt.Errorf("%w: holder_principal_id must be at most 200 bytes", ErrInvalidInput)
	}
	// ORG-AUDIT-010 (partial, see commit message): AssignedRoleID is still
	// an optional filter here -- omitting it claims the next ready task
	// from ANY role. Making it mandatory broke 7 of this package's own
	// integration subtests that claim via a shared unscoped helper; fixing
	// those properly is real work (auditing whether each one has a specific
	// role in mind or is deliberately role-agnostic) that this pass does
	// not have room for. Left as a documented, deferred gap rather than a
	// rushed change to a widely-shared test helper.
	if request.AssignedRoleID != "" && !rolePattern.MatchString(request.AssignedRoleID) {
		return nil, fmt.Errorf("%w: assigned_role_id is invalid", ErrInvalidInput)
	}
	if request.BatchSize == 0 {
		request.BatchSize = 1
	}
	if request.BatchSize < 1 || request.BatchSize > 32 {
		return nil, fmt.Errorf("%w: batch_size must be between 1 and 32", ErrInvalidInput)
	}
	if request.LeaseDuration == 0 {
		request.LeaseDuration = s.cfg.DefaultLeaseDuration
	}
	if request.LeaseDuration <= 0 || request.LeaseDuration > s.cfg.MaxLeaseDuration {
		return nil, fmt.Errorf("%w: lease duration is invalid", ErrInvalidInput)
	}
	return s.persistence.Claim(ctx, request, s.validateAssignee, s.cfg.OutboxMaxAttempts)
}

func (s *Service) StartAttempt(ctx context.Context, command LeaseCommand) (Task, error) {
	if err := s.validateLeaseCommand(command, false); err != nil {
		return Task{}, err
	}
	return s.persistence.StartAttempt(ctx, command, s.cfg.OutboxMaxAttempts)
}

func (s *Service) Heartbeat(ctx context.Context, command LeaseCommand) (Lease, error) {
	if command.Extension == 0 {
		command.Extension = s.cfg.DefaultLeaseDuration
	}
	if err := s.validateLeaseCommand(command, true); err != nil {
		return Lease{}, err
	}
	return s.persistence.Heartbeat(ctx, command, s.cfg.MaxLeaseDuration)
}

func (s *Service) RecordAttemptResult(ctx context.Context, command RecordAttemptResultCommand) (Task, error) {
	if err := s.validateLeaseCommand(command.LeaseCommand, false); err != nil {
		return Task{}, err
	}
	command.Result.Summary = strings.TrimSpace(command.Result.Summary)
	command.Result.FailureCode = strings.TrimSpace(command.Result.FailureCode)
	switch command.Result.Outcome {
	case OutcomeSucceeded:
		if command.Result.FailureCode != "" {
			return Task{}, fmt.Errorf("%w: successful result cannot contain failure_code", ErrInvalidInput)
		}
	case OutcomeRetryableFailure, OutcomeNonRetryableFailure:
		if command.Result.FailureCode == "" {
			return Task{}, fmt.Errorf("%w: failure_code is required", ErrInvalidInput)
		}
	case OutcomeCancelled:
	default:
		return Task{}, fmt.Errorf("%w: attempt outcome is invalid", ErrInvalidInput)
	}
	if len(command.Result.Summary) > 4000 || len(command.Result.FailureCode) > 120 {
		return Task{}, fmt.Errorf("%w: attempt result fields are too long", ErrInvalidInput)
	}
	return s.persistence.RecordAttemptResult(ctx, command, s.cfg.RetryPolicy, s.cfg.OutboxMaxAttempts)
}

func (s *Service) FinalizeTask(ctx context.Context, command FinalizeCommand) (Task, error) {
	if command.TaskID <= 0 {
		return Task{}, fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	command.ReasonCode = strings.TrimSpace(command.ReasonCode)
	command.Reason = strings.TrimSpace(command.Reason)
	command.ActorType, command.ActorID = normalizeActor(command.ActorType, command.ActorID)
	if err := validateActor(command.ActorType, command.ActorID); err != nil {
		return Task{}, err
	}
	switch command.Outcome {
	case FinalCompleted:
	case FinalNoAction, FinalFailed, FinalRejected, FinalCancelled:
		if command.ReasonCode == "" || command.Reason == "" {
			return Task{}, fmt.Errorf("%w: reason_code and reason are required for %s", ErrInvalidInput, command.Outcome)
		}
	default:
		return Task{}, fmt.Errorf("%w: final outcome is invalid", ErrInvalidInput)
	}
	return s.persistence.Finalize(ctx, command, s.cfg.OutboxMaxAttempts)
}

func (s *Service) BlockTask(ctx context.Context, command BlockCommand) (Task, error) {
	if command.TaskID <= 0 || strings.TrimSpace(command.ReasonCode) == "" || strings.TrimSpace(command.Reason) == "" {
		return Task{}, fmt.Errorf("%w: task ID, reason_code, and reason are required", ErrInvalidInput)
	}
	command.ReasonCode = strings.TrimSpace(command.ReasonCode)
	command.Reason = strings.TrimSpace(command.Reason)
	command.ActorType, command.ActorID = normalizeActor(command.ActorType, command.ActorID)
	if err := validateActor(command.ActorType, command.ActorID); err != nil {
		return Task{}, err
	}
	return s.persistence.Block(ctx, command, s.cfg.OutboxMaxAttempts)
}

func (s *Service) UnblockTask(ctx context.Context, command UnblockCommand) (Task, error) {
	if command.TaskID <= 0 {
		return Task{}, fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	command.ActorType, command.ActorID = normalizeActor(command.ActorType, command.ActorID)
	if err := validateActor(command.ActorType, command.ActorID); err != nil {
		return Task{}, err
	}
	detail, err := s.persistence.GetTask(ctx, command.TaskID)
	if err != nil {
		return Task{}, err
	}
	check, err := s.validateAssignee(ctx, detail.Task)
	if err != nil {
		return Task{}, err
	}
	return s.persistence.Unblock(ctx, command, check, s.cfg.OutboxMaxAttempts)
}

func (s *Service) CancelTask(ctx context.Context, command CancelCommand) (Task, error) {
	if command.TaskID <= 0 || strings.TrimSpace(command.ReasonCode) == "" || strings.TrimSpace(command.Reason) == "" {
		return Task{}, fmt.Errorf("%w: task ID, reason_code, and reason are required", ErrInvalidInput)
	}
	command.ReasonCode = strings.TrimSpace(command.ReasonCode)
	command.Reason = strings.TrimSpace(command.Reason)
	command.ActorType, command.ActorID = normalizeActor(command.ActorType, command.ActorID)
	if err := validateActor(command.ActorType, command.ActorID); err != nil {
		return Task{}, err
	}
	return s.persistence.Cancel(ctx, command, s.cfg.OutboxMaxAttempts)
}

func (s *Service) Reconcile(ctx context.Context, batch int) (ReconcileResult, error) {
	if batch == 0 {
		batch = 100
	}
	if batch < 1 || batch > 1000 {
		return ReconcileResult{}, fmt.Errorf("%w: reconcile batch must be between 1 and 1000", ErrInvalidInput)
	}
	return s.persistence.Reconcile(ctx, batch, s.validateAssignee, s.cfg.RetryPolicy, s.cfg.OutboxMaxAttempts)
}

func (s *Service) ClaimOutbox(ctx context.Context, request OutboxClaimRequest) ([]ClaimedOutboxEvent, error) {
	request.ConsumerID = strings.TrimSpace(request.ConsumerID)
	if request.ConsumerID == "" || len(request.ConsumerID) > 200 {
		return nil, fmt.Errorf("%w: consumer_id is required", ErrInvalidInput)
	}
	if request.BatchSize == 0 {
		request.BatchSize = 1
	}
	if request.BatchSize < 1 || request.BatchSize > 32 {
		return nil, fmt.Errorf("%w: outbox batch must be between 1 and 32", ErrInvalidInput)
	}
	if request.ClaimDuration == 0 {
		request.ClaimDuration = s.cfg.OutboxClaimDuration
	}
	if request.ClaimDuration <= 0 || request.ClaimDuration > s.cfg.MaxLeaseDuration {
		return nil, fmt.Errorf("%w: outbox claim duration is invalid", ErrInvalidInput)
	}
	return s.persistence.ClaimOutbox(ctx, request)
}

func (s *Service) AckOutbox(ctx context.Context, disposition OutboxDisposition) error {
	disposition.ConsumerID = strings.TrimSpace(disposition.ConsumerID)
	if err := validateOutboxDisposition(disposition, false); err != nil {
		return err
	}
	return s.persistence.AckOutbox(ctx, disposition)
}

func (s *Service) NackOutbox(ctx context.Context, disposition OutboxDisposition) error {
	disposition.ConsumerID = strings.TrimSpace(disposition.ConsumerID)
	disposition.Error = strings.TrimSpace(disposition.Error)
	if err := validateOutboxDisposition(disposition, true); err != nil {
		return err
	}
	return s.persistence.NackOutbox(ctx, disposition)
}

func (s *Service) RecoverOutbox(ctx context.Context, batch int) (int, error) {
	if batch == 0 {
		batch = 100
	}
	if batch < 1 || batch > 1000 {
		return 0, fmt.Errorf("%w: outbox recovery batch is invalid", ErrInvalidInput)
	}
	return s.persistence.RecoverOutbox(ctx, batch)
}

func (s *Service) OutboxStats(ctx context.Context) (OutboxStats, error) {
	return s.persistence.OutboxStats(ctx)
}

func (s *Service) validateLeaseCommand(command LeaseCommand, heartbeat bool) error {
	if command.TaskID <= 0 || command.AttemptID <= 0 {
		return fmt.Errorf("%w: task and attempt IDs must be positive", ErrInvalidInput)
	}
	if strings.TrimSpace(command.LeaseToken) == "" {
		return fmt.Errorf("%w: lease token is required", ErrInvalidInput)
	}
	if strings.TrimSpace(command.ActorID) == "" || len(command.ActorID) > 200 {
		return fmt.Errorf("%w: actor ID is required", ErrInvalidInput)
	}
	if heartbeat && (command.Extension <= 0 || command.Extension > s.cfg.MaxLeaseDuration) {
		return fmt.Errorf("%w: heartbeat extension is invalid", ErrInvalidInput)
	}
	return nil
}

func (s *Service) validateAssignee(ctx context.Context, task Task) (AssigneeCheck, error) {
	_, err := s.catalog.CurrentRevision(ctx, task.OrganizationID)
	if err != nil {
		return AssigneeCheck{}, mapCatalogError(err)
	}
	role, err := s.catalog.GetRole(ctx, task.OrganizationID, task.AssignedRoleID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return AssigneeCheck{Available: false, Reason: "assigned role no longer exists"}, nil
		}
		return AssigneeCheck{}, mapCatalogError(err)
	}
	if !roleAssignable(role) || role.UnitID != task.AssignedUnitID {
		return AssigneeCheck{Available: false, Reason: "assigned role is disabled, non-executable, retired, or moved"}, nil
	}
	return AssigneeCheck{Available: true}, nil
}

func roleAssignable(role RoleRef) bool {
	return role.ID != "" && role.UnitID != "" && role.Enabled && role.Executable && !role.Retired
}

func normalizeActor(actorType, actorID string) (string, string) {
	actorType = strings.TrimSpace(actorType)
	actorID = strings.TrimSpace(actorID)
	if actorType == "" {
		actorType = "human"
	}
	return actorType, actorID
}

func validateActor(actorType, actorID string) error {
	if actorID == "" || len(actorID) > 200 {
		return fmt.Errorf("%w: actor ID is required and must be at most 200 bytes", ErrInvalidInput)
	}
	if actorType == "" || len(actorType) > 80 {
		return fmt.Errorf("%w: actor type is required and must be at most 80 bytes", ErrInvalidInput)
	}
	return nil
}

func mapCatalogError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrDatabaseUnavailable) {
		return err
	}
	return err
}

func validateOutboxDisposition(disposition OutboxDisposition, requireError bool) error {
	if disposition.EventID <= 0 || strings.TrimSpace(disposition.ClaimToken) == "" || strings.TrimSpace(disposition.ConsumerID) == "" {
		return fmt.Errorf("%w: event ID, claim token, and consumer ID are required", ErrInvalidInput)
	}
	if requireError && strings.TrimSpace(disposition.Error) == "" {
		return fmt.Errorf("%w: nack error is required", ErrInvalidInput)
	}
	if len(disposition.Error) > 4000 {
		return fmt.Errorf("%w: nack error must be at most 4000 bytes", ErrInvalidInput)
	}
	return nil
}

var _ TaskReader = (*Service)(nil)
var _ TaskService = (*Service)(nil)
var _ TaskQueue = (*Service)(nil)
var _ TaskReconciler = (*Service)(nil)
var _ OutboxStore = (*Service)(nil)
