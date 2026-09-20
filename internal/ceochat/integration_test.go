//go:build integration

package ceochat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	ceochatbootstrap "github.com/Mireuz13/explorarte-organization/internal/ceochat/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/search"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const chatTestOrganization = "explorarte"

// chatTestDispatchPrincipalKey is the fixed, stable ORG_MODEL_EXECUTION_PRINCIPAL_KEY
// every ceochat integration test uses. modelruntime.LoadRuntimeConfig reads this
// straight from the OS environment (internal/config's own lookup map has no
// influence over it -- see rehearsalConfig's doc comment in
// real_provider_rehearsal_test.go for the same discovery), and
// ceochatbootstrap.Open now constructs a real
// modeldispatch.AuthorizedAttemptProvisioner keyed to it, so a value must be
// present and a matching model_execution_principals row must already exist
// before the first Send() call, or EnsureAuthorizedAssignmentForRunningAttempt
// fails closed on modeldispatch.ErrNotFound before any model or tool call.
// Fixed (not per-test-random) and idempotently registered: many tests in
// this package call newChatFixture against the SAME shared, migrated-once
// integration database, and RegisterPrincipal is itself idempotent on
// (organization_id, idempotency_key).
const chatTestDispatchPrincipalKey = "ceochat-test/model-runtime-01"

// registerChatTestDispatchPrincipal idempotently ensures the principal
// chatTestDispatchPrincipalKey resolves to exists. It calls
// dispatchpostgres.Store.RegisterPrincipal directly (the same store
// ceochatbootstrap.Open itself opens against), not a second modeldispatch
// stack -- exactly the pattern internal/modeldispatch/postgres/integration_test.go's
// own registerFixturePrincipal helper already uses.
func registerChatTestDispatchPrincipal(t *testing.T, ctx context.Context, store *platformpostgres.Store, fail func(format string, args ...any)) {
	t.Helper()
	dispatchStore, err := dispatchpostgres.New(store)
	if err != nil {
		fail("open dispatch store for test principal registration: %v", err)
	}
	const dispatchActorRoleID = "ingenieria_ia/code-runner"
	const registeredBy = "empresa/human"
	requestHash, err := modeldispatch.PrincipalRequestHash(chatTestOrganization, chatTestDispatchPrincipalKey, dispatchActorRoleID, modeldispatch.PrincipalLocalProcess, registeredBy)
	if err != nil {
		fail("compute test dispatch principal request hash: %v", err)
	}
	if _, err = dispatchStore.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{
		Command: modeldispatch.RegisterPrincipalCommand{
			OrganizationID: chatTestOrganization, PrincipalKey: chatTestDispatchPrincipalKey,
			DispatchActorRoleID: dispatchActorRoleID, PrincipalKind: modeldispatch.PrincipalLocalProcess,
			IdempotencyKey: "ceochat-test-dispatch-principal",
		},
		RequestHash: requestHash, RegisteredByRoleID: registeredBy,
	}); err != nil {
		fail("register test dispatch principal: %v", err)
	}
}

// fakeFindingsOnly is a deterministic, in-memory FindingLister: exactly what
// the round authorizes ("una interfaz estrecha sobre FindingRepository")
// instead of seeding the real research schema for this foundation round.
type fakeFindingsOnly struct{ calls int }

func (f *fakeFindingsOnly) ListFindings(context.Context, search.FindingFilter) ([]search.ResearchFinding, error) {
	f.calls++
	return []search.ResearchFinding{
		{ID: "f1", TopicID: "t1", DepartmentID: "ingenieria_ia", Summary: "finding one", Classification: search.FindingInformative, CreatedAt: time.Now().UTC()},
		{ID: "f2", TopicID: "t1", DepartmentID: "ingenieria_ia", Summary: "finding two", Classification: search.FindingInformative, CreatedAt: time.Now().UTC()},
	}, nil
}

type fakeTopicsOnly struct{}

