package postgres

import (
	"context"
	"log/slog"
	"testing"
)

// Outbox: organizational events persist durably; noise does not; payloads
// are sanitized (no secrets reach the ledger).
func TestOutbox_OrganizationalEventsPersist(t *testing.T) {
	pool := requireTestDatabase(t)
	ctx := context.Background()
	sink := NewOutboxSink(pool, slog.Default())

	// Relayed: important finding alert.
	sink.ResearchEvent("research.finding.alert", map[string]any{
		"cycle_id": "cycle-outbox-1", "department": "finanzas",
		"priority": "important", "classification": "important",
		// Malicious/accidental fields that must NEVER persist:
		"api_key": "sk-should-never-persist", "authorization": "Bearer secret",
		"prompt": "the full LLM prompt", "raw_response": "huge provider body",
	})
	// Relayed: RAG candidate submitted.
	sink.ResearchEvent("research.rag_candidate.submitted", map[string]any{
		"cycle_id": "cycle-outbox-1", "topic_id": "topic-outbox",
	})
	// Relayed: pending proposal.
	sink.ResearchEvent("research.topic.pending", map[string]any{
		"proposal_id": "prop-outbox-1", "department_id": "finanzas",
		"reject_reason": "high_frequency_review",
	})
	// Noise: must NOT persist.
	sink.ResearchEvent("research.tick.started", map[string]any{"at": "now"})
	sink.ResearchEvent("research.query.generated", map[string]any{"cycle_id": "cycle-outbox-1"})

	var pending, published int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM research_outbox_events WHERE status='pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM research_outbox_events WHERE status='published'`).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if pending != 3 {
		t.Fatalf("expected 3 pending organizational events, got %d", pending)
	}
	if published != 0 {
		t.Fatalf("nothing should be published before dispatch, got %d", published)
	}

	// Secrets must not appear anywhere in the ledger.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM research_outbox_events
		WHERE payload::text LIKE '%sk-should-never-persist%'
		   OR payload::text LIKE '%Bearer secret%'
		   OR payload::text LIKE '%the full LLM prompt%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("secrets leaked into outbox: %d rows", count)
	}

	// Dispatcher claim semantics: claim one event, verify status flip.
	var eventID int64
	if err := pool.QueryRow(ctx, `
		UPDATE research_outbox_events
		SET status='claimed',
		    claim_token_hash=encode(sha256(convert_to('token-1','UTF8')),'hex'),
		    claimed_by='dispatcher', claim_expires_at=NOW()+interval '5 minutes',
		    attempt_count=attempt_count+1, updated_at=NOW()
		WHERE id IN (SELECT id FROM research_outbox_events WHERE status='pending' ORDER BY id LIMIT 1)
		RETURNING id`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if eventID == 0 {
		t.Fatal("expected one claimable event")
	}
	if _, err := pool.Exec(ctx,
		`UPDATE research_outbox_events
		 SET status='published', published_at=NOW(), updated_at=NOW(),
		     claim_token_hash=NULL, claimed_by=NULL, claim_expires_at=NULL
		 WHERE id=$1`, eventID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM research_outbox_events WHERE status='published'`).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if published != 1 {
		t.Fatalf("publish must flip exactly one row, got %d", published)
	}
}
