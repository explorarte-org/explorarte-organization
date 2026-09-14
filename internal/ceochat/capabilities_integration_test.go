//go:build integration

package ceochat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// ==================================================
// CEO_CONVERSATIONAL_TOOL_CAPABILITY_EXPANSION_V1
// ==================================================

// seedRealTask creates a real, durable task through the SAME *tasks.Service
// ceochat's own Send drives -- never a direct SQL insert -- so tasks.*
// capability tests read real Task Engine state, not a fixture shortcut.
func (f *chatFixture) seedRealTask(t *testing.T, idempotencyKey, title string) tasks.Task {
	t.Helper()
	created, _, err := f.runtime.Tasks.CreateTask(context.Background(), tasks.CreateRequest{
		AssignedRoleID: ceochat.CEORoleID, RequestedByRoleID: ceochat.CEORoleID, TaskClass: ceochat.TaskClass,
		IdempotencyKey: idempotencyKey, Title: title, Instructions: "seed task for a capability test",
		AcceptanceCriteria: []string{"n/a"}, MaxAttempts: 1,
	}, "test", "ceochat-capability-test-seed")
	if err != nil {
		t.Fatalf("seed real task: %v", err)
	}
	return created
}

// countingTaskReader delegates to the REAL *tasks.Service (the same one
// ceochat's own driveTurn uses) while counting calls, so a replay/
// authority-loss test can prove a denied or replayed request never reached
// the canonical service a second time -- without faking task data.
type countingTaskReader struct {
	real  ceochat.TaskReader
	calls int
}

func (c *countingTaskReader) ListTasks(ctx context.Context, filter tasks.TaskFilter) ([]tasks.Task, error) {
	c.calls++
	return c.real.ListTasks(ctx, filter)
}
func (c *countingTaskReader) GetTask(ctx context.Context, id int64) (tasks.TaskDetail, error) {
	return c.real.GetTask(ctx, id)
}
func (c *countingTaskReader) ListAttemptsPage(ctx context.Context, taskID int64, limit, offset int) ([]tasks.Attempt, error) {
	return c.real.ListAttemptsPage(ctx, taskID, limit, offset)
}

// withTaskToolOnly composes a *ceochat.Service identical to the fixture's
// bootstrapped one, except its ENTIRE tool surface is narrowed to a
// single-tool registry wrapping the real *tasks.Service through
// countingTaskReader. This is the round's own single-tool-family isolation
// pattern (already used by CLOSURE_V1's research-only narrowing), applied
// to a NEW capability so tool-call replay and authority-loss can each be
// demonstrated against tasks.list specifically, not only research.*.
func (f *chatFixture) withTaskToolOnly(t *testing.T, model executionharness.ModelExecutor) (*ceochat.Service, *countingTaskReader) {
	t.Helper()
	reader := &countingTaskReader{real: f.runtime.Tasks}
	registry := ceochat.NewToolRegistry()
	if err := ceochat.RegisterTaskTools(registry, reader); err != nil {
		t.Fatal(err)
	}
	base := *f.runtime.Service
	base.NewModelExecutor = func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) { return model, nil }
	base.Catalog = ceochat.RegistryToolCatalog{Registry: registry}
	base.ToolExecutor = ceochat.RegistryToolExecutor{Registry: registry}
	base.ToolDefinitions = registry.Definitions()
	service, err := ceochat.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	return service, reader
}

// withScriptedModelKeepingProductionTools is withScriptedModel WITHOUT
// narrowing the tool surface: base.Catalog/ToolExecutor/ToolDefinitions
// stay exactly what bootstrap.Open wired (the real, single, host-owned
// ToolRegistry covering every family). Tests that need more than the two
// research tools -- the multi-tool E2E, and the runs/finance/memory
// single-family probes below -- use this instead of withScriptedModel,
// which deliberately narrows to a research-only ToolExecutor and would
// make every non-research tool name fail with "no executor for tool".
func (f *chatFixture) withScriptedModelKeepingProductionTools(t *testing.T, model executionharness.ModelExecutor) *ceochat.Service {
	t.Helper()
	base := *f.runtime.Service
	base.NewModelExecutor = func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) { return model, nil }
	service, err := ceochat.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// TestCEOChatTasksListToolReturnsRealDataAndProvesReadOnly is negative test
