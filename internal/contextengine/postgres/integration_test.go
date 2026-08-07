//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	contextpostgres "github.com/Mireuz13/explorarte-organization/internal/contextengine/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	stagingpostgres "github.com/Mireuz13/explorarte-organization/internal/staging/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const contextIntegrationOrganization = "explorarte"

func TestContextEnginePostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openContextStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrations through current tip: %v", err)
	}
	resetContextSchema(t, ctx, platform)

	repo, err := registry.NewPostgresRepository(platform)
	if err != nil {
		t.Fatal(err)
	}
	revision := syncContextCanonical(t, ctx, platform)
	store, err := contextpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	loader, err := contextengine.NewFilesystemCanonicalProvider(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	catalog := registryCatalogAdapter{reader: repo}
	resolver := contextengine.NewRegistryAudienceResolver(catalog)
	instructionSource := contextengine.NewFilesystemInstructionProvider(filepath.Join("..", "..", ".."), catalog)
	skillProvider, err := contextengine.NewCanonicalSkillProvider(canonicalDir, catalog)
	if err != nil {
		t.Fatal(err)
	}
	projects := &mutableProjectProvider{}
	projectContext := validProjectContext("projects/context-integration", "project-version-1", revision.ID)
	projects.Set(projectContext)
	memoryProvider := newMutableMemoryProvider()
	memoryRecord := validApprovedMemoryRecord("memory/context-integration", "memory-version-1", revision.ID)
	memoryProvider.Set(memoryRecord)
	ragProvider := newMutableRAGProvider()
	ragRecord := validApprovedRAGRecord("rag/context-integration", "rag-version-1", revision.ID)
	ragProvider.Set(ragRecord)
	service, err := contextengine.NewEngineService(store, loader, resolver, instructionSource, skillProvider, projects, memoryProvider, ragProvider, contextengine.ClockFunc(time.Now), contextengine.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("snapshot provenance tier order and deterministic rendering", func(t *testing.T) {
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "Context integration deterministic snapshot", ProjectRefs: []string{projectContext.Reference}, IdempotencyKey: "context-integration-deterministic"}
		first, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if first.Reused {
			t.Fatal("first build unexpectedly reused")
		}
		if first.Snapshot.Status != contextengine.SnapshotReady || first.Snapshot.RenderedHash == "" || len(first.Snapshot.Segments) == 0 {
			t.Fatalf("snapshot=%+v", first.Snapshot)
		}
		assertTierOrdering(t, first.Snapshot.Segments)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND source_kind='role_skill'`, first.Snapshot.ID, 1)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND source_kind='role_instructions'`, first.Snapshot.ID, 2)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND source_kind='project'`, first.Snapshot.ID, 1)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND source_kind='approved_memory'`, first.Snapshot.ID, 1)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND source_kind='rag_evidence'`, first.Snapshot.ID, 1)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND authority_tier=1 AND source_kind='instruction_precedence' AND included`, first.Snapshot.ID, 1)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND authority_tier=1 AND source_kind='organization' AND included`, first.Snapshot.ID, 1)
		for _, segment := range first.Snapshot.Segments {
			if segment.SourceKind == contextengine.SourceRoleSkill && segment.SourceVersion == "" {
				t.Fatal("included skill segment is missing source version")
			}
			if segment.SourceKind == contextengine.SourceRoleSkill && segment.InstructionClass != contextengine.InstructionTrustedSkill {
				t.Fatalf("skill instruction class=%s", segment.InstructionClass)
			}
		}

		second, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if !second.Reused || second.Snapshot.ID != first.Snapshot.ID || second.Snapshot.RenderedHash != first.Snapshot.RenderedHash {
			t.Fatalf("second=%+v first=%+v", second, first)
		}
		if first.Snapshot.RequestHash != second.Snapshot.RequestHash || first.Snapshot.PrecedenceHash != second.Snapshot.PrecedenceHash {
			t.Fatal("deterministic hashes changed")
		}
	})

	t.Run("same idempotency key with different request conflicts", func(t *testing.T) {
		base := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "idempotency", IdempotencyKey: "context-integration-conflict"}
		if _, err := service.Build(ctx, base); err != nil {
			t.Fatal(err)
		}
		base.Purpose = "idempotency changed"
		if _, err := service.Build(ctx, base); err == nil {
			t.Fatal("different request reused the same context idempotency key")
		}
	})

	t.Run("project role request and external memory rag boundaries", func(t *testing.T) {
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "tier coverage", TaskRef: "task:55", ProjectRefs: []string{projectContext.Reference}, RequestInput: []byte("request input bytes"), IdempotencyKey: "context-integration-tiers"}
		result, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[contextengine.SourceKind]int{}
		for _, segment := range result.Snapshot.Segments {
			if segment.Included {
				counts[segment.SourceKind]++
			}
			switch segment.SourceKind {
			case contextengine.SourceProject:
				if segment.AuthorityTier != contextengine.TierProjectTask || segment.InstructionClass != contextengine.InstructionTask || segment.TrustClass != contextengine.TrustTask {
					t.Fatalf("project semantics=%+v", segment)
				}
			case contextengine.SourceApprovedMemory, contextengine.SourceRAGEvidence:
				if segment.AuthorityTier != contextengine.TierMemoryAndRAG || segment.InstructionClass != contextengine.InstructionData || segment.TrustClass != contextengine.TrustUntrusted || segment.MayGrantCapabilities {
					t.Fatalf("external evidence semantics=%+v", segment)
				}
			}
		}
		if counts[contextengine.SourceProject] != 1 || counts[contextengine.SourceTask] != 1 || counts[contextengine.SourceRequest] != 1 || counts[contextengine.SourceApprovedMemory] != 1 || counts[contextengine.SourceRAGEvidence] != 1 {
			t.Fatalf("included tier counts=%+v", counts)
		}
	})

	t.Run("audience violations are denied before insertion", func(t *testing.T) {
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "investigacion/auditor_cerebro_empresa", Purpose: "observer boundary", IdempotencyKey: "context-integration-observer", ExplicitSources: []contextengine.SourceRecord{{Kind: contextengine.SourceOwnerTurn, AuthorityTier: contextengine.TierOwnerTurn, Reference: "owner-turn:forbidden", Content: []byte("owner only"), DataClass: contextengine.DataPublic, InstructionClass: contextengine.InstructionData, TrustClass: contextengine.TrustTrusted}}}
		if _, err := service.Build(ctx, request); err == nil {
			t.Fatal("observer audience violation was accepted")
		}
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_snapshots WHERE idempotency_key=$1`, request.IdempotencyKey, 0)
	})

	t.Run("excluded segment bytes are never persisted", func(t *testing.T) {
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "excluded bytes", IdempotencyKey: "context-integration-excluded", ExplicitSources: []contextengine.SourceRecord{{Kind: contextengine.SourceProject, AuthorityTier: contextengine.TierProjectTask, Reference: "project:excluded", Content: []byte("excluded project bytes"), DataClass: contextengine.DataOrganizational, InstructionClass: contextengine.InstructionData, TrustClass: contextengine.TrustProject, Audience: contextengine.AudienceDepartment, AudienceIDs: []string{"marketing"}}}}
		result, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND source_reference='project:excluded' AND included=FALSE AND content IS NULL AND segment_hash IS NULL`, result.Snapshot.ID, 1)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND source_reference='project:excluded' AND content IS NOT NULL`, result.Snapshot.ID, 0)
	})

	t.Run("no raw bearer token or canonical secret document body is persisted", func(t *testing.T) {
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "token privacy", IdempotencyKey: "context-integration-token", ExplicitSources: []contextengine.SourceRecord{{Kind: contextengine.SourceProject, AuthorityTier: contextengine.TierProjectTask, Reference: "project:token", Content: []byte("bearer-token-plaintext"), DataClass: contextengine.DataSecret, InstructionClass: contextengine.InstructionData, TrustClass: contextengine.TrustProject}}}
		result, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND content::text LIKE '%bearer-token-plaintext%'`, result.Snapshot.ID, 0)
		assertContextCount(t, ctx, platform, `SELECT count(*) FROM context_segments WHERE snapshot_id=$1 AND source_kind='security_policy' AND content IS NOT NULL`, result.Snapshot.ID, 0)
	})

	t.Run("snapshots and included segments are immutable", func(t *testing.T) {
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "immutability", IdempotencyKey: "context-integration-immutable"}
		result, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		var segmentID int64
		if err := platform.Pool().QueryRow(ctx, `SELECT id FROM context_segments WHERE snapshot_id=$1 AND included=TRUE ORDER BY precedence_rank,ordinal LIMIT 1`, result.Snapshot.ID).Scan(&segmentID); err != nil {
			t.Fatal(err)
		}
		if _, err := platform.Pool().Exec(ctx, `UPDATE context_segments SET source_reference='tampered' WHERE id=$1`, segmentID); err == nil {
			t.Fatal("included context segment accepted mutation")
		}
		if _, err := platform.Pool().Exec(ctx, `DELETE FROM context_segments WHERE id=$1`, segmentID); err == nil {
			t.Fatal("included context segment accepted delete")
		}
	})

	t.Run("staging promotion is rejected when recorded context snapshot is stale", func(t *testing.T) {
		projectFixture := validProjectContext("projects/staging-drift", "project-staging-v1", revision.ID)
		projects.Set(projectFixture)
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "staging drift", ProjectRefs: []string{projectFixture.Reference}, IdempotencyKey: "context-integration-staging-drift"}
		built, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		projects.Set(validProjectContext(projectFixture.Reference, "project-staging-v2", revision.ID))

		stagingStore, err := stagingpostgres.New(platform)
		if err != nil {
			t.Fatal(err)
		}
		workspace, err := stagingStore.CreateWorkspace(ctx, staging.PreparedWorkspace{RepositoryID: "repo-context-integration", OrganizationID: contextIntegrationOrganization, TaskID: 1, AttemptID: 1, ActorRoleID: "ingenieria_ia/code-runner", LeaseHolderID: "context-integration", LeaseTokenHash: contextengine.SHA256Bytes([]byte("lease")), BaseCommit: strings.Repeat("a", 40), TargetRef: "refs/heads/context-integration", WorkspacePath: "/tmp/context-integration/workspace", ArtifactRequirementID: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := stagingStore.RecordContextSnapshot(ctx, staging.ContextSnapshotRecord{WorkspaceID: workspace.ID, ContextSnapshotID: built.Snapshot.ID, ContextSnapshotVersion: built.Snapshot.Version, RenderedHash: built.Snapshot.RenderedHash, RecordedBy: "integration"}); err != nil {
			t.Fatal(err)
		}

		guard := stagingGuardAdapter{service: service}
		if err := guard.ValidateContextSnapshot(ctx, built.Snapshot.ID); err == nil {
			t.Fatal("stale context snapshot was accepted by the staging guard")
		}
	})

	t.Run("external source version drift is rejected by validate", func(t *testing.T) {
		projectFixture := validProjectContext("projects/external-drift", "project-ext-v1", revision.ID)
		memoryFixture := validApprovedMemoryRecord("memory/external-drift", "memory-ext-v1", revision.ID)
		ragFixture := validApprovedRAGRecord("rag/external-drift", "rag-ext-v1", revision.ID)
		projects.Set(projectFixture)
		memoryProvider.Set(memoryFixture)
		ragProvider.Set(ragFixture)
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "external source drift", ProjectRefs: []string{projectFixture.Reference}, IdempotencyKey: "context-integration-external-drift"}
		result, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		projects.Set(validProjectContext(projectFixture.Reference, "project-ext-v2", revision.ID))
		validation, err := service.Validate(ctx, result.Snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if validation.Valid || validation.ReasonCode != "source_version_stale" {
			t.Fatalf("project drift validation=%+v", validation)
		}

		projects.Set(projectFixture)
		memoryProvider.Set(validApprovedMemoryRecord(memoryFixture.Reference, "memory-ext-v2", revision.ID))
		secondRequest := request
		secondRequest.IdempotencyKey = "context-integration-memory-drift"
		second, err := service.Build(ctx, secondRequest)
		if err != nil {
			t.Fatal(err)
		}
		memoryProvider.Set(validApprovedMemoryRecord(memoryFixture.Reference, "memory-ext-v3", revision.ID))
		validation, err = service.Validate(ctx, second.Snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if validation.Valid || validation.ReasonCode != "source_version_stale" {
			t.Fatalf("memory drift validation=%+v", validation)
		}
	})

	t.Run("skill source version drift is rejected by validate", func(t *testing.T) {
		request := contextengine.BuildRequest{OrganizationID: contextIntegrationOrganization, ActorRoleID: "ingenieria_ia/orquestador", Purpose: "skill drift", IdempotencyKey: "context-integration-skill-drift"}
		result, err := service.Build(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		var skillSource contextengine.Segment
		found := false
		for _, segment := range result.Snapshot.Segments {
			if segment.SourceKind == contextengine.SourceRoleSkill && segment.Included {
				skillSource = segment
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected included skill source")
		}
		role, err := repo.GetRole(ctx, contextIntegrationOrganization, "ingenieria_ia/orquestador")
		if err != nil {
			t.Fatal(err)
		}
		capabilities := append([]string(nil), role.Capabilities...)
		needle := "imported_skills:sha256:"
		updated := false
		for index, capability := range capabilities {
			if strings.HasPrefix(capability, needle) {
				capabilities[index] = needle + strings.Repeat("0", 64)
				updated = true
				break
			}
		}
		if !updated {
			t.Fatalf("role missing imported_skills version capability: %+v", capabilities)
		}
		body, err := json.Marshal(capabilities)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := platform.Pool().Exec(ctx, `UPDATE organization_roles SET capabilities=$3::jsonb WHERE organization_id=$1 AND id=$2`, contextIntegrationOrganization, role.ID, body); err != nil {
			t.Fatal(err)
		}
		validation, err := service.Validate(ctx, result.Snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if validation.Valid || validation.ReasonCode != "source_version_stale" {
			t.Fatalf("skill drift validation=%+v source=%+v", validation, skillSource)
		}
	})

	t.Run("down migration and reapply current context stack", func(t *testing.T) {
		versions := []struct {
			version int
			file    string
		}{
			{18, "000018_make_provider_outcomes_transport_aware.down.sql"},
			{12, "000012_create_durable_decision_graph.down.sql"},
			{11, "000011_create_model_provider_adapter.down.sql"},
			{10, "000010_create_model_execution_identity.down.sql"},
			{9, "000009_create_model_dispatcher_assignments.down.sql"},
			{8, "000008_create_model_egress_authorization.down.sql"},
			{7, "000007_create_model_runtime_gateway.down.sql"},
			{6, "000006_create_context_engine.down.sql"},
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
		reapplied, upErr := runner.Up(ctx)
		if upErr != nil || len(reapplied.Applied) != 8 || reapplied.Current != 18 {
			t.Fatalf("reapply=%+v err=%v", reapplied, upErr)
		}
		var exists bool
		if err = platform.Pool().QueryRow(ctx, `SELECT to_regclass('public.context_snapshots') IS NOT NULL AND to_regclass('public.context_segments') IS NOT NULL`).Scan(&exists); err != nil || !exists {
			t.Fatalf("reapply exists=%v err=%v", exists, err)
		}
	})
}

func openContextStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 20, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "context-engine-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetContextSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `TRUNCATE context_segments, context_snapshots, staging_events, staging_reviews, staging_promotions, staging_checks, staging_workspace_artifacts, staging_artifacts, staging_workspaces, outbox_events, task_dead_letters, task_events, task_leases, task_attempts, task_evidence, task_requirements, task_dependencies, tasks, authorization_uses, authorization_decisions, authorization_requests, organization_reporting_lines, organization_registry_revision_documents, organization_roles, organizational_units, organizations, organization_registry_revisions, audit_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func syncContextCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, contextIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, contextIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func assertTierOrdering(t *testing.T, segments []contextengine.Segment) {
	t.Helper()
	last := contextengine.AuthorityTier(0)
	for _, segment := range segments {
		if segment.AuthorityTier < last {
			t.Fatalf("tier ordering regressed: previous=%d current=%d", last, segment.AuthorityTier)
		}
		last = segment.AuthorityTier
	}
}

func assertContextCount(t *testing.T, ctx context.Context, store *platformpostgres.Store, query string, arg any, want int) {
	t.Helper()
	var count int
	if err := store.Pool().QueryRow(ctx, query, arg).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("query=%q count=%d want=%d", query, count, want)
	}
}

