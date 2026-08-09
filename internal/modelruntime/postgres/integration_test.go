//go:build integration

package postgres_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	egresspostgres "github.com/Mireuz13/explorarte-organization/internal/modelegress/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitypostgres "github.com/Mireuz13/explorarte-organization/internal/modelidentity/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	modelpricingpostgres "github.com/Mireuz13/explorarte-organization/internal/modelpricing/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/costgate"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const modelIntegrationOrganization = "explorarte"

func TestModelRuntimeGatewayPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openModelStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrations through 000018: %v", err)
	}
	resetModelSchema(t, ctx, platform)
	syncModelCanonical(t, ctx, platform)
	store, err := modelpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	dispatchStore, err := dispatchpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	identityStore, err := identitypostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}

	repo, err := registry.NewPostgresRepository(platform)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := repo.GetCurrentRevision(ctx, modelIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	roles, err := repo.ListRoles(ctx, modelIntegrationOrganization, registry.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	baseCatalog := catalogFixture{organization: modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: revision.ID, ModelRoutingHash: revision.DocumentHashes["model-routing.yaml"], ModelEgressPolicyHash: revision.DocumentHashes["model-egress-policy.yaml"], CapabilityMatrixHash: revision.DocumentHashes["capability-matrix.yaml"]}, roles: convertRoles(roles)}

	t.Run("canonical compiled provider availability and sync are durable", func(t *testing.T) {
		service, newErr := modelruntime.NewRegistryService(filepath.Join("..", "..", "..", "docs", "canonical"), modelIntegrationOrganization, baseCatalog, store)
		if newErr != nil {
			t.Fatal(newErr)
		}
		first, syncErr := service.Sync(ctx, true, 10)
		if syncErr != nil || !first.Applied {
			t.Fatalf("first=%+v err=%v", first, syncErr)
		}
		second, syncErr := service.Sync(ctx, true, 10)
		if syncErr != nil || !second.NoOp {
			t.Fatalf("second=%+v err=%v", second, syncErr)
		}
		var enabled, available int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FILTER (WHERE dispatch_enabled), count(*) FILTER (WHERE adapter_status='available') FROM model_profile_versions WHERE organization_revision_id=$1`, revision.ID).Scan(&enabled, &available); err != nil {
			t.Fatal(err)
		}
		// executive.ceo moved from alibaba_token_plan_via_claude_code/cli_adapter
		// to openai_compatible/http_adapter (gpt-5.6-luna), and research.worker
		// moved from deepseek to gemini/gemini-2.5-flash — see the routing
		// revision that changed docs/canonical/model-routing.yaml. Both
		// openai_compatible and gemini have real compiled adapters (see
		// compiledAdapterAvailability); ceo-primary no longer matches R21's
		// alibaba-specific carve-out (internal/modelruntime/compiled_availability_r21.go)
		// since it no longer has any version with provider=alibaba.
		if enabled != 4 || available != 4 {
			t.Fatalf("compiled provider versions enabled=%d available=%d, want 4/4", enabled, available)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND provider_id='openai_compatible' AND transport='http_adapter' AND dispatch_enabled AND adapter_status='available'`, revision.ID, 2)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND profile_id='ceo-primary' AND provider_id='openai_compatible' AND transport='http_adapter' AND dispatch_enabled AND adapter_status='available'`, revision.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND provider_id='alibaba_token_plan_via_claude_code' AND (dispatch_enabled OR adapter_status<>'unavailable')`, revision.ID, 0)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND provider_id='deepseek' AND transport='http_adapter' AND dispatch_enabled AND adapter_status='available'`, revision.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND provider_id='gemini' AND transport='http_adapter' AND dispatch_enabled AND adapter_status='available'`, revision.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND provider_id NOT IN ('openai_compatible','alibaba_token_plan_via_claude_code','deepseek','gemini') AND (dispatch_enabled OR adapter_status<>'unavailable')`, revision.ID, 0)
	})

	fakeRoutingHash := modelruntime.SHA256Bytes([]byte("test.fake canonical routing fixture v1"))
	fakeEgressHash := modelruntime.SHA256Bytes([]byte("test.fake egress fixture v1"))
	fakeCapabilityHash := revision.DocumentHashes["capability-matrix.yaml"]
	fakeRevisionID := insertFakeRoutingRevision(t, ctx, platform, fakeRoutingHash, fakeEgressHash, fakeCapabilityHash)
	fakeCatalog := catalogFixture{
		organization: modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: fakeRevisionID, ModelRoutingHash: fakeRoutingHash, ModelEgressPolicyHash: fakeEgressHash, CapabilityMatrixHash: fakeCapabilityHash},
		roles: map[string]modelruntime.RoleRef{
			"ingenieria_ia/code-runner": {ID: "ingenieria_ia/code-runner", ModelPolicy: "department.worker", Enabled: true, Executable: true, AuthorityClass: "execution_service", UnitID: "ingenieria_ia"},
			"ingenieria_ia/frontend":    {ID: "ingenieria_ia/frontend", ModelPolicy: "department.worker", Enabled: true, Executable: true, AuthorityClass: "specialist", UnitID: "ingenieria_ia"},
		},
	}
	egressStore, err := egresspostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	taskRef, snapshotRef := insertModelExecutionFixture(t, ctx, platform, fakeRevisionID, "ingenieria_ia/code-runner", "code-runner")
	contexts := &staticContextReader{ref: snapshotRef, rendered: []byte("safe integration context")}
	tasks := staticTaskReader{ref: taskRef}
	principal, assignment := fixturePrincipalAndAssignment(t, ctx, dispatchStore, taskRef, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", "code-runner-fixture")
	principals, assignments := dispatchStore, dispatchStore
	identityCanonical, err := modelidentity.LoadCanonicalPolicy(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	identitySync, err := identityStore.Apply(ctx, modelIntegrationOrganization, identityCanonical)
	if err != nil || (!identitySync.Applied && !identitySync.NoOp) {
		t.Fatalf("identity policy sync=%+v err=%v", identitySync, err)
	}
	identityPrivateKey, identityKeyFile := writeExecutionIdentityKeyFile(t)
	identityPublicKey := identityPrivateKey.Public().(ed25519.PublicKey)
	preparedIdentityKey := modelidentity.PreparedKey{OrganizationID: modelIntegrationOrganization, ExecutionPrincipalID: principal.ID, PublicKey: identityPublicKey, PublicKeyFingerprint: modelidentity.PublicKeyFingerprint(identityPublicKey), SecretRef: "file://model-execution/integration/key-1", IdempotencyKey: "runtime-integration-identity-key", CreatedByRoleID: "empresa/human"}
	preparedIdentityKey.RequestHash, err = modelidentity.KeyRequestHash(preparedIdentityKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = identityStore.RegisterKey(ctx, preparedIdentityKey); err != nil {
		t.Fatal(err)
	}
	identityService, err := modelidentity.NewChallengeService(identityStore, modelidentity.ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	invocations, err := modelruntime.NewInvocationService(modelIntegrationOrganization, fakeCatalog, tasks, contexts, store, egressStore, identityStore, assignments, modelruntime.ClockFunc(time.Now), 10, false)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("organization, model and egress registries must synchronize in order", func(t *testing.T) {
		command := validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "registry-sync-order-before-model")
		if _, createErr := invocations.Create(ctx, command); !errors.Is(createErr, modelruntime.ErrBindingNotFound) {
			t.Fatalf("model registry stale error=%v", createErr)
		}

		fakeSync, syncErr := store.ApplyRegistry(ctx, fakeRegistryPlan(fakeRevisionID, fakeRoutingHash), 10)
		if syncErr != nil || !fakeSync.Applied {
			t.Fatalf("fake model sync=%+v err=%v", fakeSync, syncErr)
		}
		command.IdempotencyKey = "registry-sync-order-before-egress"
		if _, createErr := invocations.Create(ctx, command); !errors.Is(createErr, modelegress.ErrPolicyNotFound) {
			t.Fatalf("egress registry stale error=%v", createErr)
		}

		egressSync, syncErr := egressStore.Apply(ctx, fakeEgressPlan(fakeRevisionID, fakeEgressHash))
		if syncErr != nil || !egressSync.Applied {
			t.Fatalf("fake egress sync=%+v err=%v", egressSync, syncErr)
		}
		command.IdempotencyKey = "registry-sync-order-ready"
		created, createErr := invocations.Create(ctx, command)
		if createErr != nil || created.Invocation.ModelEgressPolicyVersionID == nil || created.Invocation.ModelEgressPolicyHash != fakeEgressHash {
			t.Fatalf("fully synchronized creation=%+v err=%v", created, createErr)
		}
	})
	cfg := modelruntime.RuntimeConfig{Enabled: true, CommandTimeout: 30 * time.Second, GlobalConcurrency: 4, MaxResponseBytes: 1 << 20, MaxToolIntents: 8, ClaimTTL: time.Minute, ReconcileBatchSize: 100, OutboxMaxAttempts: 10, ExecutionPrincipalKey: principal.PrincipalKey, ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: identityKeyFile}
	authorizer, err := authorization.New(repo, modelIntegrationOrganization, filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	capabilityEvaluator := authorizationDispatchAdapter{evaluator: authorizer}
	dispatch, err := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, capabilityEvaluator, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(adapter.NewFake()), modelruntime.ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("create reuse conflict and fake one-shot dispatch are durable", func(t *testing.T) {
		command := validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "pg-fake-dispatch")
		created, createErr := invocations.Create(ctx, command)
		if createErr != nil || created.Reused {
			t.Fatalf("created=%+v err=%v", created, createErr)
		}
		reused, createErr := invocations.Create(ctx, command)
		if createErr != nil || !reused.Reused || reused.Invocation.ID != created.Invocation.ID {
			t.Fatalf("reused=%+v err=%v", reused, createErr)
		}
		conflict := command
		conflict.Purpose = "different immutable request"
		if _, createErr = invocations.Create(ctx, conflict); !errors.Is(createErr, modelruntime.ErrConflict) {
			t.Fatalf("expected idempotency conflict, got %v", createErr)
		}

		result, dispatchErr := dispatch.Dispatch(ctx, created.Invocation.ID)
		if dispatchErr != nil || result.Invocation.Status != modelruntime.InvocationSucceeded || result.Result == nil || result.Usage == nil {
			t.Fatalf("result=%+v err=%v", result, dispatchErr)
		}
		if result.Result.OutputMode != modelruntime.OutputJSON || !strings.Contains(string(result.Result.JSONOutput), `"provider":"test.fake"`) {
			t.Fatalf("unexpected normalized result: %+v", result.Result)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_results WHERE invocation_id=$1`, created.Invocation.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_usage WHERE invocation_id=$1`, created.Invocation.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_provider_requests WHERE invocation_id=$1 AND provider_id='test.fake' AND adapter_id='fake' AND request_schema_version='test.fake.request.v1' AND request_hash ~ '^[0-9a-f]{64}$' AND endpoint_fingerprint ~ '^[0-9a-f]{64}$' AND credential_ref_hash ~ '^[0-9a-f]{64}$'`, created.Invocation.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_provider_outcomes WHERE invocation_id=$1 AND outcome_classification='response_received' AND transport='fake_adapter' AND http_status=200 AND response_hash ~ '^[0-9a-f]{64}$'`, created.Invocation.ID, 1)
		if _, mutationErr := platform.Pool().Exec(ctx, `UPDATE model_provider_requests SET adapter_version=2 WHERE invocation_id=$1`, created.Invocation.ID); mutationErr == nil {
			t.Fatal("provider request ledger accepted mutation")
		}
		if _, mutationErr := platform.Pool().Exec(ctx, `UPDATE model_provider_outcomes SET error_code='mutated' WHERE invocation_id=$1`, created.Invocation.ID); mutationErr == nil {
			t.Fatal("provider outcome ledger accepted mutation")
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE subject_type='model_invocation' AND subject_id=$1 AND event_type='model.invocation_succeeded'`, strconv.FormatInt(created.Invocation.ID, 10), 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM outbox_events WHERE aggregate_type='model_invocation' AND aggregate_id=$1 AND event_type='model.invocation_succeeded'`, strconv.FormatInt(created.Invocation.ID, 10), 1)

		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_execution_identity_assertions WHERE invocation_id=$1 AND verification_effect='allow'`, created.Invocation.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_execution_identity_challenges WHERE invocation_id=$1 AND consumed_at IS NOT NULL AND invalidated_at IS NULL`, created.Invocation.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_dispatch_attempts WHERE invocation_id=$1 AND execution_identity_key_id IS NOT NULL AND identity_assertion_id IS NOT NULL AND identity_verified_at IS NOT NULL`, created.Invocation.ID, 1)
		if _, mutationErr := platform.Pool().Exec(ctx, `UPDATE model_execution_identity_assertions SET verification_reason_code='mutated' WHERE invocation_id=$1`, created.Invocation.ID); mutationErr == nil {
			t.Fatal("identity assertion ledger accepted mutation")
		}
		if _, mutationErr := platform.Pool().Exec(ctx, `UPDATE model_execution_identity_challenges SET payload_hash=$2 WHERE invocation_id=$1`, created.Invocation.ID, modelidentity.SHA256Bytes([]byte("mutated-challenge"))); mutationErr == nil {
			t.Fatal("identity challenge immutable scope accepted mutation")
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_dispatcher_assignment_uses WHERE invocation_id=$1`, created.Invocation.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE subject_type='model_dispatcher_assignment' AND subject_id=$1 AND event_type='model.dispatch_assignment_consumed'`, strconv.FormatInt(assignment.ID, 10), 1)
		var usedInvocations int
		if err := platform.Pool().QueryRow(ctx, `SELECT used_invocations FROM model_dispatcher_assignments WHERE id=$1`, assignment.ID).Scan(&usedInvocations); err != nil {
			t.Fatal(err)
		}
		if usedInvocations != 1 {
			t.Fatalf("dispatcher assignment quota not consumed: used_invocations=%d", usedInvocations)
		}

		var leaked int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE subject_type='model_invocation' AND subject_id=$1 AND payload::text ~* '(safe integration context|hidden fake reasoning|rendered_context|claim_token|challenge_nonce|raw_nonce|raw_signature|private_key)'`, strconv.FormatInt(created.Invocation.ID, 10)).Scan(&leaked); err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatalf("audit leaked sensitive runtime payload in %d rows", leaked)
		}
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type='model_invocation' AND aggregate_id=$1 AND payload::text ~* '(safe integration context|hidden fake reasoning|rendered_context|claim_token|json_output|text_output)'`, strconv.FormatInt(created.Invocation.ID, 10)).Scan(&leaked); err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatalf("outbox leaked sensitive runtime payload in %d rows", leaked)
		}
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM model_provider_requests r JOIN model_provider_outcomes o ON o.provider_request_record_id=r.id WHERE r.invocation_id=$1 AND (r::text || o::text) ~* '(safe integration context|hidden fake reasoning|claim_token|challenge_nonce|raw_signature|private_key)'`, created.Invocation.ID).Scan(&leaked); err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatalf("provider evidence leaked sensitive payload in %d rows", leaked)
		}
	})

	t.Run("cost and budget reservation gates dispatch before the provider and reconciles after success", func(t *testing.T) {
		pricingStore, err := modelpricingpostgres.New(platform)
		if err != nil {
			t.Fatal(err)
		}
		pricingService, err := modelpricing.NewService(pricingStore)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pricingService.Upsert(ctx, modelpricing.PriceTier{
			ProviderID: "test.fake", ProviderModelID: "deterministic-v1", ContextTierName: "default",
			InputPriceNanosPerMillion: 1_000_000_000, OutputPriceNanosPerMillion: 2_000_000_000, EffectiveAt: time.Now().UTC().Add(-time.Minute),
		}); err != nil {
			t.Fatal(err)
		}

		walletStore, err := costledgerpostgres.New(platform)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := walletStore.SetBalance(ctx, "test.fake", modelpricing.USDFromDollars(10), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}

		budgetStore, err := agentbudgetpostgres.New(platform)
		if err != nil {
			t.Fatal(err)
		}
		limits := agentbudget.Limits{MaxUSD: modelpricing.USDFromDollars(10), MaxTokens: 100_000, MaxModelCalls: 10, MaxWallTimeMS: 3_600_000, MaxDepth: 3, MaxRetries: 3, MaxSubagents: 3}
		if _, err := budgetStore.CreateRootBudget(ctx, modelIntegrationOrganization, taskRef.TaskID, "ingenieria_ia/code-runner", limits, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}

		gate, err := costgate.New(pricingService, walletStore, budgetStore)
		if err != nil {
			t.Fatal(err)
		}
		gatedDispatch, err := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, capabilityEvaluator, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(adapter.NewFake()), modelruntime.ClockFunc(time.Now), modelruntime.WithCostBudgetGate(gate))
		if err != nil {
			t.Fatal(err)
		}

		command := validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "cost-gate-success")
		created, createErr := invocations.Create(ctx, command)
		if createErr != nil {
			t.Fatal(createErr)
		}
		walletBefore, err := walletStore.GetWallet(ctx, "test.fake")
		if err != nil {
			t.Fatal(err)
		}
		result, dispatchErr := gatedDispatch.Dispatch(ctx, created.Invocation.ID)
		if dispatchErr != nil || result.Invocation.Status != modelruntime.InvocationSucceeded {
			t.Fatalf("result=%+v err=%v", result, dispatchErr)
		}
		walletAfter, err := walletStore.GetWallet(ctx, "test.fake")
		if err != nil {
			t.Fatal(err)
		}
		if walletAfter.BalanceUSD >= walletBefore.BalanceUSD {
			t.Fatalf("wallet was not debited by a successful call: before=%d after=%d", walletBefore.BalanceUSD, walletAfter.BalanceUSD)
		}
		if walletAfter.ReservedUSD != 0 {
			t.Fatalf("reservation must be released once reconciled: reserved=%d", walletAfter.ReservedUSD)
		}
		budget, err := budgetStore.ResolveBudgetForTask(ctx, taskRef.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if budget.Usage.UsedModelCalls < 1 {
			t.Fatalf("budget model call count not consumed: %+v", budget.Usage)
		}

		// A near-empty wallet must reject the next call before the
		// provider is ever contacted — no request/outcome row at all.
		if _, err := walletStore.SetBalance(ctx, "test.fake", 1, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		starvedCommand := validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "cost-gate-starved")
		starvedCreated, createErr := invocations.Create(ctx, starvedCommand)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, dispatchErr := gatedDispatch.Dispatch(ctx, starvedCreated.Invocation.ID); dispatchErr == nil {
			t.Fatal("expected dispatch to be rejected by an exhausted wallet")
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_provider_requests WHERE invocation_id=$1`, starvedCreated.Invocation.ID, 0)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_provider_outcomes WHERE invocation_id=$1`, starvedCreated.Invocation.ID, 0)

		// A task never attached to any budget tree (no CreateRootBudget,
		// no InheritForChild) must still have its real cost tracked in the
		// provider wallet — an untracked budget is not a license to skip
		// real-money accounting. This is the CEO-plan/review/closure gap
		// found in review: those tasks are created without ever calling
		// attachChildCoordination, so ResolveBudgetForTask always fails
		// ErrBudgetNotFound for them.
		unbudgetedTaskRef, unbudgetedSnapshotRef := insertModelExecutionFixture(t, ctx, platform, fakeRevisionID, "ingenieria_ia/code-runner", "no-budget")
		fixtureAssignmentForExistingPrincipal(t, ctx, dispatchStore, unbudgetedTaskRef, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", principal, "no-budget-fixture")
		unbudgetedTasks := staticTaskReader{ref: unbudgetedTaskRef}
		unbudgetedContexts := &staticContextReader{ref: unbudgetedSnapshotRef, rendered: []byte("safe integration context")}
		unbudgetedInvocations, err := modelruntime.NewInvocationService(modelIntegrationOrganization, fakeCatalog, unbudgetedTasks, unbudgetedContexts, store, egressStore, identityStore, assignments, modelruntime.ClockFunc(time.Now), 10, false)
		if err != nil {
			t.Fatal(err)
		}
		unbudgetedDispatch, err := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, unbudgetedTasks, unbudgetedContexts, capabilityEvaluator, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(adapter.NewFake()), modelruntime.ClockFunc(time.Now), modelruntime.WithCostBudgetGate(gate))
		if err != nil {
			t.Fatal(err)
		}
		unbudgetedCommand := validInvocationCommand(unbudgetedTaskRef, unbudgetedSnapshotRef, "ingenieria_ia/code-runner", "cost-gate-no-budget")
		unbudgetedCreated, createErr := unbudgetedInvocations.Create(ctx, unbudgetedCommand)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, err := walletStore.SetBalance(ctx, "test.fake", modelpricing.USDFromDollars(10), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		walletBeforeUnbudgeted, err := walletStore.GetWallet(ctx, "test.fake")
		if err != nil {
			t.Fatal(err)
		}
		unbudgetedResult, dispatchErr := unbudgetedDispatch.Dispatch(ctx, unbudgetedCreated.Invocation.ID)
		if dispatchErr != nil || unbudgetedResult.Invocation.Status != modelruntime.InvocationSucceeded {
			t.Fatalf("unbudgeted dispatch: result=%+v err=%v", unbudgetedResult, dispatchErr)
		}
		walletAfterUnbudgeted, err := walletStore.GetWallet(ctx, "test.fake")
		if err != nil {
			t.Fatal(err)
		}
		if walletAfterUnbudgeted.BalanceUSD >= walletBeforeUnbudgeted.BalanceUSD {
			t.Fatalf("wallet must be debited even for a task with no budget attached: before=%d after=%d", walletBeforeUnbudgeted.BalanceUSD, walletAfterUnbudgeted.BalanceUSD)
		}
		if _, err := budgetStore.ResolveBudgetForTask(ctx, unbudgetedTaskRef.TaskID); !errors.Is(err, agentbudget.ErrBudgetNotFound) {
			t.Fatalf("this task must genuinely have no budget for the test to be meaningful: err=%v", err)
		}

		// A genuinely ambiguous provider outcome (timeout, transport
		// failure) must never release the wallet reservation: the
		// provider may have already processed and billed the call, so
		// releasing would hand back money that might not actually be
		// there. The reservation must stay parked until it is reconciled
		// by other means.
		ambiguousCommand := validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "cost-gate-ambiguous")
		ambiguousCreated, createErr := invocations.Create(ctx, ambiguousCommand)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, err := walletStore.SetBalance(ctx, "test.fake", modelpricing.USDFromDollars(10), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		walletBeforeAmbiguous, err := walletStore.GetWallet(ctx, "test.fake")
		if err != nil {
			t.Fatal(err)
		}
		ambiguousProvider := &classifiedAdapter{phase: modelruntime.AdapterFailureAmbiguous, outcome: modelruntime.ProviderOutcome{OutcomeClassification: modelruntime.ProviderOutcomeAmbiguous, ErrorClass: "transport", ErrorCode: "transport_timeout", Retryable: true, ResponseSchemaVersion: "test.fake.response.v1"}}
		ambiguousDispatch, err := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, capabilityEvaluator, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(ambiguousProvider), modelruntime.ClockFunc(time.Now), modelruntime.WithCostBudgetGate(gate))
		if err != nil {
			t.Fatal(err)
		}
		if _, dispatchErr := ambiguousDispatch.Dispatch(ctx, ambiguousCreated.Invocation.ID); !errors.Is(dispatchErr, modelruntime.ErrAmbiguousOutcome) {
			t.Fatalf("expected an ambiguous outcome error, got %v", dispatchErr)
		}
		walletAfterAmbiguous, err := walletStore.GetWallet(ctx, "test.fake")
		if err != nil {
			t.Fatal(err)
		}
		if walletAfterAmbiguous.BalanceUSD != walletBeforeAmbiguous.BalanceUSD {
			t.Fatalf("an ambiguous outcome must not debit the wallet: before=%d after=%d", walletBeforeAmbiguous.BalanceUSD, walletAfterAmbiguous.BalanceUSD)
		}
		if walletAfterAmbiguous.ReservedUSD <= walletBeforeAmbiguous.ReservedUSD {
			t.Fatalf("an ambiguous outcome must not release the reservation: before=%d after=%d", walletBeforeAmbiguous.ReservedUSD, walletAfterAmbiguous.ReservedUSD)
		}
	})

	t.Run("classified provider outcomes are immutable and terminal", func(t *testing.T) {
		cases := []struct {
			name       string
			phase      modelruntime.AdapterFailurePhase
			outcome    modelruntime.ProviderOutcome
			wantStatus modelruntime.InvocationStatus
			wantClass  string
		}{
			{name: "not sent after commit", phase: modelruntime.AdapterFailureBeforeRequest, outcome: modelruntime.ProviderOutcome{OutcomeClassification: modelruntime.ProviderOutcomeNotSent, ErrorClass: "credential", ErrorCode: "credential_unavailable", ResponseSchemaVersion: "test.fake.response.v1"}, wantStatus: modelruntime.InvocationFailed, wantClass: modelruntime.ProviderOutcomeNotSent},
			{name: "provider rejected", phase: modelruntime.AdapterFailureResponseReceived, outcome: modelruntime.ProviderOutcome{OutcomeClassification: modelruntime.ProviderOutcomeRejected, ProviderRequestID: "provider-rejected", HTTPStatus: 429, ErrorClass: "rate_limit", ErrorCode: "rate_limited", Retryable: true, ResponseHash: modelruntime.SHA256Bytes([]byte("provider rejection")), ResponseSchemaVersion: "test.fake.response.v1"}, wantStatus: modelruntime.InvocationFailed, wantClass: modelruntime.ProviderOutcomeRejected},
			{name: "transport ambiguous", phase: modelruntime.AdapterFailureAmbiguous, outcome: modelruntime.ProviderOutcome{OutcomeClassification: modelruntime.ProviderOutcomeAmbiguous, ErrorClass: "transport", ErrorCode: "transport_timeout", Retryable: true, ResponseSchemaVersion: "test.fake.response.v1"}, wantStatus: modelruntime.InvocationAmbiguous, wantClass: modelruntime.ProviderOutcomeAmbiguous},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "provider-outcome-"+strings.ReplaceAll(test.name, " ", "-")))
				provider := &classifiedAdapter{phase: test.phase, outcome: test.outcome}
				classifiedDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, allowEvaluator{matrixHash: fakeCapabilityHash}, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(provider), modelruntime.ClockFunc(time.Now))
				if newErr != nil {
					t.Fatal(newErr)
				}
				result, dispatchErr := classifiedDispatch.Dispatch(ctx, created.ID)
				if dispatchErr == nil || result.Invocation.Status != test.wantStatus || provider.calls != 1 {
					t.Fatalf("result=%+v err=%v calls=%d", result, dispatchErr, provider.calls)
				}
				assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_provider_requests WHERE invocation_id=$1`, created.ID, 1)
				assertModelCountTwo(t, ctx, platform, `SELECT count(*) FROM model_provider_outcomes WHERE invocation_id=$1 AND outcome_classification=$2`, created.ID, test.wantClass, 1)
			})
		}
	})

	t.Run("authorization deny is durable and never renders or calls adapter", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "authorization-deny"))
		provider := &countingAdapter{}
		deniedDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, denyEvaluator{matrixHash: fakeCapabilityHash}, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(provider), modelruntime.ClockFunc(time.Now))
		if newErr != nil {
			t.Fatal(newErr)
		}
		beforeRender := contexts.renderCalls
		result, dispatchErr := deniedDispatch.Dispatch(ctx, created.ID)
		if !errors.Is(dispatchErr, modelruntime.ErrAuthorizationDenied) || result.Invocation.Status != modelruntime.InvocationFailed || contexts.renderCalls != beforeRender || provider.calls != 0 {
			t.Fatalf("result=%+v err=%v render=%d/%d adapter_calls=%d", result, dispatchErr, beforeRender, contexts.renderCalls, provider.calls)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE invocation_id=$1 AND authorization_effect='deny' AND egress_effect='not_evaluated'`, created.ID, 1)
	})

	t.Run("task.execute without model.invoke cannot become a dispatcher", func(t *testing.T) {
		frontendTask, _ := insertModelExecutionFixture(t, ctx, platform, fakeRevisionID, "ingenieria_ia/frontend", "frontend-no-model-invoke")
		taskDecision, evalErr := authorizer.Evaluate(ctx, authorization.EvaluationRequest{OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: fakeRevisionID, ActorRoleID: "ingenieria_ia/frontend", CapabilityID: "task.execute", ResourceType: "task", ResourceID: strconv.FormatInt(frontendTask.TaskID, 10), ActionDigest: authorization.DigestAction([]byte("task-execute-fixture"))})
		if evalErr != nil || taskDecision.Effect != authorization.EffectAllow {
			t.Fatalf("fixture specialist must retain task.execute: decision=%+v err=%v", taskDecision, evalErr)
		}
		principalHash, hashErr := modeldispatch.PrincipalRequestHash(modelIntegrationOrganization, "integration/frontend-ineligible", "ingenieria_ia/frontend", modeldispatch.PrincipalLocalProcess, "empresa/human")
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		_, registerErr := dispatchStore.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{
			Command: modeldispatch.RegisterPrincipalCommand{
				OrganizationID: modelIntegrationOrganization, PrincipalKey: "integration/frontend-ineligible",
				DispatchActorRoleID: "ingenieria_ia/frontend", PrincipalKind: modeldispatch.PrincipalLocalProcess,
				IdempotencyKey: "principal-frontend-ineligible",
			},
			RequestHash: principalHash, RegisteredByRoleID: "empresa/human",
		})
		if registerErr != nil {
			t.Fatalf("store layer does not gate eligibility itself: %v", registerErr)
		}
		principals, err := modeldispatch.NewPrincipalService(modelIntegrationOrganization, authorizer, principalCatalogAdapter{repo: repo}, dispatchStore, modeldispatch.ClockFunc(time.Now))
		if err != nil {
			t.Fatal(err)
		}
		_, err = principals.Register(ctx, "empresa/human", modeldispatch.RegisterPrincipalCommand{
			OrganizationID: modelIntegrationOrganization, PrincipalKey: "integration/frontend-ineligible-via-service",
			DispatchActorRoleID: "ingenieria_ia/frontend", PrincipalKind: modeldispatch.PrincipalLocalProcess,
			IdempotencyKey: "principal-frontend-ineligible-via-service",
		})
		if !errors.Is(err, modeldispatch.ErrRoleNotEligible) {
			t.Fatalf("expected role eligibility rejection, got %v", err)
		}
	})

	t.Run("egress deny is durable and never renders or calls adapter", func(t *testing.T) {
		originalClasses := append([]string(nil), contexts.ref.DataClasses...)
		contexts.ref.DataClasses = []string{"public", "clinical"}
		defer func() { contexts.ref.DataClasses = originalClasses }()
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "egress-deny"))
		provider := &countingAdapter{}
		deniedDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, allowEvaluator{matrixHash: fakeCapabilityHash}, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(provider), modelruntime.ClockFunc(time.Now))
		if newErr != nil {
			t.Fatal(newErr)
		}
		beforeRender := contexts.renderCalls
		result, dispatchErr := deniedDispatch.Dispatch(ctx, created.ID)
		if !errors.Is(dispatchErr, modelruntime.ErrEgressDenied) || result.Invocation.Status != modelruntime.InvocationFailed || contexts.renderCalls != beforeRender || provider.calls != 0 {
			t.Fatalf("result=%+v err=%v render=%d/%d adapter_calls=%d", result, dispatchErr, beforeRender, contexts.renderCalls, provider.calls)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE invocation_id=$1 AND authorization_effect='allow' AND egress_effect='deny'`, created.ID, 1)
	})

	t.Run("adapter unavailable blocks before render", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "adapter-unavailable"))
		blockedDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, allowEvaluator{matrixHash: fakeCapabilityHash}, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(), modelruntime.ClockFunc(time.Now))
		if newErr != nil {
			t.Fatal(newErr)
		}
		beforeRender := contexts.renderCalls
		result, dispatchErr := blockedDispatch.Dispatch(ctx, created.ID)
		if !errors.Is(dispatchErr, modelruntime.ErrProviderUnavailable) || result.Invocation.Status != modelruntime.InvocationFailed || contexts.renderCalls != beforeRender {
			t.Fatalf("result=%+v err=%v render=%d/%d", result, dispatchErr, beforeRender, contexts.renderCalls)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE invocation_id=$1`, created.ID, 0)
	})

	t.Run("legacy unpinned invocation blocks before render", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "legacy-unpinned-dispatch"))
		if _, updateErr := platform.Pool().Exec(ctx, `UPDATE model_invocations SET model_egress_policy_version_id=NULL,model_egress_policy_hash=NULL,execution_identity_policy_version_id=NULL,execution_identity_policy_hash=NULL WHERE id=$1`, created.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
		provider := &countingAdapter{}
		legacyDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, allowEvaluator{matrixHash: fakeCapabilityHash}, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(provider), modelruntime.ClockFunc(time.Now))
		if newErr != nil {
			t.Fatal(newErr)
		}
		beforeRender := contexts.renderCalls
		result, dispatchErr := legacyDispatch.Dispatch(ctx, created.ID)
		if !errors.Is(dispatchErr, modelruntime.ErrEgressPolicyUnpinned) || result.Invocation.Status != modelruntime.InvocationFailed || contexts.renderCalls != beforeRender || provider.calls != 0 {
			t.Fatalf("result=%+v err=%v render=%d/%d adapter_calls=%d", result, dispatchErr, beforeRender, contexts.renderCalls, provider.calls)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE invocation_id=$1`, created.ID, 0)
	})

	t.Run("dispatcher-unpinned legacy invocation fails before claim mutation and never renders", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "dispatcher-unpinned-dispatch"))
		if _, updateErr := platform.Pool().Exec(ctx, `UPDATE model_invocations SET dispatcher_assignment_id=NULL,execution_principal_id=NULL,execution_identity_policy_version_id=NULL,execution_identity_policy_hash=NULL WHERE id=$1`, created.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
		provider := &countingAdapter{}
		legacyDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, allowEvaluator{matrixHash: fakeCapabilityHash}, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(provider), modelruntime.ClockFunc(time.Now))
		if newErr != nil {
			t.Fatal(newErr)
		}
		beforeRender := contexts.renderCalls
		result, dispatchErr := legacyDispatch.Dispatch(ctx, created.ID)
		if !errors.Is(dispatchErr, modelruntime.ErrDispatcherAssignmentUnpinned) || result.Invocation.Status != modelruntime.InvocationFailed || contexts.renderCalls != beforeRender || provider.calls != 0 {
			t.Fatalf("result=%+v err=%v render=%d/%d adapter_calls=%d", result, dispatchErr, beforeRender, contexts.renderCalls, provider.calls)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_egress_evaluations WHERE invocation_id=$1`, created.ID, 0)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_dispatcher_assignment_uses WHERE invocation_id=$1`, created.ID, 0)
	})

	t.Run("identity-unpinned legacy invocation fails before render and adapter", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "identity-unpinned-dispatch"))
		if _, updateErr := platform.Pool().Exec(ctx, `UPDATE model_invocations SET execution_identity_policy_version_id=NULL,execution_identity_policy_hash=NULL WHERE id=$1`, created.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
		provider := &countingAdapter{}
		legacyDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, tasks, contexts, allowEvaluator{matrixHash: fakeCapabilityHash}, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(provider), modelruntime.ClockFunc(time.Now))
		if newErr != nil {
			t.Fatal(newErr)
		}
		beforeRender := contexts.renderCalls
		result, dispatchErr := legacyDispatch.Dispatch(ctx, created.ID)
		if !errors.Is(dispatchErr, modelruntime.ErrExecutionIdentityUnpinned) || result.Invocation.Status != modelruntime.InvocationFailed || contexts.renderCalls != beforeRender || provider.calls != 0 {
			t.Fatalf("result=%+v err=%v render=%d/%d adapter_calls=%d", result, dispatchErr, beforeRender, contexts.renderCalls, provider.calls)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_execution_identity_challenges WHERE invocation_id=$1`, created.ID, 0)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_execution_identity_assertions WHERE invocation_id=$1`, created.ID, 0)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_dispatcher_assignment_uses WHERE invocation_id=$1`, created.ID, 0)
	})

	t.Run("tampered identity signature is denied without claim mutation", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "identity-tampered-signature"))
		command, commandErr := authenticatedClaimCommand(ctx, store, identityService, identityPrivateKey, created.ID, "tampered-signer", principal.ID)
		if commandErr != nil {
			t.Fatal(commandErr)
		}
		command.Signature = append([]byte(nil), command.Signature...)
		command.Signature[0] ^= 0xff
		if _, claimErr := store.ClaimInvocationAuthenticated(ctx, command, cfg); !errors.Is(claimErr, modelidentity.ErrAssertionInvalid) {
			t.Fatalf("tampered signature error=%v", claimErr)
		}
		loaded, loadErr := invocations.Get(ctx, created.ID)
		if loadErr != nil || loaded.Status != modelruntime.InvocationRequested {
			t.Fatalf("tampered assertion mutated invocation: %+v err=%v", loaded, loadErr)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_dispatch_attempts WHERE invocation_id=$1`, created.ID, 0)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_execution_identity_challenges WHERE id=$1 AND consumed_at IS NULL`, command.ChallengeID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE event_type='model.execution_identity_denied' AND subject_type='model_invocation' AND subject_id=$1`, strconv.FormatInt(created.ID, 10), 1)
	})

	t.Run("consumed identity challenge cannot be replayed", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "identity-replay"))
		command, commandErr := authenticatedClaimCommand(ctx, store, identityService, identityPrivateKey, created.ID, "replay-signer", principal.ID)
		if commandErr != nil {
			t.Fatal(commandErr)
		}
		first, claimErr := store.ClaimInvocationAuthenticated(ctx, command, cfg)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if _, updateErr := platform.Pool().Exec(ctx, `UPDATE model_invocations SET status='requested',updated_at=clock_timestamp() WHERE id=$1`, created.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
		if _, replayErr := store.ClaimInvocationAuthenticated(ctx, command, cfg); !errors.Is(replayErr, modelidentity.ErrReplayDenied) {
			t.Fatalf("replay error=%v", replayErr)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_dispatch_attempts WHERE invocation_id=$1`, created.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_execution_identity_assertions WHERE invocation_id=$1`, created.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE event_type='model.execution_identity_replay_denied' AND subject_type='model_invocation' AND subject_id=$1`, strconv.FormatInt(created.ID, 10), 1)
		if _, updateErr := platform.Pool().Exec(ctx, `UPDATE model_invocations SET status='claimed',updated_at=clock_timestamp() WHERE id=$1`, created.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
		if _, cleanupErr := invocations.Cancel(ctx, created.ID, "ingenieria_ia/code-runner", "identity replay integration cleanup"); cleanupErr != nil {
			t.Fatal(cleanupErr)
		}
		_ = first
	})

	t.Run("execution principal mismatch denies claim without mutating the invocation", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "principal-mismatch-dispatch"))
		otherKey := "integration/other-principal"
		otherHash, hashErr := modeldispatch.PrincipalRequestHash(modelIntegrationOrganization, otherKey, "ingenieria_ia/code-runner", modeldispatch.PrincipalLocalProcess, "empresa/human")
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		otherRegistered, registerErr := dispatchStore.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{
			Command: modeldispatch.RegisterPrincipalCommand{
				OrganizationID: modelIntegrationOrganization, PrincipalKey: otherKey,
				DispatchActorRoleID: "ingenieria_ia/code-runner", PrincipalKind: modeldispatch.PrincipalLocalProcess,
				IdempotencyKey: "principal-other-mismatch",
			},
			RequestHash: otherHash, RegisteredByRoleID: "empresa/human",
		})
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		mismatchedCfg := cfg
		mismatchedCfg.ExecutionPrincipalKey = otherRegistered.Principal.PrincipalKey
		provider := &countingAdapter{}
		mismatchedDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, mismatchedCfg, fakeCatalog, tasks, contexts, allowEvaluator{matrixHash: fakeCapabilityHash}, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(provider), modelruntime.ClockFunc(time.Now))
		if newErr != nil {
			t.Fatal(newErr)
		}
		beforeRender := contexts.renderCalls
		result, dispatchErr := mismatchedDispatch.Dispatch(ctx, created.ID)
		if !errors.Is(dispatchErr, modelruntime.ErrExecutionPrincipalMismatch) || result.Invocation.ID != 0 || contexts.renderCalls != beforeRender || provider.calls != 0 {
			t.Fatalf("mismatch was not cleanly denied: result=%+v err=%v render=%d/%d adapter_calls=%d", result, dispatchErr, beforeRender, contexts.renderCalls, provider.calls)
		}
		loaded, err := invocations.Get(ctx, created.ID)
		if err != nil || loaded.Status != modelruntime.InvocationRequested {
			t.Fatalf("principal mismatch mutated the invocation: %+v err=%v", loaded, err)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_dispatch_attempts WHERE invocation_id=$1`, created.ID, 0)
	})

	t.Run("claim token is hashed and concurrent claim has one winner", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "claim-race"))
		const contenders = 2
		results := make(chan modelruntime.ClaimedInvocation, contenders)
		errs := make(chan error, contenders)
		var wg sync.WaitGroup
		for i := 0; i < contenders; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				claimed, claimErr := claimInvocationWithIdentity(ctx, store, identityService, identityPrivateKey, created.ID, fmt.Sprintf("claimer-%d", index), principal.ID, cfg)
				if claimErr != nil {
					errs <- claimErr
					return
				}
				results <- claimed
			}(i)
		}
		wg.Wait()
		close(results)
		close(errs)
		var winner modelruntime.ClaimedInvocation
		for value := range results {
			if winner.Invocation.ID != 0 {
				t.Fatal("more than one claim winner")
			}
			winner = value
		}
		if winner.Invocation.ID == 0 || winner.ClaimToken == "" {
			t.Fatal("missing claim winner")
		}
		losers := 0
		for claimErr := range errs {
			if !errors.Is(claimErr, modelruntime.ErrClaimUnavailable) && !errors.Is(claimErr, modelruntime.ErrConflict) && !errors.Is(claimErr, modelidentity.ErrReplayDenied) && !errors.Is(claimErr, modelidentity.ErrConflict) {
				t.Fatalf("unexpected loser error: %v", claimErr)
			}
			losers++
		}
		if losers != contenders-1 {
			t.Fatalf("losers=%d", losers)
		}
		var storedHash string
		if err := platform.Pool().QueryRow(ctx, `SELECT claim_token_hash FROM model_dispatch_attempts WHERE id=$1`, winner.DispatchAttempt.ID).Scan(&storedHash); err != nil {
			t.Fatal(err)
		}
		if storedHash == winner.ClaimToken || len(storedHash) != 64 {
			t.Fatalf("raw claim token persisted: %q", storedHash)
		}
		var leaked int
		if err := platform.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE payload::text LIKE '%' || $1 || '%') + (SELECT count(*) FROM outbox_events WHERE payload::text LIKE '%' || $1 || '%')`, winner.ClaimToken).Scan(&leaked); err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatal("raw claim token leaked to event payload")
		}
		if _, err := invocations.Cancel(ctx, created.ID, "ingenieria_ia/code-runner", "integration cleanup"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("global concurrency and safe reconcile never redispatch", func(t *testing.T) {
		limited := cfg
		limited.GlobalConcurrency = 1
		first := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "concurrency-first"))
		second := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "concurrency-second"))
		claim, claimErr := claimInvocationWithIdentity(ctx, store, identityService, identityPrivateKey, first.ID, "one", principal.ID, limited)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if _, claimErr = claimInvocationWithIdentity(ctx, store, identityService, identityPrivateKey, second.ID, "two", principal.ID, limited); !errors.Is(claimErr, modelruntime.ErrConcurrencyLimit) {
			t.Fatalf("expected global concurrency limit, got %v", claimErr)
		}
		if _, err := platform.Pool().Exec(ctx, `UPDATE model_dispatch_attempts SET claimed_at=clock_timestamp()-interval '2 minutes',claim_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, claim.DispatchAttempt.ID); err != nil {
			t.Fatal(err)
		}
		reconciled, reconcileErr := invocations.Reconcile(ctx, 100)
		if reconcileErr != nil || reconciled.ReleasedBeforeSend != 1 {
			t.Fatalf("reconcile=%+v err=%v", reconciled, reconcileErr)
		}
		loaded, err := invocations.Get(ctx, first.ID)
		if err != nil || loaded.Status != modelruntime.InvocationRequested {
			t.Fatalf("released invocation=%+v err=%v", loaded, err)
		}
	})

	t.Run("down migration and reapply in disposable integration database", func(t *testing.T) {
		// R21 extends the R12 provider-outcome table, so 000018 must come down
		// before 000011. R14 also references model invocations/attempts, so
		// 000012 must come down before the earlier model migrations. 000021's
		// provider_wallet_events FKs to model_invocations, so it must come
		// down before 000007 too.
		versions := []struct {
			version int
			file    string
		}{
			{21, "000021_create_provider_wallets.down.sql"},
			{18, "000018_make_provider_outcomes_transport_aware.down.sql"},
			{12, "000012_create_durable_decision_graph.down.sql"},
			{11, "000011_create_model_provider_adapter.down.sql"},
			{10, "000010_create_model_execution_identity.down.sql"},
			{9, "000009_create_model_dispatcher_assignments.down.sql"},
			{8, "000008_create_model_egress_authorization.down.sql"},
			{7, "000007_create_model_runtime_gateway.down.sql"},
		}
		for _, item := range versions {
			down, readErr := rootmigrations.Files.ReadFile(item.file)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if _, execErr := platform.Pool().Exec(ctx, string(down)); execErr != nil {
				t.Fatalf("down migration %d: %v", item.version, execErr)
			}
			if _, execErr := platform.Pool().Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, item.version); execErr != nil {
				t.Fatal(execErr)
			}
		}
		// Current always reflects the tip of the full migration list (the
		// runner's Up loop walks every migration, setting Current even for
		// ones already applied) — migration 19 is untouched by this
		// rollback/reapply cycle (it depends on 17, not any version rolled
		// back here), so only the 7 explicitly rolled-back versions above
		// are expected in Applied, while Current still reports the real tip.
		loadedForTip, tipErr := platformmigrations.Load(rootmigrations.Files)
		if tipErr != nil {
			t.Fatal(tipErr)
		}
		tip := loadedForTip[len(loadedForTip)-1].Version
		reapplied, upErr := runner.Up(ctx)
		if upErr != nil || len(reapplied.Applied) != 8 || reapplied.Current != tip {
			t.Fatalf("reapply=%+v err=%v want current=%d", reapplied, upErr, tip)
		}
		var exists bool
		if err = platform.Pool().QueryRow(ctx, `SELECT to_regclass('public.model_invocations') IS NOT NULL AND to_regclass('public.model_dispatch_attempts') IS NOT NULL AND to_regclass('public.model_egress_policy_versions') IS NOT NULL AND to_regclass('public.model_dispatcher_assignments') IS NOT NULL AND to_regclass('public.model_execution_principals') IS NOT NULL AND to_regclass('public.model_execution_identity_policy_versions') IS NOT NULL AND to_regclass('public.model_provider_requests') IS NOT NULL AND to_regclass('public.model_provider_outcomes') IS NOT NULL`).Scan(&exists); err != nil || !exists {
			t.Fatalf("reapply exists=%v err=%v", exists, err)
		}
	})
}