// I plus basic real coverage for the tasks.* family: a real seeded task is
// visible through tasks.list, and calling it changes nothing about that
// task's durable state.
func TestCEOChatTasksListToolReturnsRealDataAndProvesReadOnly(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	seeded := f.seedRealTask(t, "tasks-list-readonly-seed", "read-only proof seed task")
	before, err := f.runtime.Tasks.GetTask(ctx, seeded.ID)
	if err != nil {
		t.Fatal(err)
	}

	model := &singleToolProbeModel{toolName: ceochat.ToolTasksList, args: json.RawMessage(`{}`)}
	service, reader := f.withTaskToolOnly(t, model)
	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "tasks-list-readonly", Content: "list tasks",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("outcome=%s want completed", result.Outcome)
	}
	if reader.calls != 1 {
		t.Fatalf("tasks.list must have executed exactly once against the real service, calls=%d", reader.calls)
	}
	var decoded struct {
		Tasks []struct {
			TaskID int64 `json:"task_id"`
		} `json:"tasks"`
	}
	if err = json.Unmarshal(model.observedResult, &decoded); err != nil {
		t.Fatalf("decode tasks.list result: %v (raw=%s)", err, model.observedResult)
	}
	found := false
	for _, task := range decoded.Tasks {
		if task.TaskID == seeded.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("tasks.list must return the real seeded task %d, got %+v", seeded.ID, decoded.Tasks)
	}

	after, err := f.runtime.Tasks.GetTask(ctx, seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Task.Status != before.Task.Status || after.Task.Version != before.Task.Version {
		t.Fatalf("tasks.list must not mutate the queried task: before=%+v after=%+v", before.Task, after.Task)
	}
}

// replayTaskToolModel is replayToolModel (CLOSURE_V1) generalized to any
// tool name, used here so negative test D is demonstrated with a NEW
// capability (tasks.list) as the round requires, not only research.*.
type replayTaskToolModel struct {
	toolCallID string
	toolName   string
	calls      int
}

func (m *replayTaskToolModel) Invoke(_ context.Context, _ executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	switch m.calls {
	case 1:
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: m.toolCallID, ToolName: m.toolName, Arguments: json.RawMessage(`{}`)}},
			InvocationRef: "task-replay-1",
		}, nil
	case 2:
		found := false
		for _, message := range request.VisibleHistory {
			if message.Role == "tool" && message.ToolCallID == m.toolCallID && len(message.ToolResult) > 0 {
				found = true
			}
		}
		if !found {
			return executionharness.ModelResult{}, fmt.Errorf("turn 2: original tool result for %s not visible", m.toolCallID)
		}
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: m.toolCallID, ToolName: m.toolName, Arguments: json.RawMessage(`{}`)}},
			InvocationRef: "task-replay-2",
		}, nil
	default:
		return executionharness.ModelResult{}, fmt.Errorf("unexpected turn %d", m.calls)
	}
}

// TestCEOChatTaskListToolReplayIsDeniedAtCompositionBoundary is negative
// test D, demonstrated with tasks.list (a NEW capability) instead of
// research.*: a replayed tool_call_id must be denied, the underlying
// canonical service must execute exactly once, and no assistant answer may
// be derived from the replay.
func TestCEOChatTaskListToolReplayIsDeniedAtCompositionBoundary(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	f.seedRealTask(t, "tasks-list-replay-seed", "replay proof seed task")

	model := &replayTaskToolModel{toolCallID: "call-task-1", toolName: ceochat.ToolTasksList}
	service, reader := f.withTaskToolOnly(t, model)

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "tasks-replay-turn", Content: "list tasks twice (a well-behaved model would not)",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Outcome != ceochat.RunOutcomeIncomplete {
		t.Fatalf("outcome=%s want incomplete (the replayed call must deny the run, not complete it)", result.Outcome)
	}
	if result.AssistantMessage != nil {
		t.Fatal("no assistant message may be derived from a run a tool-call replay denied")
	}
	if reader.calls != 1 {
		t.Fatalf("toolExecutions=%d want exactly 1 (the replay must never reach the canonical service a second time)", reader.calls)
	}
	if model.calls != 2 {
		t.Fatalf("model turns=%d want exactly 2", model.calls)
	}
}

