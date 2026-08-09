package costledger

import (
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

type ProviderWallet struct {
	ProviderID  string
	BalanceUSD  modelpricing.USDNanos
	ReservedUSD modelpricing.USDNanos
	UpdatedAt   time.Time
	Version     int64
}

// Available is the balance not already committed to an in-flight
// reservation — the amount a new call may still reserve against.
func (w ProviderWallet) Available() modelpricing.USDNanos { return w.BalanceUSD - w.ReservedUSD }

type EventKind string

const (
	EventReserved  EventKind = "reserved"
	EventCommitted EventKind = "committed"
	EventReleased  EventKind = "released"
)

func (k EventKind) Valid() bool {
	switch k {
	case EventReserved, EventCommitted, EventReleased:
		return true
	default:
		return false
	}
}

// WalletEvent.InvocationID and EmbeddingInvocationID are mutually
// exclusive and exactly one is always set — a chat dispatch call sets
// InvocationID (FK to model_invocations), an embedding call sets
// EmbeddingInvocationID (FK to embedding_invocations). Enforced at the
// database level by provider_wallet_events_exactly_one_invocation
// (migration 000030).
type WalletEvent struct {
	ID                    int64
	ProviderID            string
	InvocationID          *int64
	EmbeddingInvocationID *int64
	Kind                  EventKind
	AmountUSD             modelpricing.USDNanos
	CreatedAt             time.Time
}

// EmbeddingOperation names the caller-side reason an embedding call was
// made, for attribution/auditing — not used for any authorization or
// pricing decision.
type EmbeddingOperation string

const (
	EmbeddingOperationRAGQuery       EmbeddingOperation = "rag_query"
	EmbeddingOperationRAGReindex     EmbeddingOperation = "rag_reindex"
	EmbeddingOperationMemoryPropose  EmbeddingOperation = "memory_propose"
	EmbeddingOperationMemorySearch   EmbeddingOperation = "memory_search"
	EmbeddingOperationMemoryBackfill EmbeddingOperation = "memory_backfill"
)

func (o EmbeddingOperation) Valid() bool {
	switch o {
	case EmbeddingOperationRAGQuery, EmbeddingOperationRAGReindex, EmbeddingOperationMemoryPropose, EmbeddingOperationMemorySearch, EmbeddingOperationMemoryBackfill:
		return true
	default:
		return false
	}
}

// EmbeddingInvocation is the lightweight identity row an embedding call's
// wallet events attach to — deliberately not model_invocations, which
// requires a task_attempt/context_snapshot/model_profile an embedding call
// has no equivalent of. Unlike model_invocations, it carries no dispatch/
// claim state machine: success or failure of the spend itself is recorded
// entirely by which wallet event kind lands (reserved-only vs
// reserved+committed vs reserved+released), same as the chat path.
type EmbeddingInvocation struct {
	ID              int64
	OrganizationID  string
	ActorRoleID     string
	TaskID          *int64
	ProviderID      string
	ProviderModelID string
	BillingMode     modelpricing.BillingMode
	Operation       EmbeddingOperation
	CreatedAt       time.Time
}

type SettlementStatus string

const (
	SettlementReserved  SettlementStatus = "reserved"
	SettlementCommitted SettlementStatus = "committed"
	SettlementReleased  SettlementStatus = "released"
)

// CallBreakdown joins the append-only wallet ledger to the durable model
// invocation and its provider-reported usage. Money remains exact nanodollars;
// presentation layers may use USDNanos.String for human-readable dollars.
type CallBreakdown struct {
	InvocationID         int64                 `json:"invocation_id"`
	OrganizationID       string                `json:"organization_id"`
	TaskID               int64                 `json:"task_id"`
	AttemptID            int64                 `json:"attempt_id"`
	DispatchActorRoleID  string                `json:"dispatch_actor_role_id"`
	SubjectRoleID        string                `json:"subject_role_id"`
	WalletProviderID     string                `json:"wallet_provider_id"`
	InvocationProviderID string                `json:"invocation_provider_id"`
	ProviderModelID      string                `json:"provider_model_id"`
	InvocationStatus     string                `json:"invocation_status"`
	InvocationErrorCode  string                `json:"invocation_error_code,omitempty"`
	Settlement           SettlementStatus      `json:"settlement"`
	EstimatedUSD         modelpricing.USDNanos `json:"estimated_usd_nanos"`
	ChargedUSD           modelpricing.USDNanos `json:"charged_usd_nanos"`
	ReleasedUSD          modelpricing.USDNanos `json:"released_usd_nanos"`
	InputTokens          int64                 `json:"input_tokens"`
	OutputTokens         int64                 `json:"output_tokens"`
	TotalTokens          int64                 `json:"total_tokens"`
	ProviderReported     bool                  `json:"provider_reported"`
	ProviderMismatch     bool                  `json:"provider_mismatch"`
	InvocationCreatedAt  time.Time             `json:"invocation_created_at"`
	InvocationTerminalAt *time.Time            `json:"invocation_terminal_at,omitempty"`
	LastLedgerAt         time.Time             `json:"last_ledger_at"`
}