type registryCatalogAdapter struct{ reader registry.Reader }

func (a registryCatalogAdapter) CurrentRevision(ctx context.Context, organizationID string) (contextengine.OrganizationRevision, error) {
	revision, err := a.reader.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return contextengine.OrganizationRevision{}, err
	}
	if revision == nil {
		return contextengine.OrganizationRevision{}, registry.ErrNotFound
	}
	return contextengine.OrganizationRevision{ID: revision.ID, CanonicalHash: revision.CanonicalHash, DocumentHashes: revision.DocumentHashes}, nil
}

func (a registryCatalogAdapter) GetRole(ctx context.Context, organizationID, roleID string) (contextengine.RoleView, error) {
	role, err := a.reader.GetRole(ctx, organizationID, roleID)
	if err != nil {
		return contextengine.RoleView{}, err
	}
	return contextengine.RoleView{ID: role.ID, UnitID: role.UnitID, Capabilities: append([]string(nil), role.Capabilities...), ModelPolicy: role.ModelPolicy, ToolPolicy: role.ToolPolicy, ContextPolicy: role.ContextPolicy, Enabled: role.Enabled, Executable: role.Executable, Retired: role.Retired, ProfileStatus: role.ProfileStatus}, nil
}

func (a registryCatalogAdapter) ListRoles(ctx context.Context, organizationID string) ([]contextengine.RoleView, error) {
	roles, err := a.reader.ListRoles(ctx, organizationID, registry.RoleFilter{})
	if err != nil {
		return nil, err
	}
	result := make([]contextengine.RoleView, 0, len(roles))
	for _, role := range roles {
		result = append(result, contextengine.RoleView{ID: role.ID, UnitID: role.UnitID, Capabilities: append([]string(nil), role.Capabilities...), ModelPolicy: role.ModelPolicy, ToolPolicy: role.ToolPolicy, ContextPolicy: role.ContextPolicy, Enabled: role.Enabled, Executable: role.Executable, Retired: role.Retired, ProfileStatus: role.ProfileStatus})
	}
	return result, nil
}