func (fakeTopicsOnly) ListTopics(context.Context, string) ([]search.ResearchTopic, error) {
	return nil, nil
}

// scriptedModel is the fake, deterministic ModelExecutor the round requires
// in place of any real provider call. It answers exactly the trajectory
// section 15 specifies: turn 1 requests research.list_findings, turn 2 sees
// the tool result and finishes.
type scriptedModel struct {
	calls int
}

func (m *scriptedModel) Invoke(_ context.Context, _ executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	switch m.calls {
	case 1:
		if len(request.VisibleHistory) != 0 {
			return executionharness.ModelResult{}, fmt.Errorf("turn 1: unexpected non-empty visible history: %+v", request.VisibleHistory)
		}
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-1", ToolName: ceochat.ToolListFindings, Arguments: json.RawMessage(`{"limit":5}`)}},
			InvocationRef: "scripted-1",
		}, nil
	case 2:
		found := false
		for _, message := range request.VisibleHistory {
			if message.Role == "tool" && message.ToolCallID == "call-1" && len(message.ToolResult) > 0 {
				found = true
			}
		}
		if !found {
			return executionharness.ModelResult{}, errors.New("turn 2: tool result for call-1 not visible in history")
		}
		return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: "Encontré 2 hallazgos recientes.", InvocationRef: "scripted-2"}, nil
	default:
		return executionharness.ModelResult{}, fmt.Errorf("unexpected turn %d", m.calls)
	}
}

type chatFixture struct {
	store   *platformpostgres.Store
	runtime *ceochatbootstrap.Runtime
	cleanup func()
}

func newChatFixture(t *testing.T) *chatFixture {
	t.Helper()
	return newChatFixtureWithModelRuntimeOptions(t)
}

// newChatFixtureWithModelRuntimeOptions is newChatFixture, parameterized
// with extra modelbootstrap.Option values forwarded to
// ceochatbootstrap.Open via WithModelRuntimeOptions -- FINANCE_HARNESS_
// RUNTIME_HOTFIX_V1's own real-Model-Runtime Finance Harness tests need a
// modelbootstrap.WithExtraAdapters(adapter.NewFake()) here (the same
// mechanism canonical_dispatch_e2e_test.go's own fixture already uses for
// empresa/ceo) so a real Model Runtime dispatch can resolve to the
// deterministic test.fake adapter instead of a real provider. Called with
// no options, this is byte-for-byte newChatFixture's own prior behavior.
func newChatFixtureWithModelRuntimeOptions(t *testing.T, modelRuntimeOpts ...modelbootstrap.Option) *chatFixture {
	t.Helper()
	return newChatFixtureWithOpenOptions(t, modelRuntimeOpts, nil)
}