func writeExecutionIdentityKeyFile(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "execution-identity.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return privateKey, path
}

func claimInvocationWithIdentity(ctx context.Context, store *modelpostgres.Store, identity *modelidentity.ChallengeService, privateKey ed25519.PrivateKey, invocationID int64, claimedBy string, principalID int64, cfg modelruntime.RuntimeConfig) (modelruntime.ClaimedInvocation, error) {
	command, err := authenticatedClaimCommand(ctx, store, identity, privateKey, invocationID, claimedBy, principalID)
	if err != nil {
		return modelruntime.ClaimedInvocation{}, err
	}
	return store.ClaimInvocationAuthenticated(ctx, command, cfg)
}

func authenticatedClaimCommand(ctx context.Context, store *modelpostgres.Store, identity *modelidentity.ChallengeService, privateKey ed25519.PrivateKey, invocationID int64, claimedBy string, principalID int64) (modelruntime.AuthenticatedClaimCommand, error) {
	invocation, err := store.GetInvocation(ctx, invocationID)
	if err != nil {
		return modelruntime.AuthenticatedClaimCommand{}, err
	}
	if invocation.ExecutionIdentityPolicyVersionID == nil || invocation.DispatcherAssignmentID == nil {
		return modelruntime.AuthenticatedClaimCommand{}, modelruntime.ErrExecutionIdentityUnpinned
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	key, err := identity.ResolveActiveKeyByFingerprint(ctx, invocation.OrganizationID, principalID, modelidentity.PublicKeyFingerprint(publicKey))
	if err != nil {
		return modelruntime.AuthenticatedClaimCommand{}, err
	}
	policy, err := identity.ResolvePolicyByID(ctx, invocation.OrganizationID, *invocation.ExecutionIdentityPolicyVersionID)
	if err != nil {
		return modelruntime.AuthenticatedClaimCommand{}, err
	}
	digest, err := modelruntime.ActionDigest(invocation)
	if err != nil {
		return modelruntime.AuthenticatedClaimCommand{}, err
	}
	issued, err := identity.Issue(ctx, modelidentity.ChallengeScope{OrganizationID: invocation.OrganizationID, OrganizationRevisionID: invocation.OrganizationRevisionID, InvocationID: invocation.ID, DispatcherAssignmentID: *invocation.DispatcherAssignmentID, ExecutionPrincipalID: principalID, ExecutionIdentityPolicyVersionID: *invocation.ExecutionIdentityPolicyVersionID, ExecutionIdentityPolicyHash: invocation.ExecutionIdentityPolicyHash, ExecutionIdentityKeyID: key.ID, ActionDigest: digest, RequestHash: invocation.RequestHash}, policy)
	if err != nil {
		return modelruntime.AuthenticatedClaimCommand{}, err
	}
	signature := ed25519.Sign(privateKey, issued.Payload)
	if _, err = identity.Verify(key, issued, signature, policy); err != nil {
		return modelruntime.AuthenticatedClaimCommand{}, err
	}
	return modelruntime.AuthenticatedClaimCommand{
		InvocationID: invocation.ID, ClaimedBy: claimedBy,
		ExecutionPrincipalID: principalID, IdentityKeyID: key.ID,
		ChallengeID: issued.Challenge.ID, ChallengeNonce: issued.Nonce,
		Signature: signature,
	}, nil
}

type catalogFixture struct {
	organization modelruntime.OrganizationRef
	roles        map[string]modelruntime.RoleRef
}

func (c catalogFixture) CurrentOrganization(context.Context, string) (modelruntime.OrganizationRef, error) {
	return c.organization, nil
}
func (c catalogFixture) GetRole(_ context.Context, _, id string) (modelruntime.RoleRef, error) {
	value, ok := c.roles[id]
	if !ok {
		return modelruntime.RoleRef{}, modelruntime.ErrNotFound
	}
	return value, nil
}
func (c catalogFixture) ListRoles(context.Context, string) ([]modelruntime.RoleRef, error) {
	result := make([]modelruntime.RoleRef, 0, len(c.roles))
	for _, role := range c.roles {
		result = append(result, role)
	}
	return result, nil
}

type principalCatalogAdapter struct{ repo registry.Reader }

func (a principalCatalogAdapter) CurrentRevision(ctx context.Context, organizationID string) (int64, error) {
	revision, err := a.repo.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	if revision == nil {
		return 0, registry.ErrNotFound
	}
	return revision.ID, nil
}
func (a principalCatalogAdapter) GetRole(ctx context.Context, organizationID, roleID string) (modeldispatch.RoleRef, error) {
	role, err := a.repo.GetRole(ctx, organizationID, roleID)
	if err != nil {
		return modeldispatch.RoleRef{}, err
	}
	return modeldispatch.RoleRef{ID: role.ID, Enabled: role.Enabled, Executable: role.Executable, AuthorityClass: role.AuthorityClass}, nil
}

type staticTaskReader struct{ ref modelruntime.TaskAttemptRef }

func (r staticTaskReader) GetTaskAttempt(context.Context, int64, int64) (modelruntime.TaskAttemptRef, error) {
	return r.ref, nil
}

type staticContextReader struct {
	ref         modelruntime.ContextSnapshotRef
	rendered    []byte
	renderErr   error
	renderCalls int
}

func (r *staticContextReader) GetContextSnapshot(context.Context, int64) (modelruntime.ContextSnapshotRef, error) {
	return r.ref, nil
}
func (r *staticContextReader) ValidateContextSnapshot(context.Context, int64) error { return nil }
func (r *staticContextReader) RenderContextSnapshot(context.Context, int64) ([]byte, error) {
	r.renderCalls++
	if r.renderErr != nil {
		return nil, r.renderErr
	}
	return append([]byte(nil), r.rendered...), nil
}

type authorizationDispatchAdapter struct{ evaluator authorization.Evaluator }

func (a authorizationDispatchAdapter) EvaluateDispatch(ctx context.Context, organizationID string, revisionID int64, actorRoleID, resourceID, actionDigest string) (modelruntime.AuthorizationDecision, error) {
	result, err := a.evaluator.Evaluate(ctx, authorization.EvaluationRequest{OrganizationID: organizationID, OrganizationRevisionID: revisionID, ActorRoleID: actorRoleID, CapabilityID: "model.invoke", ResourceType: "model_invocation", ResourceID: resourceID, ActionDigest: actionDigest})
	if err != nil {
		return modelruntime.AuthorizationDecision{}, err
	}
	effect := modelegress.AuthorizationDeny
	if result.Effect == authorization.EffectAllow {
		effect = modelegress.AuthorizationAllow
	}
	return modelruntime.AuthorizationDecision{Effect: effect, Allowed: result.Effect == authorization.EffectAllow, ReasonCode: string(result.ReasonCode), MatrixHash: result.MatrixHash}, nil
}

type allowEvaluator struct{ matrixHash string }

func (a allowEvaluator) EvaluateDispatch(context.Context, string, int64, string, string, string) (modelruntime.AuthorizationDecision, error) {
	return modelruntime.AuthorizationDecision{Effect: modelegress.AuthorizationAllow, Allowed: true, ReasonCode: "allowed_by_grant", MatrixHash: a.matrixHash}, nil
}

type denyEvaluator struct{ matrixHash string }

func (d denyEvaluator) EvaluateDispatch(context.Context, string, int64, string, string, string) (modelruntime.AuthorizationDecision, error) {
	return modelruntime.AuthorizationDecision{Effect: modelegress.AuthorizationDeny, Allowed: false, ReasonCode: "grant_missing", MatrixHash: d.matrixHash}, nil
}

type countingAdapter struct{ calls int }

func (a *countingAdapter) ProviderID() string { return "test.fake" }
func (a *countingAdapter) Descriptor() modelruntime.AdapterDescriptor {
	return modelruntime.AdapterDescriptor{
		ProviderID: "test.fake", AdapterID: "counting-test", AdapterVersion: 1,
		Transport: modelruntime.TransportFake, RequestSchemaVersion: "test.fake.request.v1",
		ResponseSchemaVersion: "test.fake.response.v1",
		EndpointFingerprint:   modelruntime.SHA256Bytes([]byte("test.fake:endpoint")),
		CredentialRefHash:     modelruntime.SHA256Bytes([]byte("test.fake:credential")),
	}
}
func (a *countingAdapter) Preflight(ctx context.Context, request modelruntime.ProviderPreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ProviderID != "test.fake" || request.ProviderModelID == "" || request.Deadline.IsZero() {
		return modelruntime.ErrInvalidRequest
	}
	return nil
}
func (a *countingAdapter) Dispatch(context.Context, modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	a.calls++
	content := []byte(`{"provider":"test.fake"}`)
	return modelruntime.RawResponse{Content: content, ProviderRequestID: "counting", ProviderOutcome: modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		ProviderRequestID:     "counting", HTTPStatus: 200,
		ResponseHash: modelruntime.SHA256Bytes(content), ResponseSchemaVersion: "test.fake.response.v1",
	}}, nil
}

