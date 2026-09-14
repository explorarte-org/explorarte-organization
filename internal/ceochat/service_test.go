package ceochat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// memoryStore is a minimal, single-process fake of Store. It enforces the
// same idempotency contract the PostgreSQL migration's UNIQUE(conversation_id,
// idempotency_key) constraint does, so tests against it exercise the same
// Service-level decision logic (reuse vs conflict) the real store drives.
type memoryStore struct {
	mu            sync.Mutex
	conversations map[int64]Conversation
	messages      map[int64][]Message
	nextConv      int64
	nextMsg       int64
}

func newMemoryStore() *memoryStore {
	return &memoryStore{conversations: map[int64]Conversation{}, messages: map[int64][]Message{}}
}

func (s *memoryStore) CreateConversation(_ context.Context, conversation Conversation) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextConv++
	conversation.ID = s.nextConv
	s.conversations[conversation.ID] = conversation
	return conversation, nil
}

func (s *memoryStore) GetConversation(_ context.Context, _ string, id int64) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.conversations[id]
	if !ok {
		return Conversation{}, fmt.Errorf("%w: %d", ErrConversationNotFound, id)
	}
	return conversation, nil
}

func (s *memoryStore) AppendMessage(_ context.Context, message Message) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message.Role == MessageOwner {
		for _, existing := range s.messages[message.ConversationID] {
			if existing.Role == MessageOwner && existing.IdempotencyKey == message.IdempotencyKey {
				return Message{}, fmt.Errorf("%w: conversation %d", ErrIdempotencyConflict, message.ConversationID)
			}
		}
	}
	s.nextMsg++
	message.ID = s.nextMsg
	message.Sequence = int64(len(s.messages[message.ConversationID]) + 1)
	s.messages[message.ConversationID] = append(s.messages[message.ConversationID], message)
	return message, nil
}

func (s *memoryStore) FindOwnerMessage(_ context.Context, conversationID int64, idempotencyKey string) (Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, message := range s.messages[conversationID] {
		if message.Role == MessageOwner && message.IdempotencyKey == idempotencyKey {
			return message, true, nil
		}
	}
	return Message{}, false, nil
}

func (s *memoryStore) FindAssistantReply(_ context.Context, ownerMessageID int64) (Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var owner *Message
	for conv, messages := range s.messages {
		for i := range messages {
			if messages[i].ID == ownerMessageID {
				owner = &s.messages[conv][i]
			}
		}
	}
	if owner == nil {
		return Message{}, false, nil
	}
	for _, message := range s.messages[owner.ConversationID] {
		if message.Role == MessageAssistant && message.TaskID == owner.TaskID {
			return message, true, nil
		}
	}
	return Message{}, false, nil
}

func (s *memoryStore) ListMessages(_ context.Context, conversationID int64, limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.messages[conversationID]
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	out := make([]Message, len(all))
	copy(out, all)
	return out, nil
}

var _ Store = (*memoryStore)(nil)

func TestValidateSendRequestBounds(t *testing.T) {
	base := SendRequest{ConversationID: 1, ActorRoleID: "empresa/human", IdempotencyKey: "k1", Content: "hola"}
	if err := validateSendRequest(base); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	missingConv := base
	missingConv.ConversationID = 0
	if err := validateSendRequest(missingConv); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput for missing conversation", err)
	}
	emptyKey := base
	emptyKey.IdempotencyKey = ""
	if err := validateSendRequest(emptyKey); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput for empty key", err)
	}
	emptyContent := base
	emptyContent.Content = ""
	if err := validateSendRequest(emptyContent); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput for empty content", err)
	}
	tooLong := base
	tooLong.Content = strings.Repeat("a", maxOwnerContentBytes+1)
	if err := validateSendRequest(tooLong); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput for oversized content", err)
	}
}