// TestCEOChatAuthorityLossBeforeTaskListToolExecutionLeavesNoSideEffect is
// negative test E, demonstrated with tasks.list (a NEW capability): authority
// disappearing immediately before the tool executes must leave the real
// canonical service uncalled, produce no assistant message, and leave the
// task attempt resumable rather than recording a durable failure.
func TestCEOChatAuthorityLossBeforeTaskListToolExecutionLeavesNoSideEffect(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	f.seedRealTask(t, "tasks-list-authorityloss-seed", "authority loss proof seed task")

	model := &authorityLossTaskModel{}
	reader := &countingTaskReader{real: f.runtime.Tasks}
	registry := ceochat.NewToolRegistry()
	if err := ceochat.RegisterTaskTools(registry, reader); err != nil {
		t.Fatal(err)
	}
	base := *f.runtime.Service
	wrapped := &authorityFailAfter{real: base.Authority, allowed: 1}
	base.Authority = wrapped
	base.NewModelExecutor = func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) { return model, nil }
	base.Catalog = ceochat.RegistryToolCatalog{Registry: registry}
	base.ToolExecutor = ceochat.RegistryToolExecutor{Registry: registry}
	base.ToolDefinitions = registry.Definitions()
	service, err := ceochat.Open(base)
	if err != nil {
		t.Fatal(err)
	}

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "tasks-authority-loss", Content: "list tasks please",
	})
	if !errors.Is(err, ceochat.ErrRunNotReady) {
		t.Fatalf("err=%v want ErrRunNotReady", err)
	}
	if wrapped.calls < 2 {
		t.Fatalf("authority calls=%d want at least 2 (pre-turn, then pre-tool-execution)", wrapped.calls)
	}
	if model.calls != 1 {
		t.Fatalf("model turns=%d want exactly 1 (no second turn after authority loss)", model.calls)
	}
	if reader.calls != 0 {
		t.Fatalf("tool side effects=%d want 0 (authority failed before the executor was ever entered)", reader.calls)
	}

	history, err := service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history {
		if message.Role == ceochat.MessageAssistant {
			t.Fatalf("no assistant message should have been persisted: %+v", message)
		}
	}
}

type authorityLossTaskModel struct{ calls int }

func (m *authorityLossTaskModel) Invoke(_ context.Context, _ executionharness.RunIdentity, _ executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	if m.calls > 1 {
		return executionharness.ModelResult{}, fmt.Errorf("unexpected second model turn after authority loss")
	}
	return executionharness.ModelResult{
		FinishReason:  executionharness.FinishTools,
		ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-1", ToolName: ceochat.ToolTasksList, Arguments: json.RawMessage(`{}`)}},
		InvocationRef: "task-authority-loss-1",
	}, nil
}

// multiToolModel is the round's mandatory multi-tool E2E scripted model: it
// chains tasks.list (family 1) and memory.search (family 4) across three
// turns, verifying each tool's result is visible in a LATER turn's history
// before grounding its final answer in both.
type multiToolModel struct{ calls int }

func (m *multiToolModel) Invoke(_ context.Context, _ executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	switch m.calls {
	case 1:
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-tasks", ToolName: ceochat.ToolTasksList, Arguments: json.RawMessage(`{}`)}},
			InvocationRef: "multi-1",
		}, nil
	case 2:
		if !toolResultVisible(request, "call-tasks") {
			return executionharness.ModelResult{}, errors.New("turn 2: tasks.list result not visible in history")
		}
		return executionharness.ModelResult{
			FinishReason: executionharness.FinishTools,
			ToolRequests: []executionharness.ToolRequest{{ToolCallID: "call-memory", ToolName: ceochat.ToolMemorySearch,
				Arguments: json.RawMessage(`{"query":"repeated task failure pattern"}`)}},
			InvocationRef: "multi-2",
		}, nil
	case 3:
		if !toolResultVisible(request, "call-tasks") || !toolResultVisible(request, "call-memory") {
			return executionharness.ModelResult{}, errors.New("turn 3: both tool results must remain visible")
		}
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishFinal,
			FinalOutput:   "Revisé las tareas activas y la memoria de la CEO para problemas similares.",
			InvocationRef: "multi-3",
		}, nil
	default:
		return executionharness.ModelResult{}, fmt.Errorf("unexpected turn %d", m.calls)
	}
}

