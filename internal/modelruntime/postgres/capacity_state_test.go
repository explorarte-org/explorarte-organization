//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// This file tests model_routing_capacity_state (migration 000072) and its
// two Go entry points -- Store.CapacityState (the public
// modelruntime.CapacityStateReader implementation) and applyCapacityFeedback
// (the internal projection insertProviderOutcome calls in the same
// transaction) -- directly, against real PostgreSQL. It lives in package
// postgres (not postgres_test) specifically so it can call
// applyCapacityFeedback with synthetic, deliberately out-of-order outcome
// ids: proving the write-ordering guard (Section 6) this way is far cheaper
// and more precise than fabricating two full concurrent Invocation lifecycles
// racing in real time, and does not touch the real classifier/feedback
// PATH at all (that is what dynamic_routing_capacity_e2e_test.go, driven
// through real Dispatch() calls, is for).
//
// model_routing_capacity_state's last_provider_outcome_id/last_invocation_id/
// last_dispatch_attempt_id are deliberately plain BIGINT (see migration
// 000072's comment) with no foreign key to model_provider_outcomes/
// model_invocations/model_dispatch_attempts, which is what makes calling
// applyCapacityFeedback here, with synthetic ids that name no real row,
// possible at all.

func openCapacityTestStore(t *testing.T, ctx context.Context) (*platformpostgres.Store, *Store) {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 10, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	platform, err := platformpostgres.Open(ctx, cfg, "model-capacity-state-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, url, platform.Pool()); err != nil {
		platform.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		platform.Close()
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		platform.Close()
		t.Fatalf("migrations through 000072: %v", err)
	}
	if err := testdbguard.RequireDestructive(ctx, url, platform.Pool()); err != nil {
		platform.Close()
		t.Fatalf("refusing destructive reset: %v", err)
	}
	if _, err = platform.Pool().Exec(ctx, `TRUNCATE model_routing_capacity_state RESTART IDENTITY CASCADE`); err != nil {
		platform.Close()
		t.Fatal(err)
	}
	store, err := New(platform)
	if err != nil {
		platform.Close()
		t.Fatal(err)
	}
	return platform, store
}

// A: a candidate this (organization, provider, model) has never produced a
// row for reads back as the zero-value CandidateState -- eligible.
func TestCapacityStateMissingRowIsEligible(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	platform, store := openCapacityTestStore(t, ctx)
	defer platform.Close()

	state, err := store.CapacityState(ctx, "capacity-state-org-a", "test.fake", "model-never-seen")
	if err != nil {
		t.Fatal(err)
	}
	if state.Disabled || state.QuotaExhausted || state.CooldownUntil != nil {
		t.Fatalf("missing row must read back as the zero value, got %+v", state)
	}
}

// B: an explicitly persisted row -- including disabled=true, which Model
// Capacity State V1's write path (applyCapacityFeedback) never sets itself
// (Section 4: reserved for administrative/canonical control) -- reads back
// through Store.CapacityState exactly.
func TestCapacityStatePersistedFieldsReadBackExactly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	platform, store := openCapacityTestStore(t, ctx)
	defer platform.Close()

	cooldown := time.Now().Add(90 * time.Minute).UTC().Truncate(time.Microsecond)
	if _, err := platform.Pool().Exec(ctx, `
INSERT INTO model_routing_capacity_state(organization_id, provider_id, provider_model_id, disabled, quota_exhausted, cooldown_until)
VALUES ($1, 'test.fake', 'model-b', TRUE, TRUE, $2)`, "capacity-state-org-b", cooldown); err != nil {
		t.Fatal(err)
	}

	state, err := store.CapacityState(ctx, "capacity-state-org-b", "test.fake", "model-b")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Disabled || !state.QuotaExhausted || state.CooldownUntil == nil || !state.CooldownUntil.Equal(cooldown) {
		t.Fatalf("persisted state did not read back exactly: got %+v want disabled=true quota_exhausted=true cooldown_until=%v", state, cooldown)
	}
}

// C: two organizations with the SAME provider/model each see only their own
// state.
func TestCapacityStateOrganizationIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	platform, store := openCapacityTestStore(t, ctx)
	defer platform.Close()

	if _, err := platform.Pool().Exec(ctx, `
INSERT INTO model_routing_capacity_state(organization_id, provider_id, provider_model_id, quota_exhausted)
VALUES ($1, 'test.fake', 'shared-model', TRUE)`, "capacity-state-org-c1"); err != nil {
		t.Fatal(err)
	}

	stateC1, err := store.CapacityState(ctx, "capacity-state-org-c1", "test.fake", "shared-model")
	if err != nil {
		t.Fatal(err)
	}
	if !stateC1.QuotaExhausted {
		t.Fatal("organization c1 must see its own quota_exhausted state")
	}
	stateC2, err := store.CapacityState(ctx, "capacity-state-org-c2", "test.fake", "shared-model")
	if err != nil {
		t.Fatal(err)
	}
	if stateC2.QuotaExhausted {
		t.Fatal("organization c2 must NOT see organization c1's capacity state -- same provider/model, different organization")
	}
}

