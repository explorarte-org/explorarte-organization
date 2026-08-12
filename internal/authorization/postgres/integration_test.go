//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	integrationOrganization = "explorarte"
	integrationOwner        = "empresa/human"
	// creativo/ was consolidated into negocio/ in the canonical catalog
	// (see role-catalog.yaml). The old ID stopped resolving, so every
	// approval request in this suite failed with role_not_found -- on main,
	// not just here.
	integrationRequester = "negocio/copywriter"
	approvalCapability   = "rag.publish_approved"
)

func TestDurableCapabilityPolicyEngine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store := openIntegrationStore(t, ctx)
	defer store.Close()
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	resetIntegrationSchema(t, ctx, store)
	syncCanonical(t, ctx, store)

	cfg := integrationConfig()
	runtime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		t.Fatalf("open authorization runtime: %v", err)
	}
	service := runtime.Service

	t.Run("create request, idempotency, and conflict", func(t *testing.T) {
		digest := authorization.DigestAction([]byte("publish candidate one"))
		command := requestCommand("request-idempotency", "candidate-1", digest)
		first, err := service.RequestApproval(ctx, command)
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		if first.Reused || first.Request.Status != authorization.RequestPending || first.Request.RequestHash == "" {
			t.Fatalf("unexpected first request: %+v", first)
		}
		second, err := service.RequestApproval(ctx, command)
		if err != nil {
			t.Fatalf("reuse request: %v", err)
		}
		if !second.Reused || second.Request.ID != first.Request.ID {
			t.Fatalf("unexpected reuse: first=%+v second=%+v", first, second)
		}
		command.ResourceID = "candidate-other"
		if _, err = service.RequestApproval(ctx, command); !errors.Is(err, authorization.ErrIdempotencyConflict) {
			t.Fatalf("idempotency conflict=%v", err)
		}
		assertEventCount(t, ctx, store, first.Request.ID, "authorization.request_created", 1)
		assertEventCount(t, ctx, store, first.Request.ID, "authorization.request_reused", 1)
	})

	t.Run("owner decision, requester consumption, and exact retry", func(t *testing.T) {
		request := createRequest(t, ctx, service, "request-consume", "candidate-consume", "consume action")
		if _, err := service.DecideRequest(ctx, authorization.DecideRequestCommand{RequestID: request.ID, Decision: authorization.DecisionApprove, ActorRoleID: "empresa/ceo", Reason: "not the owner"}); !errors.Is(err, authorization.ErrApproverNotOwner) {
			t.Fatalf("non-owner decision=%v", err)
		}
		approved := approveRequest(t, ctx, service, request.ID)
		if approved.Status != authorization.RequestApproved {
			t.Fatalf("approved=%+v", approved)
		}
		result, err := service.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: request.ID, ActorRoleID: integrationRequester, ActionDigest: request.ActionDigest, CorrelationID: "consume-correlation", CausationID: "consume-causation"})
		if err != nil || result.Reused || result.Use == nil || result.Request.Status != authorization.RequestConsumed {
			t.Fatalf("consume result=%+v err=%v", result, err)
		}
		retry, err := service.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: request.ID, ActorRoleID: integrationRequester, ActionDigest: request.ActionDigest})
		if err != nil || !retry.Reused || retry.Use == nil || retry.Use.ID != result.Use.ID {
			t.Fatalf("retry=%+v err=%v", retry, err)
		}
		var uses int
		if err = store.Pool().QueryRow(ctx, `SELECT count(*) FROM authorization_uses WHERE request_id=$1`, request.ID).Scan(&uses); err != nil || uses != 1 {
			t.Fatalf("uses=%d err=%v", uses, err)
		}
		var correlation, causation *string
		if err = store.Pool().QueryRow(ctx, `SELECT correlation_id,causation_id FROM audit_events WHERE subject_type='authorization_request' AND subject_id=$1 AND event_type='authorization.approval_consumed'`, fmt.Sprint(request.ID)).Scan(&correlation, &causation); err != nil {
			t.Fatal(err)
		}
		if correlation == nil || causation == nil || *correlation != "consume-correlation" || *causation != "consume-causation" {
			t.Fatalf("audit correlation=%v causation=%v", correlation, causation)
		}
	})

	t.Run("concurrent consumption creates exactly one use", func(t *testing.T) {
		request := createRequest(t, ctx, service, "request-concurrent", "candidate-concurrent", "concurrent action")
		approveRequest(t, ctx, service, request.ID)
		const workers = 12
		var successful, first, reused atomic.Int32
		errorsSeen := make(chan error, workers)
		var group sync.WaitGroup
		for index := 0; index < workers; index++ {
			group.Add(1)
			go func() {
				defer group.Done()
				result, consumeErr := service.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: request.ID, ActorRoleID: integrationRequester, ActionDigest: request.ActionDigest})
				if consumeErr != nil {
					errorsSeen <- consumeErr
					return
				}
				successful.Add(1)
				if result.Reused {
					reused.Add(1)
				} else {
					first.Add(1)
				}
			}()
		}
		group.Wait()
		close(errorsSeen)
		for consumeErr := range errorsSeen {
			t.Errorf("concurrent consume: %v", consumeErr)
		}
		if successful.Load() != workers || first.Load() != 1 || reused.Load() != workers-1 {
			t.Fatalf("successful=%d first=%d reused=%d", successful.Load(), first.Load(), reused.Load())
		}
		var uses int
		if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM authorization_uses WHERE request_id=$1`, request.ID).Scan(&uses); err != nil || uses != 1 {
			t.Fatalf("uses=%d err=%v", uses, err)
		}
	})

	t.Run("rejected approval cannot be consumed", func(t *testing.T) {
		request := createRequest(t, ctx, service, "request-rejected", "candidate-rejected", "reject action")
		if _, err := service.DecideRequest(ctx, authorization.DecideRequestCommand{RequestID: request.ID, Decision: authorization.DecisionReject, ActorRoleID: integrationOwner, Reason: "owner rejected"}); err != nil {
			t.Fatal(err)
		}
		result, err := service.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: request.ID, ActorRoleID: integrationRequester, ActionDigest: request.ActionDigest})
		if !errors.Is(err, authorization.ErrApprovalRejected) || result.ReasonCode != authorization.ReasonApprovalRejected {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("expiration and cancellation", func(t *testing.T) {
		base := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
		clock := &mutableClock{now: base}
		clockService, err := authorization.NewService(authorization.ServiceConfig{OrganizationID: integrationOrganization, DefaultTTL: time.Minute, MaxTTL: time.Hour, ExpireBatchSize: 100, OutboxMaxAttempts: 10}, runtime.Authorizer, runtime.Store, clock)
		if err != nil {
			t.Fatal(err)
		}
		digest := authorization.DigestAction([]byte("expire action"))
		command := requestCommand("request-expire", "candidate-expire", digest)
		command.TTL = 0 // Exercise the service default TTL of one minute.
		created, err := clockService.RequestApproval(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		approveRequestWithService(t, ctx, clockService, created.Request.ID)
		clock.now = base.Add(2 * time.Minute)
		result, err := clockService.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: created.Request.ID, ActorRoleID: integrationRequester, ActionDigest: digest})
		if !errors.Is(err, authorization.ErrApprovalExpired) || result.Request.Status != authorization.RequestExpired {
			t.Fatalf("expired result=%+v err=%v", result, err)
		}

		cancelled := createRequest(t, ctx, service, "request-cancel", "candidate-cancel", "cancel action")
		value, err := service.CancelRequest(ctx, authorization.CancelRequestCommand{RequestID: cancelled.ID, ActorRoleID: integrationRequester, Reason: "requester cancelled"})
		if err != nil || value.Status != authorization.RequestCancelled {
			t.Fatalf("cancel=%+v err=%v", value, err)
		}
	})

	t.Run("revision and matrix policy drift", func(t *testing.T) {
		revisionRequest := createRequest(t, ctx, service, "request-revision-drift", "candidate-revision-drift", "revision drift")
		approveRequest(t, ctx, service, revisionRequest.ID)
		oldRevision := revisionRequest.OrganizationRevisionID
		var newRevision int64
		err := store.Pool().QueryRow(ctx, `
			INSERT INTO organization_registry_revisions(canonical_hash,previous_revision_id,status,schema_versions,document_hashes,counts,diff,applied_at)
			SELECT repeat('d',64),id,'applied',schema_versions,document_hashes,counts,diff,clock_timestamp()
			FROM organization_registry_revisions WHERE id=$1 RETURNING id
		`, oldRevision).Scan(&newRevision)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$2 WHERE id=$1`, integrationOrganization, newRevision); err != nil {
			t.Fatal(err)
		}
		result, err := service.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: revisionRequest.ID, ActorRoleID: integrationRequester, ActionDigest: revisionRequest.ActionDigest})
		if !errors.Is(err, authorization.ErrApprovalPolicyDrift) || result.ReasonCode != authorization.ReasonApprovalPolicyDrift {
			t.Fatalf("revision drift result=%+v err=%v", result, err)
		}
		if _, err = store.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$2 WHERE id=$1`, integrationOrganization, oldRevision); err != nil {
			t.Fatal(err)
		}

		matrixRequest := createRequest(t, ctx, service, "request-matrix-drift", "candidate-matrix-drift", "matrix drift")
		approveRequest(t, ctx, service, matrixRequest.ID)
		if _, err = store.Pool().Exec(ctx, `UPDATE organization_registry_revisions SET document_hashes=jsonb_set(document_hashes,'{capability-matrix.yaml}',to_jsonb(repeat('e',64))) WHERE id=$1`, oldRevision); err != nil {
			t.Fatal(err)
		}
		result, err = service.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: matrixRequest.ID, ActorRoleID: integrationRequester, ActionDigest: matrixRequest.ActionDigest})
		if !errors.Is(err, authorization.ErrApprovalPolicyDrift) || result.ReasonCode != authorization.ReasonApprovalPolicyDrift {
			t.Fatalf("matrix drift result=%+v err=%v", result, err)
		}
		if _, err = store.Pool().Exec(ctx, `UPDATE organization_registry_revisions SET document_hashes=jsonb_set(document_hashes,'{capability-matrix.yaml}',to_jsonb($2::text)) WHERE id=$1`, oldRevision, runtime.Authorizer.MatrixHash()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("role retirement after approval", func(t *testing.T) {
		request := createRequest(t, ctx, service, "request-retired-role", "candidate-retired-role", "retired role action")
		approveRequest(t, ctx, service, request.ID)
		if _, err := store.Pool().Exec(ctx, `UPDATE organization_roles SET retired_at=clock_timestamp() WHERE organization_id=$1 AND id=$2`, integrationOrganization, integrationRequester); err != nil {
			t.Fatal(err)
		}
		result, err := service.ConsumeApproval(ctx, authorization.ConsumeApprovalCommand{RequestID: request.ID, ActorRoleID: integrationRequester, ActionDigest: request.ActionDigest})
		if !errors.Is(err, authorization.ErrRoleRetired) || result.ReasonCode != authorization.ReasonRoleRetired {
			t.Fatalf("retired role result=%+v err=%v", result, err)
		}
		if _, err = store.Pool().Exec(ctx, `UPDATE organization_roles SET retired_at=NULL WHERE organization_id=$1 AND id=$2`, integrationOrganization, integrationRequester); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("hard deny is impossible to approve", func(t *testing.T) {
		digest := authorization.DigestAction([]byte("read clinical data"))
		_, err := service.RequestApproval(ctx, authorization.RequestApprovalCommand{ActorRoleID: integrationOwner, CapabilityID: "cell.read_clinical_data", ResourceType: "clinical_record", ResourceID: "forbidden", ActionDigest: digest, IdempotencyKey: "hard-deny", Reason: "must never be approved"})
		if !errors.Is(err, authorization.ErrCapabilityDenied) {
			t.Fatalf("hard deny request=%v", err)
		}
		var count int
		if queryErr := store.Pool().QueryRow(ctx, `SELECT count(*) FROM authorization_requests WHERE idempotency_key='hard-deny'`).Scan(&count); queryErr != nil || count != 0 {
			t.Fatalf("hard deny persisted count=%d err=%v", count, queryErr)
		}

		deploymentDigest := authorization.DigestAction([]byte("deployment approval drift"))
		created, err := service.RequestApproval(ctx, authorization.RequestApprovalCommand{ActorRoleID: "empresa/ceo", CapabilityID: "deployment.request", ResourceType: "deployment", ResourceID: "drift", ActionDigest: deploymentDigest, IdempotencyKey: "hard-deny-after-request", Reason: "request created before authority drift"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.Pool().Exec(ctx, `UPDATE organization_roles SET authority_class='transversal_audit' WHERE organization_id=$1 AND id='empresa/ceo'`, integrationOrganization); err != nil {
			t.Fatal(err)
		}
		_, err = service.DecideRequest(ctx, authorization.DecideRequestCommand{RequestID: created.Request.ID, Decision: authorization.DecisionApprove, ActorRoleID: integrationOwner, Reason: "must fail after hard deny drift"})
		if !errors.Is(err, authorization.ErrCapabilityDenied) {
			t.Fatalf("hard deny decision=%v", err)
		}
		var status authorization.RequestStatus
		var decisions int
		if err = store.Pool().QueryRow(ctx, `SELECT status FROM authorization_requests WHERE id=$1`, created.Request.ID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err = store.Pool().QueryRow(ctx, `SELECT count(*) FROM authorization_decisions WHERE request_id=$1`, created.Request.ID).Scan(&decisions); err != nil {
			t.Fatal(err)
		}
		if status != authorization.RequestPending || decisions != 0 {
			t.Fatalf("hard-denied request status=%s decisions=%d", status, decisions)
		}
		if _, err = store.Pool().Exec(ctx, `UPDATE organization_roles SET authority_class='executive' WHERE organization_id=$1 AND id='empresa/ceo'`, integrationOrganization); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("audit and outbox failure roll back request", func(t *testing.T) {
		assertCreateRollback(t, ctx, store, service, "audit", "request-rollback-audit", []string{
			`CREATE OR REPLACE FUNCTION fail_authorization_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='authorization.request_created' THEN RAISE EXCEPTION 'forced audit failure'; END IF; RETURN NEW; END $$`,
			`CREATE TRIGGER authorization_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_authorization_audit()`,
		}, []string{`DROP TRIGGER authorization_audit_failure ON audit_events`, `DROP FUNCTION fail_authorization_audit()`})
		assertCreateRollback(t, ctx, store, service, "outbox", "request-rollback-outbox", []string{
			`CREATE OR REPLACE FUNCTION fail_authorization_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='authorization.request_created' THEN RAISE EXCEPTION 'forced outbox failure'; END IF; RETURN NEW; END $$`,
			`CREATE TRIGGER authorization_outbox_failure BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION fail_authorization_outbox()`,
		}, []string{`DROP TRIGGER authorization_outbox_failure ON outbox_events`, `DROP FUNCTION fail_authorization_outbox()`})
	})

	t.Run("events use existing audit and outbox schemas", func(t *testing.T) {
		var auditCount, outboxCount int
		if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE subject_type='authorization_request'`).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type='authorization_request'`).Scan(&outboxCount); err != nil {
			t.Fatal(err)
		}
		if auditCount == 0 || outboxCount == 0 {
			t.Fatalf("audit=%d outbox=%d", auditCount, outboxCount)
		}
		var containsReason, containsReference bool
		if err := store.Pool().QueryRow(ctx, `
			SELECT payload ? 'reason', payload ? 'reference'
			FROM outbox_events WHERE aggregate_type='authorization_request' ORDER BY id LIMIT 1
		`).Scan(&containsReason, &containsReference); err != nil {
			t.Fatal(err)
		}
		if containsReason || containsReference {
			t.Fatal("outbox leaked free-form reason or opaque reference")
		}
	})

	t.Run("down migration removes only authorization schemas", func(t *testing.T) {
		if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
			t.Fatalf("refusing destructive operation: %v", err)
		}
		if _, err := store.Pool().Exec(ctx, `TRUNCATE authorization_uses,authorization_decisions,authorization_requests RESTART IDENTITY CASCADE`); err != nil {
			t.Fatal(err)
		}
		down, err := rootmigrations.Files.ReadFile("000005_create_capability_policy_engine.down.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.Pool().Exec(ctx, string(down)); err != nil {
			t.Fatalf("down migration: %v", err)
		}
		var requestsMissing, auditPresent, outboxPresent bool
		if err = store.Pool().QueryRow(ctx, `SELECT to_regclass('public.authorization_requests') IS NULL,to_regclass('public.audit_events') IS NOT NULL,to_regclass('public.outbox_events') IS NOT NULL`).Scan(&requestsMissing, &auditPresent, &outboxPresent); err != nil {
			t.Fatal(err)
		}
		if !requestsMissing || !auditPresent || !outboxPresent {
			t.Fatalf("requestsMissing=%t auditPresent=%t outboxPresent=%t", requestsMissing, auditPresent, outboxPresent)
		}
		if _, err = store.Pool().Exec(ctx, `DELETE FROM schema_migrations WHERE version=5`); err != nil {
			t.Fatal(err)
		}
		if _, err = runner.Up(ctx); err != nil {
			t.Fatalf("restore migration: %v", err)
		}
	})
}

type mutableClock struct{ now time.Time }

func (c *mutableClock) Now() time.Time { return c.now }

func requestCommand(idempotency, resourceID, digest string) authorization.RequestApprovalCommand {
	return authorization.RequestApprovalCommand{ActorRoleID: integrationRequester, CapabilityID: approvalCapability, ResourceType: "rag_candidate", ResourceID: resourceID, ActionDigest: digest, IdempotencyKey: idempotency, Reason: "one-time owner decision", TTL: 30 * time.Minute, CorrelationID: "integration-correlation", CausationID: "integration-causation"}
}

func createRequest(t *testing.T, ctx context.Context, service *authorization.Service, idempotency, resourceID, action string) authorization.ApprovalRequest {
	t.Helper()
	result, err := service.RequestApproval(ctx, requestCommand(idempotency, resourceID, authorization.DigestAction([]byte(action))))
	if err != nil {
		t.Fatalf("create %s: %v", idempotency, err)
	}
	return result.Request
}

func approveRequest(t *testing.T, ctx context.Context, service *authorization.Service, id int64) authorization.ApprovalRequest {
	t.Helper()
	return approveRequestWithService(t, ctx, service, id)
}

func approveRequestWithService(t *testing.T, ctx context.Context, service *authorization.Service, id int64) authorization.ApprovalRequest {
	t.Helper()
	value, err := service.DecideRequest(ctx, authorization.DecideRequestCommand{RequestID: id, Decision: authorization.DecisionApprove, ActorRoleID: integrationOwner, Reason: "owner approves one action", Reference: "ticket:integration"})
	if err != nil {
		t.Fatalf("approve %d: %v", id, err)
	}
	return value
}

func assertEventCount(t *testing.T, ctx context.Context, store *platformpostgres.Store, requestID int64, eventType string, expected int) {
	t.Helper()
	var count int
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE subject_type='authorization_request' AND subject_id=$1 AND event_type=$2`, fmt.Sprint(requestID), eventType).Scan(&count); err != nil || count != expected {
		t.Fatalf("event %s count=%d want=%d err=%v", eventType, count, expected, err)
	}
}