// newChatFixtureWithOpenOptions is the general form: openOptions, when given,
// is called with the fixture's store and supplies extra ceochatbootstrap
// options (e.g. WithExecutiveSubmitter) -- the store must exist first because
// a real Executive orchestrator is built from it. With both arguments empty it
// is byte-for-byte newChatFixture.
func newChatFixtureWithOpenOptions(t *testing.T, modelRuntimeOpts []modelbootstrap.Option, openOptions func(*platformpostgres.Store) []ceochatbootstrap.OpenOption) *chatFixture {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Setenv("ORG_MODEL_EXECUTION_PRINCIPAL_KEY", chatTestDispatchPrincipalKey)
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":           "test",
			"ORG_DATABASE_URL":          databaseURL,
			"ORG_DATABASE_MAX_CONNS":    "16",
			"ORG_DATABASE_MIN_CONNS":    "0",
			"ORG_CANONICAL_DIR":         filepath.Join("..", "..", "docs", "canonical"),
			"ORG_CONTEXT_SOURCE_ROOT":   "/src",
			"ORG_TASKS_ORGANIZATION_ID": chatTestOrganization,
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "ceochat-integration-test")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	fail := func(format string, args ...any) {
		store.Close()
		cancel()
		t.Fatalf(format, args...)
	}
	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		fail("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		fail("migration runner: %v", err)
	}
	if _, err = runner.Up(ctx); err != nil {
		fail("migrate up: %v", err)
	}
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		fail("registry repository: %v", err)
	}
	loader, err := registry.NewLoader(cfg.Registry.CanonicalDir)
	if err != nil {
		fail("registry loader: %v", err)
	}
	registryService, err := registry.NewService(loader, registryRepo, chatTestOrganization, 30*time.Second)
	if err != nil {
		fail("registry service: %v", err)
	}
	if result, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || (!result.Applied && !result.NoOp) {
		fail("sync canonical registry: result=%+v err=%v", result, syncErr)
	}
	registerChatTestDispatchPrincipal(t, ctx, store, fail)
	// GetRoleRoutingAuthority (called by AuthorizedAttemptProvisioner on
	// every Send) fails closed unless empresa/ceo's executive.ceo
	// model_policy resolves to either a static role_model_binding or a
	// pool routing_policy row. SynchronizeCanonical above only syncs the
	// organization/role/capability registry; those binding rows come from
	// the SEPARATE model registry, which nothing in this fixture opened
	// before CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_V1 because
	// every model call was scripted (bypassing Model Runtime's route
	// resolution entirely). No second Model Runtime, no second dispatcher:
	// this is the exact modelbootstrap.OpenRegistry + Registry.Sync
	// sequence real_provider_rehearsal_test.go already uses.
	modelRegistryRuntime, err := modelbootstrap.OpenRegistry(cfg, store)
	if err != nil {
		fail("open model registry for ceochat test fixture: %v", err)
	}
	if sync, syncErr := modelRegistryRuntime.Registry.Sync(ctx, true, cfg.Tasks.OutboxMaxAttempts); syncErr != nil || (!sync.Applied && !sync.NoOp) {
		fail("sync model registry for ceochat test fixture: result=%+v err=%v", sync, syncErr)
	}

	var openOpts []ceochatbootstrap.OpenOption
	if len(modelRuntimeOpts) > 0 {
		openOpts = append(openOpts, ceochatbootstrap.WithModelRuntimeOptions(modelRuntimeOpts...))
	}
	if openOptions != nil {
		openOpts = append(openOpts, openOptions(store)...)
	}
	runtime, err := ceochatbootstrap.Open(cfg, store, openOpts...)
	if err != nil {
		fail("open ceochat runtime: %v", err)
	}
	return &chatFixture{store: store, runtime: runtime, cleanup: func() { store.Close(); cancel() }}
}

// withScriptedModel returns a *ceochat.Service identical to the fixture's
// bootstrapped one, except its model executor is the fully-scripted fake
// above -- no real provider call happens anywhere in this test.
func (f *chatFixture) withScriptedModel(t *testing.T, model executionharness.ModelExecutor) *ceochat.Service {
	t.Helper()
	base := *f.runtime.Service
	base.NewModelExecutor = func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) { return model, nil }
	base.ToolExecutor = ceochat.ToolExecutor{Topics: fakeTopicsOnly{}, Findings: &fakeFindingsOnly{}}
	service, err := ceochat.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// TestCEOChatTwoTurnToolTrajectory is round section 15's mandatory