func (a registryCatalogAdapter) GetUnit(ctx context.Context, organizationID, unitID string) (contextengine.UnitView, error) {
	unit, err := a.reader.GetUnit(ctx, organizationID, unitID)
	if err != nil {
		return contextengine.UnitView{}, err
	}
	return contextengine.UnitView{ID: unit.ID, Kind: unit.Kind, LeaderRoleID: unit.LeaderRoleID, ReadOnly: unit.ReadOnly, Leaderless: unit.Leaderless}, nil
}

func (a registryCatalogAdapter) IsDirectReport(ctx context.Context, organizationID, supervisorRoleID, subordinateRoleID string) (bool, error) {
	lines, err := a.reader.ListReportingLines(ctx, organizationID, nil, nil)
	if err != nil {
		return false, err
	}
	for _, line := range lines {
		if line.SupervisorRoleID == supervisorRoleID && line.SubordinateRoleID == subordinateRoleID {
			return true, nil
		}
	}
	return false, nil
}

type mutableProjectProvider struct {
	values map[string]contextengine.ProjectContext
}

func (p *mutableProjectProvider) Set(value contextengine.ProjectContext) {
	if p.values == nil {
		p.values = map[string]contextengine.ProjectContext{}
	}
	p.values[value.Reference] = value
}

