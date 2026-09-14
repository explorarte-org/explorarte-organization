package ceochat

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// DefaultHistoryMessageLimit and MaxHistoryBytes bound how much prior
// conversation the model sees for a new turn. They are host-owned, not
// configurable by the model, and applied on top of whatever the Context
// Engine's own canonical instructions already carry.
const (
	DefaultHistoryMessageLimit = 32
	MaxHistoryBytes            = 64 << 10 // 64 KiB

	maxOwnerContentBytes = 32 << 10
	maxIdempotencyKeyLen = 120

	defaultLeaseDuration   = 10 * time.Minute
	defaultInvocationTTL   = 12 * time.Minute
	defaultMaxOutputTokens = 2000

	ceoChatWorkerID = "ceochat"
	ceoChatActorID  = "ceochat"
)

// ModelExecutorFactory builds the Harness's model boundary for one chat
// turn. It exists because the execution-contract instructions (the bounded
// conversation history) are per-turn while the provider stack underneath is
// opened once, at bootstrap -- exactly the same shape
// runtimeadapter.HarnessModelExecutorFactory uses for typed tasks.
type ModelExecutorFactory func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error)

// Service is the host-owned CEO chat service: CreateConversation, Send,
// History. It drives the same Task Engine, Execution Harness, and Model
// Runtime the Executive uses, under the executive/chat/v1 execution
// profile, and persists only the owner-visible conversation.
type Service struct {
	OrganizationID string

	Store      Store
	Tasks      TaskCoordinator
	Principals PrincipalResolver
	Contexts   ContextBuilder

	Authority        executionharness.ExecutionAuthorityPort
	HarnessHistory   executionharness.ExecutionHistoryStore
	DescriptorStore  executionharness.RunDescriptorStore
	NewModelExecutor ModelExecutorFactory
	// Catalog and ToolExecutor are the two Harness-facing ends of whatever
	// tool source this Service was composed with. Production always wires
	// both from the same *ToolRegistry (RegistryToolCatalog /
	// RegistryToolExecutor -- see bootstrap.Open), so "known to the
	// catalog" and "executed by the executor" can never diverge into two
	// different tool sets. Both are interfaces (not the concrete ceochat
	// ToolCatalog/ToolExecutor types) so a test may still compose the
	// narrower, research-only pair those types provide.
	Catalog      executionharness.ToolCatalog
	ToolExecutor executionharness.ToolExecutor
	// ToolDefinitions is the exact RunSpec.Tools set exposed to every chat
	// turn -- it must describe precisely the tools Catalog/ToolExecutor make
	// known and executable. Open defaults it to NewToolCatalog().Definitions()
	// only when Catalog itself is also left at that default; a Service
	// composed with any other Catalog (e.g. a *ToolRegistry-backed one) must
	// supply its own ToolDefinitions explicitly, because "known to the
	// catalog" and "exposed to this run" silently diverging is exactly the
	// failure ToolRegistry.DefinitionsFor's fail-closed design exists to
	// prevent.
	ToolDefinitions []executionharness.ToolDefinition

	Clock func() time.Time

	// LeaseDuration/InvocationTTL/MaxOutputTokens are conservative V1
	// defaults; zero values fall back to the package defaults in Open.
	LeaseDuration   time.Duration
	InvocationTTL   time.Duration
	MaxOutputTokens int
}