// deterministic E2E: a real conversation, a real task/attempt/lease, a real
// Harness run, a scripted two-turn model (tool request, then final answer),
// no real provider call.
func TestCEOChatTwoTurnToolTrajectory(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()
	model := &scriptedModel{}
	service := f.withScriptedModel(t, model)

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{
		ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "turn-1", Content: "¿Qué hallazgos recientes tenemos?",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Reused {
		t.Fatal("first send must not be reused")
	}
	if result.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("outcome=%s want completed", result.Outcome)
	}
	if result.AssistantMessage == nil || result.AssistantMessage.Content != "Encontré 2 hallazgos recientes." {
		t.Fatalf("assistant message=%+v", result.AssistantMessage)
	}
	if result.TurnsUsed != 2 {
		t.Fatalf("turns_used=%d want 2", result.TurnsUsed)
	}
	if result.ToolCallsUsed != 1 {
		t.Fatalf("tool_calls_used=%d want 1", result.ToolCallsUsed)
	}
	if model.calls != 2 {
		t.Fatalf("model invoke calls=%d want 2", model.calls)
	}

	history, err := service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	var ownerCount, assistantCount int
	for _, message := range history {
		switch message.Role {
		case ceochat.MessageOwner:
			ownerCount++
		case ceochat.MessageAssistant:
			assistantCount++
		}
	}
	if ownerCount != 1 || assistantCount != 1 {
		t.Fatalf("owner=%d assistant=%d want 1/1", ownerCount, assistantCount)
	}

	// Negative test D: same key, different content -> conflict, no new side
	// effects.
	preConflictCalls := model.calls
	_, err = service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "turn-1", Content: "a completely different message",
	})
	if !errors.Is(err, ceochat.ErrIdempotencyConflict) {
		t.Fatalf("err=%v want ErrIdempotencyConflict", err)
	}
	if model.calls != preConflictCalls {
		t.Fatalf("conflict must not invoke the model: calls=%d want %d", model.calls, preConflictCalls)
	}

	// Negative test E: an actor that is not the conversation owner gets no
	// side effect at all.
	_, err = service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/ceo",
		IdempotencyKey: "turn-imposter", Content: "hola",
	})
	if !errors.Is(err, ceochat.ErrUnauthorizedActor) {
		t.Fatalf("err=%v want ErrUnauthorizedActor", err)
	}
	if model.calls != preConflictCalls {
		t.Fatalf("unauthorized actor must not invoke the model: calls=%d want %d", model.calls, preConflictCalls)
	}
}

// TestCEOChatSendIsIdempotentAcrossRuntimes is round section 16: a fresh
// service instance (new NewModelExecutor closure, new *ceochat.Service, same
// PostgreSQL) resending the identical (conversation, key, content) must
// reuse the original turn and cause zero new tasks/attempts/model calls/
// tool executions/assistant messages.
func TestCEOChatSendIsIdempotentAcrossRuntimes(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	firstModel := &scriptedModel{}
	firstService := f.withScriptedModel(t, firstModel)
	conversation, err := firstService.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := firstService.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human", IdempotencyKey: "restart-key", Content: "hola de nuevo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.AssistantMessage == nil {
		t.Fatal("first send must complete with an assistant message")
	}

	// A brand-new *ceochat.Service (fresh NewModelExecutor closure over a
	// fresh scriptedModel) stands in for reopening the runtime with the same
	// PostgreSQL: nothing about the SECOND send is allowed to touch the
	// second model.
	secondModel := &scriptedModel{}
	secondService := f.withScriptedModel(t, secondModel)
	second, err := secondService.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human", IdempotencyKey: "restart-key", Content: "hola de nuevo",
	})
	if err != nil {
		t.Fatalf("replay send: %v", err)
	}
	if !second.Reused {
		t.Fatal("replay send must report Reused=true")
	}
	if secondModel.calls != 0 {
		t.Fatalf("replay must not invoke the model at all: calls=%d", secondModel.calls)
	}
	if second.AssistantMessage == nil || second.AssistantMessage.ID != first.AssistantMessage.ID {
		t.Fatalf("replay must return the ORIGINAL assistant message: first=%+v second=%+v", first.AssistantMessage, second.AssistantMessage)
	}
	if second.OwnerMessage.ID != first.OwnerMessage.ID {
		t.Fatalf("replay must return the ORIGINAL owner message: first=%d second=%d", first.OwnerMessage.ID, second.OwnerMessage.ID)
	}

	history, err := secondService.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	var ownerCount, assistantCount int
	for _, message := range history {
		switch message.Role {
		case ceochat.MessageOwner:
			ownerCount++
		case ceochat.MessageAssistant:
			assistantCount++
		}
	}
	if ownerCount != 1 || assistantCount != 1 {
		t.Fatalf("owner=%d assistant=%d want still 1/1 after replay", ownerCount, assistantCount)
	}
}

