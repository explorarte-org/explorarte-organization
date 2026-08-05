//go:build integration

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