func (p *mutableProjectProvider) ListProjectContext(_ context.Context, organizationID, actorRoleID string, projectRefs []string) ([]contextengine.ProjectContext, error) {
	result := make([]contextengine.ProjectContext, 0, len(projectRefs))
	for _, reference := range projectRefs {
		value, ok := p.values[reference]
		if !ok || value.OrganizationID != organizationID || value.ActorRoleID != actorRoleID {
			continue
		}
		result = append(result, value)
	}
	return result, nil
}

func (p *mutableProjectProvider) ValidateVersion(_ context.Context, organizationID, actorRoleID, reference, version string) error {
	value, ok := p.values[reference]
	if !ok || value.OrganizationID != organizationID || value.ActorRoleID != actorRoleID || value.Version != version {
		return contextengine.ErrSourceVersionStale
	}
	return nil
}

type mutableMemoryProvider struct {
	values map[string]contextengine.ApprovedMemoryRecord
}

func newMutableMemoryProvider() *mutableMemoryProvider {
	return &mutableMemoryProvider{values: map[string]contextengine.ApprovedMemoryRecord{}}
}

func (p *mutableMemoryProvider) Set(value contextengine.ApprovedMemoryRecord) {
	p.values[value.Reference] = value
}

func (p *mutableMemoryProvider) ListApproved(_ context.Context, organizationID, actorRoleID string) ([]contextengine.ApprovedMemoryRecord, error) {
	result := []contextengine.ApprovedMemoryRecord{}
	for _, value := range p.values {
		if value.OrganizationID == organizationID && value.ActorRoleID == actorRoleID {
			result = append(result, value)
		}
	}
	return result, nil
}

