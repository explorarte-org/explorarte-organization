//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	contextenginepostgres "github.com/Mireuz13/explorarte-organization/internal/contextengine/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// TestContextTokenTelemetryPostgreSQL17 is M1.2's dedicated Postgres
// integration coverage (section 22). It is deliberately self-contained
// rather than appended to TestModelRuntimeGatewayPostgreSQL17: M1.2's
// write/read paths only need a real invocation row, a real durable
// ExecutionContextView, and the registry sync that provides a real model
// profile version -- not egress evaluation, execution identity challenges,
// or the cost gate -- so this builds its own minimal fixtures directly
// through canonical store APIs and the existing insertModelExecutionFixture
// helper (same package as integration_test.go), never a parallel
// simulation of the full dispatch flow those other suites already
// exercise (section 22.I permits exactly this when the existing heavy
// fixture is too heavy for what is under test).
func TestContextTokenTelemetryPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	platform := openModelStore(t, ctx)
	defer platform.Close()

	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("migration applies from tip, contiguously, and down/up reproduces it", func(t *testing.T) {
		if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), platform.Pool()); err != nil {
			t.Fatalf("refusing destructive migration down: %v", err)
		}
		applied, err := runner.Up(ctx)
		if err != nil {
			t.Fatalf("up: %v", err)
		}
		loadedForTip, tipErr := platformmigrations.Load(rootmigrations.Files)
		if tipErr != nil {
			t.Fatal(tipErr)
		}
		realTip := loadedForTip[len(loadedForTip)-1].Version
		if applied.Current != realTip {
			t.Fatalf("migration tip after up=%d, want %d (the real repo tip)", applied.Current, realTip)
		}
		hasColumn := func() bool {
			var exists bool
			if err := platform.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='model_invocation_render_telemetry' AND column_name='execution_context_view_id')`).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			return exists
		}
		if !hasColumn() {
			t.Fatal("migration 000052 did not add execution_context_view_id")
		}
		down, readErr := rootmigrations.Files.ReadFile("000052_add_context_token_telemetry.down.sql")
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := platform.Pool().Exec(ctx, string(down)); execErr != nil {
			t.Fatalf("down migration 000052: %v", execErr)
		}
		if _, execErr := platform.Pool().Exec(ctx, `DELETE FROM schema_migrations WHERE version=52`); execErr != nil {
			t.Fatal(execErr)
		}
		if hasColumn() {
			t.Fatal("down migration did not remove execution_context_view_id")
		}
		reapplied, err := runner.Up(ctx)
		if err != nil || len(reapplied.Applied) != 1 || reapplied.Current != realTip {
			t.Fatalf("reapply=%+v err=%v want current=%d", reapplied, err, realTip)
		}
		if !hasColumn() {
			t.Fatal("reapplied up migration did not restore execution_context_view_id")
		}
	})

	resetModelSchema(t, ctx, platform)
	syncModelCanonical(t, ctx, platform)

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
	catalog := catalogFixture{
		organization: modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: revision.ID, ModelRoutingHash: revision.DocumentHashes["model-routing.yaml"], ModelEgressPolicyHash: revision.DocumentHashes["model-egress-policy.yaml"], CapabilityMatrixHash: revision.DocumentHashes["capability-matrix.yaml"]},
		roles:        convertRoles(roles),
	}

	modelStore, err := modelpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	ecvStore, err := contextcompilerpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	contextSnapshotStore, err := contextenginepostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	modelStore.SetContextSnapshotReader(contextSnapshotStore)

	registrySvc, err := modelruntime.NewRegistryService(filepath.Join("..", "..", "..", "docs", "canonical"), modelIntegrationOrganization, catalog, modelStore)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registrySvc.Sync(ctx, true, 10); err != nil {
		t.Fatalf("sync compiled provider/profile availability: %v", err)
	}

	var profileID, providerID, providerModelID string
	var profileVersionID int64
	if err := platform.Pool().QueryRow(ctx, `SELECT id, profile_id, provider_id, provider_model_id FROM model_profile_versions WHERE organization_revision_id=$1 AND dispatch_enabled LIMIT 1`, revision.ID).Scan(&profileVersionID, &profileID, &providerID, &providerModelID); err != nil {
		t.Fatalf("no dispatch-enabled model profile version to build fixtures from: %v", err)
	}

	// newInvocation builds one real, minimal model_invocations row bound to
	// a fresh real context snapshot row (via insertModelExecutionFixture),
	// satisfying every FK model_invocations itself declares -- without
	// going through the full CreateInvocation/egress/identity pipeline,
	// which is irrelevant to what M1.2's telemetry write/read paths do.
	newInvocation := func(t *testing.T, suffix string) (invocationID, snapshotID int64) {
		t.Helper()
		taskRef, snapshotRef := insertModelExecutionFixture(t, ctx, platform, revision.ID, "ingenieria_ia/code-runner", suffix)
		now := time.Now().UTC()
		if err := platform.Pool().QueryRow(ctx, `
