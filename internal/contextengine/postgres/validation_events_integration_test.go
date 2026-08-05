//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextpostgres "github.com/Mireuz13/explorarte-organization/internal/contextengine/postgres"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func TestContextValidationFailureEventsPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	platform := openStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetSchema(t, ctx, platform)
	syncCanonical(t, ctx, platform)
	store, err := contextpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := validSnapshot(t, ctx, store, "validation-events")
	if _, err = store.Create(ctx, contextengine.CreateSnapshotCommand{Snapshot: snapshot, Now: snapshot.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	validation := contextengine.SnapshotValidation{Valid: false, ReasonCode: string(contextengine.ReasonSnapshotStale), Drift: []contextengine.DriftFinding{{ReasonCode: string(contextengine.ReasonCanonicalBundleDrift), Reference: "docs/canonical"}}}
	if err = store.RecordValidationFailure(ctx, snapshot, validation, snapshot.CreatedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE subject_type='context_snapshot' AND subject_id=$1 AND event_type IN ('context.snapshot_validation_failed','context.policy_drift_rejected')`, fmt.Sprint(snapshot.ID), 2)
	assertCount(t, ctx, platform, `SELECT count(*) FROM outbox_events WHERE aggregate_type='context_snapshot' AND aggregate_id=$1 AND event_type IN ('context.snapshot_validation_failed','context.policy_drift_rejected')`, fmt.Sprint(snapshot.ID), 2)
	rows, err := platform.Pool().Query(ctx, `SELECT payload::text FROM outbox_events WHERE aggregate_type='context_snapshot' AND aggregate_id=$1 AND event_type IN ('context.snapshot_validation_failed','context.policy_drift_rejected')`, fmt.Sprint(snapshot.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err = rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"content", "memory", "rag_evidence", "profile"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("validation event leaked %q: %s", forbidden, payload)
			}
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}