func (p *mutableMemoryProvider) ValidateVersion(_ context.Context, organizationID, actorRoleID, reference, version string) error {
	value, ok := p.values[reference]
	if !ok || value.OrganizationID != organizationID || value.ActorRoleID != actorRoleID || value.Version != version {
		return contextengine.ErrSourceVersionStale
	}
	return nil
}

type mutableRAGProvider struct {
	values map[string]contextengine.RAGEvidenceRecord
}

func newMutableRAGProvider() *mutableRAGProvider {
	return &mutableRAGProvider{values: map[string]contextengine.RAGEvidenceRecord{}}
}

func (p *mutableRAGProvider) Set(value contextengine.RAGEvidenceRecord) {
	p.values[value.Reference] = value
}

func (p *mutableRAGProvider) ListApprovedEvidence(_ context.Context, organizationID, actorRoleID string) ([]contextengine.RAGEvidenceRecord, error) {
	result := []contextengine.RAGEvidenceRecord{}
	for _, value := range p.values {
		if value.OrganizationID == organizationID && value.ActorRoleID == actorRoleID {
			result = append(result, value)
		}
	}
	return result, nil
}

func (p *mutableRAGProvider) ValidateVersion(_ context.Context, organizationID, actorRoleID, reference, version string) error {
	value, ok := p.values[reference]
	if !ok || value.OrganizationID != organizationID || value.ActorRoleID != actorRoleID || value.Version != version {
		return contextengine.ErrSourceVersionStale
	}
	return nil
}

