//go:build integration

package sleep_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextbootstrap "github.com/Mireuz13/explorarte-organization/internal/contextengine/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/canonical"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisiongraphpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/executive/sleep"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragbootstrap "github.com/Mireuz13/explorarte-organization/internal/rag/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func TestApprovedSleepCandidateBecomesContextEvidenceOnlyAfterHumanGovernance(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	contextSourceRoot := writeContextSourceFixture(t)
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "24",
			"ORG_DATABASE_MIN_CONNS": "0",
			"ORG_CANONICAL_DIR":      canonicalDir,
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Context.SourceRoot = contextSourceRoot
	store, err := platformpostgres.Open(ctx, cfg.Database, "sleep-context-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := testdbguard.RequireDestructive(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err = store.Pool().Exec(ctx, `TRUNCATE organizations, organization_registry_revisions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, sleepIntegrationOrg, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if syncResult, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || !syncResult.Applied {
		t.Fatalf("sync registry: result=%+v err=%v", syncResult, syncErr)
	}
	revision, err := registryRepo.GetCurrentRevision(ctx, sleepIntegrationOrg)
	if err != nil || revision == nil {
		t.Fatalf("current registry revision=%+v err=%v", revision, err)
	}

	modelRuntime, err := modelbootstrap.OpenRegistry(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	modelSync, err := modelRuntime.Registry.Sync(ctx, true, cfg.Tasks.OutboxMaxAttempts)
	if err != nil || (!modelSync.Applied && !modelSync.NoOp) {
		t.Fatalf("sync model registry: result=%+v err=%v", modelSync, err)
	}
	binding := loadModelBinding(t, ctx, store, revision.ID, sleepIntegrationRole)

	graphStore, err := decisiongraphpostgres.New(store, sleepIntegrationOrg)
	if err != nil {
		t.Fatal(err)
	}
	graphService, err := decisiongraph.NewService(graphStore, decisiongraph.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	canonicalProvider, err := canonical.New(canonicalDir, int64(cfg.Context.MaxTotalBytes))
	if err != nil {
		t.Fatal(err)
	}
	recorder := runtimeadapter.DecisionGraph{Service: graphService, Canonical: canonicalProvider, Limits: executive.DefaultLimits(), Clock: executive.ClockFunc(time.Now)}
	for index := 0; index < 3; index++ {
		taskID, attemptID := insertObservedAttempt(t, ctx, store, revision.ID, binding, index+101, executive.CompletionPass)
		if err := recorder.RecordAttemptDecision(ctx, executive.AttemptDecisionRecord{TaskID: taskID, AttemptID: attemptID, Verdict: executive.CompletionPass, Detail: fmt.Sprintf("approved context fixture %d", index+1)}); err != nil {
			t.Fatal(err)
		}
	}

	ragRuntime, err := ragbootstrap.Open(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := sleep.NewPostgresReader(store, ragRuntime.Manager)
	if err != nil {
		t.Fatal(err)
	}
	sleepService, err := sleep.NewService(reader, ragRuntime.Manager, sleep.ClockFunc(time.Now), sleep.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := sleepService.RunCycle(ctx, sleepIntegrationOrg, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if cycle.CandidatesProposed != 1 || len(cycle.Proposals) != 1 {
		t.Fatalf("cycle=%+v", cycle)
	}
	version, err := ragRuntime.Manager.GetForRevalidation(ctx, sleepIntegrationOrg, cycle.Proposals[0].VersionID)
	if err != nil {
		t.Fatal(err)
	}
	if version.Lifecycle != rag.LifecycleCandidate {
		t.Fatalf("sleep bypassed review: lifecycle=%s", version.Lifecycle)
	}

	version = approveRAGVersion(t, ctx, ragRuntime, version)
	if version.Lifecycle != rag.LifecycleApproved {
		t.Fatalf("approved lifecycle=%s", version.Lifecycle)
	}
	generation := approveAndReindexRAG(t, ctx, ragRuntime, version.NamespaceKind, version.NamespaceID)
	if generation.Status != rag.GenerationActive {
		t.Fatalf("generation=%+v", generation)
	}

	results, err := ragRuntime.Manager.Query(ctx, rag.QueryRequest{OrganizationID: sleepIntegrationOrg, ActorRoleID: "ingenieria_ia/orquestador", Scope: rag.NamespaceDepartment, QueryText: "completion pattern", Limit: 10})
	if err != nil || len(results) == 0 {
		t.Fatalf("approved sleep knowledge not retrievable: results=%+v err=%v", results, err)
	}

	contextRuntime, err := contextbootstrap.Open(cfg, store, nil)
	if err != nil {
		t.Fatalf("open context runtime: %v", err)
	}
	build, err := contextRuntime.Service.Build(ctx, contextengine.BuildRequest{
		OrganizationID: sleepIntegrationOrg, OrganizationRevisionID: revision.ID,
		ActorRoleID: "ingenieria_ia/orquestador", Purpose: "completion pattern",
		IdempotencyKey: "sleep-approved-context-evidence",
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	found := false
	for _, segment := range build.Snapshot.Segments {
		if segment.SourceKind != contextengine.SourceRAGEvidence || !segment.Included {
			continue
		}
		found = true
		if segment.TrustClass != contextengine.TrustUntrusted || segment.InstructionClass != contextengine.InstructionData || segment.MayGrantCapabilities {
			t.Fatalf("RAG authority escalated in context: %+v", segment)
		}
		if segment.DataClass != contextengine.DataOrganizational {
			t.Fatalf("RAG data class=%s want organizational", segment.DataClass)
		}
	}
	if !found {
		t.Fatalf("context snapshot has no approved sleep RAG evidence: %+v", build.Snapshot.Segments)
	}
}

func approveRAGVersion(t *testing.T, ctx context.Context, runtime *ragbootstrap.Runtime, before rag.KnowledgeVersion) rag.KnowledgeVersion {
	t.Helper()
	actor := "empresa/human"
	reason := "sleep integration human review"
	digest := rag.ContentHash(strings.Join([]string{
		"rag-mutation.v1", before.ID, before.CanonicalHash, string(before.Lifecycle), string(rag.LifecycleApproved), fmt.Sprint(before.Revision), actor, reason,
	}, "|"))
	request, err := runtime.Authorization.RequestApproval(ctx, authorization.RequestApprovalCommand{
		ActorRoleID: actor, CapabilityID: rag.CapabilityPublish,
		ResourceType: "knowledge_version", ResourceID: before.ID, ActionDigest: digest,
		IdempotencyKey: "sleep-approve-" + before.ID, Reason: reason,
	})
	if err != nil {
		t.Fatalf("request RAG review approval: %v", err)
	}
	if _, err = runtime.Authorization.DecideRequest(ctx, authorization.DecideRequestCommand{
		RequestID: request.Request.ID, Decision: authorization.DecisionApprove,
		ActorRoleID: actor, Reason: "human approved evidence-backed sleep candidate",
	}); err != nil {
		t.Fatalf("decide RAG review approval: %v", err)
	}
	requestID := request.Request.ID
	approved, err := runtime.Manager.Review(ctx, rag.ReviewRequest{
		Mutation: rag.MutationRequest{
			OrganizationID: sleepIntegrationOrg, VersionID: before.ID, ExpectedRevision: before.Revision,
			ActorRoleID: actor, Reason: reason, ApprovalRequestID: &requestID,
		},
		Outcome: rag.ReviewApprove,
	})
	if err != nil {
		t.Fatalf("review RAG candidate: %v", err)
	}
	return approved
}

func approveAndReindexRAG(t *testing.T, ctx context.Context, runtime *ragbootstrap.Runtime, namespaceKind rag.NamespaceKind, namespaceID string) rag.IndexGeneration {
	t.Helper()
	actor := "empresa/human"
	digest := rag.ContentHash("rag-reindex.v1|" + sleepIntegrationOrg + "|" + string(namespaceKind) + "|" + namespaceID)
	request, err := runtime.Authorization.RequestApproval(ctx, authorization.RequestApprovalCommand{
		ActorRoleID: actor, CapabilityID: rag.CapabilityPublish,
		ResourceType: "knowledge_index", ResourceID: string(namespaceKind) + ":" + namespaceID,
		ActionDigest: digest, IdempotencyKey: "sleep-reindex-" + string(namespaceKind) + "-" + namespaceID,
		Reason: "index human-approved sleep knowledge",
	})
	if err != nil {
		t.Fatalf("request RAG reindex approval: %v", err)
	}
	if _, err = runtime.Authorization.DecideRequest(ctx, authorization.DecideRequestCommand{
		RequestID: request.Request.ID, Decision: authorization.DecisionApprove,
		ActorRoleID: actor, Reason: "human approved indexing sleep knowledge",
	}); err != nil {
		t.Fatalf("decide RAG reindex approval: %v", err)
	}
	requestID := request.Request.ID
	generation, err := runtime.Manager.Reindex(ctx, rag.ReindexRequest{
		OrganizationID: sleepIntegrationOrg, NamespaceKind: namespaceKind, NamespaceID: namespaceID,
		ActorRoleID: actor, ApprovalRequestID: &requestID,
	})
	if err != nil {
		t.Fatalf("reindex approved sleep knowledge: %v", err)
	}
	return generation
}
