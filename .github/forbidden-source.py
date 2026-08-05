from pathlib import Path

# Add an optional recorder boundary for policy-rejected source attempts.
interfaces = Path("internal/contextengine/interfaces.go")
text = interfaces.read_text()
needle = '''type ValidationEventRecorder interface {
	RecordValidationFailure(context.Context, Snapshot, SnapshotValidation, time.Time) error
}

type Service interface {
'''
replacement = '''type ValidationEventRecorder interface {
	RecordValidationFailure(context.Context, Snapshot, SnapshotValidation, time.Time) error
}

type ForbiddenSourceEventRecorder interface {
	RecordForbiddenSourceRejection(context.Context, BuildRequest, ReasonCode, time.Time) error
}

type Service interface {
'''
if needle not in text:
    raise SystemExit("validation recorder marker not found")
interfaces.write_text(text.replace(needle, replacement, 1))

# Route forbidden-source failures through the optional recorder without changing policy decisions.
service = Path("internal/contextengine/service.go")
text = service.read_text()
text = text.replace(
'''\torganization, revision, role, unit, bundle, sources, resolvedSkills, err := s.resolve(ctx, request)
\tif err != nil {
\t\treturn BuildResult{}, err
\t}
''',
'''\torganization, revision, role, unit, bundle, sources, resolvedSkills, err := s.resolve(ctx, request)
\tif err != nil {
\t\treturn s.rejectBuild(ctx, request, err)
\t}
''', 1)
text = text.replace(
'''\tif err != nil {
\t\treturn BuildResult{}, err
\t}
\trequestHash := DigestBuildRequest''',
'''\tif err != nil {
\t\treturn s.rejectBuild(ctx, request, err)
\t}
\trequestHash := DigestBuildRequest''', 1)
text = text.replace(
'''\tif err = s.revalidateResolved(ctx, request, revision.ID, bundle, role, sources, resolvedSkills); err != nil {
\t\treturn BuildResult{}, err
\t}
''',
'''\tif err = s.revalidateResolved(ctx, request, revision.ID, bundle, role, sources, resolvedSkills); err != nil {
\t\treturn s.rejectBuild(ctx, request, err)
\t}
''', 1)
marker = '''func (s *contextService) resolve(ctx context.Context, request BuildRequest)'''
helper = '''func (s *contextService) rejectBuild(ctx context.Context, request BuildRequest, cause error) (BuildResult, error) {
	reason := ReasonOf(cause)
	if !isForbiddenSourceReason(reason) {
		return BuildResult{}, cause
	}
	recorder, ok := s.store.(ForbiddenSourceEventRecorder)
	if !ok {
		return BuildResult{}, cause
	}
	if err := recorder.RecordForbiddenSourceRejection(ctx, request, reason, s.clock.Now().UTC()); err != nil {
		return BuildResult{}, errors.Join(cause, fmt.Errorf("record forbidden source rejection: %w", err))
	}
	return BuildResult{}, cause
}

func isForbiddenSourceReason(reason ReasonCode) bool {
	switch reason {
	case ReasonForbiddenDataClass, ReasonSecretDataRejected, ReasonClinicalDataRejected,
		ReasonUnsafeInstructionSource, ReasonSourcePathEscape, ReasonSourceSymlinkEscape:
		return true
	default:
		return false
	}
}

func (s *contextService) resolve(ctx context.Context, request BuildRequest)'''
if marker not in text:
    raise SystemExit("resolve marker not found")
service.write_text(text.replace(marker, helper, 1))

