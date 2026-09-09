package postgres

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	search "github.com/Mireuz13/explorarte-organization/internal/search"
)

// =============================================================================
// Research Outbox Sink
//
// Research observability events that carry organizational meaning —
// important/critical findings, RAG candidate submissions, pending topic
// proposals, cycle failures — are persisted to research_outbox_events using
// the canonical outbox contract (pending → claimed → published/dead, claim
// tokens, bounded attempts, recovery index). slog stays for operational
// noise; the outbox is the durable organizational record that the kernel's
// dispatcher can forward with identical semantics.
//
// Safe-by-construction: payload fields pass through a whitelist before
// persistence, so an accidental credential in event fields can never reach
// the ledger.
// =============================================================================

// eventTypesRelayed lists which research event types are organizational
// events (durable outbox) versus operational noise (slog only).
var eventTypesRelayed = map[string]bool{
	"research.finding.created":         true,
	"research.finding.alert":           true,
	"research.rag_candidate.submitted": true,
	"research.topic.proposed":          true,
	"research.topic.pending":           true,
	"research.topic.autoaccepted":      true,
	"research.cycle.failed":            true,
	"research.topic.claimed":           false, // operational
	"research.topic.postponed":         false, // operational
	"research.tick.started":            false,
	"research.tick.completed":          false,
	"research.cycle.started":           false,
	"research.cycle.completed":         false, // metrics cover it
	"research.query.generated":         false,
	"research.search.completed":        false,
	"research.novelty.detected":        false,
}

// OutboxExecutor is the minimal persistence surface (pgxpool satisfies it).
type OutboxExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// OutboxSink implements search.ResearchEventSink over the durable outbox.
type OutboxSink struct {
	Exec OutboxExecutor
	// Now is overridable for tests.
	Now func() time.Time
	// Logger receives persistence failures (never event payloads).
	Logger *slog.Logger
}

// NewOutboxSink returns the durable research event sink.
func NewOutboxSink(executor OutboxExecutor, logger *slog.Logger) *OutboxSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxSink{Exec: executor, Now: func() time.Time { return time.Now().UTC() }, Logger: logger}
}

// ResearchEvent implements search.ResearchEventSink. Non-relayed events are
// dropped (they remain slog-level operational noise upstream); relayed
// events are persisted with sanitized payloads. Persistence failures are
// logged, never fatal to the research flow — but they are loud.
func (s *OutboxSink) ResearchEvent(event string, fields map[string]any) {
	if !eventTypesRelayed[event] {
		return
	}
	payload := sanitizeEventPayload(fields)
	raw, err := json.Marshal(payload)
	if err != nil {
		s.Logger.Error("research outbox: marshal payload failed", "event", event)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = s.Exec.Exec(ctx,
		`INSERT INTO research_outbox_events
			(aggregate_type, aggregate_id, event_type, payload, max_attempts)
		 VALUES ('research', $1, $2, $3::jsonb, 10)`,
		eventAggregateID(fields), event, string(raw))
	if err != nil {
		s.Logger.Error("research outbox: persist event failed", "event", event, "error", err)
	}
}

// eventFieldAllowlist is the whitelist of safe, bounded payload fields.
// Never persisted: prompts, full URLs, credentials, raw provider responses.
var eventFieldAllowlist = []string{
	"cycle_id", "topic_id", "department", "department_id", "classification",
	"durable_eligible", "priority", "outcome", "error_class",
	"new_evidence", "duplicates", "result_count", "queries_attempted",
	"providers_used", "searches_used", "topics_run", "reason",
	"reject_reason", "decision", "proposal_id", "finding_id",
}

func sanitizeEventPayload(fields map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range eventFieldAllowlist {
		if value, ok := fields[key]; ok {
			out[key] = value
		}
	}
	return out
}

// eventAggregateID picks the most specific identifier present.
func eventAggregateID(fields map[string]any) string {
	for _, key := range []string{"cycle_id", "finding_id", "proposal_id", "topic_id"} {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unknown"
}

// Compile-time proof.
var _ search.ResearchEventSink = (*OutboxSink)(nil)