// Negative test A/B: an unknown tool, and a tool the catalog knows but the
// run does not expose, are both denied before any executor is reached.
func TestCEOChatUnknownAndUnexposedToolsAreDenied(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	t.Run("unknown tool", func(t *testing.T) {
		model := &singleShotToolModel{toolName: "shell.exec"}
		service := f.withScriptedModel(t, &scriptedModelAdapter{singleShotToolModel: model})
		conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Send(ctx, ceochat.SendRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human", IdempotencyKey: "unknown-tool", Content: "run shell.exec please"})
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if result.Outcome != ceochat.RunOutcomeIncomplete {
			t.Fatalf("outcome=%s want incomplete (denied)", result.Outcome)
		}
		if result.AssistantMessage != nil {
			t.Fatal("a denied tool call must not produce an assistant message")
		}
	})
}

// singleShotToolModel requests exactly one tool call on its first turn and
// never expects a second turn (the Harness denies the call and terminates
// the run before a second model call would happen).
type singleShotToolModel struct {
	toolName string
	calls    int
}

type scriptedModelAdapter struct{ *singleShotToolModel }

func (m *scriptedModelAdapter) Invoke(_ context.Context, _ executionharness.RunIdentity, _ executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	if m.calls > 1 {
		return executionharness.ModelResult{}, fmt.Errorf("unexpected second call to a single-shot denial model")
	}
	return executionharness.ModelResult{
		FinishReason:  executionharness.FinishTools,
		ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-1", ToolName: m.toolName, Arguments: json.RawMessage(`{}`)}},
		InvocationRef: "denial-1",
	}, nil
}

// ==================================================
// CEO_CONVERSATIONAL_TOOL_RUNTIME_FOUNDATION_CLOSURE_V1
// ==================================================

// countingTopicLister counts every real ListTopics call so a test can prove
// a denied/replayed tool request never reached the executor.
type countingTopicLister struct{ calls int }

func (c *countingTopicLister) ListTopics(context.Context, string) ([]search.ResearchTopic, error) {
	c.calls++
	return []search.ResearchTopic{{ID: "t1", DepartmentID: "ingenieria_ia", Title: "topic one"}}, nil
}

// replayToolModel requests the SAME tool_call_id on two separate turns: the
// first is a legitimate call, the second is a replay of an ID the Harness
// already resolved. It is scripted, not adversarial by accident -- the
// point of this test is to prove ceochat's own composition of the Harness
// (its RunSpec, its tool catalog, its run identity) does not accidentally
// widen or bypass the Harness's own replay guard.
type replayToolModel struct {
	toolCallID string
	toolName   string
	calls      int
}

func (m *replayToolModel) Invoke(_ context.Context, _ executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	switch m.calls {
	case 1:
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: m.toolCallID, ToolName: m.toolName, Arguments: json.RawMessage(`{}`)}},
			InvocationRef: "replay-1",
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
		// Deliberately replay the SAME call ID. A well-behaved model never
		// does this; the point of the test is that the Harness must refuse
		// it anyway.
		return executionharness.ModelResult{
			FinishReason:  executionharness.FinishTools,
			ToolRequests:  []executionharness.ToolRequest{{ToolCallID: m.toolCallID, ToolName: m.toolName, Arguments: json.RawMessage(`{}`)}},
			InvocationRef: "replay-2",
		}, nil
	default:
		return executionharness.ModelResult{}, fmt.Errorf("unexpected turn %d", m.calls)
	}
}