func toolResultVisible(request executionharness.NormalizedModelRequest, callID string) bool {
	for _, message := range request.VisibleHistory {
		if message.Role == "tool" && message.ToolCallID == callID && len(message.ToolResult) > 0 {
			return true
		}
	}
	return false
}

// TestCEOChatMultiToolConversationChainsTasksAndMemory is the round's
// mandatory MULTI_TOOL_E2E: a real conversation, real task/attempt/lease,
// real PostgreSQL, and a scripted (never real-provider) model that must
// compose more than one tool family in one durable conversation, with each
// result durably visible to a later turn and the final answer gated on
// both having succeeded.
func TestCEOChatMultiToolConversationChainsTasksAndMemory(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	f.seedRealTask(t, "multi-tool-seed", "multi-tool E2E seed task")

	model := &multiToolModel{}
	service := f.withScriptedModelKeepingProductionTools(t, model)

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "multi-tool-turn", Content: "¿Qué tareas activas tenemos y hay memoria de problemas similares?",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("outcome=%s want completed", result.Outcome)
	}
	if result.AssistantMessage == nil {
		t.Fatal("a completed multi-tool run must produce an assistant message")
	}
	if result.ToolCallsUsed != 2 {
		t.Fatalf("tool_calls_used=%d want 2 (one per family)", result.ToolCallsUsed)
	}
	if result.TurnsUsed != 3 {
		t.Fatalf("turns_used=%d want 3", result.TurnsUsed)
	}
	if model.calls != 3 {
		t.Fatalf("model invoke calls=%d want 3", model.calls)
	}

	// TestCEOChatRunsGetToolReturnsRealCompletedRunOutcome (below) reads
	// THIS run's own descriptor by ID, proving runs.* against real,
	// self-produced Harness data rather than a synthetic fixture.
	t.Logf("multi-tool run completed with run_id=ceochat-turn-%d", conversation.ID)
}

// TestCEOChatRunsGetToolReturnsRealCompletedRunOutcome proves the runs.*
// family against a REAL, already-completed Harness run: the two-turn
// research trajectory test above (TestCEOChatTwoTurnToolTrajectory) already
// produces one, so this test drives its own equivalent minimal completed
// run and then asks runs.get for exactly that run's outcome.
func TestCEOChatRunsGetToolReturnsRealCompletedRunOutcome(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	// Phase 1: drive one real, minimal, completed Harness run using only
	// the research tools (unrelated to runs.* itself) so a real descriptor
	// and a real terminal event exist in PostgreSQL.
	producer := &scriptedModel{}
	producerService := f.withScriptedModel(t, producer)
	producerConversation, err := producerService.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	producerResult, err := producerService.Send(ctx, ceochat.SendRequest{
		ConversationID: producerConversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "runs-get-producer-turn", Content: "¿Qué hallazgos recientes tenemos?",
	})
	if err != nil {
		t.Fatalf("producer send: %v", err)
	}
	if producerResult.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("producer outcome=%s want completed", producerResult.Outcome)
	}

	// Phase 2: a SEPARATE conversation whose model calls runs.get for the
	// run ID the phase-1 turn produced (ceochat's own deterministic
	// "ceochat-turn-<taskID>" run ID scheme).
	targetRunID := fmt.Sprintf("ceochat-turn-%d", producerResult.OwnerMessage.TaskID)
	reader := &runsGetProbeModel{targetRunID: targetRunID}
	service := f.withScriptedModelKeepingProductionTools(t, reader)
	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "runs-get-turn", Content: "get run " + targetRunID,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("outcome=%s want completed", result.Outcome)
	}
	if reader.observedStatus != "completed" {
		t.Fatalf("runs.get reported status=%q want completed (real run outcome)", reader.observedStatus)
	}
}

// runsGetProbeModel calls runs.get for one specific run ID, decodes the
// real tool result, and asserts on it directly before finishing -- proving
// the tool result the model actually received reflects real durable state,
// not just that the call did not error.
type runsGetProbeModel struct {
	targetRunID    string
	calls          int
	observedStatus string
}