// Open validates and normalizes a Service. Every dependency is required
// except the three duration/token knobs, which default when zero.
func Open(service Service) (*Service, error) {
	s := service
	if strings.TrimSpace(s.OrganizationID) == "" {
		return nil, fmt.Errorf("%w: ceochat service requires an organization", ErrInvalidInput)
	}
	if s.Store == nil || s.Tasks == nil || s.Principals == nil || s.Contexts == nil ||
		s.Authority == nil || s.HarnessHistory == nil || s.DescriptorStore == nil || s.NewModelExecutor == nil {
		return nil, fmt.Errorf("%w: ceochat service dependencies are incomplete", ErrInvalidInput)
	}
	if s.Catalog == nil {
		defaultCatalog := NewToolCatalog()
		s.Catalog = defaultCatalog
		if s.ToolDefinitions == nil {
			s.ToolDefinitions = defaultCatalog.Definitions()
		}
	}
	if s.ToolExecutor == nil {
		return nil, fmt.Errorf("%w: ceochat service requires a tool executor", ErrInvalidInput)
	}
	if len(s.ToolDefinitions) == 0 {
		return nil, fmt.Errorf("%w: ceochat service requires tool definitions", ErrInvalidInput)
	}
	if s.Clock == nil {
		s.Clock = time.Now
	}
	if s.LeaseDuration <= 0 {
		s.LeaseDuration = defaultLeaseDuration
	}
	if s.InvocationTTL <= 0 {
		s.InvocationTTL = defaultInvocationTTL
	}
	if s.MaxOutputTokens <= 0 {
		s.MaxOutputTokens = defaultMaxOutputTokens
	}
	return &s, nil
}

func (s *Service) CreateConversation(ctx context.Context, request CreateConversationRequest) (Conversation, error) {
	if strings.TrimSpace(request.OwnerRoleID) == "" {
		return Conversation{}, fmt.Errorf("%w: owner role is required", ErrInvalidInput)
	}
	if request.ActorRoleID != request.OwnerRoleID {
		return Conversation{}, fmt.Errorf("%w: a conversation may only be opened by its own owner role", ErrUnauthorizedActor)
	}
	return s.Store.CreateConversation(ctx, Conversation{
		OrganizationID: s.OrganizationID,
		OwnerRoleID:    request.OwnerRoleID,
		Status:         ConversationActive,
	})
}

func (s *Service) History(ctx context.Context, request HistoryRequest) ([]Message, error) {
	conversation, err := s.Store.GetConversation(ctx, s.OrganizationID, request.ConversationID)
	if err != nil {
		return nil, err
	}
	if request.ActorRoleID != conversation.OwnerRoleID {
		return nil, fmt.Errorf("%w: actor %q is not the owner of conversation %d", ErrUnauthorizedActor, request.ActorRoleID, conversation.ID)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = DefaultHistoryMessageLimit
	}
	return s.Store.ListMessages(ctx, conversation.ID, limit)
}