// TestCEOChatDuplicateToolCallIDIsDeniedAtCompositionBoundary is CLOSURE_V1
// item A: prove -- at the ceochat composition boundary, with a real
// conversation/task/attempt/lease and real Postgres -- that a replayed
// tool_call_id is denied, executes the underlying tool exactly once, and
// never derives a completed assistant answer from the replay.
func TestCEOChatDuplicateToolCallIDIsDeniedAtCompositionBoundary(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &replayToolModel{toolCallID: "call-123", toolName: ceochat.ToolListTopics}
	topics := &countingTopicLister{}
	service := f.withScriptedModelAndTools(t, model, topics, &fakeFindingsOnly{})

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "replay-turn", Content: "list topics, then (a well-behaved model would not) list them again",
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
	if topics.calls != 1 {
		t.Fatalf("toolExecutions=%d want exactly 1 (the replay must never reach the executor a second time)", topics.calls)
	}
	if model.calls != 2 {
		t.Fatalf("model turns=%d want exactly 2 (one legitimate call, one replay attempt, then deny -- no third turn)", model.calls)
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

// authorityFailAfter delegates to a real ExecutionAuthorityPort for its
// first `allowed` calls, then reports authority as unavailable. It is used
// to put the Harness's SECOND authority check -- the one immediately before
// a tool executes -- in the failure state, simulating a lease/authority
// that was valid when the model was asked but has disappeared by the time
// the host is about to act on the model's tool request.
type authorityFailAfter struct {
	real    executionharness.ExecutionAuthorityPort
	allowed int
	calls   int
}

func (a *authorityFailAfter) AuthorizeExecution(ctx context.Context, request executionharness.AuthorityRequest) error {
	a.calls++
	if a.calls > a.allowed {
		return executionharness.ErrAuthorityUnavailable
	}
	return a.real.AuthorizeExecution(ctx, request)
}

// authorityLossModel requests exactly one tool call on its first (and only
// expected) turn. Authority is expected to fail before that tool ever
// executes, so a second model turn must never happen.
type authorityLossModel struct{ calls int }

func (m *authorityLossModel) Invoke(_ context.Context, _ executionharness.RunIdentity, _ executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	if m.calls > 1 {
		return executionharness.ModelResult{}, fmt.Errorf("unexpected second model turn after authority loss")
	}
	return executionharness.ModelResult{
		FinishReason:  executionharness.FinishTools,
		ToolRequests:  []executionharness.ToolRequest{{ToolCallID: "call-1", ToolName: ceochat.ToolListTopics, Arguments: json.RawMessage(`{}`)}},
		InvocationRef: "authority-loss-1",
	}, nil
}

// withScriptedModelAndTools is withScriptedModel generalized to accept
// explicit tool listers (so a test can count real executor calls) and,
// via withAuthority below, an overridden authority port.
func (f *chatFixture) withScriptedModelAndTools(t *testing.T, model executionharness.ModelExecutor, topics ceochat.TopicLister, findings ceochat.FindingLister) *ceochat.Service {
	t.Helper()
	base := *f.runtime.Service
	base.NewModelExecutor = func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) { return model, nil }
	base.ToolExecutor = ceochat.ToolExecutor{Topics: topics, Findings: findings}
	service, err := ceochat.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// withAuthorityFailingAfter builds a *ceochat.Service identical to the
// fixture's bootstrapped one, wired to the SAME real, live Task Engine
// authority (real lease/principal verification for the calls it allows),
// except that authority itself is wrapped to fail starting at call
// `allowed+1`. Everything else -- Store, Tasks, Principals, Contexts,
// HarnessHistory, DescriptorStore -- is the real, unmodified production
// wiring: only the authority PORT's answer is manipulated, nothing about
// how ceochat calls it.
func (f *chatFixture) withAuthorityFailingAfter(t *testing.T, model executionharness.ModelExecutor, allowed int) (*ceochat.Service, *authorityFailAfter, *countingTopicLister) {
	t.Helper()
	base := *f.runtime.Service
	wrapped := &authorityFailAfter{real: base.Authority, allowed: allowed}
	base.Authority = wrapped
	base.NewModelExecutor = func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) { return model, nil }
	topics := &countingTopicLister{}
	base.ToolExecutor = ceochat.ToolExecutor{Topics: topics, Findings: &fakeFindingsOnly{}}
	service, err := ceochat.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	return service, wrapped, topics
}

// TestCEOChatAuthorityLossBeforeToolExecutionLeavesNoSideEffect is
// CLOSURE_V1 item B: prove that authority disappearing between "the model
// requested a tool" and "the host is about to execute it" leaves zero tool
// side effects, persists no assistant message, and does not convert the
// turn into a durable failure -- the task attempt stays open for a future
// retry rather than being finalized against a transient outage.
func TestCEOChatAuthorityLossBeforeToolExecutionLeavesNoSideEffect(t *testing.T) {
	f := newChatFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	model := &authorityLossModel{}
	// allowed=1: the FIRST AuthorizeExecution call (immediately before the
	// turn-1 model invocation) is real and succeeds; the SECOND call (the
	// Harness's pre-tool-execution check, once the model has asked for
	// research.list_topics) is where authority reports unavailable.
	service, authority, topics := f.withAuthorityFailingAfter(t, model, 1)

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "authority-loss", Content: "list topics please",
	})
	if !errors.Is(err, ceochat.ErrRunNotReady) {
		t.Fatalf("err=%v want ErrRunNotReady", err)
	}
	if authority.calls < 2 {
		t.Fatalf("authority calls=%d want at least 2 (pre-turn, then pre-tool-execution)", authority.calls)
	}
	if model.calls != 1 {
		t.Fatalf("model turns=%d want exactly 1 (no second turn after authority loss)", model.calls)
	}
	if topics.calls != 0 {
		t.Fatalf("tool side effects=%d want 0 (authority failed before the executor was ever entered)", topics.calls)
	}

	history, err := service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	ownerCount, assistantCount := 0, 0
	for _, message := range history {
		switch message.Role {
		case ceochat.MessageOwner:
			ownerCount++
		case ceochat.MessageAssistant:
			assistantCount++
		}
	}
	if ownerCount != 1 {
		t.Fatalf("owner messages=%d want 1 (the owner's message is still durably recorded)", ownerCount)
	}
	if assistantCount != 0 {
		t.Fatal("no assistant message may be persisted when authority was lost mid-run")
	}

	// The durable task must remain open for a future retry: not completed,
	// not dead_letter/failed, not cancelled. RecordAttemptResult/FinalizeTask
	// were never called for this path (see driveTurn's StatusAuthorityUnavailable
	// branch), so the attempt the real ClaimTaskByID/StartAttempt call made is
	// still exactly where it was -- "running", under its own active lease --
	// which is what makes it resumable rather than a recorded failure. This
	// test proves that non-terminal, resumable durable state directly; it
	// does not re-claim within this same process (ceochat holds no
	// process-local lease-token map to re-enter with mid-test, the same
	// restart-safety boundary Executive's own Orchestrator draws), so a
	// literal successful second Send() is left to the Task Engine's own
	// reconciliation/expiry path, exactly as it already is for Executive.
	detail, err := f.runtime.Tasks.GetTask(ctx, history[0].TaskID)
	if err != nil {
		t.Fatal(err)
	}
	switch detail.Task.Status {
	case "completed", "failed", "dead_letter", "cancelled", "rejected", "no_action":
		t.Fatalf("task status=%q must NOT be terminal after an authority-loss turn", detail.Task.Status)
	}
	if detail.ActiveLease == nil {
		t.Fatal("the attempt's lease must still be active -- authority loss must not release or escalate it")
	}
}