INSERT INTO model_invocations(
    organization_id, organization_revision_id, task_id, attempt_id,
    dispatch_actor_role_id, subject_role_id, context_snapshot_id, purpose,
    model_profile_id, model_profile_version_id, provider_id, provider_model_id,
    output_mode, max_output_tokens, thinking_mode, idempotency_key, request_hash, status, deadline
) VALUES ($1,$2,$3,$4,'ingenieria_ia/code-runner','ingenieria_ia/code-runner',$5,'token telemetry integration',
    $6,$7,$8,$9,'text',256,'disabled',$10,$11,'requested',$12
) RETURNING id`,
			modelIntegrationOrganization, revision.ID, taskRef.TaskID, taskRef.AttemptID, snapshotRef.ID,
			profileID, profileVersionID, providerID, providerModelID,
			"m12-token-telemetry-"+suffix, modelruntime.SHA256Bytes([]byte("m12-request-"+suffix)), now.Add(30*time.Minute),
		).Scan(&invocationID); err != nil {
			t.Fatalf("insert model_invocations: %v", err)
		}
		return invocationID, snapshotRef.ID
	}

	// persistView builds and durably persists a minimal, valid
	// ExecutionContextView bound to snapshotID through the real M1.1
	// canonical store API (contextcompiler/postgres.Store.Persist) -- the
	// same store productive Executive/Model Runtime code uses -- rather
	// than reimplementing its integrity/CHECK contract by hand.
	persistView := func(t *testing.T, snapshotID int64, payload string) contextcompiler.ExecutionContextView {
		t.Helper()
		bytes := []byte(payload)
		view := contextcompiler.ExecutionContextView{
			OrganizationID: modelIntegrationOrganization, ContextSnapshotID: snapshotID,
			ContextProfileID: "research.corpus_curate", ContextProfileVersion: "v1",
			FellBackToCanonical: false, SelectionKind: contextcompiler.SelectionTaskClass, SelectorAlgorithmVersion: contextcompiler.SelectorAlgorithmVersion,
			ProviderRenderVersion: "research-corpus-curate-render/v2",
			StablePrefixHash:      modelruntime.SHA256Bytes([]byte("stable")), StablePrefixBytes: 30,
			DynamicSuffixHash: modelruntime.SHA256Bytes([]byte("dynamic")), DynamicSuffixBytes: 15,
			AuthorityOrderHash: modelruntime.SHA256Bytes([]byte("order")), CompiledContentHash: modelruntime.SHA256Bytes([]byte("content")),
			// A non-fallback (FellBackToCanonical=false) view must carry
			// internally coherent SegmentDiffs, never an empty array
			// merely for fixture convenience -- an empty SegmentDiffs
			// array is only ever valid for a true canonical fallback (see
			// contextcompiler.attributeSegmentTokenEstimates).
			SegmentDiffs:         []contextcompiler.SegmentDiff{{SourceReference: "docs/canonical/role-catalog.yaml", AuthorityTier: "approved_skill", OriginalBytes: 300, ProjectedBytes: 120, Projected: true, Reason: "projected_subset:role_catalog_self_entry"}},
			ProviderVisibleBytes: bytes, ProviderVisibleDigest: modelruntime.SHA256Bytes(bytes), ProviderVisibleByteCount: len(bytes),
		}
		persisted, err := ecvStore.Persist(ctx, view)
		if err != nil {
			t.Fatalf("persist execution context view: %v", err)
		}
		return persisted
	}

	baseTelemetry := func(view contextcompiler.ExecutionContextView) modelruntime.ContextTokenTelemetry {
		return modelruntime.ContextTokenTelemetry{
			ExecutionContextViewID: view.ID, ContextSnapshotID: view.ContextSnapshotID,
			ContextProfileID: view.ContextProfileID, ContextProfileVersion: view.ContextProfileVersion,
			EstimatorID: contextcompiler.EstimatorID, EstimatorVersion: contextcompiler.EstimatorVersion,
			ProviderVisibleBytes: view.ProviderVisibleByteCount, EstimatedProviderVisibleTokens: contextcompiler.EstimateTokens(view.ProviderVisibleByteCount),
			StablePrefixBytes: view.StablePrefixBytes, EstimatedStablePrefixTokens: contextcompiler.EstimateTokens(view.StablePrefixBytes),
			DynamicSuffixBytes: view.DynamicSuffixBytes, EstimatedDynamicSuffixTokens: contextcompiler.EstimateTokens(view.DynamicSuffixBytes),
			SegmentTokenEstimates: []modelruntime.SegmentTokenEstimate{
				{SourceReference: "docs/canonical/role-catalog.yaml", AuthorityTier: "approved_skill", OriginalBytes: 300, DeliveredBytes: 120, OriginalEstimatedTokens: contextcompiler.EstimateTokens(300), DeliveredEstimatedTokens: contextcompiler.EstimateTokens(120), Projected: true, ProjectionReason: "projected_subset:role_catalog_self_entry"},
			},
		}
	}

	// recordBaseR10_4 writes the R10.4 base row RecordContextTokenTelemetry
	// layers onto -- M1.2 telemetry is additive to that existing row, not a
	// replacement for it (section 10).
	recordBaseR10_4 := func(t *testing.T, invocationID int64) {
		t.Helper()
		if err := modelStore.RecordProviderRenderTelemetry(ctx, invocationID, modelruntime.ProviderRenderTelemetry{
			Version: "research-corpus-curate-render/v2", FallbackToLegacy: false,
			StablePrefixHash: "s", StablePrefixBytes: 30, DynamicSuffixHash: "d", DynamicSuffixBytes: 15,
			ProviderRenderHash: "h", ProviderVisibleBytes: 45,
		}); err != nil {
			t.Fatalf("record base R10.4 telemetry: %v", err)
		}
	}

	t.Run("invocation to view binding is proven and durable", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "binding")
		view := persistView(t, snapshotID, "invocation to view binding payload")
		recordBaseR10_4(t, invocationID)
		telemetry := baseTelemetry(view)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, telemetry); err != nil {
			t.Fatalf("record context token telemetry: %v", err)
		}
		var storedViewID int64
		if err := platform.Pool().QueryRow(ctx, `SELECT execution_context_view_id FROM model_invocation_render_telemetry WHERE invocation_id=$1`, invocationID).Scan(&storedViewID); err != nil {
			t.Fatal(err)
		}
		if storedViewID != view.ID {
			t.Fatalf("stored execution_context_view_id=%d, want %d", storedViewID, view.ID)
		}
		var invocationSnapshot, viewSnapshot int64
		if err := platform.Pool().QueryRow(ctx, `SELECT context_snapshot_id FROM model_invocations WHERE id=$1`, invocationID).Scan(&invocationSnapshot); err != nil {
			t.Fatal(err)
		}
		if err := platform.Pool().QueryRow(ctx, `SELECT context_snapshot_id FROM execution_context_views WHERE id=$1`, storedViewID).Scan(&viewSnapshot); err != nil {
			t.Fatal(err)
		}
		if invocationSnapshot != viewSnapshot {
			t.Fatalf("stored view does not belong to the invocation's own context snapshot: invocation=%d view=%d", invocationSnapshot, viewSnapshot)
		}
	})

	t.Run("wrong view is refused and never persisted", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "wrong-view")
		_ = persistView(t, snapshotID, "the invocation's own view payload")
		_, otherSnapshotID := newInvocation(t, "wrong-view-other")
		otherView := persistView(t, otherSnapshotID, "a completely different snapshot's view payload")
		recordBaseR10_4(t, invocationID)

		mismatched := baseTelemetry(otherView) // otherView belongs to a different context snapshot
		err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, mismatched)
		if !errors.Is(err, modelruntime.ErrContextTokenTelemetryBindingMismatch) {
			t.Fatalf("want ErrContextTokenTelemetryBindingMismatch, got %v", err)
		}
		var storedViewID *int64
		if err := platform.Pool().QueryRow(ctx, `SELECT execution_context_view_id FROM model_invocation_render_telemetry WHERE invocation_id=$1`, invocationID).Scan(&storedViewID); err != nil {
			t.Fatal(err)
		}
		if storedViewID != nil {
			t.Fatalf("a mismatched binding must never be persisted, got execution_context_view_id=%v", *storedViewID)
		}
	})

	t.Run("identical telemetry twice is idempotent, no duplicate, no corruption", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "idempotent")
		view := persistView(t, snapshotID, "idempotent telemetry payload")
		recordBaseR10_4(t, invocationID)
		telemetry := baseTelemetry(view)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, telemetry); err != nil {
			t.Fatalf("first record: %v", err)
		}
		// A structurally distinct but logically identical copy, to prove
		// the comparison is by value.
		second := telemetry
		second.SegmentTokenEstimates = append([]modelruntime.SegmentTokenEstimate(nil), telemetry.SegmentTokenEstimates...)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, second); err != nil {
			t.Fatalf("second identical record must be idempotent, got: %v", err)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_render_telemetry WHERE invocation_id=$1`, invocationID, 1)
	})

	t.Run("contradictory telemetry never overwrites the original row", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "contradiction")
		view := persistView(t, snapshotID, "contradiction telemetry payload")
		recordBaseR10_4(t, invocationID)
		telemetry := baseTelemetry(view)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, telemetry); err != nil {
			t.Fatalf("first record: %v", err)
		}
		contradictory := telemetry
		contradictory.EstimatedProviderVisibleTokens = telemetry.EstimatedProviderVisibleTokens + 999
		err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, contradictory)
		if !errors.Is(err, modelruntime.ErrContextTokenTelemetryContradiction) {
			t.Fatalf("want ErrContextTokenTelemetryContradiction, got %v", err)
		}
		var storedTokens int64
		if err := platform.Pool().QueryRow(ctx, `SELECT estimated_provider_visible_tokens FROM model_invocation_render_telemetry WHERE invocation_id=$1`, invocationID).Scan(&storedTokens); err != nil {
			t.Fatal(err)
		}
		if storedTokens != telemetry.EstimatedProviderVisibleTokens {
			t.Fatalf("contradictory attempt corrupted the original row: stored=%d want=%d", storedTokens, telemetry.EstimatedProviderVisibleTokens)
		}
	})

	t.Run("R10.4-only row never fabricates M1.2 telemetry", func(t *testing.T) {
		invocationID, _ := newInvocation(t, "r10-4-only")
		// Only the R10.4 base row is written -- M1.2's own columns
		// (execution_context_view_id, token_estimator_id, ...) are left
		// NULL, exactly as a historical pre-000052 row or a best-effort
		// M1.2 write failure would leave them. This must never read back
		// as a fake zero-estimate/empty-estimator record.
		recordBaseR10_4(t, invocationID)
		_, err := modelStore.GetContextExecutionTelemetry(ctx, modelIntegrationOrganization, invocationID)
		if !errors.Is(err, modelruntime.ErrNotFound) {
			t.Fatalf("want ErrNotFound for an R10.4-only row with no M1.2 telemetry, got %v", err)
		}
	})

	t.Run("R10.4-only row plus actual usage still does not fabricate M1.2 telemetry", func(t *testing.T) {
		invocationID, _ := newInvocation(t, "r10-4-only-with-usage")
		recordBaseR10_4(t, invocationID)
		insertDispatchAttemptAndUsage(t, ctx, platform, invocationID, usageFixture{
			inputTokens: 60, outputTokens: 12, providerReported: true,
		})
		// Provider usage existing independently must never be treated as
		// proof that Context Assembly telemetry exists too -- the two are
		// recorded by different, independent best-effort writers.
		_, err := modelStore.GetContextExecutionTelemetry(ctx, modelIntegrationOrganization, invocationID)
		if !errors.Is(err, modelruntime.ErrNotFound) {
			t.Fatalf("want ErrNotFound even though provider usage exists, got %v", err)
		}
	})

	t.Run("combined read model binds estimate and actual provider usage under distinct field names", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "usage-success")
		view := persistView(t, snapshotID, "usage success telemetry payload")
		recordBaseR10_4(t, invocationID)
		telemetry := baseTelemetry(view)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, telemetry); err != nil {
			t.Fatal(err)
		}
		insertDispatchAttemptAndUsage(t, ctx, platform, invocationID, usageFixture{
			inputTokens: 500, outputTokens: 80, providerReported: true,
			cacheHit: int64Ptr(120), cacheMiss: int64Ptr(380),
		})

		result, err := modelStore.GetContextExecutionTelemetry(ctx, modelIntegrationOrganization, invocationID)
		if err != nil {
			t.Fatal(err)
		}
		if result.ExecutionContextViewID != view.ID || result.EstimatorID != contextcompiler.EstimatorID {
			t.Fatalf("estimate half of the read model is wrong: %+v", result)
		}
		if result.ContextProfileID != "research.corpus_curate" || result.ContextProfileVersion != "v1" {
			t.Fatalf("profile identity not read from the durable ECV: %+v", result)
		}
		if result.StablePrefixHash != "s" || result.StablePrefixBytes != 30 {
			t.Fatalf("stable prefix hash/bytes not read from the R10.4 columns: hash=%q bytes=%d", result.StablePrefixHash, result.StablePrefixBytes)
		}
		if result.ActualProviderInputTokens == nil || *result.ActualProviderInputTokens != 500 {
			t.Fatalf("actual provider input tokens = %v, want 500", result.ActualProviderInputTokens)
		}
		if result.ActualProviderOutputTokens == nil || *result.ActualProviderOutputTokens != 80 {
			t.Fatalf("actual provider output tokens = %v, want 80", result.ActualProviderOutputTokens)
		}
		if result.EstimatedProviderVisibleTokens == *result.ActualProviderInputTokens {
			// Not a hard invariant in general, but this fixture's fake
			// estimate and fake usage are deliberately different values --
			// if they ever collided here it would suggest the two are
			// silently being read from the same column.
			t.Fatalf("estimated and actual token fields collapsed to the same value: %d", result.EstimatedProviderVisibleTokens)
		}
		if result.PromptCacheHitTokens == nil || *result.PromptCacheHitTokens != 120 || result.PromptCacheMissTokens == nil || *result.PromptCacheMissTokens != 380 {
			t.Fatalf("cache tokens not preserved: hit=%v miss=%v", result.PromptCacheHitTokens, result.PromptCacheMissTokens)
		}
	})

	t.Run("usage absent leaves actual fields nil, never zero", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "usage-absent")
		view := persistView(t, snapshotID, "usage absent telemetry payload")
		recordBaseR10_4(t, invocationID)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, baseTelemetry(view)); err != nil {
			t.Fatal(err)
		}
		result, err := modelStore.GetContextExecutionTelemetry(ctx, modelIntegrationOrganization, invocationID)
		if err != nil {
			t.Fatal(err)
		}
		if result.ActualProviderInputTokens != nil || result.ActualProviderOutputTokens != nil || result.ActualProviderTotalTokens != nil || result.ProviderReported != nil {
			t.Fatalf("no usage row exists; actual provider fields must stay nil, got %+v", result)
		}
	})

	t.Run("cache hit/miss nullability survives NULL, explicit zero, and positive value", func(t *testing.T) {
		cases := []struct {
			name      string
			hit, miss *int64
		}{
			{"null", nil, nil},
			{"explicit zero", int64Ptr(0), int64Ptr(0)},
			{"positive", int64Ptr(42), int64Ptr(958)},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				invocationID, snapshotID := newInvocation(t, "cache-"+c.name)
				view := persistView(t, snapshotID, "cache nullability payload "+c.name)
				recordBaseR10_4(t, invocationID)
				if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, baseTelemetry(view)); err != nil {
					t.Fatal(err)
				}
				insertDispatchAttemptAndUsage(t, ctx, platform, invocationID, usageFixture{
					inputTokens: 10, outputTokens: 5, providerReported: true, cacheHit: c.hit, cacheMiss: c.miss,
				})
				result, err := modelStore.GetContextExecutionTelemetry(ctx, modelIntegrationOrganization, invocationID)
				if err != nil {
					t.Fatal(err)
				}
				if !int64PtrEqual(result.PromptCacheHitTokens, c.hit) || !int64PtrEqual(result.PromptCacheMissTokens, c.miss) {
					t.Fatalf("cache nullability not preserved for %s: hit=%v miss=%v", c.name, result.PromptCacheHitTokens, result.PromptCacheMissTokens)
				}
			})
		}
	})

	t.Run("recovered failure usage is still visible through the combined read model", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "recovered-failure")
		view := persistView(t, snapshotID, "recovered failure telemetry payload")
		recordBaseR10_4(t, invocationID)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, baseTelemetry(view)); err != nil {
			t.Fatal(err)
		}
		// Mark the invocation as a business failure (status flips), while
		// its provider usage row -- recovered from the response envelope
		// even though the call failed, exactly like
		// dispatch_service.go's recoveredUsage -- is what the combined
		// read model must still surface.
		if _, err := platform.Pool().Exec(ctx, `UPDATE model_invocations SET status='failed', terminal_at=now() WHERE id=$1`, invocationID); err != nil {
			t.Fatal(err)
		}
		insertDispatchAttemptAndUsage(t, ctx, platform, invocationID, usageFixture{
			inputTokens: 200, outputTokens: 0, providerReported: true,
		})
		result, err := modelStore.GetContextExecutionTelemetry(ctx, modelIntegrationOrganization, invocationID)
		if err != nil {
			t.Fatal(err)
		}
		if result.ActualProviderInputTokens == nil || *result.ActualProviderInputTokens != 200 {
			t.Fatalf("recovered failure usage not visible through the read model: %+v", result)
		}
	})

	t.Run("cross-organization read is refused", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "cross-org")
		view := persistView(t, snapshotID, "cross-org telemetry payload")
		recordBaseR10_4(t, invocationID)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, baseTelemetry(view)); err != nil {
			t.Fatal(err)
		}
		_, err := modelStore.GetContextExecutionTelemetry(ctx, "a-different-organization", invocationID)
		if !errors.Is(err, modelruntime.ErrNotFound) {
			t.Fatalf("want ErrNotFound for a foreign organization, got %v", err)
		}
	})

	t.Run("restart/reload reproduces identity, estimate, segments, and usage", func(t *testing.T) {
		invocationID, snapshotID := newInvocation(t, "restart-reload")
		view := persistView(t, snapshotID, "restart reload telemetry payload")
		recordBaseR10_4(t, invocationID)
		telemetry := baseTelemetry(view)
		if err := modelStore.RecordContextTokenTelemetry(ctx, invocationID, telemetry); err != nil {
			t.Fatal(err)
		}
		insertDispatchAttemptAndUsage(t, ctx, platform, invocationID, usageFixture{
			inputTokens: 77, outputTokens: 3, providerReported: true, cacheHit: int64Ptr(1), cacheMiss: int64Ptr(76),
		})

		freshStore, err := modelpostgres.New(platform)
		if err != nil {
			t.Fatal(err)
		}
		freshStore.SetContextSnapshotReader(contextSnapshotStore)
		reloaded, err := freshStore.GetContextExecutionTelemetry(ctx, modelIntegrationOrganization, invocationID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.ExecutionContextViewID != view.ID {
			t.Fatalf("reloaded view identity diverged: %d != %d", reloaded.ExecutionContextViewID, view.ID)
		}
		if reloaded.ContextProfileID != view.ContextProfileID || reloaded.ContextProfileVersion != view.ContextProfileVersion {
			t.Fatalf("reloaded profile identity diverged: %+v", reloaded)
		}
		if reloaded.EstimatorID != telemetry.EstimatorID || reloaded.EstimatorVersion != telemetry.EstimatorVersion {
			t.Fatalf("reloaded estimator identity diverged: %+v", reloaded)
		}
		if reloaded.StablePrefixHash != "s" || reloaded.StablePrefixBytes != 30 {
			t.Fatalf("reloaded stable prefix hash/bytes diverged: hash=%q bytes=%d", reloaded.StablePrefixHash, reloaded.StablePrefixBytes)
		}
		if reloaded.EstimatedProviderVisibleTokens != telemetry.EstimatedProviderVisibleTokens {
			t.Fatalf("reloaded estimate diverged: %d != %d", reloaded.EstimatedProviderVisibleTokens, telemetry.EstimatedProviderVisibleTokens)
		}
		if !reflect.DeepEqual(reloaded.SegmentTokenEstimates, telemetry.SegmentTokenEstimates) {
			t.Fatalf("reloaded segment estimates diverged: %+v != %+v", reloaded.SegmentTokenEstimates, telemetry.SegmentTokenEstimates)
		}
		if reloaded.ActualProviderInputTokens == nil || *reloaded.ActualProviderInputTokens != 77 || reloaded.PromptCacheHitTokens == nil || *reloaded.PromptCacheHitTokens != 1 {
			t.Fatalf("reloaded usage/cache diverged: %+v", reloaded)
		}
	})
}