// Send is the whole conversational pipeline: validate the actor, resolve or
// record the owner message under its idempotency key, create-or-reuse the
// turn's durable task, drive one Execution Harness run, and persist the
// assistant's final message only if that run actually completed.
//
// Every step is independently idempotent, so Send itself needs no separate
// "have I done this before" flag: calling it twice with the same
// (conversation, idempotency_key, content) reaches the same durable state
// by re-entering already-idempotent primitives, and Reused simply reports
// whether the owner message row already existed when this call started.
func (s *Service) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if err := validateSendRequest(request); err != nil {
		return SendResult{}, err
	}
	conversation, err := s.Store.GetConversation(ctx, s.OrganizationID, request.ConversationID)
	if err != nil {
		return SendResult{}, err
	}
	if request.ActorRoleID != conversation.OwnerRoleID {
		return SendResult{}, fmt.Errorf("%w: actor %q is not the owner of conversation %d", ErrUnauthorizedActor, request.ActorRoleID, conversation.ID)
	}

	taskKey := turnTaskIdempotencyKey(conversation.ID, request.IdempotencyKey)
	correlationID := conversationCorrelationID(conversation.ID)
	createdTask, _, err := s.Tasks.CreateTask(ctx, tasks.CreateRequest{
		AssignedRoleID:     CEORoleID,
		RequestedByRoleID:  request.ActorRoleID,
		TaskClass:          TaskClass,
		IdempotencyKey:     taskKey,
		Title:              "CEO chat turn",
		Instructions:       "Answer the owner's message using only the tools and context provided.",
		AcceptanceCriteria: []string{"produce a final answer to the owner, or a durable reason why not"},
		MaxAttempts:        1,
		CorrelationID:      correlationID,
		CausationID:        request.IdempotencyKey,
	}, "service", ceoChatActorID)
	if err != nil {
		return SendResult{}, fmt.Errorf("create ceochat turn task: %w", err)
	}

	ownerMessage, isNew, err := s.recordOwnerMessage(ctx, conversation, createdTask.ID, request, correlationID)
	if err != nil {
		return SendResult{}, err
	}

	if !isNew {
		if reply, found, err := s.Store.FindAssistantReply(ctx, ownerMessage.ID); err != nil {
			return SendResult{}, err
		} else if found {
			// The answer is durable, but that alone does not mean the turn
			// is DONE: driveTurn appends the message only AFTER
			// RecordAttemptResult (the Task Engine's own authoritative
			// lease/token/holder check) has already succeeded, but
			// FinalizeTask still runs after that append and can itself
			// fail, leaving the task short of its terminal Completed state.
			// Only report Completed once the task has actually converged
			// there; otherwise fall through so driveTurn's
			// StatusAwaitingVerification recovery can finish the job
			// instead of this call silently declaring victory while the
			// task is stuck.
			current, err := s.Tasks.GetTask(ctx, createdTask.ID)
			if err != nil {
				return SendResult{}, fmt.Errorf("read ceochat turn task: %w", err)
			}
			if current.Task.Status == tasks.StatusCompleted {
				return SendResult{Reused: true, Outcome: RunOutcomeCompleted, OwnerMessage: ownerMessage, AssistantMessage: &reply}, nil
			}
		}
	}

	return s.driveTurn(ctx, conversation, createdTask, ownerMessage, correlationID, !isNew)
}

func validateSendRequest(request SendRequest) error {
	switch {
	case request.ConversationID <= 0:
		return fmt.Errorf("%w: conversation ID is required", ErrInvalidInput)
	case strings.TrimSpace(request.ActorRoleID) == "":
		return fmt.Errorf("%w: actor role is required", ErrInvalidInput)
	case strings.TrimSpace(request.IdempotencyKey) == "" || len(request.IdempotencyKey) > maxIdempotencyKeyLen:
		return fmt.Errorf("%w: idempotency key must be 1..%d bytes", ErrInvalidInput, maxIdempotencyKeyLen)
	case strings.TrimSpace(request.Content) == "" || len(request.Content) > maxOwnerContentBytes:
		return fmt.Errorf("%w: message content must be 1..%d bytes", ErrInvalidInput, maxOwnerContentBytes)
	}
	return nil
}

// recordOwnerMessage inserts the owner row exactly once per (conversation,
// idempotency_key). A collision is resolved by reading the row that won it:
// identical content is a replay (isNew=false, no error); different content
// is a genuine conflict the caller must see.
func (s *Service) recordOwnerMessage(ctx context.Context, conversation Conversation, taskID int64, request SendRequest, correlationID string) (Message, bool, error) {
	message := Message{
		ConversationID: conversation.ID,
		OrganizationID: s.OrganizationID,
		Role:           MessageOwner,
		Content:        request.Content,
		IdempotencyKey: request.IdempotencyKey,
		TaskID:         taskID,
		CorrelationID:  correlationID,
		CausationID:    request.IdempotencyKey,
	}
	appended, err := s.Store.AppendMessage(ctx, message)
	if err == nil {
		return appended, true, nil
	}
	if !errors.Is(err, ErrIdempotencyConflict) {
		return Message{}, false, err
	}
	existing, found, findErr := s.Store.FindOwnerMessage(ctx, conversation.ID, request.IdempotencyKey)
	if findErr != nil {
		return Message{}, false, findErr
	}
	if !found {
		// The unique constraint fired but a concurrent reader cannot see the
		// row yet under this transaction's isolation; surface the original
		// conflict rather than fabricating a message.
		return Message{}, false, err
	}
	if existing.Content != request.Content {
		return Message{}, false, fmt.Errorf("%w: conversation %d key %q", ErrIdempotencyConflict, conversation.ID, request.IdempotencyKey)
	}
	return existing, false, nil
}

