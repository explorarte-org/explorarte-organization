//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egresspostgres "github.com/Mireuz13/explorarte-organization/internal/modelegress/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const egressIntegrationOrganization = "explorarte"

func TestModelEgressPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openEgressStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != 8 {
		t.Fatalf("current migration=%d want=8", result.Current)
	}
	resetEgressSchema(t, ctx, platform)
	revision := syncEgressCanonical(t, ctx, platform)
	store, err := egresspostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := modelegress.LoadCanonicalPolicy(filepath.Join("..", "..", "..", "docs", "canonical"), modelegress.LoadOptions{KnownProviders: []string{"alibaba_token_plan_via_claude_code", "deepseek", "openai_compatible"}})
	if err != nil {
		t.Fatal(err)
	}
	plan := modelegress.RegistryPlan{OrganizationID: egressIntegrationOrganization, OrganizationRevisionID: revision.ID, CanonicalHash: canonical.CanonicalHash, Policy: canonical}

	t.Run("initial sync, immutable rules and idempotency", func(t *testing.T) {
		first, applyErr := store.Apply(ctx, plan)
		if applyErr != nil || !first.Applied || first.NoOp {
			t.Fatalf("first=%+v err=%v", first, applyErr)
		}
		second, applyErr := store.Apply(ctx, plan)
		if applyErr != nil || !second.NoOp || second.PolicyVersionID != first.PolicyVersionID {
			t.Fatalf("second=%+v err=%v", second, applyErr)
		}
		status, statusErr := store.Status(ctx, plan)
		if statusErr != nil || !status.Synchronized || status.Rules != len(canonical.HardDenies)+len(canonical.Rules) {
			t.Fatalf("status=%+v err=%v", status, statusErr)
		}
		var allowRules int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM model_egress_rules WHERE policy_version_id=$1 AND effect='allow'`, first.PolicyVersionID).Scan(&allowRules); err != nil {
			t.Fatal(err)
		}
		if allowRules != 0 {
			t.Fatalf("productive materialization has %d allow rules", allowRules)
		}
		if _, err := platform.Pool().Exec(ctx, `UPDATE model_egress_policy_versions SET canonical_hash=$1 WHERE id=$2`, modelegress.SHA256Bytes([]byte("mutated")), first.PolicyVersionID); err == nil {
			t.Fatal("historical policy version was mutable")
		}
		if _, err := platform.Pool().Exec(ctx, `INSERT INTO model_egress_rules(policy_version_id,organization_id,provider_id,data_classification,effect,reason_code,hard_deny) VALUES($1,$2,'post.materialization','public','deny','late_rule_forbidden',FALSE)`, first.PolicyVersionID, egressIntegrationOrganization); err == nil {
			t.Fatal("historical policy accepted a late rule")
		}
	})

	resolved, err := store.ResolveForRevision(ctx, egressIntegrationOrganization, revision.ID)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("same policy version can bind multiple organization revisions", func(t *testing.T) {
		secondRevision := insertEgressRevision(t, ctx, platform, canonical.CanonicalHash, "same-policy-new-org-revision")
		secondPlan := plan
		secondPlan.OrganizationRevisionID = secondRevision
		applied, applyErr := store.Apply(ctx, secondPlan)
		if applyErr != nil || !applied.Applied || applied.PolicyVersionID != resolved.Version.ID {
			t.Fatalf("binding reuse=%+v err=%v", applied, applyErr)
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_revision_bindings WHERE policy_version_id=$1`, resolved.Version.ID, 2)
		setCurrentEgressRevision(t, ctx, platform, revision.ID)
	})

	t.Run("version conflicts and new semantic versions", func(t *testing.T) {
		newPolicy := canonical
		newPolicy.PolicyVersion = 2
		newPolicy.Rules = append([]modelegress.Rule(nil), canonical.Rules...)
		newPolicy.Rules[0].ReasonCode = "organizational_egress_still_not_approved"
		newPolicy.CanonicalHash = semanticFixtureHash(newPolicy)
		newRevision := insertEgressRevision(t, ctx, platform, newPolicy.CanonicalHash, "egress-v2")
		newPlan := modelegress.RegistryPlan{OrganizationID: egressIntegrationOrganization, OrganizationRevisionID: newRevision, CanonicalHash: newPolicy.CanonicalHash, Policy: newPolicy}
		created, applyErr := store.Apply(ctx, newPlan)
		if applyErr != nil || !created.Applied || created.PolicyVersionID == resolved.Version.ID {
			t.Fatalf("new version=%+v err=%v", created, applyErr)
		}

		conflicting := newPolicy
		conflicting.CanonicalHash = modelegress.SHA256Bytes([]byte("same-version-different-hash"))
		conflictRevision := insertEgressRevision(t, ctx, platform, conflicting.CanonicalHash, "conflicting-v2")
		_, applyErr = store.Apply(ctx, modelegress.RegistryPlan{OrganizationID: egressIntegrationOrganization, OrganizationRevisionID: conflictRevision, CanonicalHash: conflicting.CanonicalHash, Policy: conflicting})
		if !errors.Is(applyErr, modelegress.ErrPolicyConflict) {
			t.Fatalf("same version/different hash error=%v", applyErr)
		}

		redundant := newPolicy
		redundant.PolicyVersion = 3
		redundantRevision := insertEgressRevision(t, ctx, platform, redundant.CanonicalHash, "redundant-v3")
		_, applyErr = store.Apply(ctx, modelegress.RegistryPlan{OrganizationID: egressIntegrationOrganization, OrganizationRevisionID: redundantRevision, CanonicalHash: redundant.CanonicalHash, Policy: redundant})
		if !errors.Is(applyErr, modelegress.ErrPolicyConflict) {
			t.Fatalf("new version/same hash error=%v", applyErr)
		}
		setCurrentEgressRevision(t, ctx, platform, revision.ID)
	})

	t.Run("concurrent sync creates one version and one binding", func(t *testing.T) {
		concurrent := canonical
		concurrent.PolicyVersion = 4
		concurrent.Rules = append([]modelegress.Rule(nil), canonical.Rules...)
		concurrent.Rules[0].ReasonCode = "concurrent_sync_fixture"
		concurrent.CanonicalHash = semanticFixtureHash(concurrent)
		concurrentRevision := insertEgressRevision(t, ctx, platform, concurrent.CanonicalHash, "concurrent-v4")
		concurrentPlan := modelegress.RegistryPlan{OrganizationID: egressIntegrationOrganization, OrganizationRevisionID: concurrentRevision, CanonicalHash: concurrent.CanonicalHash, Policy: concurrent}
		var wg sync.WaitGroup
		results := make(chan modelegress.RegistrySyncResult, 2)
		errorsCh := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				value, applyErr := store.Apply(ctx, concurrentPlan)
				if applyErr != nil {
					errorsCh <- applyErr
					return
				}
				results <- value
			}()
		}
		wg.Wait()
		close(results)
		close(errorsCh)
		for applyErr := range errorsCh {
			t.Fatalf("concurrent apply error=%v", applyErr)
		}
		applied, noOp := 0, 0
		for value := range results {
			if value.Applied {
				applied++
			}
			if value.NoOp {
				noOp++
			}
		}
		if applied != 1 || noOp != 1 {
			t.Fatalf("concurrent outcomes applied=%d noop=%d", applied, noOp)
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_policy_versions WHERE organization_id=$1 AND policy_version=4`, egressIntegrationOrganization, 1)
		setCurrentEgressRevision(t, ctx, platform, revision.ID)
	})

	t.Run("database enforces hard-deny and tenant-safe constraints", func(t *testing.T) {
		if _, err := platform.Pool().Exec(ctx, `INSERT INTO model_egress_rules(policy_version_id,organization_id,provider_id,data_classification,effect,reason_code,hard_deny) VALUES($1,$2,'test.invalid','secret','deny','invalid_secret',FALSE)`, resolved.Version.ID, egressIntegrationOrganization); err == nil {
			t.Fatal("secret rule without hard_deny persisted")
		}
		if _, err := platform.Pool().Exec(ctx, `INSERT INTO model_egress_revision_bindings(organization_id,organization_revision_id,policy_version_id,canonical_hash) VALUES('other',$1,$2,$3)`, revision.ID, resolved.Version.ID, resolved.CanonicalHash); err == nil {
			t.Fatal("cross-tenant binding persisted")
		}
		for _, classes := range []string{`[]`, `["clinical"]`, `["secret"]`, `["unknown"]`} {
			fixture := insertPreSendFixture(t, ctx, platform, revision.ID, resolved.Version.ID, resolved.CanonicalHash, "invalid-allow-"+modelegress.SHA256Bytes([]byte(classes))[:8])
			if _, err := platform.Pool().Exec(ctx, `
INSERT INTO model_egress_evaluations(
    invocation_id,dispatch_attempt_id,policy_version_id,organization_id,
    organization_revision_id,model_profile_version_id,provider_id,provider_transport,
    action_digest,capability_matrix_hash,context_classifications,
    context_classifications_hash,authorization_effect,authorization_reason_code,
    egress_effect,egress_reason_codes,decision_hash
) VALUES($1,$2,$3,$4,$5,$6,'test.fake','fake_adapter',$7,$8,$9::jsonb,$10,'allow','allowed_by_grant','allow','["fixture_allow"]'::jsonb,$11)`,
				fixture.invocationID, fixture.attemptID, fixture.policyID, egressIntegrationOrganization,
				fixture.revisionID, fixture.profileID, modelegress.SHA256Bytes([]byte("action")),
				fixture.capabilityHash, classes, modelegress.SHA256Bytes([]byte(classes)), modelegress.SHA256Bytes([]byte("decision"))); err == nil {
				t.Fatalf("database accepted egress allow for classifications %s", classes)
			}
		}
	})

	t.Run("allow evaluation and send_started are atomic", func(t *testing.T) {
		fixture := insertPreSendFixture(t, ctx, platform, revision.ID, resolved.Version.ID, resolved.CanonicalHash, "allow")
		evaluation := fixture.evaluation(modelegress.AuthorizationAllow, modelegress.EffectAllow, []string{"fixture_organizational_allow"})
		if err := store.PersistPreSendAllowAndMarkSendStarted(ctx, modelegress.PersistAllowCommand{Evaluation: evaluation, ClaimToken: fixture.claimToken, ProviderIdempotencyKeyHash: modelegress.SHA256Bytes([]byte("provider-idempotency")), Deadline: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		var invocationStatus, attemptStatus string
		if err := platform.Pool().QueryRow(ctx, `SELECT i.status,a.status FROM model_invocations i JOIN model_dispatch_attempts a ON a.invocation_id=i.id WHERE i.id=$1`, fixture.invocationID).Scan(&invocationStatus, &attemptStatus); err != nil {
			t.Fatal(err)
		}
		if invocationStatus != "send_started" || attemptStatus != "send_started" {
			t.Fatalf("states invocation=%s attempt=%s", invocationStatus, attemptStatus)
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE dispatch_attempt_id=$1 AND authorization_effect='allow' AND egress_effect='allow'`, fixture.attemptID, 1)
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE subject_type='model_invocation' AND subject_id=$1 AND event_type='model.egress_allowed'`, fmt.Sprint(fixture.invocationID), 1)
	})

	t.Run("deny evaluation and failed_before_send are atomic", func(t *testing.T) {
		fixture := insertPreSendFixture(t, ctx, platform, revision.ID, resolved.Version.ID, resolved.CanonicalHash, "deny")
		evaluation := fixture.evaluation(modelegress.AuthorizationDeny, modelegress.EffectNotEvaluated, nil)
		evaluation.AuthorizationReasonCode = "grant_missing"
		evaluation.DecisionHash = modelegress.DecisionHash(evaluation)
		if err := store.PersistPreSendDenyAndFail(ctx, modelegress.PersistDenyCommand{Evaluation: evaluation, ClaimToken: fixture.claimToken, ErrorCode: "authorization_denied", OutboxMaxAttempts: 10}); err != nil {
			t.Fatal(err)
		}
		var invocationStatus, attemptStatus string
		if err := platform.Pool().QueryRow(ctx, `SELECT i.status,a.status FROM model_invocations i JOIN model_dispatch_attempts a ON a.invocation_id=i.id WHERE i.id=$1`, fixture.invocationID).Scan(&invocationStatus, &attemptStatus); err != nil {
			t.Fatal(err)
		}
		if invocationStatus != "failed" || attemptStatus != "failed_before_send" {
			t.Fatalf("states invocation=%s attempt=%s", invocationStatus, attemptStatus)
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM outbox_events WHERE aggregate_type='model_invocation' AND aggregate_id=$1 AND event_type='model.invocation_failed'`, fmt.Sprint(fixture.invocationID), 1)
	})

	t.Run("audit failure rolls back evaluation and state transition", func(t *testing.T) {
		fixture := insertPreSendFixture(t, ctx, platform, revision.ID, resolved.Version.ID, resolved.CanonicalHash, "rollback")
		_, err := platform.Pool().Exec(ctx, `
CREATE FUNCTION fail_model_egress_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.event_type='model.egress_allowed' THEN RAISE EXCEPTION 'forced egress audit failure'; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER fail_model_egress_audit_trigger BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_model_egress_audit();`)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = platform.Pool().Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_model_egress_audit_trigger ON audit_events; DROP FUNCTION IF EXISTS fail_model_egress_audit()`)
		})
		evaluation := fixture.evaluation(modelegress.AuthorizationAllow, modelegress.EffectAllow, []string{"fixture_allow"})
		err = store.PersistPreSendAllowAndMarkSendStarted(ctx, modelegress.PersistAllowCommand{Evaluation: evaluation, ClaimToken: fixture.claimToken, ProviderIdempotencyKeyHash: modelegress.SHA256Bytes([]byte("rollback-provider-key")), Deadline: time.Now().Add(time.Hour)})
		if err == nil {
			t.Fatal("forced audit failure was ignored")
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE dispatch_attempt_id=$1`, fixture.attemptID, 0)
		var invocationStatus, attemptStatus string
		if err := platform.Pool().QueryRow(ctx, `SELECT i.status,a.status FROM model_invocations i JOIN model_dispatch_attempts a ON a.invocation_id=i.id WHERE i.id=$1`, fixture.invocationID).Scan(&invocationStatus, &attemptStatus); err != nil {
			t.Fatal(err)
		}
		if invocationStatus != "claimed" || attemptStatus != "claimed" {
			t.Fatalf("rollback states invocation=%s attempt=%s", invocationStatus, attemptStatus)
		}
	})

	t.Run("claim and metadata mismatches reject without partial writes", func(t *testing.T) {
		fixture := insertPreSendFixture(t, ctx, platform, revision.ID, resolved.Version.ID, resolved.CanonicalHash, "claim-mismatch")
		evaluation := fixture.evaluation(modelegress.AuthorizationAllow, modelegress.EffectAllow, []string{"fixture_allow"})
		command := modelegress.PersistAllowCommand{Evaluation: evaluation, ClaimToken: "wrong-claim", ProviderIdempotencyKeyHash: modelegress.SHA256Bytes([]byte("claim-mismatch-provider")), Deadline: time.Now().Add(time.Hour)}
		if persistErr := store.PersistPreSendAllowAndMarkSendStarted(ctx, command); !errors.Is(persistErr, modelegress.ErrClaimMismatch) {
			t.Fatalf("claim mismatch error=%v", persistErr)
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE dispatch_attempt_id=$1`, fixture.attemptID, 0)

		command.ClaimToken = fixture.claimToken
		command.Evaluation.ActionDigest = modelegress.SHA256Bytes([]byte("wrong-action-digest"))
		command.Evaluation.DecisionHash = modelegress.DecisionHash(command.Evaluation)
		if persistErr := store.PersistPreSendAllowAndMarkSendStarted(ctx, command); !errors.Is(persistErr, modelegress.ErrEvaluationConflict) {
			t.Fatalf("action digest mismatch error=%v", persistErr)
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE dispatch_attempt_id=$1`, fixture.attemptID, 0)

		command.Evaluation = fixture.evaluation(modelegress.AuthorizationAllow, modelegress.EffectAllow, []string{"fixture_allow"})
		command.Evaluation.ProviderID = "other-provider"
		command.Evaluation.DecisionHash = modelegress.DecisionHash(command.Evaluation)
		if persistErr := store.PersistPreSendAllowAndMarkSendStarted(ctx, command); !errors.Is(persistErr, modelegress.ErrEvaluationConflict) {
			t.Fatalf("metadata mismatch error=%v", persistErr)
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE dispatch_attempt_id=$1`, fixture.attemptID, 0)
	})

	t.Run("evaluation is unique and immutable", func(t *testing.T) {
		fixture := insertPreSendFixture(t, ctx, platform, revision.ID, resolved.Version.ID, resolved.CanonicalHash, "immutable")
		evaluation := fixture.evaluation(modelegress.AuthorizationDeny, modelegress.EffectNotEvaluated, nil)
		evaluation.AuthorizationReasonCode = "grant_missing"
		evaluation.DecisionHash = modelegress.DecisionHash(evaluation)
		command := modelegress.PersistDenyCommand{Evaluation: evaluation, ClaimToken: fixture.claimToken, ErrorCode: "authorization_denied", OutboxMaxAttempts: 10}
		if persistErr := store.PersistPreSendDenyAndFail(ctx, command); persistErr != nil {
			t.Fatal(persistErr)
		}
		if _, updateErr := platform.Pool().Exec(ctx, `UPDATE model_egress_evaluations SET authorization_reason_code='mutated' WHERE dispatch_attempt_id=$1`, fixture.attemptID); updateErr == nil {
			t.Fatal("historical evaluation was mutable")
		}
		if persistErr := store.PersistPreSendDenyAndFail(ctx, command); !errors.Is(persistErr, modelegress.ErrEvaluationConflict) {
			t.Fatalf("duplicate evaluation error=%v", persistErr)
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE dispatch_attempt_id=$1`, fixture.attemptID, 1)
	})

	t.Run("deny audit failure rolls back evaluation and terminal state", func(t *testing.T) {
		fixture := insertPreSendFixture(t, ctx, platform, revision.ID, resolved.Version.ID, resolved.CanonicalHash, "deny-rollback")
		_, triggerErr := platform.Pool().Exec(ctx, `
CREATE FUNCTION fail_model_egress_deny_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.event_type='model.egress_denied' THEN RAISE EXCEPTION 'forced egress deny audit failure'; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER fail_model_egress_deny_audit_trigger BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_model_egress_deny_audit();`)
		if triggerErr != nil {
			t.Fatal(triggerErr)
		}
		t.Cleanup(func() {
			_, _ = platform.Pool().Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_model_egress_deny_audit_trigger ON audit_events; DROP FUNCTION IF EXISTS fail_model_egress_deny_audit()`)
		})
		evaluation := fixture.evaluation(modelegress.AuthorizationAllow, modelegress.EffectDeny, []string{"fixture_denied"})
		if persistErr := store.PersistPreSendDenyAndFail(ctx, modelegress.PersistDenyCommand{Evaluation: evaluation, ClaimToken: fixture.claimToken, ErrorCode: "egress_denied", OutboxMaxAttempts: 10}); persistErr == nil {
			t.Fatal("forced deny audit failure was ignored")
		}
		assertEgressCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE dispatch_attempt_id=$1`, fixture.attemptID, 0)
		var invocationStatus, attemptStatus string
		if queryErr := platform.Pool().QueryRow(ctx, `SELECT i.status,a.status FROM model_invocations i JOIN model_dispatch_attempts a ON a.invocation_id=i.id WHERE i.id=$1`, fixture.invocationID).Scan(&invocationStatus, &attemptStatus); queryErr != nil {
			t.Fatal(queryErr)
		}
		if invocationStatus != "claimed" || attemptStatus != "claimed" {
			t.Fatalf("rollback states invocation=%s attempt=%s", invocationStatus, attemptStatus)
		}
	})

	t.Run("migration preserves legacy unpinned invocations", func(t *testing.T) {
		fixture := insertPreSendFixture(t, ctx, platform, revision.ID, resolved.Version.ID, resolved.CanonicalHash, "legacy")
		if _, err := platform.Pool().Exec(ctx, `UPDATE model_invocations SET model_egress_policy_version_id=NULL,model_egress_policy_hash=NULL WHERE id=$1`, fixture.invocationID); err != nil {
			t.Fatal(err)
		}
		var versionID *int64
		var policyHash *string
		if err := platform.Pool().QueryRow(ctx, `SELECT model_egress_policy_version_id,model_egress_policy_hash FROM model_invocations WHERE id=$1`, fixture.invocationID).Scan(&versionID, &policyHash); err != nil {
			t.Fatal(err)
		}
		if versionID != nil || policyHash != nil {
			t.Fatalf("legacy pair=%v/%v", versionID, policyHash)
		}
	})

	t.Run("migration 000008 down and reapply are ordered", func(t *testing.T) {
		down, readErr := rootmigrations.Files.ReadFile("000008_create_model_egress_authorization.down.sql")
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := platform.Pool().Exec(ctx, string(down)); execErr != nil {
			t.Fatalf("down migration 000008: %v", execErr)
		}
		if _, execErr := platform.Pool().Exec(ctx, `DELETE FROM schema_migrations WHERE version=8`); execErr != nil {
			t.Fatal(execErr)
		}
		var tableExists bool
		var policyColumns int
		if queryErr := platform.Pool().QueryRow(ctx, `SELECT to_regclass('public.model_egress_policy_versions') IS NOT NULL`).Scan(&tableExists); queryErr != nil {
			t.Fatal(queryErr)
		}
		if queryErr := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='model_invocations' AND column_name IN ('model_egress_policy_version_id','model_egress_policy_hash')`).Scan(&policyColumns); queryErr != nil {
			t.Fatal(queryErr)
		}
		if tableExists || policyColumns != 0 {
			t.Fatalf("down left table=%v policy_columns=%d", tableExists, policyColumns)
		}
		reapplied, upErr := runner.Up(ctx)
		if upErr != nil || len(reapplied.Applied) != 1 || reapplied.Current != 8 {
			t.Fatalf("reapply=%+v err=%v", reapplied, upErr)
		}
	})
}

type preSendFixture struct {
	invocationID   int64
	attemptID      int64
	profileID      int64
	claimToken     string
	policyID       int64
	policyHash     string
	revisionID     int64
	capabilityHash string
	requestHash    string
}

func (f preSendFixture) evaluation(auth modelegress.AuthorizationEffect, egress modelegress.Effect, reasons []string) modelegress.PreSendEvaluation {
	classes, classHash := modelegress.NormalizeClassifications([]string{"organizational"})
	actionDigest, err := modelegress.InvocationActionDigest(f.invocationID, f.requestHash, f.policyID, f.policyHash)
	if err != nil {
		panic(err)
	}
	value := modelegress.PreSendEvaluation{
		InvocationID: f.invocationID, DispatchAttemptID: f.attemptID, PolicyVersionID: f.policyID, PolicyHash: f.policyHash,
		OrganizationID: egressIntegrationOrganization, OrganizationRevisionID: f.revisionID,
		DispatchActorRoleID: "ingenieria_ia/code-runner", SubjectRoleID: "ingenieria_ia/code-runner",
		ModelProfileVersionID: f.profileID, ProviderID: "test.fake", ProviderTransport: "fake_adapter",
		ActionDigest: actionDigest, CapabilityMatrixHash: f.capabilityHash,
		ContextClassifications: classes, ContextClassificationsHash: classHash,
		AuthorizationEffect: auth, AuthorizationReasonCode: "allowed_by_grant", EgressEffect: egress, EgressReasonCodes: reasons,
		CorrelationID: "egress-integration", CausationID: "branch-09",
	}
	value.DecisionHash = modelegress.DecisionHash(value)
	return value
}

func insertPreSendFixture(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID, policyVersionID int64, policyHash, suffix string) preSendFixture {
	t.Helper()
	now := time.Now().UTC()
	providerHash := modelegress.SHA256Bytes([]byte("provider-" + suffix))
	if _, err := store.Pool().Exec(ctx, `INSERT INTO model_providers(organization_id,id,transport,adapter_status,dispatch_enabled,direct_http_forbidden,canonical_hash,organization_revision_id) VALUES($1,'test.fake','fake_adapter','available',TRUE,FALSE,$2,$3) ON CONFLICT DO NOTHING`, egressIntegrationOrganization, providerHash, revisionID); err != nil {
		t.Fatal(err)
	}
	profileName := "egress-fixture-" + suffix
	policyName := "egress.fixture." + suffix
	if _, err := store.Pool().Exec(ctx, `INSERT INTO model_profiles(organization_id,id,policy_id) VALUES($1,$2,$3)`, egressIntegrationOrganization, profileName, policyName); err != nil {
		t.Fatal(err)
	}
	var profileVersionID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO model_profile_versions(organization_id,profile_id,version_number,organization_revision_id,canonical_document_hash,version_hash,provider_id,provider_model_id,transport,adapter_status,dispatch_enabled) VALUES($1,$2,1,$3,$4,$5,'test.fake','deterministic-v1','fake_adapter','available',TRUE) RETURNING id`, egressIntegrationOrganization, profileName, revisionID, modelegress.SHA256Bytes([]byte("routing")), modelegress.SHA256Bytes([]byte("version-"+suffix))).Scan(&profileVersionID); err != nil {
		t.Fatal(err)
	}
	var taskID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO tasks(organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version) VALUES($1,$2,'ingenieria_ia/code-runner','ingenieria_ia',$3,$4,'Egress fixture','Evaluate pre-send transaction.','[]','running',0,$5,3,1,1) RETURNING id`, egressIntegrationOrganization, revisionID, "egress-task-"+suffix, modelegress.SHA256Bytes([]byte("task-"+suffix)), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	var taskAttemptID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at) VALUES($1,1,'running','egress-integration',$2,$2,$2,$2) RETURNING id`, taskID, now).Scan(&taskAttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `INSERT INTO task_leases(task_id,attempt_id,token_hash,holder_id,status,issued_at,heartbeat_at,expires_at) VALUES($1,$2,$3,'egress-integration','active',$4,$4,$5)`, taskID, taskAttemptID, modelegress.SHA256Bytes([]byte("lease-"+suffix)), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var snapshotID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO context_snapshots(organization_id,organization_revision_id,actor_role_id,purpose,task_ref,idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,created_at) VALUES($1,$2,'ingenieria_ia/code-runner','egress fixture',$3,$4,$5,$6,$7,$8,'ready',1,0,0,0,0,$9) RETURNING id`, egressIntegrationOrganization, revisionID, fmt.Sprint(taskID), "egress-context-"+suffix, modelegress.SHA256Bytes([]byte("context-"+suffix)), modelegress.SHA256Bytes([]byte("precedence")), modelegress.SHA256Bytes([]byte("bundle")), modelegress.SHA256Bytes([]byte("rendered")), now).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	requestHash := modelegress.SHA256Bytes([]byte("request-" + suffix))
	var invocationID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO model_invocations(organization_id,organization_revision_id,task_id,attempt_id,dispatch_actor_role_id,subject_role_id,context_snapshot_id,purpose,model_profile_id,model_profile_version_id,provider_id,provider_model_id,required_capabilities,output_mode,max_output_tokens,thinking_mode,idempotency_key,request_hash,status,deadline,correlation_id,causation_id,model_egress_policy_version_id,model_egress_policy_hash) VALUES($1,$2,$3,$4,'ingenieria_ia/code-runner','ingenieria_ia/code-runner',$5,'egress fixture',$6,$7,'test.fake','deterministic-v1','[]','text',64,'disabled',$8,$9,'claimed',$10,'egress-integration','branch-09',$11,$12) RETURNING id`, egressIntegrationOrganization, revisionID, taskID, taskAttemptID, snapshotID, profileName, profileVersionID, "egress-invocation-"+suffix, requestHash, now.Add(time.Hour), policyVersionID, policyHash).Scan(&invocationID); err != nil {
		t.Fatal(err)
	}
	var capabilityHash string
	if err := store.Pool().QueryRow(ctx, `SELECT document_hashes->>'capability-matrix.yaml' FROM organization_registry_revisions WHERE id=$1`, revisionID).Scan(&capabilityHash); err != nil {
		t.Fatal(err)
	}
	claimToken := "claim-token-" + suffix
	claimDigest := sha256.Sum256([]byte(claimToken))
	var dispatchAttemptID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO model_dispatch_attempts(invocation_id,attempt_number,status,claim_token_hash,claimed_by,claimed_at,claim_expires_at,retry_safety) VALUES($1,1,'claimed',$2,'egress-integration',$3,$4,'safe_before_send') RETURNING id`, invocationID, hex.EncodeToString(claimDigest[:]), now, now.Add(time.Hour)).Scan(&dispatchAttemptID); err != nil {
		t.Fatal(err)
	}
	return preSendFixture{invocationID: invocationID, attemptID: dispatchAttemptID, profileID: profileVersionID, claimToken: claimToken, policyID: policyVersionID, policyHash: policyHash, revisionID: revisionID, capabilityHash: capabilityHash, requestHash: requestHash}
}

func semanticFixtureHash(policy modelegress.CanonicalPolicy) string {
	copyPolicy := policy
	copyPolicy.CanonicalHash = ""
	copyPolicy.Path = ""
	body, _ := json.Marshal(copyPolicy)
	return modelegress.SHA256Bytes(body)
}

func openEgressStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "model-egress-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetEgressSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `TRUNCATE model_egress_evaluations,model_invocation_usage,model_invocation_results,model_dispatch_attempts,model_invocations,model_egress_revision_bindings,model_egress_rules,model_egress_policy_versions,role_model_bindings,model_capability_snapshots,model_profile_versions,model_profiles,model_providers,context_segments,context_snapshots,authorization_uses,authorization_decisions,authorization_requests,staging_events,staging_reviews,staging_promotions,staging_checks,staging_workspace_artifacts,staging_artifacts,staging_workspaces,outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,task_requirements,task_dependencies,tasks,organization_reporting_lines,organization_registry_revision_documents,organization_roles,organizational_units,organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func syncEgressCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, egressIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, egressIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func insertEgressRevision(t *testing.T, ctx context.Context, store *platformpostgres.Store, egressHash, label string) int64 {
	t.Helper()
	var currentHashes []byte
	if err := store.Pool().QueryRow(ctx, `SELECT r.document_hashes FROM organizations o JOIN organization_registry_revisions r ON r.id=o.current_revision_id WHERE o.id=$1`, egressIntegrationOrganization).Scan(&currentHashes); err != nil {
		t.Fatal(err)
	}
	var hashes map[string]string
	if err := json.Unmarshal(currentHashes, &hashes); err != nil {
		t.Fatal(err)
	}
	hashes[modelegress.PolicyFileName] = egressHash
	body, _ := json.Marshal(hashes)
	var id int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO organization_registry_revisions(canonical_hash,status,schema_versions,document_hashes,counts,diff,applied_at) VALUES($1,'applied','{}',$2::jsonb,'{}','{}',clock_timestamp()) RETURNING id`, modelegress.SHA256Bytes([]byte(label)), body).Scan(&id); err != nil {
		t.Fatal(err)
	}
	setCurrentEgressRevision(t, ctx, store, id)
	return id
}

func setCurrentEgressRevision(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64) {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$1,updated_at=clock_timestamp() WHERE id=$2`, revisionID, egressIntegrationOrganization); err != nil {
		t.Fatal(err)
	}
}

func assertEgressCount(t *testing.T, ctx context.Context, store *platformpostgres.Store, query string, arg any, want int) {
	t.Helper()
	var count int
	if err := store.Pool().QueryRow(ctx, query, arg).Scan(&count); err != nil || count != want {
		t.Fatalf("count=%d want=%d err=%v", count, want, err)
	}
}