func assertCreateRollback(t *testing.T, ctx context.Context, store *platformpostgres.Store, service *authorization.Service, label, idempotency string, setupSQL, cleanupSQL []string) {
	t.Helper()
	for _, statement := range setupSQL {
		if _, err := store.Pool().Exec(ctx, statement); err != nil {
			t.Fatalf("install %s failure trigger: %v", label, err)
		}
	}
	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		for _, statement := range cleanupSQL {
			if _, err := store.Pool().Exec(context.Background(), statement); err != nil {
				t.Errorf("remove %s failure trigger: %v", label, err)
			}
		}
	}
	defer cleanup()
	command := requestCommand(idempotency, "candidate-"+label, authorization.DigestAction([]byte("rollback "+label)))
	if _, err := service.RequestApproval(ctx, command); err == nil {
		t.Fatalf("%s failure did not propagate", label)
	}
	var requests, outbox int
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM authorization_requests WHERE idempotency_key=$1`, idempotency).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type='authorization_request' AND payload->>'resource_id'=$1`, "candidate-"+label).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || outbox != 0 {
		t.Fatalf("%s rollback requests=%d outbox=%d", label, requests, outbox)
	}
	cleanup()
}

func openIntegrationStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("ORG_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: databaseURL, SSLMode: "disable", MaxConns: 20, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 30 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "authorization-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetIntegrationSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
		TRUNCATE authorization_uses,authorization_decisions,authorization_requests,
		         staging_events,staging_reviews,staging_promotions,staging_checks,
		         staging_workspace_artifacts,staging_artifacts,staging_workspaces,
		         outbox_events,task_dead_letters,task_events,task_leases,task_attempts,
		         task_evidence,task_requirements,task_dependencies,tasks,
		         organization_reporting_lines,organization_registry_revision_documents,
		         organization_roles,organizational_units,organizations,
		         organization_registry_revisions,audit_events RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatal(err)
	}
}

func syncCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	repository, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repository, integrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := service.SynchronizeCanonical(ctx, true); err != nil || !result.Applied {
		t.Fatalf("sync canonical: result=%+v err=%v", result, err)
	}
}

func integrationConfig() config.Config {
	return config.Config{
		Registry:      config.RegistryConfig{CanonicalDir: filepath.Join("..", "..", "..", "docs", "canonical"), SyncTimeout: 30 * time.Second},
		Tasks:         config.TaskConfig{OrganizationID: integrationOrganization, OutboxMaxAttempts: 10},
		Authorization: config.AuthorizationConfig{DefaultTTL: 30 * time.Minute, MaxTTL: 24 * time.Hour, CommandTimeout: 30 * time.Second, ExpireBatchSize: 100},
	}
}