func (s *Service) driveTurn(ctx context.Context, conversation Conversation, task tasks.Task, ownerMessage Message, correlationID string, isReplay bool) (SendResult, error) {
	detail, err := s.Tasks.GetTask(ctx, task.ID)
	if err != nil {
		return SendResult{}, fmt.Errorf("read ceochat turn task: %w", err)
	}
	current := detail.Task

	switch current.Status {
	case tasks.StatusCompleted, tasks.StatusFailed, tasks.StatusDeadLetter, tasks.StatusNoAction, tasks.StatusRejected, tasks.StatusCancelled:
		// The task already reached a terminal state with no durable assistant
		// reply recorded (otherwise Send would have returned it already).
		// Nothing more to drive; report the outcome honestly instead of
		// re-claiming a task that cannot be claimed again.
		return SendResult{Reused: isReplay, Outcome: RunOutcomeIncomplete, OwnerMessage: ownerMessage}, nil
	case tasks.StatusAwaitingVerification:
		// RecordAttemptResult already succeeded on a prior attempt -- the
		// Task Engine has already confirmed that attempt's lease/token/
		// holder were valid -- but the turn did not converge any further
		// (the process crashed, or FinalizeTask itself failed). Recover
		// without re-claiming, without re-invoking the model, and without
		// re-running any tool: the Harness's own durable history already
		// has the answer that authoritative result was recorded for.
		return s.recoverAwaitingVerificationTurn(ctx, conversation, current, ownerMessage, correlationID, isReplay)
	case tasks.StatusLeased, tasks.StatusRunning:
		// A prior process claimed this attempt and this one holds no local
		// proof it owns that lease. Waiting for it is the correct answer --
		// see EXECUTIVE_ACTIVE_LEASE_BARRIER_FIX_V1: never adopt, never
		// start a second attempt beside an active lease this process did not
		// just claim itself.
		return SendResult{}, fmt.Errorf("%w: task %d attempt is active under another process", ErrRunNotReady, task.ID)
	case tasks.StatusReady:
		// fall through to claim below
	default:
		return SendResult{}, fmt.Errorf("%w: task %d is in status %q", ErrRunNotReady, task.ID, current.Status)
	}

	principalID, err := s.Principals.Resolve(ctx, CEORoleID)
	if err != nil {
		return SendResult{}, fmt.Errorf("resolve ceo execution principal: %w", err)
	}
	claimed, err := s.Tasks.ClaimTaskByID(ctx, task.ID, tasks.ClaimRequest{
		WorkerID: ceoChatWorkerID, HolderPrincipalID: principalID, AssignedRoleID: CEORoleID, LeaseDuration: s.LeaseDuration,
	})
	if err != nil {
		return SendResult{}, fmt.Errorf("claim ceochat turn task: %w", err)
	}
	if _, err = s.Tasks.StartAttempt(ctx, tasks.LeaseCommand{
		TaskID: claimed.Task.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: principalID,
	}); err != nil {
		return SendResult{}, fmt.Errorf("start ceochat turn attempt: %w", err)
	}

	priorMessages, err := s.Store.ListMessages(ctx, conversation.ID, DefaultHistoryMessageLimit+1)
	if err != nil {
		return SendResult{}, fmt.Errorf("read conversation history: %w", err)
	}
	// ownerMessage was already appended by recordOwnerMessage above, so it is
	// the tail of priorMessages; exclude it here so the contract text does
	// not duplicate the new message inside the "history" block.
	priorMessages = excludeMessage(priorMessages, ownerMessage.ID)
	contract := renderConversationContract(priorMessages, ownerMessage)

	snapshot, err := s.Contexts.Build(ctx, ContextRequest{
		ActorRoleID:            CEORoleID,
		OrganizationRevisionID: claimed.Task.OrganizationRevisionID,
		TaskRef:                "task:" + strconv.FormatInt(claimed.Task.ID, 10),
		CorrelationID:          correlationID,
		CausationID:            ownerMessage.IdempotencyKey,
		IdempotencyKey:         turnTaskIdempotencyKey(conversation.ID, ownerMessage.IdempotencyKey),
	})
	if err != nil {
		return SendResult{}, fmt.Errorf("build ceochat context snapshot: %w", err)
	}

	models, err := s.NewModelExecutor(modelruntimeadapter.Config{
		MaxOutputTokens:               s.MaxOutputTokens,
		ThinkingMode:                  modelruntime.ThinkingOpaque,
		InvocationTTL:                 s.InvocationTTL,
		OutputMode:                    modelruntime.OutputText,
		ExecutionContractInstructions: contract,
		Purpose:                       "executive.ceo_chat",
	})
	if err != nil {
		return SendResult{}, fmt.Errorf("build ceochat model executor: %w", err)
	}
	runtime, err := executionharness.NewWithDescriptorStore(s.Authority, models, s.Catalog, s.ToolExecutor, s.HarnessHistory, s.DescriptorStore)
	if err != nil {
		return SendResult{}, fmt.Errorf("build ceochat harness runtime: %w", err)
	}

	spec := executionharness.RunSpec{
		Identity: executionharness.RunIdentity{
			RunID:                turnRunID(claimed.Task.ID),
			OrganizationID:       s.OrganizationID,
			TaskID:               claimed.Task.ID,
			AttemptID:            claimed.Attempt.ID,
			RoleID:               CEORoleID,
			ExecutionPrincipalID: principalID,
			CorrelationID:        correlationID,
			CausationID:          ownerMessage.IdempotencyKey,
		},
		LeaseToken: claimed.LeaseToken,
		Context: executionharness.InitialContext{
			ID:      strconv.FormatInt(snapshot.ID, 10),
			Version: snapshot.Version,
			Digest:  snapshot.Digest,
			Content: snapshot.Content,
		},
		Tools: s.ToolDefinitions,
		Policy: executionharness.RunPolicy{
			MaxTurns:           MaxTurns,
			MaxToolCalls:       MaxToolCalls,
			ExecutionProfileID: ExecutionProfileID,
			ModelPolicyRef:     ModelPolicyRef,
		},
	}
	result := runtime.Execute(ctx, spec)

	if result.Status == executionharness.StatusAuthorityUnavailable {
		// Nothing was appended, no model or tool call happened. The attempt
		// is left exactly as it was so the same run identity resumes here.
		return SendResult{}, fmt.Errorf("%w: execution authority unavailable for task %d", ErrRunNotReady, task.ID)
	}

	if result.Status != executionharness.StatusCompleted {
		// MaxAttempts=1 (set at task creation): a non-retryable outcome here
		// already carries the task to its terminal dead_letter state on its
		// own. Calling FinalizeTask afterward is not just redundant, it is
		// invalid -- FinalizeTask's explicit failed path requires
		// awaiting_verification (the gated-completion state a SUCCESS
		// leaves the task in), which a running failure never reaches.
		if _, err = s.Tasks.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{
			LeaseCommand: tasks.LeaseCommand{TaskID: claimed.Task.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: principalID},
			Result:       tasks.AttemptResult{Outcome: tasks.OutcomeNonRetryableFailure, FailureCode: string(result.Status), Summary: result.TerminationReason},
		}); err != nil {
			return SendResult{}, fmt.Errorf("record ceochat turn failure: %w", err)
		}
		return SendResult{Reused: isReplay, Outcome: RunOutcomeIncomplete, OwnerMessage: ownerMessage, TurnsUsed: result.TurnsUsed, ToolCallsUsed: result.ToolCallsUsed}, nil
	}

	// RecordAttemptResult runs FIRST, deliberately: it is the Task Engine's
	// own authoritative check that this attempt's lease is still active,
	// its token matches, and its holder is the actor -- exactly the check
	// that must gate any durable answer, not follow it. Only once that
	// authority is confirmed durable is it safe to persist the answer.
	if _, err = s.Tasks.RecordAttemptResult(ctx, tasks.RecordAttemptResultCommand{
		LeaseCommand: tasks.LeaseCommand{TaskID: claimed.Task.ID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: principalID},
		Result:       tasks.AttemptResult{Outcome: tasks.OutcomeSucceeded, Summary: "ceo chat turn answered"},
	}); err != nil {
		return SendResult{}, fmt.Errorf("record ceochat turn result: %w", err)
	}

	// The task is now durably awaiting_verification with a confirmed
	// authoritative result. If the process crashes at any point from here
	// on -- before this append, or before FinalizeTask below -- a retry
	// lands in driveTurn's StatusAwaitingVerification case, which recovers
	// this exact answer from the Harness's own durable history (never
	// re-invoking the model) and completes whichever of append/finalize is
	// still outstanding. Neither step is skipped on a crash: retrying
	// converges the durable answer, the confirmed authority, and the
	// task's terminal state together, rather than leaving any one of them
	// behind.
	assistantMessage, err := s.Store.AppendMessage(ctx, Message{
		ConversationID: conversation.ID,
		OrganizationID: s.OrganizationID,
		Role:           MessageAssistant,
		Content:        result.FinalOutput,
		TaskID:         claimed.Task.ID,
		AttemptID:      claimed.Attempt.ID,
		RunID:          spec.Identity.RunID,
		CorrelationID:  correlationID,
		CausationID:    ownerMessage.IdempotencyKey,
	})
	if err != nil {
		return SendResult{}, fmt.Errorf("persist ceochat assistant message: %w", err)
	}
	if _, err = s.Tasks.FinalizeTask(ctx, tasks.FinalizeCommand{
		TaskID: claimed.Task.ID, Outcome: tasks.FinalCompleted, ActorType: "service", ActorID: ceoChatActorID,
	}); err != nil {
		return SendResult{}, fmt.Errorf("finalize ceochat turn: %w", err)
	}

	return SendResult{
		Reused: isReplay, Outcome: RunOutcomeCompleted, OwnerMessage: ownerMessage, AssistantMessage: &assistantMessage,
		TurnsUsed: result.TurnsUsed, ToolCallsUsed: result.ToolCallsUsed,
	}, nil
}