# Add an idempotent, transactionally audited rejected-attempt event using the snapshot ID sequence.
store = Path("internal/contextengine/postgres/store.go")
text = store.read_text()
marker = '''func (s *Store) RecordValidationFailure(ctx context.Context, snapshot contextengine.Snapshot, validation contextengine.SnapshotValidation, now time.Time) (err error) {
'''
method = '''func (s *Store) RecordForbiddenSourceRejection(ctx context.Context, request contextengine.BuildRequest, reason contextengine.ReasonCode, now time.Time) (err error) {
	keyPayload, err := json.Marshal(struct {
		OrganizationID string                   `json:"organization_id"`
		IdempotencyKey string                   `json:"idempotency_key"`
		Reason         contextengine.ReasonCode `json:"reason"`
	}{request.OrganizationID, request.IdempotencyKey, reason})
	if err != nil {
		return fmt.Errorf("encode forbidden source rejection key: %w", err)
	}
	rejectionHash := contextengine.DigestCanonicalBytes(keyPayload)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return mapError(err)
	}
	defer func() {
		if err != nil {
			rollback(tx)
		}
	}()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, rejectionHash); err != nil {
		return mapError(err)
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT aggregate_id FROM outbox_events WHERE aggregate_type='context_snapshot' AND event_type='context.forbidden_source_rejected' AND payload->>'organization_id'=$1 AND payload->>'request_hash'=$2 LIMIT 1`, request.OrganizationID, rejectionHash).Scan(&existing)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return mapError(err)
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return mapError(err)
	}
	var id int64
	if err = tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('context_snapshots','id'))`).Scan(&id); err != nil {
		return mapError(err)
	}
	attempt := contextengine.Snapshot{
		ID: id, OrganizationID: request.OrganizationID, OrganizationRevisionID: request.OrganizationRevisionID,
		ActorRoleID: request.ActorRoleID, Purpose: request.Purpose, ProjectRef: request.ProjectRef, TaskRef: request.TaskRef,
		Status: contextengine.SnapshotStatus("rejected"), RequestHash: rejectionHash,
		CorrelationID: request.CorrelationID, CausationID: request.CausationID, CreatedAt: now,
	}
	if err = appendAuditAndOutbox(ctx, tx, attempt, "context.forbidden_source_rejected", "system", "orgd", reason); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

''' + marker
if marker not in text:
    raise SystemExit("validation recorder method marker not found")
text = text.replace(marker, method, 1)
old_payload = '''		"omitted_segment_count":    snapshot.OmittedSegmentCount,
	}
'''
new_payload = '''		"omitted_segment_count":    snapshot.OmittedSegmentCount,
	}
	if snapshot.RequestHash != "" {
		value["request_hash"] = snapshot.RequestHash
	}
'''
if old_payload not in text:
    raise SystemExit("event payload marker not found")
store.write_text(text.replace(old_payload, new_payload, 1))

# Unit coverage for policy rejection recording and unchanged rejection semantics.
Path("internal/contextengine/service_forbidden_test.go").write_text(r'''package contextengine

import (
	"context"
	"errors"
	"testing"
	"time"
)

type secretMemoryProvider struct{}

func (secretMemoryProvider) ListApproved(context.Context, BuildRequest) ([]SourceRecord, error) {
	content := []byte("classified")
	return []SourceRecord{{Reference: "memory/secret", Version: "1", DataClass: DataSecret, Content: content, ContentHash: DigestMarkdown(content), Included: true}}, nil
}
func (secretMemoryProvider) ValidateVersion(context.Context, SourceRecord) error { return nil }

type forbiddenRecordingStore struct {
	SnapshotStore
	calls  int
	reason ReasonCode
}

func (s *forbiddenRecordingStore) RecordForbiddenSourceRejection(_ context.Context, _ BuildRequest, reason ReasonCode, _ time.Time) error {
	s.calls++
	s.reason = reason
	return nil
}

func TestServiceRecordsForbiddenSourceWithoutChangingRejection(t *testing.T) {
	fixture := newServiceFixture(t)
	recording := &forbiddenRecordingStore{SnapshotStore: fixture.store}
	service, err := NewService(
		ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20},
		fixture.registry,
		fixture.docs,
		fixture.canonical,
		NoopOwnerConstraintProvider{},
		secretMemoryProvider{},
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
	_, err = service.Build(t.Context(), fixture.request("forbidden-source"))
	if !errors.Is(err, ErrRejected) || ReasonOf(err) != ReasonSecretDataRejected {
		t.Fatalf("error=%v", err)
	}
	if recording.calls != 1 || recording.reason != ReasonSecretDataRejected {
		t.Fatalf("recorder calls=%d reason=%s", recording.calls, recording.reason)
	}
}
''')