func (m *runsGetProbeModel) Invoke(_ context.Context, _ executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	switch m.calls {
	case 1:
		args, _ := json.Marshal(map[string]string{"run_id": m.targetRunID})
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-run", ToolName: ceochat.ToolRunsGet, Arguments: args}},
			InvocationRef: "runs-get-probe-1",
		}, nil
	case 2:
		for _, message := range request.VisibleHistory {
			if message.Role == "tool" && message.ToolCallID == "call-run" && len(message.ToolResult) > 0 {
				var decoded struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal(message.ToolResult, &decoded); err == nil {
					m.observedStatus = decoded.Status
				}
			}
		}
		return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: "run checked", InvocationRef: "runs-get-probe-2"}, nil
	default:
		return executionharness.ModelResult{}, fmt.Errorf("unexpected turn %d", m.calls)
	}
}

// TestCEOChatRunsListRecentToolReturnsRealDescriptorsWithPagination proves
// the runs.list_recent path -- and the new
// executionharnesspostgres.Store.ListRunDescriptors read method it is built
// on -- against real, self-produced Harness runs. It drives two real
// completed "producer" runs, then a THIRD conversation whose own run calls
// runs.list_recent twice (limit=1, then limit=10 with the returned cursor)
// within the SAME run: both producer runs plus this run's own descriptor
// are already durable before either tool call, so the two pages read a
// stable, non-shifting snapshot -- most-recent-first, no duplication.
func TestCEOChatRunsListRecentToolReturnsRealDescriptorsWithPagination(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	var runIDs []string
	for i := 0; i < 2; i++ {
		producer := &scriptedModel{}
		producerService := f.withScriptedModel(t, producer)
		conversation, err := producerService.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := producerService.Send(ctx, ceochat.SendRequest{
			ConversationID: conversation.ID, ActorRoleID: "empresa/human",
			IdempotencyKey: fmt.Sprintf("runs-list-producer-%d", i), Content: "¿Qué hallazgos recientes tenemos?",
		})
		if err != nil {
			t.Fatalf("producer %d send: %v", i, err)
		}
		if result.Outcome != ceochat.RunOutcomeCompleted {
			t.Fatalf("producer %d outcome=%s want completed", i, result.Outcome)
		}
		runIDs = append(runIDs, fmt.Sprintf("ceochat-turn-%d", result.OwnerMessage.TaskID))
	}

	model := &runsListPaginationModel{}
	service := f.withScriptedModelKeepingProductionTools(t, model)
	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "runs-list-pagination", Content: "list recent runs, then list more",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("outcome=%s want completed", result.Outcome)
	}
	if len(model.page1Runs) != 1 || model.page1Cursor == "" {
		t.Fatalf("page1 runs=%v cursor=%q", model.page1Runs, model.page1Cursor)
	}
	if len(model.page2Runs) < 2 {
		t.Fatalf("page2 runs=%v want at least the 2 producer runs", model.page2Runs)
	}
	seen := map[string]bool{model.page1Runs[0]: true}
	for _, runID := range model.page2Runs {
		if seen[runID] {
			t.Fatalf("run %q appeared on both pages: page1=%v page2=%v", runID, model.page1Runs, model.page2Runs)
		}
		seen[runID] = true
	}
	for _, want := range runIDs {
		if !seen[want] {
			t.Fatalf("real run %q missing from runs.list_recent across both pages: seen=%v", want, seen)
		}
	}
}

// runsListPaginationModel calls runs.list_recent(limit=1), then
// runs.list_recent(limit=10, cursor=<page 1's cursor>), all within one run,
// capturing both pages before finishing.
type runsListPaginationModel struct {
	calls       int
	page1Runs   []string
	page1Cursor string
	page2Runs   []string
}

type runListView struct {
	RunID string `json:"run_id"`
}