// recoverAwaitingVerificationTurn completes a turn whose authoritative
// RecordAttemptResult already succeeded (the task is awaiting_verification)
// but did not converge any further -- AppendMessage and/or FinalizeTask are
// still outstanding, most likely because the process crashed between them
// or FinalizeTask itself failed transiently. It recovers the final answer
// from the Harness's own durable event history (the ModelResult of the
// last EventModelResponseRecorded before EventRunCompleted), never
// re-invoking the model or a tool, appends it only if not already durable,
// and always retries FinalizeTask so the task keeps converging to
// Completed on every call until it actually gets there.
func (s *Service) recoverAwaitingVerificationTurn(ctx context.Context, conversation Conversation, task tasks.Task, ownerMessage Message, correlationID string, isReplay bool) (SendResult, error) {
	runID := turnRunID(task.ID)
	events, err := s.HarnessHistory.Read(ctx, runID)
	if err != nil {
		return SendResult{}, fmt.Errorf("read ceochat harness history for recovery: %w", err)
	}
	finalOutput, attemptID, ok := recoverCompletedFinalOutput(events)
	if !ok {
		// RecordAttemptResult(Succeeded) was called, which driveTurn only
		// ever does after observing StatusCompleted for this exact run --
		// so the Harness history not agreeing is a genuine inconsistency
		// this service cannot resolve on its own. Surface it rather than
		// fabricate a completion.
		return SendResult{}, fmt.Errorf("%w: task %d is awaiting_verification with no recoverable final answer in its Harness history", ErrRunNotReady, task.ID)
	}

	assistantMessage, found, err := s.Store.FindAssistantReply(ctx, ownerMessage.ID)
	if err != nil {
		return SendResult{}, err
	}
	if !found {
		assistantMessage, err = s.Store.AppendMessage(ctx, Message{
			ConversationID: conversation.ID,
			OrganizationID: s.OrganizationID,
			Role:           MessageAssistant,
			Content:        finalOutput,
			TaskID:         task.ID,
			AttemptID:      attemptID,
			RunID:          runID,
			CorrelationID:  correlationID,
			CausationID:    ownerMessage.IdempotencyKey,
		})
		if err != nil {
			return SendResult{}, fmt.Errorf("persist recovered ceochat assistant message: %w", err)
		}
	}

	if _, err = s.Tasks.FinalizeTask(ctx, tasks.FinalizeCommand{
		TaskID: task.ID, Outcome: tasks.FinalCompleted, ActorType: "service", ActorID: ceoChatActorID,
	}); err != nil {
		return SendResult{}, fmt.Errorf("finalize recovered ceochat turn: %w", err)
	}

	return SendResult{Reused: isReplay, Outcome: RunOutcomeCompleted, OwnerMessage: ownerMessage, AssistantMessage: &assistantMessage}, nil
}

