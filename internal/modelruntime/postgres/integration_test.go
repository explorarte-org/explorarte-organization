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
	"sort"
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
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	harnessmodelruntime "github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/tasksauthority"
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
	taskdomain "github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const modelIntegrationOrganization = "explorarte"

type allowHarnessAuthority struct{}

func (allowHarnessAuthority) AuthorizeExecution(context.Context, executionharness.AuthorityRequest) error {
	return nil
}

type emptyHarnessToolCatalog struct{}

func (emptyHarnessToolCatalog) Lookup(context.Context, string) (executionharness.ToolDefinition, bool) {
	return executionharness.ToolDefinition{}, false
}

func (emptyHarnessToolCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return errors.New("no tools are exposed in this integration fixture")
}

type rejectHarnessToolExecutor struct{}

func (rejectHarnessToolExecutor) Execute(context.Context, executionharness.RunIdentity, executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	return executionharness.ToolExecutionResult{}, errors.New("no tool execution is allowed in this integration fixture")
}

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
		// Every canonical routing policy must resolve through an HTTP API
		// adapter. The expected count is derived from the routing document
		// rather than hardcoded: it used to read "want 6/6", which silently
		// went stale the moment a seventh policy landed and then failed the
		// trunk for a reason that had nothing to do with the behavior under
		// test. Alibaba Token Plan remains a retired historical
		// implementation and must not materialize as a provider or profile
		// version -- that is what makes this a real assertion rather than a
		// tautology, since it is absent from the routing document too.
		routingForCount, routingErr := modelruntime.LoadCanonicalRouting(filepath.Join("..", "..", "..", "docs", "canonical"))
		if routingErr != nil {
			t.Fatalf("load canonical routing: %v", routingErr)
		}
		wantVersions := len(routingForCount.Policies)
		if enabled != wantVersions || available != wantVersions {
			t.Fatalf("compiled provider versions enabled=%d available=%d, want %d/%d", enabled, available, wantVersions, wantVersions)
		}
		// R30 retired every gemini routing policy (research.audit/research.worker
		// moved to deepseek-v4-pro/flash, department.leader moved to
		// deepseek-v4-pro) — gemini remains a compiled HTTP adapter (still used
		// by internal/embeddingruntime, a separate dispatch path this registry
		// sync does not touch) but now resolves zero profile versions here.
		// Per-provider expectations come from the canonical routing document.
		// They were hardcoded (openai_compatible: 2, deepseek: 4, gemini: 0)
		// and drifted as policies moved between providers, failing the trunk
		// for a reason unrelated to the behavior under test. Deriving them
		// keeps the assertion honest without needing a human to remember.
		perProvider := make(map[string]int, len(routingForCount.Policies))
		for _, policy := range routingForCount.Policies {
			perProvider[policy.Provider]++
		}
		for _, provider := range []string{"openai_compatible", "deepseek", "gemini", "openai_responses"} {
			assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND provider_id='`+provider+`' AND transport='http_adapter' AND dispatch_enabled AND adapter_status='available'`, revision.ID, perProvider[provider])
		}
		// The CEO profile must resolve to whichever provider the canonical
		// routing assigns to executive.ceo -- it was pinned to
		// openai_compatible here and silently became wrong when the policy
		// moved to openai_responses.
		ceoPolicy, hasCEO := routingForCount.Policies["executive.ceo"]
		if !hasCEO {
			t.Fatal("canonical routing has no executive.ceo policy")
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND profile_id='ceo-primary' AND provider_id='`+ceoPolicy.Provider+`' AND transport='http_adapter' AND dispatch_enabled AND adapter_status='available'`, revision.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND provider_id='alibaba_token_plan_via_claude_code' AND (dispatch_enabled OR adapter_status<>'unavailable')`, revision.ID, 0)
		// Nothing outside the canonically routed providers may materialize.
		// The exclusion list was hardcoded and went stale as providers were
		// added, so it started flagging legitimately routed ones; it is now
		// built from the routing document itself.
		quoted := make([]string, 0, len(perProvider)+1)
		for provider := range perProvider {
			quoted = append(quoted, "'"+provider+"'")
		}
		quoted = append(quoted, "'alibaba_token_plan_via_claude_code'")
		sort.Strings(quoted)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_profile_versions WHERE organization_revision_id=$1 AND provider_id NOT IN (`+strings.Join(quoted, ",")+`) AND (dispatch_enabled OR adapter_status<>'unavailable')`, revision.ID, 0)
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

	t.Run("execution harness turn traverses durable model runtime dispatch", func(t *testing.T) {
		taskDB, taskErr := taskpostgres.New(platform)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		principalID := strconv.FormatInt(principal.ID, 10)
		harnessTaskRef, harnessSnapshotRef, harnessLeaseToken := claimModelExecutionFixture(t, ctx, platform, taskDB, fakeRevisionID, "ingenieria_ia/code-runner", "executive-orchestrator", principalID, "harness-modelruntime")
		fixtureAssignmentForExistingPrincipal(t, ctx, dispatchStore, harnessTaskRef, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", principal, "harness-modelruntime")
		harnessContexts := &staticContextReader{ref: harnessSnapshotRef, rendered: []byte("safe integration context")}
		harnessTasks := staticTaskReader{ref: harnessTaskRef}
		harnessInvocations, newErr := modelruntime.NewInvocationService(modelIntegrationOrganization, fakeCatalog, harnessTasks, harnessContexts, store, egressStore, identityStore, assignments, modelruntime.ClockFunc(time.Now), 10, false)
		if newErr != nil {
			t.Fatal(newErr)
		}
		sequence := &harnessSequenceAdapter{}
		harnessDispatch, newErr := modelruntime.NewDispatchService(modelIntegrationOrganization, cfg, fakeCatalog, harnessTasks, harnessContexts, capabilityEvaluator, egressStore, modelegress.NewEvaluator(), store, principals, assignments, identityService, store, adapter.NewRegistry(sequence), modelruntime.ClockFunc(time.Now))
		if newErr != nil {
			t.Fatal(newErr)
		}
		principalReader, taskErr := tasksauthority.NewCanonicalPrincipalReader(dispatchStore)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		authority, taskErr := tasksauthority.New(taskDB, principalReader)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		modelExecutor, newErr := harnessmodelruntime.New(harnessInvocations, harnessDispatch, harnessmodelruntime.ClockFunc(time.Now), harnessmodelruntime.Config{
			MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled, InvocationTTL: 20 * time.Minute,
		})
		if newErr != nil {
			t.Fatal(newErr)
		}
		contextBody := string(harnessContexts.rendered)
		spec := executionharness.RunSpec{
			Identity: executionharness.RunIdentity{
				RunID: "pg-harness-modelruntime-1", OrganizationID: modelIntegrationOrganization,
				TaskID: harnessTaskRef.TaskID, AttemptID: harnessTaskRef.AttemptID, RoleID: harnessTaskRef.AssignedRoleID,
				ExecutionPrincipalID: principalID, CorrelationID: "pg-harness-modelruntime", CausationID: "pg-harness-entry",
			},
			LeaseToken: harnessLeaseToken,
			Context: executionharness.InitialContext{
				ID: strconv.FormatInt(harnessSnapshotRef.ID, 10), Version: "context-snapshot-v1",
				Digest: modelruntime.SHA256Bytes([]byte(contextBody)), Content: contextBody,
			},
			Tools:  []executionharness.ToolDefinition{{Name: "lookup_fixture", Description: "read deterministic fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}},
			Policy: executionharness.RunPolicy{MaxTurns: 3, MaxToolCalls: 2, ExecutionProfileID: "standard-v1", ModelPolicyRef: "canonical-role-binding", BuildRef: "integration-build"},
		}
		history := executionharness.NewMemoryHistoryStore()
		harness, newErr := executionharness.New(authority, modelExecutor, integrationHarnessToolCatalog{definition: spec.Tools[0]}, &integrationHarnessToolExecutor{}, history)
		if newErr != nil {
			t.Fatal(newErr)
		}
		result := harness.Execute(ctx, spec)
		if result.Status != executionharness.StatusCompleted || result.FinalOutput != "integration final" || sequence.calls != 2 {
			t.Fatalf("harness result=%+v", result)
		}
		events, readErr := history.Read(ctx, spec.Identity.RunID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var invocationRefs []string
		for _, event := range events {
			if event.Type == executionharness.EventModelResponseRecorded && event.ModelResult != nil {
				invocationRefs = append(invocationRefs, event.ModelResult.InvocationRef)
			}
		}
		if len(invocationRefs) != 2 || invocationRefs[0] == invocationRefs[1] {
			t.Fatalf("expected two distinct durable invocation IDs, got %v", invocationRefs)
		}
		invocationRef := ""
		for _, event := range events {
			if event.Type == executionharness.EventModelResponseRecorded && event.ModelResult != nil {
				invocationRef = event.ModelResult.InvocationRef
				break
			}
		}
		invocationID, parseErr := strconv.ParseInt(invocationRef, 10, 64)
		if parseErr != nil {
			t.Fatalf("invocation ref=%q: %v", invocationRef, parseErr)
		}
		input, inputErr := store.GetModelInput(ctx, invocationID)
		if inputErr != nil || input.Envelope.CanonicalProjectionDigest == "" || input.Envelope.ContextSnapshotID != harnessSnapshotRef.ID ||
			len(input.Envelope.StablePrefix) != 1 || input.Envelope.StablePrefix[0].Content != contextBody {
			t.Fatalf("durable harness model input=%+v err=%v", input, inputErr)
		}
		request, projectErr := executionharness.Project(spec, nil)
		if projectErr != nil {
			t.Fatal(projectErr)
		}
		recovered, recoverErr := modelExecutor.Invoke(ctx, spec.Identity, request)
		if recoverErr != nil || recovered.InvocationRef != invocationRef || len(recovered.ToolRequests) != 1 || recovered.ToolRequests[0].ToolCallID != "harness-call-1" {
			t.Fatalf("idempotent durable recovery=%+v err=%v", recovered, recoverErr)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_inputs WHERE invocation_id=$1`, invocationID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_results WHERE invocation_id=$1`, invocationID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_usage WHERE invocation_id=$1`, invocationID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_provider_requests WHERE invocation_id=$1 AND provider_id='test.fake'`, invocationID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_execution_identity_assertions WHERE invocation_id=$1 AND verification_effect='allow'`, invocationID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_dispatcher_assignment_uses WHERE invocation_id=$1`, invocationID, 1)

		runMutation := func(name string, mutate func() error, restore func() error) {
			sequence.calls = 0
			mutatingTools := &integrationHarnessToolExecutor{beforeReply: mutate}
			mutatingHistory := executionharness.NewMemoryHistoryStore()
			mutatingHarness, harnessErr := executionharness.New(authority, modelExecutor, integrationHarnessToolCatalog{definition: spec.Tools[0]}, mutatingTools, mutatingHistory)
			if harnessErr != nil {
				t.Fatal(harnessErr)
			}
			mutatedSpec := spec
			mutatedSpec.Identity.RunID = name
			mutatedSpec.Identity.CorrelationID = name + ":correlation"
			mutatedSpec.Identity.CausationID = name + ":causation"
			got := mutatingHarness.Execute(ctx, mutatedSpec)
			restoreErr := restore()
			if restoreErr != nil {
				t.Fatal(restoreErr)
			}
			if got.Status != executionharness.StatusAuthorizationDenied || sequence.calls != 1 || mutatingTools.calls != 1 {
				t.Fatalf("mid-run authority mutation was not fail-closed: result=%+v provider_calls=%d tool_calls=%d", got, sequence.calls, mutatingTools.calls)
			}
		}

		runMutation("pg-harness-principal-revoked", func() error {
			_, err := platform.Pool().Exec(ctx, `UPDATE model_execution_principals SET status='disabled',disabled_at=clock_timestamp(),disabled_by_role_id='empresa/human',disable_reason_code='integration_revoke' WHERE id=$1`, principal.ID)
			return err
		}, func() error {
			_, err := platform.Pool().Exec(ctx, `UPDATE model_execution_principals SET status='active',disabled_at=NULL,disabled_by_role_id=NULL,disable_reason_code=NULL WHERE id=$1`, principal.ID)
			return err
		})
		runMutation("pg-harness-lease-revoked", func() error {
			_, err := platform.Pool().Exec(ctx, `UPDATE task_leases SET status='revoked',released_at=clock_timestamp(),release_reason='integration_revoke' WHERE task_id=$1 AND attempt_id=$2`, harnessTaskRef.TaskID, harnessTaskRef.AttemptID)
			return err
		}, func() error {
			_, err := platform.Pool().Exec(ctx, `UPDATE task_leases SET status='active',released_at=NULL,release_reason=NULL WHERE task_id=$1 AND attempt_id=$2`, harnessTaskRef.TaskID, harnessTaskRef.AttemptID)
			return err
		})

		// Expiry is a different scenario from revocation even though both must
		// deny. Revocation flips the row's status; expiry leaves the lease
		// active and only moves its deadline into the past, which is what the
		// authority query actually tests with clock_timestamp() < expires_at.
		// The assertion inside the mutation keeps the two from collapsing into
		// each other: if the row ever came back non-active this would stop
		// being an expiry test and silently become a second revocation test.
		runMutation("pg-harness-lease-expired", func() error {
			// task_leases enforces CHECK (expires_at > issued_at), so an
			// expired lease is modelled as one issued two hours ago that
			// lapsed an hour ago -- the state a lease reaper has not swept
			// yet, which is exactly the dangerous one.
			if _, err := platform.Pool().Exec(ctx, `UPDATE task_leases SET issued_at=clock_timestamp()-make_interval(secs=>7200),heartbeat_at=clock_timestamp()-make_interval(secs=>7200),expires_at=clock_timestamp()-make_interval(secs=>3600) WHERE task_id=$1 AND attempt_id=$2`, harnessTaskRef.TaskID, harnessTaskRef.AttemptID); err != nil {
				return err
			}
			var status string
			if err := platform.Pool().QueryRow(ctx, `SELECT status FROM task_leases WHERE task_id=$1 AND attempt_id=$2`, harnessTaskRef.TaskID, harnessTaskRef.AttemptID).Scan(&status); err != nil {
				return err
			}
			if status != "active" {
				return fmt.Errorf("expiry scenario degraded into revocation: lease status=%q", status)
			}
			return nil
		}, func() error {
			_, err := platform.Pool().Exec(ctx, `UPDATE task_leases SET issued_at=clock_timestamp(),heartbeat_at=clock_timestamp(),expires_at=clock_timestamp()+make_interval(secs=>1800) WHERE task_id=$1 AND attempt_id=$2`, harnessTaskRef.TaskID, harnessTaskRef.AttemptID)
			return err
		})

		// The mirror of the expiry case: a lease renewed while it is still
		// valid must NOT be denied. Without this, an authority that simply
		// refused every second turn would pass every test above.
		sequence.calls = 0
		renewingTools := &integrationHarnessToolExecutor{beforeReply: func() error {
			_, heartbeatErr := taskDB.Heartbeat(ctx, taskdomain.LeaseCommand{
				TaskID: harnessTaskRef.TaskID, AttemptID: harnessTaskRef.AttemptID,
				LeaseToken: harnessLeaseToken, ActorID: principalID, Extension: 30 * time.Minute,
			}, 15*time.Minute)
			return heartbeatErr
		}}
		renewedSpec := spec
		renewedSpec.Identity.RunID = "pg-harness-lease-renewed"
		renewedSpec.Identity.CorrelationID = "pg-harness-lease-renewed:correlation"
		renewedSpec.Identity.CausationID = "pg-harness-lease-renewed:causation"
		renewingHarness, renewErr := executionharness.New(authority, modelExecutor, integrationHarnessToolCatalog{definition: spec.Tools[0]}, renewingTools, executionharness.NewMemoryHistoryStore())
		if renewErr != nil {
			t.Fatal(renewErr)
		}
		renewed := renewingHarness.Execute(ctx, renewedSpec)
		if renewed.Status != executionharness.StatusCompleted || sequence.calls != 2 || renewingTools.calls != 1 {
			t.Fatalf("renewed lease was denied: result=%+v provider_calls=%d tool_calls=%d", renewed, sequence.calls, renewingTools.calls)
		}
	})

	t.Run("credential-bearing model input is rejected before durable admission", func(t *testing.T) {
		const idempotencyKey = "pg-secret-admission"
		command := validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", idempotencyKey)
		command.ModelInput = &modelruntime.ModelInputEnvelope{
			SchemaVersion: modelruntime.ModelInputEnvelopeSchemaV1, ContextSnapshotID: snapshotRef.ID,
			CanonicalProjectionDigest: modelruntime.SHA256Bytes([]byte("secret-admission-projection")),
			StablePrefix:              []modelruntime.ModelInputMessage{{Role: modelruntime.ModelInputRoleUser, Content: string(contexts.rendered)}},
			VisibleHistory:            []modelruntime.ModelInputMessage{{Role: modelruntime.ModelInputRoleAssistant, Content: "API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456"}},
		}
		if _, createErr := invocations.Create(ctx, command); !errors.Is(createErr, modelruntime.ErrModelInputSecretRejected) {
			t.Fatalf("credential-bearing model input error=%v", createErr)
		}
		assertModelCountTwo(t, ctx, platform, `SELECT count(*) FROM model_invocations WHERE organization_id=$1 AND idempotency_key=$2`, modelIntegrationOrganization, idempotencyKey, 0)
		assertModelCountTwo(t, ctx, platform, `SELECT count(*) FROM model_invocation_inputs i JOIN model_invocations v ON v.id=i.invocation_id WHERE v.organization_id=$1 AND v.idempotency_key=$2`, modelIntegrationOrganization, idempotencyKey, 0)
	})

	t.Run("create reuse conflict and fake one-shot dispatch are durable", func(t *testing.T) {
		command := validInvocationCommand(taskRef, snapshotRef, "ingenieria_ia/code-runner", "pg-fake-dispatch")
		created, createErr := invocations.Create(ctx, command)
		if createErr != nil || created.Reused {
			t.Fatalf("created=%+v err=%v", created, createErr)
		}
		input, inputErr := store.GetModelInput(ctx, created.Invocation.ID)
		if inputErr != nil || input.Envelope.SchemaVersion != modelruntime.ModelInputEnvelopeSchemaV1 || input.Envelope.ContextSnapshotID != snapshotRef.ID || len(input.CanonicalBytes) == 0 || modelruntime.SHA256Bytes(input.CanonicalBytes) != input.CanonicalDigest {
			t.Fatalf("durable model input=%+v err=%v", input, inputErr)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_inputs WHERE invocation_id=$1`, created.Invocation.ID, 1)
		if _, mutationErr := platform.Pool().Exec(ctx, `UPDATE model_invocation_inputs SET schema_version='mutated' WHERE invocation_id=$1`, created.Invocation.ID); mutationErr == nil {
			t.Fatal("model invocation input accepted mutation")
		}
		if _, mutationErr := platform.Pool().Exec(ctx, `DELETE FROM model_invocation_inputs WHERE invocation_id=$1`, created.Invocation.ID); mutationErr == nil {
			t.Fatal("model invocation input accepted deletion")
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
			InputPriceNanosPerMillion: 1_000_000_000, OutputPriceNanosPerMillion: 2_000_000_000, BillingMode: modelpricing.BillingOnline, EffectiveAt: time.Now().UTC().Add(-time.Minute),
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
		// This table is the parity check between what the domain calls a
		// valid ProviderOutcome and what PostgreSQL will actually store.
		//
		// It already covered ambiguous_transport before AUTONOMY-SMOKE-017-R1,
		// and still missed the incident, because its only ambiguous case
		// carried no status and no response hash -- the one shape the old
		// CHECK happened to allow. A classification is not covered by an
		// example of it; it is covered by the shapes the domain admits.
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
			// AUTONOMY-SMOKE-017-R1: the provider began answering and the
			// body stopped arriving. modelruntime.IncompleteReadOutcome
			// keeps what was observed -- the status, the request ID and a
			// hash of the partial body -- because the call may already have
			// been billed. The schema used to refuse exactly this, so a
			// recoverable transport failure became a hard invalid request
			// and ended the campaign.
			{name: "transport ambiguous with a partial response", phase: modelruntime.AdapterFailureAmbiguous, outcome: modelruntime.IncompleteReadOutcome(200, "provider-partial-body", modelruntime.SHA256Bytes([]byte("partial body that stopped arriving")), "test.fake.response.v1"), wantStatus: modelruntime.InvocationAmbiguous, wantClass: modelruntime.ProviderOutcomeAmbiguous},
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

				// The outcome must come back as the domain described it.
				// Counting rows only proves something was written; the
				// defect this guards was about which observations survive.
				if test.outcome.Validate() != nil {
					t.Fatalf("the fixture is not a valid outcome, so persisting it would prove nothing: %v", test.outcome.Validate())
				}
				var gotStatus *int
				var gotHash, gotRequestID, gotErrorCode *string
				var gotRetryable bool
				if queryErr := platform.Pool().QueryRow(ctx, `
					SELECT http_status, response_hash, provider_request_id, error_code, retryable
					FROM model_provider_outcomes WHERE invocation_id=$1`, created.ID).
					Scan(&gotStatus, &gotHash, &gotRequestID, &gotErrorCode, &gotRetryable); queryErr != nil {
					t.Fatalf("the outcome did not come back: %v", queryErr)
				}
				if (gotStatus == nil) != (test.outcome.HTTPStatus == 0) ||
					(gotStatus != nil && *gotStatus != test.outcome.HTTPStatus) {
					t.Fatalf("http status changed in the store: got %v want %d", gotStatus, test.outcome.HTTPStatus)
				}
				if (gotHash == nil) != (test.outcome.ResponseHash == "") ||
					(gotHash != nil && *gotHash != test.outcome.ResponseHash) {
					t.Fatalf("response hash changed in the store: got %v want %q", gotHash, test.outcome.ResponseHash)
				}
				if (gotRequestID == nil) != (test.outcome.ProviderRequestID == "") ||
					(gotRequestID != nil && *gotRequestID != test.outcome.ProviderRequestID) {
					t.Fatalf("provider request id changed in the store: got %v want %q", gotRequestID, test.outcome.ProviderRequestID)
				}
				if gotErrorCode == nil || *gotErrorCode != test.outcome.ErrorCode || gotRetryable != test.outcome.Retryable {
					t.Fatalf("error code or retryability changed in the store: code=%v retryable=%v", gotErrorCode, gotRetryable)
				}
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
		if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), platform.Pool()); err != nil {
			t.Fatalf("refusing destructive migration DownSQL: %v", err)
		}
		// R21 extends the R12 provider-outcome table, so 000018 must come down
		// before 000011. R14 also references model invocations/attempts, so
		// 000012 must come down before the earlier model migrations. 000021's
		// provider_wallet_events FKs to model_invocations, so it must come
		// down before 000007 too.
		//
		// Any later migration that ALTERs provider_wallet_events/
		// provider_wallets (a table 000021 creates and whose down.sql DROPs
		// outright) must ALSO be rolled back here before 000021, or this
		// down/reapply cycle silently corrupts it: DROP TABLE removes every
		// index/column on it regardless of which migration added them, and
		// Up()'s loop skips re-running any migration whose schema_migrations
		// row this test did not itself delete — so that migration's
		// structure never comes back even though schema_migrations still
		// claims it's applied. 000025 (the single-terminal-event unique
		// index) and 000030 (the embedding invocation path) both do this
		// today; this list was originally missing both, which is exactly
		// what caused the mysterious "index/column does not exist" failures
		// chased down while building R29 — 000025's gap predates R29
		// entirely and had simply never been exercised by a full down/
		// reapply cycle until this test file was extended with 000030.
		// Any FUTURE migration that alters this table needs an entry here
		// too, ordered before 000021.
		versions := []struct {
			version int
			file    string
		}{
			// 000049 owns a FK to model_invocations, so its immutable input
			// table must come down before 000007 drops the invocation table.
			// Reapplying the ordered migration set recreates the 1:1 input
			// representation only after the Model Runtime gateway exists.
			{49, "000049_create_model_invocation_inputs.down.sql"},
			// 000044 replaced 000008 cross-table CHECK constraints with
			// deferrable constraint triggers, so 000008 down cannot drop
			// constraints that no longer exist. Its own down restores them
			// first, which is why it must be rolled back before 000008 --
			// the same rule the comment above states for any migration that
			// alters these tables.
			{44, "000044_make_egress_revision_ownership_restorable.down.sql"},
			// 000047 seeds a provider_wallets row; it must come down before
			// 000021 drops that table outright, same rule as everything else
			// in this list.
			{47, "000047_seed_openai_responses_pricing_and_wallet.down.sql"},
			// 000053 and 000052 both ALTER model_invocation_render_telemetry
			// (000053 also alters execution_context_views/tasks/
			// context_snapshots, none of which this list drops, so only its
			// effect on this specific table matters here) -- 000040's own
			// down.sql DROPs that table outright, so both must come down
			// first or this down/reapply cycle silently corrupts them,
			// exactly the failure mode every other entry in this list
			// documents.
			{53, "000053_add_semantic_selector_facts.down.sql"},
			{52, "000052_add_context_token_telemetry.down.sql"},
			{40, "000040_add_provider_render_telemetry.down.sql"},
			// 000039 replaces the CHECK constraints 000037 defines on
			// provider_wallet_events (adding the subscription_resource_consumed
			// value), so it must come down before 000037 removes the columns
			// those constraints check. Discovered the same way 000025/000030
			// were: this list silently omitted 000037/000039/000047 for a
			// while after they landed, and nothing running after this suite in
			// the shared harness database happened to depend on cost_provenance
			// or the openai_responses wallet row surviving -- until
			// costledger-postgres and modelpricing-postgres joined the official
			// harness manifest and both started failing with columns/rows
			// silently missing, even though a standalone fresh migrate-up (and
			// this very test's own PASS) never showed anything wrong.
			{39, "000039_add_subscription_billing_provenance.down.sql"},
			{37, "000037_add_cost_settlement_provenance.down.sql"},
			// 000034 widens embedding_invocations_operation_check (adds
			// memory_backfill); 000030's down.sql DROPs embedding_invocations
			// outright (it created the table), so 000034 must come down first
			// or schema_migrations keeps claiming 000034 is applied while the
			// table it altered no longer exists -- Up()'s reapply then recreates
			// the table via 000030 with its original, narrower 4-value
			// constraint and silently skips 000034 forever (it is still marked
			// applied), because nothing in THIS list deleted its
			// schema_migrations row. Exactly the same omission class as
			// 000025/000030/000037/000039/000047 before it -- this table just
			// happens to be a different one than the provider_wallet* pair the
			// comment above was scoped to, so it slipped through that fix too.
			// Found only once cmd/orgctl (RunnerKind: "costledger", fixture
			// r30-09) started inserting operation='memory_backfill' rows through
			// the official harness for the first time.
			// model_invocation_reasoning references model_dispatch_attempts,
			// so it has to come down before 000007 can drop that table. This
			// list is the same omission class the comment above describes:
			// a migration that adds a table and forgets to say how it goes
			// away is only found here, by a rollback that suddenly cannot
			// complete.
			{55, "000055_create_model_invocation_reasoning.down.sql"},
			{34, "000034_add_memory_backfill_embedding_operation.down.sql"},
			{30, "000030_extend_wallet_for_embedding_invocations.down.sql"},
			{25, "000025_enforce_wallet_single_terminal.down.sql"},
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
		// back here), so only the explicitly rolled-back versions above are
		// expected in Applied, while Current still reports the real tip.
		loadedForTip, tipErr := platformmigrations.Load(rootmigrations.Files)
		if tipErr != nil {
			t.Fatal(tipErr)
		}
		tip := loadedForTip[len(loadedForTip)-1].Version
		reapplied, upErr := runner.Up(ctx)
		// Derived from the rollback list above rather than hardcoded: adding a
		// migration there used to require remembering to bump a literal here
		// too, and forgetting turned a correct rollback into a red suite.
		if upErr != nil || len(reapplied.Applied) != len(versions) || reapplied.Current != tip {
			t.Fatalf("reapply=%+v err=%v want current=%d", reapplied, upErr, tip)
		}
		var exists bool
		if err = platform.Pool().QueryRow(ctx, `SELECT to_regclass('public.model_invocations') IS NOT NULL AND to_regclass('public.model_invocation_inputs') IS NOT NULL AND to_regclass('public.model_dispatch_attempts') IS NOT NULL AND to_regclass('public.model_egress_policy_versions') IS NOT NULL AND to_regclass('public.model_dispatcher_assignments') IS NOT NULL AND to_regclass('public.model_execution_principals') IS NOT NULL AND to_regclass('public.model_execution_identity_policy_versions') IS NOT NULL AND to_regclass('public.model_provider_requests') IS NOT NULL AND to_regclass('public.model_provider_outcomes') IS NOT NULL`).Scan(&exists); err != nil || !exists {
			t.Fatalf("reapply exists=%v err=%v", exists, err)
		}
		// Prove 000030's ALTER TABLE actually reran, not just that 000021's
		// CREATE TABLE did — this is the exact regression this test's own
		// omission of migration 30 from the down-list previously caused.
		var embeddingInvocationIDNullable bool
		if err = platform.Pool().QueryRow(ctx, `SELECT is_nullable='YES' FROM information_schema.columns WHERE table_schema='public' AND table_name='provider_wallet_events' AND column_name='embedding_invocation_id'`).Scan(&embeddingInvocationIDNullable); err != nil {
			t.Fatalf("provider_wallet_events.embedding_invocation_id missing after reapply: %v", err)
		}
		if !embeddingInvocationIDNullable {
			t.Fatal("embedding_invocation_id must be nullable")
		}
		var invocationIDNullable bool
		if err = platform.Pool().QueryRow(ctx, `SELECT is_nullable='YES' FROM information_schema.columns WHERE table_schema='public' AND table_name='provider_wallet_events' AND column_name='invocation_id'`).Scan(&invocationIDNullable); err != nil || !invocationIDNullable {
			t.Fatalf("provider_wallet_events.invocation_id must be nullable after reapply: nullable=%v err=%v", invocationIDNullable, err)
		}
		var embeddingInvocationsExists bool
		if err = platform.Pool().QueryRow(ctx, `SELECT to_regclass('public.embedding_invocations') IS NOT NULL`).Scan(&embeddingInvocationsExists); err != nil || !embeddingInvocationsExists {
			t.Fatalf("embedding_invocations missing after reapply: exists=%v err=%v", embeddingInvocationsExists, err)
		}
		// Prove 000037/000039 actually reran too, not just 000030/000021 --
		// this is the exact regression this test's own prior omission of
		// migrations 37/39/47 from the down-list caused for costledger-postgres
		// and modelpricing-postgres once they joined the official harness.
		var costProvenanceExists bool
		if err = platform.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='provider_wallet_events' AND column_name='cost_provenance')`).Scan(&costProvenanceExists); err != nil || !costProvenanceExists {
			t.Fatalf("provider_wallet_events.cost_provenance missing after reapply: exists=%v err=%v", costProvenanceExists, err)
		}
		// Prove 000047's seed row actually reran too -- costgate.Reserve fails
		// closed for openai_responses without it (ORG-AUDIT-003).
		var openaiResponsesWalletExists bool
		if err = platform.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM provider_wallets WHERE provider_id='openai_responses')`).Scan(&openaiResponsesWalletExists); err != nil || !openaiResponsesWalletExists {
			t.Fatalf("provider_wallets openai_responses row missing after reapply: exists=%v err=%v", openaiResponsesWalletExists, err)
		}
		// Prove 000034 actually reran too, not just 000030's CREATE TABLE --
		// insert a memory_backfill row directly and roll it back; the CHECK
		// constraint accepting it is the exact fact 000034 adds, and rolling
		// the insert back keeps this assertion side-effect-free.
		if _, err = platform.Pool().Exec(ctx, `
BEGIN;
INSERT INTO embedding_invocations (organization_id, actor_role_id, provider_id, provider_model_id, billing_mode, operation, created_at)
SELECT 'explorarte', 'ingenieria_ia/orquestador', 'reapply-probe', 'reapply-probe', 'online', 'memory_backfill', NOW()
WHERE EXISTS (SELECT 1 FROM organizations WHERE id='explorarte');
ROLLBACK;
`); err != nil {
			t.Fatalf("embedding_invocations_operation_check missing memory_backfill after reapply: %v", err)
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

type harnessSequenceAdapter struct{ calls int }

func (a *harnessSequenceAdapter) ProviderID() string { return "test.fake" }
func (a *harnessSequenceAdapter) Descriptor() modelruntime.AdapterDescriptor {
	return modelruntime.AdapterDescriptor{
		ProviderID: "test.fake", AdapterID: "harness-sequence", AdapterVersion: 1,
		Transport: modelruntime.TransportFake, RequestSchemaVersion: "test.fake.request.v1",
		ResponseSchemaVersion: "test.fake.response.v1",
		EndpointFingerprint:   modelruntime.SHA256Bytes([]byte("harness-sequence:endpoint")),
		CredentialRefHash:     modelruntime.SHA256Bytes([]byte("harness-sequence:credential")),
	}
}
func (a *harnessSequenceAdapter) Preflight(ctx context.Context, request modelruntime.ProviderPreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ProviderID != "test.fake" || request.ProviderModelID == "" || request.Deadline.IsZero() {
		return modelruntime.ErrInvalidRequest
	}
	return nil
}
func (a *harnessSequenceAdapter) Dispatch(_ context.Context, request modelruntime.CanonicalRequest) (modelruntime.RawResponse, error) {
	a.calls++
	response := modelruntime.RawResponse{ProviderRequestID: fmt.Sprintf("harness-%d", a.calls), InputTokens: 12, OutputTokens: 4, ProviderReported: false}
	if a.calls == 1 {
		response.ToolIntents = []modelruntime.RawToolIntent{{ID: "harness-call-1", Name: "lookup_fixture", Arguments: []byte(`{"key":"alpha"}`)}}
	} else {
		response.Content = []byte("integration final")
	}
	response.ProviderOutcome = modelruntime.ProviderOutcome{
		OutcomeClassification: modelruntime.ProviderOutcomeResponseReceived,
		ProviderRequestID:     response.ProviderRequestID, HTTPStatus: 200,
		ResponseHash: modelruntime.SHA256Bytes(response.Content), ResponseSchemaVersion: "test.fake.response.v1",
	}
	return response, nil
}

type integrationHarnessToolCatalog struct {
	definition executionharness.ToolDefinition
}

func (c integrationHarnessToolCatalog) Lookup(context.Context, string) (executionharness.ToolDefinition, bool) {
	return c.definition, true
}
func (integrationHarnessToolCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return nil
}

type integrationHarnessToolExecutor struct {
	calls       int
	beforeReply func() error
}

func (e *integrationHarnessToolExecutor) Execute(context.Context, executionharness.RunIdentity, executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	e.calls++
	if e.beforeReply != nil {
		if err := e.beforeReply(); err != nil {
			return executionharness.ToolExecutionResult{}, err
		}
	}
	return executionharness.ToolExecutionResult{Content: json.RawMessage(`{"value":"integration fixture"}`), Provenance: "postgres/integration"}, nil
}

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
	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetModelSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
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

// availableAssignee accepts every candidate. Assignee availability has its own
// coverage in the tasks package; here it would only add noise.
func availableAssignee(context.Context, taskdomain.Task) (taskdomain.AssigneeCheck, error) {
	return taskdomain.AssigneeCheck{Available: true}, nil
}

// claimModelExecutionFixture builds the Harness task through the productive
// claim path instead of inserting task_leases by hand.
//
// The lease is issued by tasks/postgres to the canonical execution principal
// through ClaimRequest.HolderPrincipalID, while the operational worker name
// stays on the attempt and on the task transition. The attempt is then started
// under the holder identity, because StartAttempt, Heartbeat and
// RecordAttemptResult all require ActorID == task_leases.holder_id.
//
// Nothing here rewrites task_leases afterwards, and nothing should: an
// out-of-band UPDATE would silently restore the implicit equality between the
// worker name and the security principal that this fixture exists to disprove.
func claimModelExecutionFixture(t *testing.T, ctx context.Context, store *platformpostgres.Store, taskDB *taskpostgres.Store, revisionID int64, roleID, workerID, holderPrincipalID, suffix string) (modelruntime.TaskAttemptRef, modelruntime.ContextSnapshotRef, string) {
	t.Helper()
	now := time.Now().UTC()
	var taskID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO tasks(organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version) VALUES($1,$2,$3,'ingenieria_ia',$4,$5,'Model runtime integration','Exercise fake one-shot dispatch.','[]','ready',0,$6,5,0,1) RETURNING id`, modelIntegrationOrganization, revisionID, roleID, "model-runtime-integration-task-"+suffix, modelruntime.SHA256Bytes([]byte("task-"+suffix)), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	rendered := []byte("safe integration context")
	var snapshotID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO context_snapshots(organization_id,organization_revision_id,actor_role_id,purpose,task_ref,idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,created_at) VALUES($1,$2,$3,'model invocation',$4,$5,$6,$7,$8,$9,'ready',1,0,0,0,0,$10) RETURNING id`, modelIntegrationOrganization, revisionID, roleID, strconv.FormatInt(taskID, 10), "model-runtime-integration-context-"+suffix, modelruntime.SHA256Bytes([]byte("context-request-"+suffix)), modelruntime.SHA256Bytes([]byte("precedence")), modelruntime.SHA256Bytes([]byte("bundle")), modelruntime.SHA256Bytes(rendered), now).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	claimed, err := taskDB.ClaimSpecific(ctx, taskID, taskdomain.ClaimRequest{
		OrganizationID:    modelIntegrationOrganization,
		WorkerID:          workerID,
		HolderPrincipalID: holderPrincipalID,
		AssignedRoleID:    roleID,
		LeaseDuration:     30 * time.Minute,
	}, availableAssignee, 10)
	if err != nil {
		t.Fatalf("productive claim: %v", err)
	}
	if claimed.Lease.HolderID != holderPrincipalID {
		t.Fatalf("lease issued to %q, want canonical execution principal %q", claimed.Lease.HolderID, holderPrincipalID)
	}
	if claimed.Attempt.WorkerID != workerID {
		t.Fatalf("attempt attributed to %q, want operational worker %q", claimed.Attempt.WorkerID, workerID)
	}
	if _, err = taskDB.StartAttempt(ctx, taskdomain.LeaseCommand{
		TaskID: taskID, AttemptID: claimed.Attempt.ID, LeaseToken: claimed.LeaseToken, ActorID: holderPrincipalID,
	}, 10); err != nil {
		t.Fatalf("start attempt under the lease holder identity: %v", err)
	}
	return modelruntime.TaskAttemptRef{TaskID: taskID, AttemptID: claimed.Attempt.ID, OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, AssignedRoleID: roleID, TaskStatus: "running", AttemptStatus: "running", LeaseHolderID: claimed.Lease.HolderID, LeaseExpiresAt: claimed.Lease.ExpiresAt}, modelruntime.ContextSnapshotRef{ID: snapshotID, OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, ActorRoleID: roleID, TaskRef: strconv.FormatInt(taskID, 10), Status: "ready", RenderedHash: modelruntime.SHA256Bytes(rendered), DataClasses: []string{"organizational"}}, claimed.LeaseToken
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
