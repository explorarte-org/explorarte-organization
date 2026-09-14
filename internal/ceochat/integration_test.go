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
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/search"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const chatTestOrganization = "explorarte"

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
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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

	runtime, err := ceochatbootstrap.Open(cfg, store)
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

	history, err := service.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID})
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

	history, err := secondService.History(ctx, ceochat.HistoryRequest{ConversationID: conversation.ID})
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