type classifiedAdapter struct {
	phase   modelruntime.AdapterFailurePhase
	outcome modelruntime.ProviderOutcome
	calls   int
}

func (a *classifiedAdapter) ProviderID() string { return "test.fake" }
func (a *classifiedAdapter) Descriptor() modelruntime.AdapterDescriptor {
	return modelruntime.AdapterDescriptor{
		ProviderID: "test.fake", AdapterID: "classified-test", AdapterVersion: 1,
		Transport: modelruntime.TransportFake, RequestSchemaVersion: "test.fake.request.v1",
		ResponseSchemaVersion: "test.fake.response.v1",
		EndpointFingerprint:   modelruntime.SHA256Bytes([]byte("test.fake:endpoint")),
		CredentialRefHash:     modelruntime.SHA256Bytes([]byte("test.fake:credential")),
	}
}
func (a *classifiedAdapter) Preflight(ctx context.Context, request modelruntime.ProviderPreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
func (a *classifiedAdapter) Dispatch(context.Context, modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	a.calls++
	return modelruntime.RawResponse{}, &modelruntime.AdapterError{Phase: a.phase, Outcome: a.outcome, Cause: errors.New("classified provider failure")}
}

func openModelStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "model-runtime-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetModelSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `TRUNCATE model_provider_outcomes,model_provider_requests,model_egress_evaluations,model_invocation_usage,model_invocation_results,model_dispatch_attempts,model_invocations,model_egress_revision_bindings,model_egress_rules,model_egress_policy_versions,role_model_bindings,model_capability_snapshots,model_profile_versions,model_profiles,model_providers,context_segments,context_snapshots,authorization_uses,authorization_decisions,authorization_requests,staging_events,staging_reviews,staging_promotions,staging_checks,staging_workspace_artifacts,staging_artifacts,staging_workspaces,outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,task_requirements,task_dependencies,tasks,organization_reporting_lines,organization_registry_revision_documents,organization_roles,organizational_units,organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func syncModelCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, modelIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
}