# PostgreSQL 17 coverage: one idempotent event, no partial snapshot, decimal aggregate ID, safe payload.
Path("internal/contextengine/postgres/forbidden_source_integration_test.go").write_text(r'''//go:build integration

package postgres_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextpostgres "github.com/Mireuz13/explorarte-organization/internal/contextengine/postgres"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func TestForbiddenSourceRejectionIsIdempotentAndLeavesNoSnapshot(t *testing.T) {
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
	request := contextengine.BuildRequest{OrganizationID: integrationOrganization, OrganizationRevisionID: 1, ActorRoleID: "ingenieria_ia/qa", Purpose: "forbidden source test", IdempotencyKey: "forbidden-source-idempotency", CorrelationID: "corr-forbidden"}
	for range 2 {
		if err = store.RecordForbiddenSourceRejection(ctx, request, contextengine.ReasonClinicalDataRejected, time.Date(2026, 8, 5, 6, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	var snapshots, audits, outbox int
	if err = platform.Pool().QueryRow(ctx, `SELECT count(*) FROM context_snapshots WHERE organization_id=$1`, integrationOrganization).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err = platform.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE event_type='context.forbidden_source_rejected'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	var aggregateID, payload string
	if err = platform.Pool().QueryRow(ctx, `SELECT aggregate_id,payload::text FROM outbox_events WHERE event_type='context.forbidden_source_rejected'`).Scan(&aggregateID, &payload); err != nil {
		t.Fatal(err)
	}
	if err = platform.Pool().QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='context.forbidden_source_rejected'`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 || audits != 1 || outbox != 1 || !regexp.MustCompile(`^[0-9]+$`).MatchString(aggregateID) {
		t.Fatalf("snapshots=%d audits=%d outbox=%d aggregate=%q", snapshots, audits, outbox, aggregateID)
	}
	for _, forbidden := range []string{"classified", "clinical content", "memory", "rag_evidence", "profile"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("forbidden rejection payload leaked %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(payload, `"reason_code": "clinical_data_rejected"`) && !strings.Contains(payload, `"reason_code":"clinical_data_rejected"`) {
		t.Fatalf("missing reason code: %s", payload)
	}
}
''')

# Fitness and implementation documentation.
fitness = Path("scripts/check-context-fitness.sh")
text = fitness.read_text()
needle = "require 'context.policy_drift_rejected' internal/contextengine/postgres/store.go 'policy drift event missing'\n"
replacement = needle + "require 'context.forbidden_source_rejected' internal/contextengine/postgres/store.go 'forbidden source event missing'\n"
if needle not in text:
    raise SystemExit("fitness event marker not found")
fitness.write_text(text.replace(needle, replacement, 1))

doc = Path("docs/implementation/branch-07-context-engine/INTEGRATION.md")
text = doc.read_text()
appendix = '''

## Rejected forbidden-source attempts

A source rejected as `secret`, `clinical`, forbidden, path-escaping, symlink-escaping, or capability-granting from an unsafe tier never creates a `context_snapshots` row. PostgreSQL allocates an attempt identifier from the same snapshot sequence only for audit correlation and emits one idempotent `context.forbidden_source_rejected` audit/outbox pair per organization, idempotency key, and reason. The payload contains bounded metadata and hashes, never source content. This preserves the no-partial-snapshot invariant while satisfying rejection traceability.
'''
if "## Rejected forbidden-source attempts" not in text:
    doc.write_text(text.rstrip() + appendix)
