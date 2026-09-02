package migrations_test

import (
	"strings"
	"testing"

	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// TestMigrationTipAndContiguity preserves both branch contracts after the
// rebase of the security branch onto main.
//
// Before the rebase the two branches each defined their own 000041, and this
// worktree carried a deliberate hole at that version while its own migration
// sat at 000042. Main now supplies 000041
// (harden_rag_knowledge_version_immutability), so the hole is filled and the
// sequence is contiguous again. Both facts are asserted together on purpose:
// contiguity is Worker A's invariant, and the identity of 000042/000043 is
// this branch's.
func TestMigrationTipAndContiguity(t *testing.T) {
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}

	const wantCount = 61
	if len(loaded) != wantCount {
		t.Fatalf("migration count=%d want %d", len(loaded), wantCount)
	}
	for index, migration := range loaded {
		want := int64(index + 1)
		if migration.Version != want {
			t.Fatalf("migration[%d].version=%d want %d (the sequence must stay contiguous)", index, migration.Version, want)
		}
	}

	expected := map[int64]string{
		41: "harden_rag_knowledge_version_immutability",
		42: "add_agent_message_authorization_and_hardening",
		43: "restrict_agent_message_type",
		44: "make_egress_revision_ownership_restorable",
		// Grok Audit Baseline 001 remediation:
		45: "make_audit_events_immutable",
		46: "recognize_historical_egress_revision_bindings",
		47: "seed_openai_responses_pricing_and_wallet",
		// EXEC-PRINCIPAL-001 remediation:
		48: "enforce_single_active_execution_principal_per_role",
		49: "create_model_invocation_inputs",
		// HARNESS-RELIABILITY-CLOSEOUT-001:
		50: "create_execution_run_events",
		// CONTEXT-ASSEMBLY-M1.1:
		51: "create_execution_context_views",
		// CONTEXT-ASSEMBLY-M1.2:
		52: "add_context_token_telemetry",
		// CONTEXT-ASSEMBLY-M1.3:
		53: "add_semantic_selector_facts",
		// GROK-ADVERSARIAL-REVIEW-001: pricing is a precondition of
		// activation -- without it costgate refuses every review.
		54: "add_xai_grok_pricing",
		// ROLE-REASONING-TRACEABILITY-001: reasoning stops being discarded
		// and gets one governed destination, so a role's decision can be
		// explained later without that explanation entering any path built
		// for material that carries no secrets.
		55: "create_model_invocation_reasoning",
		56: "create_composition_lifecycle",
		57: "create_executive_goal_acceptance",
		// AUTONOMY-SMOKE-017 found this one: PR #107 made
		// repository_evidence a productive source kind without extending
		// the persistence contract, so the first design that observed its
		// own repository failed at the first durable write.
		58: "allow_repository_evidence_source",
		// AUTONOMY-SMOKE-017-R1: the schema still described an ambiguous
		// transport as one that learned nothing, so an incomplete read --
		// the ambiguity that most needs to be durable -- could not be
		// written down at all.
		59: "align_ambiguous_transport_with_domain",
		// AUTONOMY-SMOKE-017-R4: a reservation that is never settled is
		// not a budget. 69%% of a campaign ceiling went to output space
		// that was never used.
		60: "settle_agent_budget_after_the_call",
		// ORGANIZATION-GRAND-AUDIT-001 G2-001 remediation: xai/grok-4.6 got
		// real pricing in 000054 but no migration ever seeded its wallet --
		// the exact gap this finding is about, reproduced in the migration
		// history itself.
		61: "seed_xai_wallet",
	}
	byVersion := make(map[int64]string, len(loaded))
	for _, migration := range loaded {
		byVersion[migration.Version] = migration.Name
	}
	for version, name := range expected {
		if byVersion[version] != name {
			t.Fatalf("migration %06d name=%q want %q", version, byVersion[version], name)
		}
	}

	if tip := loaded[len(loaded)-1]; tip.Version != wantCount {
		t.Fatalf("migration tip=%06d want %06d", tip.Version, wantCount)
	}
}

func TestModelInvocationInputMigrationContract(t *testing.T) {
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	var up string
	for _, migration := range loaded {
		if migration.Version == 49 {
			up = migration.UpSQL
			break
		}
	}
	for _, required := range []string{
		"CREATE TABLE model_invocation_inputs",
		"invocation_id BIGINT PRIMARY KEY",
		"FOREIGN KEY (invocation_id, context_snapshot_id)",
		"canonical_bytes BYTEA NOT NULL",
		"canonical_digest TEXT NOT NULL",
		"BEFORE UPDATE OR DELETE ON model_invocation_inputs",
		"cannot install model input envelopes while nonterminal model invocations exist",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("migration 49 missing immutable input contract %q", required)
		}
	}
	for _, forbidden := range []string{"harness_history", "workflow_events", "session_events"} {
		if strings.Contains(up, forbidden) {
			t.Fatalf("migration 49 added out-of-scope persistence %q", forbidden)
		}
	}
}