type usageFixture struct {
	inputTokens, outputTokens int64
	providerReported          bool
	cacheHit, cacheMiss       *int64
}

// insertDispatchAttemptAndUsage inserts the minimal completed
// model_dispatch_attempts row and its model_invocation_usage row a real
// successful (or recovered-failure) dispatch would have produced, directly
// via SQL -- the full DispatchService state machine is exercised by other
// suites and is not what M1.2's read model is testing.
func insertDispatchAttemptAndUsage(t *testing.T, ctx context.Context, platform *platformpostgres.Store, invocationID int64, usage usageFixture) {
	t.Helper()
	now := time.Now().UTC()
	var attemptID int64
	if err := platform.Pool().QueryRow(ctx, `
INSERT INTO model_dispatch_attempts(
    invocation_id, attempt_number, status, claim_token_hash, claimed_by,
    claimed_at, claim_expires_at, send_started_at, response_received_at,
    provider_idempotency_key_hash, retry_safety, finished_at
) VALUES ($1,1,'completed',$2,'integration-worker',$3,$4,$3,$3,$5,'not_retryable',$3)
RETURNING id`,
		invocationID, modelruntime.SHA256Bytes([]byte("claim-token")), now, now.Add(20*time.Minute), modelruntime.SHA256Bytes([]byte("idempotency")),
	).Scan(&attemptID); err != nil {
		t.Fatalf("insert model_dispatch_attempts: %v", err)
	}
	if _, err := platform.Pool().Exec(ctx, `
INSERT INTO model_invocation_usage(
    invocation_id, dispatch_attempt_id, input_tokens, output_tokens, total_tokens,
    provider_reported, prompt_cache_hit_tokens, prompt_cache_miss_tokens
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		invocationID, attemptID, usage.inputTokens, usage.outputTokens, usage.inputTokens+usage.outputTokens,
		usage.providerReported, usage.cacheHit, usage.cacheMiss,
	); err != nil {
		t.Fatalf("insert model_invocation_usage: %v", err)
	}
}

func int64Ptr(v int64) *int64 { return &v }

func int64PtrEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