// recoverCompletedFinalOutput scans a run's durable event history for its
// final answer: the ModelResult of the last EventModelResponseRecorded
// whose FinishReason is FinishFinal, confirmed by an EventRunCompleted
// event with TerminalStatus=StatusCompleted also being present. ok is
// false if the history does not actually show a completed run -- a
// deliberately conservative signal, never a best-effort guess.
func recoverCompletedFinalOutput(events []executionharness.Event) (finalOutput string, attemptID int64, ok bool) {
	completed := false
	for _, event := range events {
		if event.Type == executionharness.EventRunCompleted && event.TerminalStatus == executionharness.StatusCompleted {
			completed = true
		}
		if event.Type == executionharness.EventModelResponseRecorded && event.ModelResult != nil && event.ModelResult.FinishReason == executionharness.FinishFinal {
			finalOutput = event.ModelResult.FinalOutput
			attemptID = event.AttemptID
		}
	}
	return finalOutput, attemptID, completed && finalOutput != ""
}

func excludeMessage(messages []Message, id int64) []Message {
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.ID == id {
			continue
		}
		out = append(out, message)
	}
	return out
}

func turnTaskIdempotencyKey(conversationID int64, idempotencyKey string) string {
	return "ceochat:turn:" + strconv.FormatInt(conversationID, 10) + ":" + idempotencyKey
}