func convertRoles(values []registry.Role) map[string]modelruntime.RoleRef {
	result := make(map[string]modelruntime.RoleRef, len(values))
	for _, role := range values {
		policy := ""
		if role.ModelPolicy != nil {
			policy = *role.ModelPolicy
		}
		result[role.ID] = modelruntime.RoleRef{ID: role.ID, ModelPolicy: policy, Enabled: role.Enabled, Executable: role.Executable, AuthorityClass: role.AuthorityClass, UnitID: role.UnitID}
	}
	return result
}

func fakeRegistryPlan(revisionID int64, routingHash string) modelruntime.RegistryPlan {
	const (
		roleID    = "ingenieria_ia/code-runner"
		policyID  = "department.worker"
		profileID = "worker-default"
		provider  = "test.fake"
	)
	capabilities := []modelruntime.ModelCapability{"structured.output"}
	return modelruntime.RegistryPlan{
		OrganizationID:         modelIntegrationOrganization,
		OrganizationRevisionID: revisionID,
		CanonicalHash:          routingHash,
		Providers: []modelruntime.Provider{{
			OrganizationID:         modelIntegrationOrganization,
			ID:                     provider,
			Transport:              modelruntime.TransportFake,
			AdapterStatus:          modelruntime.AdapterAvailable,
			DispatchEnabled:        true,
			DirectHTTPForbidden:    false,
			CanonicalHash:          modelruntime.SHA256Bytes([]byte("fake-provider-" + routingHash)),
			OrganizationRevisionID: revisionID,
		}},
		Profiles: []modelruntime.Profile{{
			OrganizationID: modelIntegrationOrganization,
			ID:             profileID,
			PolicyID:       policyID,
		}},
		Versions: []modelruntime.ProfileVersion{{
			OrganizationID:         modelIntegrationOrganization,
			ProfileID:              profileID,
			OrganizationRevisionID: revisionID,
			CanonicalDocumentHash:  routingHash,
			VersionHash:            modelruntime.SHA256Bytes([]byte("fake-version-" + routingHash)),
			ProviderID:             provider,
			ProviderModelID:        "deterministic-v1",
			Transport:              modelruntime.TransportFake,
			AdapterStatus:          modelruntime.AdapterAvailable,
			DispatchEnabled:        true,
		}},
		CapabilitySnapshots: []modelruntime.CapabilitySnapshot{{
			OrganizationID: modelIntegrationOrganization,
			ProfileID:      profileID,
			Capabilities:   capabilities,
			CapabilityHash: modelruntime.SHA256Bytes([]byte("structured.output")),
		}},
		Bindings: []modelruntime.RoleBinding{
			{OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, RoleID: roleID, PolicyID: policyID, ProfileID: profileID, BindingHash: modelruntime.SHA256Bytes([]byte("fake-binding-code-runner-" + routingHash)), Active: true},
			{OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, RoleID: "ingenieria_ia/frontend", PolicyID: policyID, ProfileID: profileID, BindingHash: modelruntime.SHA256Bytes([]byte("fake-binding-frontend-" + routingHash)), Active: true},
		},
	}
}