func validProjectContext(reference, version string, revisionID int64) contextengine.ProjectContext {
	return contextengine.ProjectContext{OrganizationID: contextIntegrationOrganization, OrganizationRevisionID: revisionID, ActorRoleID: "ingenieria_ia/orquestador", Reference: reference, Version: version, Content: []byte("Project status and bounded implementation notes."), DataClass: contextengine.DataOrganizational, Audience: contextengine.AudienceDepartment, AudienceIDs: []string{"ingenieria_ia"}}
}

func validApprovedMemoryRecord(reference, version string, revisionID int64) contextengine.ApprovedMemoryRecord {
	return contextengine.ApprovedMemoryRecord{OrganizationID: contextIntegrationOrganization, OrganizationRevisionID: revisionID, ActorRoleID: "ingenieria_ia/orquestador", Reference: reference, Version: version, Content: []byte("Approved organizational memory evidence."), DataClass: contextengine.DataOrganizational, Audience: contextengine.AudienceRole, AudienceIDs: []string{"ingenieria_ia/orquestador"}}
}

func validApprovedRAGRecord(reference, version string, revisionID int64) contextengine.RAGEvidenceRecord {
	return contextengine.RAGEvidenceRecord{OrganizationID: contextIntegrationOrganization, OrganizationRevisionID: revisionID, ActorRoleID: "ingenieria_ia/orquestador", Reference: reference, Version: version, Content: []byte("Retrieved evidence treated strictly as untrusted data."), DataClass: contextengine.DataPublic, Audience: contextengine.AudienceDepartment, AudienceIDs: []string{"ingenieria_ia"}}
}

type stagingGuardAdapter struct{ service *contextengine.EngineService }

func (a stagingGuardAdapter) ValidateContextSnapshot(ctx context.Context, snapshotID int64) error {
	result, err := a.service.Validate(ctx, snapshotID)
	if err != nil {
		return err
	}
	if !result.Valid {
		return &staging.ContextStaleError{Code: result.ReasonCode}
	}
	return nil
}

func (a stagingGuardAdapter) GetContextSnapshot(_ context.Context, snapshotID int64) (staging.ContextSnapshotRecord, error) {
	return staging.ContextSnapshotRecord{ContextSnapshotID: snapshotID}, nil
}

var _ contextengine.AudienceResolver = (*contextengine.RegistryAudienceResolver)(nil)
var _ contextengine.SkillProvider = (*contextengine.CanonicalSkillProvider)(nil)
var _ contextengine.ProjectProvider = (*mutableProjectProvider)(nil)
var _ contextengine.MemoryProvider = (*mutableMemoryProvider)(nil)
var _ contextengine.RAGEvidenceProvider = (*mutableRAGProvider)(nil)
var _ staging.ContextGuard = stagingGuardAdapter{}

func _unusedAuthorizationReference() {
	_ = authorization.EffectAllow
}