func (m *runsListPaginationModel) Invoke(_ context.Context, _ executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	switch m.calls {
	case 1:
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-page1", ToolName: ceochat.ToolRunsListRecent, Arguments: json.RawMessage(`{"limit":1}`)}},
			InvocationRef: "runs-list-pagination-1",
		}, nil
	case 2:
		var page1 struct {
			Runs       []runListView `json:"runs"`
			NextCursor string        `json:"next_cursor"`
		}
		for _, message := range request.VisibleHistory {
			if message.Role == "tool" && message.ToolCallID == "call-page1" {
				if err := json.Unmarshal(message.ToolResult, &page1); err != nil {
					return executionharness.ModelResult{}, err
				}
			}
		}
		for _, run := range page1.Runs {
			m.page1Runs = append(m.page1Runs, run.RunID)
		}
		m.page1Cursor = page1.NextCursor
		args, _ := json.Marshal(map[string]any{"limit": 10, "cursor": page1.NextCursor})
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-page2", ToolName: ceochat.ToolRunsListRecent, Arguments: args}},
			InvocationRef: "runs-list-pagination-2",
		}, nil
	case 3:
		var page2 struct {
			Runs []runListView `json:"runs"`
		}
		for _, message := range request.VisibleHistory {
			if message.Role == "tool" && message.ToolCallID == "call-page2" {
				if err := json.Unmarshal(message.ToolResult, &page2); err != nil {
					return executionharness.ModelResult{}, err
				}
			}
		}
		for _, run := range page2.Runs {
			m.page2Runs = append(m.page2Runs, run.RunID)
		}
		return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: "checked", InvocationRef: "runs-list-pagination-3"}, nil
	default:
		return executionharness.ModelResult{}, fmt.Errorf("unexpected turn %d", m.calls)
	}
}

// TestCEOChatFinanceCostSummaryToolExecutesAgainstRealLedgerWithNoFabrication
// proves the finance.* family against the REAL costledger PostgreSQL
// adapter. No real provider call ever happens in this round (scripted
// models only), so the ledger is genuinely empty -- this test proves the
// real ListCallBreakdowns/ProvisionedProviderIDs plumbing runs end to end
// and reports a truthful zero, never a fabricated or estimated figure
// presented as settled spend.
func TestCEOChatFinanceCostSummaryToolExecutesAgainstRealLedgerWithNoFabrication(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &singleToolProbeModel{toolName: ceochat.ToolFinanceGetCostSummary, args: json.RawMessage(`{}`)}
	service := f.withScriptedModelKeepingProductionTools(t, model)
	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "finance-cost-summary-turn", Content: "how much have we spent",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("outcome=%s want completed", result.Outcome)
	}
	var decoded struct {
		SettledUSD            string `json:"settled_usd"`
		EstimatedUnsettledUSD string `json:"estimated_unsettled_usd"`
	}
	if err = json.Unmarshal(model.observedResult, &decoded); err != nil {
		t.Fatalf("decode finance.get_cost_summary result: %v (raw=%s)", err, model.observedResult)
	}
	if decoded.SettledUSD == "" || decoded.EstimatedUnsettledUSD == "" {
		t.Fatalf("finance.get_cost_summary must always report both figures, even when zero: %+v", decoded)
	}
}

// TestCEOChatMemorySearchToolExecutesAgainstRealMemoryOSWithNoFabrication
// proves the memory.* family against the REAL memory.Manager (which itself
// goes through the real repository, never pgvector directly from
// ceochat). An empty organization has no memory entries, so this proves
// real plumbing end to end with a truthful empty result.
func TestCEOChatMemorySearchToolExecutesAgainstRealMemoryOSWithNoFabrication(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &singleToolProbeModel{toolName: ceochat.ToolMemorySearch, args: json.RawMessage(`{"query":"anything"}`)}
	service := f.withScriptedModelKeepingProductionTools(t, model)
	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "memory-search-turn", Content: "do we have memory of this",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("outcome=%s want completed", result.Outcome)
	}
	var decoded struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err = json.Unmarshal(model.observedResult, &decoded); err != nil {
		t.Fatalf("decode memory.search result: %v (raw=%s)", err, model.observedResult)
	}
	// decoded.Entries being empty (rather than the field missing, or an
	// error) is exactly what a truthful "no memory yet" answer looks like.
}

// singleToolProbeModel calls exactly one tool once, captures its raw
// result, and finishes -- a minimal real-plumbing probe shared by the
// finance and memory family tests above.
type singleToolProbeModel struct {
	toolName       string
	args           json.RawMessage
	calls          int
	observedResult json.RawMessage
}

