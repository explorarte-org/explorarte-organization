//go:build integration

package postgres_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	harnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
)

// This test owns its migrated organization/task/attempt fixture and does not
// require an external provider, another test's rows, or assumed numeric IDs.
func TestSkillForgeHarnessPostgresDurability(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	content := "SkillForge durable context fixture"
	digestBytes := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(digestBytes[:])
	var snapshotID int64
	err := f.store.Pool().QueryRow(f.ctx, `
 INSERT INTO context_snapshots(organization_id,organization_revision_id,actor_role_id,purpose,
 task_ref,idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,
 status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,created_at)
 SELECT organization_id,organization_revision_id,assigned_role_id,'harness durability',id::text,
 'skillforge-durability-context',$2,$2,$2,$2,'ready',1,1,1,0,$3,NOW() FROM tasks WHERE id=$1
 RETURNING id`, f.taskID, digest, len(content)).Scan(&snapshotID)
	if err != nil {
		t.Fatalf("create context snapshot: %v", err)
	}
	_, err = f.store.Pool().Exec(f.ctx, `
 INSERT INTO context_segments(snapshot_id,organization_id,ordinal,render_ordinal,authority_priority,
 authority_tier,source_kind,source_reference,source_version,instruction_class,trust_class,data_class,
 included,content_hash,byte_count,content,created_at)
 VALUES($1,$2,1,1,5,'task_context','task_context',$3,'v1','data','scoped','organizational',true,$4,$5,$6,NOW())`,
		snapshotID, historyOrganization, strconv.FormatInt(f.taskID, 10), digest, len(content), []byte(content))
	if err != nil {
		t.Fatalf("create context bytes: %v", err)
	}
	spec := integrationDescriptorSpec(f, "skillforge-durable-run")
	spec.Context = executionharness.InitialContext{ID: strconv.FormatInt(snapshotID, 10), Version: "1", Digest: digest, Content: content}
	spec.Policy.ExecutionProfileID = "worker/skill-forge/v1"
	descriptor, err := executionharness.BuildRunDescriptor(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.history.EnsureRunDescriptor(f.ctx, descriptor); err != nil {
		t.Fatal(err)
	}
	event := f.event(spec.Identity.RunID, executionharness.EventRunStarted)
	if _, err = f.history.Append(f.ctx, spec.Identity.RunID, 0, event); err != nil {
		t.Fatal(err)
	}
	// Close the original pool before constructing independent store objects.
	f.store.Close()
	dsn := os.Getenv("ORG_TEST_DATABASE_URL")
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": dsn, "ORG_DATABASE_MAX_CONNS": "4", "ORG_DATABASE_MIN_CONNS": "0"}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := platformpostgres.Open(f.ctx, cfg.Database, "skillforge-durability-reopened")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = testdbguard.RequireTestDatabase(f.ctx, dsn, reopened.Pool()); err != nil {
		t.Fatal(err)
	}
	history, err := harnesspostgres.New(reopened, historyOrganization)
	if err != nil {
		t.Fatal(err)
	}
	got, err := history.ReadRunDescriptor(f.ctx, historyOrganization, spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := descriptor.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	gotDigest, err := got.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if gotDigest != wantDigest || got.TaskID != f.taskID || got.AttemptID != f.attemptID || got.ContextID != spec.Context.ID || got.ContextDigest != digest {
		t.Fatalf("descriptor changed after reopening: %+v", got)
	}
	events, err := history.Read(f.ctx, spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Sequence != 1 || events[0].Type != event.Type || events[0].TaskID != f.taskID || events[0].AttemptID != f.attemptID || events[0].OrganizationID != historyOrganization || events[0].CorrelationID != event.CorrelationID || events[0].CausationID != event.CausationID {
		t.Fatalf("event changed after reopening: %+v", events)
	}
	var stored []byte
	var storedHash string
	if err = reopened.Pool().QueryRow(f.ctx, `SELECT s.content,c.rendered_hash FROM context_segments s JOIN context_snapshots c ON c.id=s.snapshot_id WHERE c.id=$1 AND c.organization_id=$2`, snapshotID, historyOrganization).Scan(&stored, &storedHash); err != nil {
		t.Fatal(err)
	}
	if string(stored) != content || storedHash != digest {
		t.Fatal("persisted context bytes/digest differ")
	}
	t.Log("Harness descriptor, event identity, and real context survive store closure and recreation")
}