func conversationCorrelationID(conversationID int64) string {
	return "ceochat:" + strconv.FormatInt(conversationID, 10)
}

func turnRunID(taskID int64) string {
	return "ceochat-turn-" + strconv.FormatInt(taskID, 10)
}

// renderConversationContract renders the bounded prior history plus the new
// owner message as one execution-contract instruction, delivered to the
// model as a stable-prefix message AFTER the byte-exact context snapshot
// render (see modelruntimeadapter.Config.ExecutionContractInstructions).
// Bounded by both message count and total bytes; the oldest messages are
// dropped first when the byte bound would otherwise be exceeded.
func renderConversationContract(prior []Message, newMessage Message) string {
	bounded := prior
	if len(bounded) > DefaultHistoryMessageLimit {
		bounded = bounded[len(bounded)-DefaultHistoryMessageLimit:]
	}
	var lines []string
	total := 0
	for i := len(bounded) - 1; i >= 0; i-- {
		line := fmt.Sprintf("[%s]: %s", bounded[i].Role, bounded[i].Content)
		total += len(line) + 1
		if total > MaxHistoryBytes {
			break
		}
		lines = append([]string{line}, lines...)
	}
	var b strings.Builder
	b.WriteString("Conversation so far (bounded, oldest first):\n")
	if len(lines) == 0 {
		b.WriteString("(no prior messages)\n")
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("\n[owner] (new message):\n")
	b.WriteString(newMessage.Content)
	return b.String()
}