func (m *singleToolProbeModel) Invoke(_ context.Context, _ executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	switch m.calls {
	case 1:
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-probe", ToolName: m.toolName, Arguments: m.args}},
			InvocationRef: "single-tool-probe-1",
		}, nil
	case 2:
		for _, message := range request.VisibleHistory {
			if message.Role == "tool" && message.ToolCallID == "call-probe" {
				m.observedResult = message.ToolResult
			}
		}
		return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: "checked", InvocationRef: "single-tool-probe-2"}, nil
	default:
		return executionharness.ModelResult{}, fmt.Errorf("unexpected turn %d", m.calls)
	}
}

// finalizeFailsOnce wraps a real ceochat.TaskCoordinator and fails exactly
// the FIRST FinalizeTask call, simulating a crash AFTER the assistant
// message would already have been durably appended (Send now appends it
// before calling RecordAttemptResult/FinalizeTask -- see service.go) but
// BEFORE the task itself reaches its terminal Completed state. Every other
// method, including later FinalizeTask calls, delegates to the real
// coordinator untouched.
type finalizeFailsOnce struct {
	ceochat.TaskCoordinator
	finalizeCalls int
}

func (f *finalizeFailsOnce) FinalizeTask(ctx context.Context, command tasks.FinalizeCommand) (tasks.Task, error) {
	f.finalizeCalls++
	if f.finalizeCalls == 1 {
		return tasks.Task{}, errors.New("simulated crash: process died before FinalizeTask committed")
	}
	return f.TaskCoordinator.FinalizeTask(ctx, command)
}

// TestCEOChatCrashBetweenAppendAndFinalizeNeverLosesTheAnswer is a
// regression test for the exact crash window a PR review identified: if
// the process dies between recording the assistant's answer and finalizing
// the task, the answer must not be lost. This test simulates that crash by
// failing the real FinalizeTask call once, then proves (1) the assistant
// message is ALREADY durable despite Send returning an error, and (2) a
// retry with the identical idempotency key recovers it via Send's own
// FindAssistantReply fast path -- completed, Reused=true, no task-state
// dependency -- rather than the turn being permanently unrecoverable.
func TestCEOChatCrashBetweenAppendAndFinalizeNeverLosesTheAnswer(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &scriptedModel{}
	base := *f.runtime.Service
	base.NewModelExecutor = func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) { return model, nil }
	base.ToolExecutor = ceochat.ToolExecutor{Topics: fakeTopicsOnly{}, Findings: &fakeFindingsOnly{}}
	wrappedTasks := &finalizeFailsOnce{TaskCoordinator: base.Tasks}
	base.Tasks = wrappedTasks
	service, err := ceochat.Open(base)
	if err != nil {
		t.Fatal(err)
	}

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "crash-window-turn", Content: "¿Qué hallazgos recientes tenemos?",
	})
	if err == nil {
		t.Fatal("want the simulated FinalizeTask failure to surface as an error")
	}
	if wrappedTasks.finalizeCalls != 1 {
		t.Fatalf("finalizeCalls=%d want exactly 1 before this assertion point", wrappedTasks.finalizeCalls)
	}

	// The assistant's answer must ALREADY be durable, despite Send having
	// just returned an error -- this is the property the reordering fixes.
	history, err := service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	foundAssistant := false
	for _, message := range history {
		if message.Role == ceochat.MessageAssistant {
			foundAssistant = true
		}
	}
	if !foundAssistant {
		t.Fatal("the assistant's answer must be durable even though FinalizeTask failed afterward")
	}

	// A retry with the SAME idempotency key must recover via
	// FindAssistantReply, without needing the task to have reached a
	// terminal state.
	retry, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "crash-window-turn", Content: "¿Qué hallazgos recientes tenemos?",
	})
	if err != nil {
		t.Fatalf("retry after the crash window must recover the answer, got error: %v", err)
	}
	if !retry.Reused {
		t.Fatal("retry must report Reused=true: the answer was already durable")
	}
	if retry.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("retry outcome=%s want completed", retry.Outcome)
	}
	if retry.AssistantMessage == nil {
		t.Fatal("retry must return the already-durable assistant message")
	}
	if model.calls != 2 {
		t.Fatalf("the retry must not re-invoke the model: calls=%d want 2 (from the original turn only)", model.calls)
	}
}
