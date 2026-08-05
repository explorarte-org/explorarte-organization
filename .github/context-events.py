from pathlib import Path

# Add an optional production recorder boundary without widening SnapshotStore.
interfaces = Path("internal/contextengine/interfaces.go")
text = interfaces.read_text()
needle = '''type Service interface {
'''
insert = '''type ValidationEventRecorder interface {
	RecordValidationFailure(context.Context, Snapshot, SnapshotValidation, time.Time) error
}

type Service interface {
'''
if needle not in text:
    raise SystemExit("Service interface marker not found")
interfaces.write_text(text.replace(needle, insert, 1))

# Record explicit stale validation results, while preserving operational failures.
service = Path("internal/contextengine/service.go")
text = service.read_text()
old = '''	if len(drift) > 0 {
		return SnapshotValidation{Valid: false, ReasonCode: string(ReasonSnapshotStale), Drift: drift}, nil
	}
	return SnapshotValidation{Valid: true}, nil
'''
new = '''	if len(drift) > 0 {
		validation := SnapshotValidation{Valid: false, ReasonCode: string(ReasonSnapshotStale), Drift: drift}
		if recorder, ok := s.store.(ValidationEventRecorder); ok {
			if recordErr := recorder.RecordValidationFailure(ctx, snapshot, validation, s.clock.Now().UTC()); recordErr != nil {
				return SnapshotValidation{}, fmt.Errorf("record snapshot validation failure: %w", recordErr)
			}
		}
		return validation, nil
	}
	return SnapshotValidation{Valid: true}, nil
'''
if old not in text:
    raise SystemExit("validation return block not found")
service.write_text(text.replace(old, new, 1))

# Add transactional audit/outbox recording for explicit validation failures.
store = Path("internal/contextengine/postgres/store.go")
text = store.read_text()
marker = '''const snapshotColumns = `id,organization_id,organization_revision_id,actor_role_id,purpose,project_ref,task_ref,idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,correlation_id,causation_id,created_at,invalidated_at,invalidation_reason`
'''
method = '''func (s *Store) RecordValidationFailure(ctx context.Context, snapshot contextengine.Snapshot, validation contextengine.SnapshotValidation, now time.Time) (err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return mapError(err)
	}
	defer func() {
		if err != nil {
			rollback(tx)
		}
	}()
	current, err := getSnapshot(ctx, tx, snapshot.ID, true)
	if err != nil {
		return err
	}
	if current.Status != contextengine.SnapshotReady {
		return contextengine.ErrSnapshotInvalidated
	}
	eventSnapshot := current
	eventSnapshot.CreatedAt = now
	if err = appendAuditAndOutbox(ctx, tx, eventSnapshot, "context.snapshot_validation_failed", "system", "orgd", contextengine.ReasonSnapshotStale); err != nil {
		return err
	}
	if reason := policyDriftReason(validation); reason != "" {
		if err = appendAuditAndOutbox(ctx, tx, eventSnapshot, "context.policy_drift_rejected", "system", "orgd", reason); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

func policyDriftReason(validation contextengine.SnapshotValidation) contextengine.ReasonCode {
	for _, finding := range validation.Drift {
		reason := contextengine.ReasonCode(finding.ReasonCode)
		switch reason {
		case contextengine.ReasonRevisionMismatch, contextengine.ReasonPrecedenceHashMismatch, contextengine.ReasonCanonicalBundleDrift:
			return reason
		}
	}
	return ""
}

''' + marker
if marker not in text:
    raise SystemExit("snapshot columns marker not found")
store.write_text(text.replace(marker, method, 1))

# Unit coverage proving the service invokes the recorder only for structured drift.
Path("internal/contextengine/service_events_test.go").write_text(r'''package contextengine

import (
	"context"
	"testing"
	"time"
)

type validationRecordingStore struct {
	SnapshotStore
	calls      int
	validation SnapshotValidation
}

func (s *validationRecordingStore) RecordValidationFailure(_ context.Context, _ Snapshot, validation SnapshotValidation, _ time.Time) error {
	s.calls++
	s.validation = validation
	return nil
}

func TestServiceRecordsStructuredValidationFailure(t *testing.T) {
	fixture := newServiceFixture(t)
	recording := &validationRecordingStore{SnapshotStore: fixture.store}
	service, err := NewService(
		ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20},
		fixture.registry,
		fixture.docs,
		fixture.canonical,
		NoopOwnerConstraintProvider{},
		UnavailableMemoryProvider{},
		emptySkillProvider{},
		UnavailableProjectProvider{},
		UnavailableTaskProvider{},
		UnavailableRAGProvider{},
		NewAssembler(),
		NewRenderer(),
		recording,
		fixedClock{now: time.Date(2026, 8, 5, 6, 0, 0, 0, time.UTC)},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Build(t.Context(), fixture.request("validation-event"))
	if err != nil {
		t.Fatal(err)
	}
	profile := fixture.docs.docs["ingenieria_ia/qa/PERFIL.md"]
	profile.Hash = DigestMarkdown([]byte("changed profile"))
	fixture.docs.docs[profile.Path] = profile
	validation, err := service.Validate(t.Context(), result.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if validation.Valid || recording.calls != 1 || recording.validation.ReasonCode != string(ReasonSnapshotStale) {
		t.Fatalf("validation=%+v recorder=%+v", validation, recording)
	}
}
''')

# PostgreSQL 17 coverage for atomic audit/outbox events and safe payloads.
Path("internal/contextengine/postgres/validation_events_integration_test.go").write_text(r'''//go:build integration

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
	for _, eventType := range []string{"context.snapshot_validation_failed", "context.policy_drift_rejected"} {
		assertCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE subject_type='context_snapshot' AND subject_id=$1 AND event_type=$2`, fmt.Sprint(snapshot.ID), eventType, 1)
		assertCount(t, ctx, platform, `SELECT count(*) FROM outbox_events WHERE aggregate_type='context_snapshot' AND aggregate_id=$1 AND event_type=$2`, fmt.Sprint(snapshot.ID), eventType, 1)
	}
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
''')

# Extend fitness coverage for the mandatory validation/policy events.
fitness = Path("scripts/check-context-fitness.sh")
text = fitness.read_text()
needle = "require 'outbox_events' internal/contextengine/postgres/store.go 'context outbox integration missing'\n"
replacement = needle + "require 'context.snapshot_validation_failed' internal/contextengine/postgres/store.go 'validation failure event missing'\nrequire 'context.policy_drift_rejected' internal/contextengine/postgres/store.go 'policy drift event missing'\n"
if needle not in text:
    raise SystemExit("fitness insertion marker not found")
fitness.write_text(text.replace(needle, replacement, 1))