// TestHistoryDeniesAnActorThatIsNotTheConversationOwner is a regression
// test: History must enforce the same owner boundary Send already does
// (request.ActorRoleID == conversation.OwnerRoleID) before reading any
// message. A conversation's transcript is no less sensitive than the
// ability to add to it.
func TestHistoryDeniesAnActorThatIsNotTheConversationOwner(t *testing.T) {
	store := newMemoryStore()
	service := &Service{Store: store, OrganizationID: "explorarte"}
	conversation, err := service.CreateConversation(context.Background(), CreateConversationRequest{
		ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.History(context.Background(), HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/ceo"}); !errors.Is(err, ErrUnauthorizedActor) {
		t.Fatalf("err=%v want ErrUnauthorizedActor for a non-owner reader", err)
	}
	if _, err = service.History(context.Background(), HistoryRequest{ConversationID: conversation.ID, ActorRoleID: "empresa/human"}); err != nil {
		t.Fatalf("the real owner must still be able to read: %v", err)
	}
}

// Negative test D: same idempotency key with a different owner message must
// be a durable conflict, never a silent overwrite.
func TestRecordOwnerMessageConflictOnDifferentContent(t *testing.T) {
	store := newMemoryStore()
	service := &Service{Store: store, OrganizationID: "explorarte"}
	conversation := Conversation{ID: 1, OrganizationID: "explorarte", OwnerRoleID: "empresa/human"}

	first, isNew, err := service.recordOwnerMessage(context.Background(), conversation, 1, SendRequest{IdempotencyKey: "k1", Content: "first"}, "corr")
	if err != nil || !isNew {
		t.Fatalf("first insert: message=%+v isNew=%v err=%v", first, isNew, err)
	}

	_, _, err = service.recordOwnerMessage(context.Background(), conversation, 1, SendRequest{IdempotencyKey: "k1", Content: "different"}, "corr")
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err=%v want ErrIdempotencyConflict", err)
	}

	second, isNew, err := service.recordOwnerMessage(context.Background(), conversation, 1, SendRequest{IdempotencyKey: "k1", Content: "first"}, "corr")
	if err != nil || isNew {
		t.Fatalf("replay: message=%+v isNew=%v err=%v want isNew=false, no error", second, isNew, err)
	}
	if second.ID != first.ID {
		t.Fatalf("replay returned a different message: first=%d second=%d", first.ID, second.ID)
	}
}

func TestRenderConversationContractBoundsMessageCount(t *testing.T) {
	var prior []Message
	for i := 0; i < DefaultHistoryMessageLimit+10; i++ {
		prior = append(prior, Message{ID: int64(i + 1), Role: MessageOwner, Content: fmt.Sprintf("msg-%d", i)})
	}
	contract := renderConversationContract(prior, Message{Content: "new message"})
	if strings.Contains(contract, "msg-0\n") {
		t.Fatal("oldest message should have been dropped by the count bound")
	}
	if !strings.Contains(contract, "new message") {
		t.Fatal("new message must be present in the contract")
	}
}

func TestExcludeMessageRemovesExactID(t *testing.T) {
	messages := []Message{{ID: 1}, {ID: 2}, {ID: 3}}
	out := excludeMessage(messages, 2)
	if len(out) != 2 || out[0].ID != 1 || out[1].ID != 3 {
		t.Fatalf("out=%+v", out)
	}
}

// Architecture review §22.1/§22.2/§22.6: the chat profile is a SEPARATE,
// wider execution profile from executive/typed-task/v1, never a relaxation
// of it. This proves the chat side of that distinction from ceochat's own
// package; the typed-task side (MaxTurns=1/MaxToolCalls=0/Tools=nil) is
// internal/executive/runtimeadapter's existing, UNMODIFIED constants (see
// harness.go in that package, not touched by this round).
func TestChatProfileIsDistinctFromExecutiveTypedTaskProfile(t *testing.T) {
	if MaxTurns <= 1 {
		t.Fatalf("MaxTurns=%d must be > 1 (a typed task pins MaxTurns=1)", MaxTurns)
	}
	if MaxToolCalls <= 0 {
		t.Fatalf("MaxToolCalls=%d must be > 0 (a typed task pins MaxToolCalls=0)", MaxToolCalls)
	}
	if ExecutionProfileID == "executive/typed-task/v1" {
		t.Fatal("chat must not reuse the typed-task execution profile ID")
	}
	if len(NewToolCatalog().Definitions()) == 0 {
		t.Fatal("chat must expose at least one tool (a typed task exposes none)")
	}
}