func fakeEgressPlan(revisionID int64, canonicalHash string) modelegress.RegistryPlan {
	return modelegress.RegistryPlan{
		OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, CanonicalHash: canonicalHash,
		Policy: modelegress.CanonicalPolicy{
			SchemaVersion: "0.1.0", DocumentStatus: "test_fixture", PolicyID: "model-egress", PolicyVersion: 1,
			DefaultAction: modelegress.EffectDeny, CanonicalHash: canonicalHash,
			HardDenies: []modelegress.HardDeny{
				{DataClassification: modelegress.ClassificationSecret, ReasonCode: "secret_egress_forbidden"},
				{DataClassification: modelegress.ClassificationClinical, ReasonCode: "clinical_egress_forbidden"},
			},
			Rules: []modelegress.Rule{
				{ProviderID: "test.fake", DataClassification: modelegress.ClassificationPublic, Effect: modelegress.EffectAllow, ReasonCode: "fixture_public_allow"},
				{ProviderID: "test.fake", DataClassification: modelegress.ClassificationSanitized, Effect: modelegress.EffectAllow, ReasonCode: "fixture_sanitized_allow"},
				{ProviderID: "test.fake", DataClassification: modelegress.ClassificationOrganizational, Effect: modelegress.EffectAllow, ReasonCode: "fixture_organizational_allow"},
			},
		},
	}
}