// D: two provider_model_ids under the same organization+provider are
// isolated from each other.
func TestCapacityStateProviderModelIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	platform, store := openCapacityTestStore(t, ctx)
	defer platform.Close()

	if _, err := platform.Pool().Exec(ctx, `
INSERT INTO model_routing_capacity_state(organization_id, provider_id, provider_model_id, disabled)
VALUES ($1, 'test.fake', 'model-x', TRUE)`, "capacity-state-org-d"); err != nil {
		t.Fatal(err)
	}

	stateX, err := store.CapacityState(ctx, "capacity-state-org-d", "test.fake", "model-x")
	if err != nil {
		t.Fatal(err)
	}
	if !stateX.Disabled {
		t.Fatal("model-x must read back disabled")
	}
	stateY, err := store.CapacityState(ctx, "capacity-state-org-d", "test.fake", "model-y")
	if err != nil {
		t.Fatal(err)
	}
	if stateY.Disabled {
		t.Fatal("model-y (a different provider_model_id under the same provider) must NOT inherit model-x's state")
	}
}

// E: state written through one Store instance is visible through a second,
// independently constructed Store over the same connection pool -- proving
// this is durable Postgres state, not anything cached in the Store value.
func TestCapacityStateDurableAcrossStoreInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	platform, _ := openCapacityTestStore(t, ctx)
	defer platform.Close()

	if _, err := platform.Pool().Exec(ctx, `
INSERT INTO model_routing_capacity_state(organization_id, provider_id, provider_model_id, quota_exhausted)
VALUES ($1, 'test.fake', 'model-e', TRUE)`, "capacity-state-org-e"); err != nil {
		t.Fatal(err)
	}

	storeB, err := New(platform)
	if err != nil {
		t.Fatal(err)
	}
	state, err := storeB.CapacityState(ctx, "capacity-state-org-e", "test.fake", "model-e")
	if err != nil {
		t.Fatal(err)
	}
	if !state.QuotaExhausted {
		t.Fatal("a second Store instance over the same pool must see what the first wrote")
	}
}

// F / Section 6: an outcome with a LOWER id than the one already projected
// must never be allowed to overwrite it, no matter what order the two
// writes actually run in -- and a genuinely higher id must still be able to
// advance state normally. Exercised directly against applyCapacityFeedback
// with synthetic ids naming no real row (see the file doc comment for why
// that is possible and deliberate here).
func TestApplyCapacityFeedbackOlderOutcomeCannotOverwriteNewer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	platform, store := openCapacityTestStore(t, ctx)
	defer platform.Close()

	apply := func(outcomeID int64, feedback modelruntime.CapacityFeedback, outcome modelruntime.ProviderOutcome) {
		t.Helper()
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := applyCapacityFeedback(ctx, tx, "capacity-state-org-f", "test.fake", "model-f", outcomeID, outcomeID, outcomeID, outcome, feedback); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// Outcome 20 (the logically NEWER one) lands first and sets a cooldown.
	apply(20,
		modelruntime.CapacityFeedback{Kind: modelruntime.CapacityFeedbackCooldown, Duration: 30 * time.Second, Reason: "retryable_5xx"},
		modelruntime.ProviderOutcome{OutcomeClassification: modelruntime.ProviderOutcomeRejected, HTTPStatus: 503, Retryable: true, ErrorCode: "http_error"},
	)

	state, err := store.CapacityState(ctx, "capacity-state-org-f", "test.fake", "model-f")
	if err != nil {
		t.Fatal(err)
	}
	if state.CooldownUntil == nil {
		t.Fatal("outcome 20's cooldown must be projected")
	}
	firstCooldown := *state.CooldownUntil

	// Outcome 10 (an OLDER, strictly lower id) commits late and tries to
	// project healthy -- it must NOT be allowed to clear the cooldown that
	// outcome 20 already set.
	apply(10,
		modelruntime.CapacityFeedback{Kind: modelruntime.CapacityFeedbackHealthy, Reason: "response_received"},
		modelruntime.ProviderOutcome{OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived},
	)

	state, err = store.CapacityState(ctx, "capacity-state-org-f", "test.fake", "model-f")
	if err != nil {
		t.Fatal(err)
	}
	if state.CooldownUntil == nil || !state.CooldownUntil.Equal(firstCooldown) {
		t.Fatalf("older outcome 10 must not overwrite newer outcome 20's state: got cooldown=%v want %v", state.CooldownUntil, firstCooldown)
	}

	// Outcome 30 (a genuinely NEWER id) must be allowed to advance state.
	apply(30,
		modelruntime.CapacityFeedback{Kind: modelruntime.CapacityFeedbackHealthy, Reason: "response_received"},
		modelruntime.ProviderOutcome{OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived},
	)

	state, err = store.CapacityState(ctx, "capacity-state-org-f", "test.fake", "model-f")
	if err != nil {
		t.Fatal(err)
	}
	if state.CooldownUntil != nil {
		t.Fatalf("outcome 30 (strictly newer than 20) must be able to clear the cooldown, got %v", state.CooldownUntil)
	}
}