func insertFakeRoutingRevision(t *testing.T, ctx context.Context, store *platformpostgres.Store, routingHash, egressHash, capabilityHash string) int64 {
	t.Helper()
	documentHashes, err := json.Marshal(map[string]string{"model-routing.yaml": routingHash, "model-egress-policy.yaml": egressHash, "capability-matrix.yaml": capabilityHash})
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = store.Pool().QueryRow(ctx, `INSERT INTO organization_registry_revisions(canonical_hash,status,schema_versions,document_hashes,counts,diff,applied_at) VALUES($1,'applied','{}',$2::jsonb,'{}','{}',clock_timestamp()) RETURNING id`, modelruntime.SHA256Bytes([]byte("fake-revision-"+routingHash)), documentHashes).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$1,updated_at=clock_timestamp() WHERE id=$2`, id, modelIntegrationOrganization); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertModelExecutionFixture(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, roleID, suffix string) (modelruntime.TaskAttemptRef, modelruntime.ContextSnapshotRef) {
	t.Helper()
	now := time.Now().UTC()
	var taskID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO tasks(organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version) VALUES($1,$2,$3,'ingenieria_ia',$4,$5,'Model runtime integration','Exercise fake one-shot dispatch.','[]','running',0,$6,5,1,1) RETURNING id`, modelIntegrationOrganization, revisionID, roleID, "model-runtime-integration-task-"+suffix, modelruntime.SHA256Bytes([]byte("task-"+suffix)), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	var attemptID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at) VALUES($1,1,'running','integration-worker',$2,$2,$2,$2) RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	leaseExpiry := now.Add(30 * time.Minute)
	if _, err := store.Pool().Exec(ctx, `INSERT INTO task_leases(task_id,attempt_id,token_hash,holder_id,status,issued_at,heartbeat_at,expires_at) VALUES($1,$2,$3,'integration-worker','active',$4,$4,$5)`, taskID, attemptID, modelruntime.SHA256Bytes([]byte("lease")), now, leaseExpiry); err != nil {
		t.Fatal(err)
	}
	rendered := []byte("safe integration context")
	var snapshotID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO context_snapshots(organization_id,organization_revision_id,actor_role_id,purpose,task_ref,idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,created_at) VALUES($1,$2,$3,'model invocation',$4,$5,$6,$7,$8,$9,'ready',1,0,0,0,0,$10) RETURNING id`, modelIntegrationOrganization, revisionID, roleID, strconv.FormatInt(taskID, 10), "model-runtime-integration-context-"+suffix, modelruntime.SHA256Bytes([]byte("context-request-"+suffix)), modelruntime.SHA256Bytes([]byte("precedence")), modelruntime.SHA256Bytes([]byte("bundle")), modelruntime.SHA256Bytes(rendered), now).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	return modelruntime.TaskAttemptRef{TaskID: taskID, AttemptID: attemptID, OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, AssignedRoleID: roleID, TaskStatus: "running", AttemptStatus: "running", LeaseHolderID: "integration-worker", LeaseExpiresAt: leaseExpiry}, modelruntime.ContextSnapshotRef{ID: snapshotID, OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, ActorRoleID: roleID, TaskRef: strconv.FormatInt(taskID, 10), Status: "ready", RenderedHash: modelruntime.SHA256Bytes(rendered), DataClasses: []string{"organizational"}}
}

func validInvocationCommand(task modelruntime.TaskAttemptRef, snapshot modelruntime.ContextSnapshotRef, roleID, key string) modelruntime.CreateInvocationCommand {
	return modelruntime.CreateInvocationCommand{OrganizationID: modelIntegrationOrganization, TaskID: task.TaskID, AttemptID: task.AttemptID, SubjectRoleID: roleID, ContextSnapshotID: snapshot.ID, Purpose: "PostgreSQL fake adapter integration", RequiredCapabilities: []modelruntime.ModelCapability{"structured.output"}, OutputMode: modelruntime.OutputJSON, OutputSchema: json.RawMessage(`{"type":"object","required":["provider"],"properties":{"provider":{"type":"string"}}}`), MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled, IdempotencyKey: key, CorrelationID: "model-integration", CausationID: "branch-10", Deadline: time.Now().UTC().Add(20 * time.Minute)}
}

func fixturePrincipalAndAssignment(t *testing.T, ctx context.Context, store *dispatchpostgres.Store, task modelruntime.TaskAttemptRef, subjectRoleID, dispatchActorRoleID, suffix string) (modeldispatch.ExecutionPrincipal, modeldispatch.DispatcherAssignment) {
	t.Helper()
	principalKey := "integration/model-runtime-" + suffix
	principalHash, err := modeldispatch.PrincipalRequestHash(modelIntegrationOrganization, principalKey, dispatchActorRoleID, modeldispatch.PrincipalLocalProcess, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{
		Command:     modeldispatch.RegisterPrincipalCommand{OrganizationID: modelIntegrationOrganization, PrincipalKey: principalKey, DispatchActorRoleID: dispatchActorRoleID, PrincipalKind: modeldispatch.PrincipalLocalProcess, IdempotencyKey: "principal-" + suffix},
		RequestHash: principalHash, RegisteredByRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := registered.Principal
	validFrom := time.Now().UTC()
	validUntil := task.LeaseExpiresAt
	const maxInvocations = 50
	assignmentHash, err := modeldispatch.AssignmentScopeHash(modelIntegrationOrganization, task.OrganizationRevisionID, task.TaskID, task.AttemptID, subjectRoleID, dispatchActorRoleID, principal.ID, maxInvocations, validFrom, validUntil)
	if err != nil {
		t.Fatal(err)
	}
	requestHash, err := modeldispatch.AssignmentRequestHash(modelIntegrationOrganization, task.OrganizationRevisionID, task.TaskID, task.AttemptID, subjectRoleID, principal.ID, principal.PrincipalKey, dispatchActorRoleID, validFrom, validUntil, maxInvocations, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateAssignment(ctx, modeldispatch.PreparedCreateAssignment{
		Command:   modeldispatch.CreateAssignmentCommand{OrganizationID: modelIntegrationOrganization, TaskID: task.TaskID, AttemptID: task.AttemptID, SubjectRoleID: subjectRoleID, ExecutionPrincipalKey: principal.PrincipalKey, MaxInvocations: maxInvocations, IdempotencyKey: "assignment-" + suffix},
		Principal: principal, OrganizationRevisionID: task.OrganizationRevisionID, ValidFrom: validFrom, ValidUntil: validUntil, AssignmentHash: assignmentHash, RequestHash: requestHash, CreatedByRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	return principal, created.Assignment
}

func fixtureAssignmentForExistingPrincipal(t *testing.T, ctx context.Context, store *dispatchpostgres.Store, task modelruntime.TaskAttemptRef, subjectRoleID, dispatchActorRoleID string, principal modeldispatch.ExecutionPrincipal, suffix string) modeldispatch.DispatcherAssignment {
	t.Helper()
	validFrom := time.Now().UTC()
	validUntil := task.LeaseExpiresAt
	const maxInvocations = 50
	assignmentHash, err := modeldispatch.AssignmentScopeHash(modelIntegrationOrganization, task.OrganizationRevisionID, task.TaskID, task.AttemptID, subjectRoleID, dispatchActorRoleID, principal.ID, maxInvocations, validFrom, validUntil)
	if err != nil {
		t.Fatal(err)
	}
	requestHash, err := modeldispatch.AssignmentRequestHash(modelIntegrationOrganization, task.OrganizationRevisionID, task.TaskID, task.AttemptID, subjectRoleID, principal.ID, principal.PrincipalKey, dispatchActorRoleID, validFrom, validUntil, maxInvocations, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateAssignment(ctx, modeldispatch.PreparedCreateAssignment{
		Command:   modeldispatch.CreateAssignmentCommand{OrganizationID: modelIntegrationOrganization, TaskID: task.TaskID, AttemptID: task.AttemptID, SubjectRoleID: subjectRoleID, ExecutionPrincipalKey: principal.PrincipalKey, MaxInvocations: maxInvocations, IdempotencyKey: "assignment-" + suffix},
		Principal: principal, OrganizationRevisionID: task.OrganizationRevisionID, ValidFrom: validFrom, ValidUntil: validUntil, AssignmentHash: assignmentHash, RequestHash: requestHash, CreatedByRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatal(err)
	}
	return created.Assignment
}

func createModelInvocation(t *testing.T, ctx context.Context, service *modelruntime.InvocationService, command modelruntime.CreateInvocationCommand) modelruntime.Invocation {
	t.Helper()
	result, err := service.Create(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	return result.Invocation
}

func assertModelCount(t *testing.T, ctx context.Context, store *platformpostgres.Store, query string, arg any, want int) {
	t.Helper()
	var count int
	if err := store.Pool().QueryRow(ctx, query, arg).Scan(&count); err != nil || count != want {
		t.Fatalf("count=%d want=%d err=%v", count, want, err)
	}
}

func assertModelCountTwo(t *testing.T, ctx context.Context, store *platformpostgres.Store, query string, first, second any, want int) {
	t.Helper()
	var count int
	if err := store.Pool().QueryRow(ctx, query, first, second).Scan(&count); err != nil || count != want {
		t.Fatalf("count=%d want=%d err=%v", count, want, err)
	}
}
