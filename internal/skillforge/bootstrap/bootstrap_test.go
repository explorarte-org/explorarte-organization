package bootstrap_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	skillregistrypostgres "github.com/Mireuz13/explorarte-organization/internal/skillregistry/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
)

type fakeAuthority struct{}

func (fakeAuthority) AuthorizeExecution(context.Context, executionharness.AuthorityRequest) error {
	return nil
}

type fakeModelExecutor struct{}

func (fakeModelExecutor) Invoke(context.Context, executionharness.RunIdentity, executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	return executionharness.ModelResult{FinishReason: executionharness.FinishFinal}, nil
}

type fakeCatalog struct{}

func (fakeCatalog) Lookup(context.Context, string) (executionharness.ToolDefinition, bool) {
	return executionharness.ToolDefinition{}, false
}

func (fakeCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return nil
}

type fakeTools struct{}

func (fakeTools) Execute(context.Context, executionharness.RunIdentity, executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	return executionharness.ToolExecutionResult{}, nil
}

type noopGate struct{}

func (noopGate) AuthorizeProposal(_ context.Context, _, _, _ string) (skillregistry.GovernanceEvidence, error) {
	return skillregistry.GovernanceEvidence{DecisionRef: "dec:prop", ActorRoleID: "empresa/human", DecidedAt: time.Now()}, nil
}

func (noopGate) AuthorizeLifecycleChange(_ context.Context, _, _, _ string, _, _ skillregistry.Lifecycle) (skillregistry.GovernanceEvidence, error) {
	return skillregistry.GovernanceEvidence{DecisionRef: "dec:life", ActorRoleID: "empresa/human", DecidedAt: time.Now()}, nil
}

func (noopGate) AuthorizeAssignmentChange(_ context.Context, _, _, _, _, _ string) (skillregistry.GovernanceEvidence, error) {
	return skillregistry.GovernanceEvidence{DecisionRef: "dec:assign", ActorRoleID: "empresa/human", DecidedAt: time.Now()}, nil
}

func TestBootstrapOpenValidation(t *testing.T) {
	// Missing organization ID fails
	_, err := bootstrap.Open(bootstrap.Config{}, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing organization ID")
	}

	// Missing PostgreSQL store fails
	_, err = bootstrap.Open(bootstrap.Config{OrganizationID: "explorarte"}, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil PostgreSQL store")
	}
}

func TestBootstrapOpenProductiveComposition(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required for bootstrap composition test")
	}

	ctx := context.Background()
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "4",
			"ORG_DATABASE_MIN_CONNS": "0",
		}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}

	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "skillforge-bootstrap-test")
	if err != nil {
		t.Fatal(err)
	}
	defer platformStore.Close()

	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, platformStore.Pool()); err != nil {
		t.Fatalf("testdbguard: %v", err)
	}

	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	if _, err := os.Stat(canonicalDir); os.IsNotExist(err) {
		canonicalDir = filepath.Join("docs", "canonical")
	}

	repoDir := t.TempDir()
	materializeDir := t.TempDir()

	// Fail-closed check: enabled=true requires harnesses and skillManager
	bCfg := bootstrap.Config{
		OrganizationID:      "explorarte",
		CanonicalDir:        canonicalDir,
		SkillSourceRepoRoot: repoDir,
		SkillRuntimeRoot:    materializeDir,
		SourceRepositoryDir: repoDir,
		MaterializeRootDir:  materializeDir,
		PublishedRemoteURL:  "git@github.com:explorarte-org/skills.git",
		Enabled:             true,
	}
	_, err = bootstrap.Open(bCfg, platformStore, nil, nil, nil)
	if err == nil {
		t.Fatal("expected fail-closed error when Enabled=true without harnesses")
	}

	// Now provide minimal mock harnesses and manager to prove complete composition root wiring
	harness, err := executionharness.New(fakeAuthority{}, fakeModelExecutor{}, fakeCatalog{}, fakeTools{}, executionharness.NewMemoryHistoryStore())
	if err != nil {
		t.Fatal(err)
	}
	registryStore, err := skillregistrypostgres.New(platformStore, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := skillregistry.NewManager(skillregistry.NewService(nil), registryStore, noopGate{})
	if err != nil {
		t.Fatal(err)
	}

	runtime, err := bootstrap.Open(bCfg, platformStore, harness, harness, manager)
	if err != nil {
		t.Fatalf("bootstrap.Open failed: %v", err)
	}

	// Verify all components in composition root are wired
	if runtime.Engine == nil {
		t.Fatal("expected Engine in composition root")
	}
	if runtime.NeedStore == nil {
		t.Fatal("expected NeedStore in composition root")
	}
	if runtime.RegistryStore == nil {
		t.Fatal("expected RegistryStore in composition root")
	}
	if runtime.Publisher == nil {
		t.Fatal("expected Publisher in composition root")
	}
	if runtime.PinnedReader == nil {
		t.Fatal("expected PinnedReader in composition root")
	}
	if runtime.Materializer == nil {
		t.Fatal("expected Materializer in composition root")
	}
	if runtime.GovernanceReader == nil {
		t.Fatal("expected GovernanceReader in composition root")
	}
	if runtime.Authorer == nil {
		t.Fatal("expected Authorer in composition root")
	}
	if runtime.Evaluator == nil {
		t.Fatal("expected Evaluator in composition root")
	}
	if runtime.Validator == nil {
		t.Fatal("expected Validator in composition root")
	}
	if !runtime.Enabled {
		t.Fatal("expected Enabled=true")
	}

	t.Log("PRODUCTIVE_SKILLFORGE_COMPOSITION = PASS")
}

func TestBootstrapRootIsolation(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required for bootstrap composition test")
	}

	ctx := context.Background()
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "4",
			"ORG_DATABASE_MIN_CONNS": "0",
		}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}

	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "skillforge-isolation-test")
	if err != nil {
		t.Fatal(err)
	}
	defer platformStore.Close()

	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, platformStore.Pool()); err != nil {
		t.Fatalf("testdbguard: %v", err)
	}

	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	if _, err := os.Stat(canonicalDir); os.IsNotExist(err) {
		canonicalDir = filepath.Join("docs", "canonical")
	}

	harness, err := executionharness.New(fakeAuthority{}, fakeModelExecutor{}, fakeCatalog{}, fakeTools{}, executionharness.NewMemoryHistoryStore())
	if err != nil {
		t.Fatal(err)
	}
	registryStore, err := skillregistrypostgres.New(platformStore, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := skillregistry.NewManager(skillregistry.NewService(nil), registryStore, noopGate{})
	if err != nil {
		t.Fatal(err)
	}

	sharedDir := t.TempDir()
	separateDir := t.TempDir()

	// 1. Same roots -> DENY
	bCfgSame := bootstrap.Config{
		OrganizationID:      "explorarte",
		CanonicalDir:        canonicalDir,
		SkillSourceRepoRoot: sharedDir,
		SkillRuntimeRoot:    sharedDir,
		PublishedRemoteURL:  "git@github.com:explorarte-org/skills.git",
		Enabled:             true,
	}
	_, err = bootstrap.Open(bCfgSame, platformStore, harness, harness, manager)
	if err == nil {
		t.Fatal("expected fail-closed error for identical source repo root and runtime root (same roots -> DENY)")
	}
	if !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("unexpected error message: %v", err)
	}
	t.Log("✓ same roots -> DENY PASS")

	// 2. Separate roots -> PASS
	bCfgSeparate := bootstrap.Config{
		OrganizationID:      "explorarte",
		CanonicalDir:        canonicalDir,
		SkillSourceRepoRoot: sharedDir,
		SkillRuntimeRoot:    separateDir,
		PublishedRemoteURL:  "git@github.com:explorarte-org/skills.git",
		Enabled:             true,
	}
	runtime, err := bootstrap.Open(bCfgSeparate, platformStore, harness, harness, manager)
	if err != nil {
		t.Fatalf("bootstrap.Open failed with separate roots: %v", err)
	}
	if runtime == nil {
		t.Fatal("expected non-nil runtime")
	}
	t.Log("✓ separate roots -> PASS")
}

func TestBootstrapRemotePublicationRequirement(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required for bootstrap composition test")
	}

	ctx := context.Background()
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "4",
			"ORG_DATABASE_MIN_CONNS": "0",
		}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}

	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "skillforge-remote-test")
	if err != nil {
		t.Fatal(err)
	}
	defer platformStore.Close()

	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, platformStore.Pool()); err != nil {
		t.Fatalf("testdbguard: %v", err)
	}

	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	if _, err := os.Stat(canonicalDir); os.IsNotExist(err) {
		canonicalDir = filepath.Join("docs", "canonical")
	}

	harness, err := executionharness.New(fakeAuthority{}, fakeModelExecutor{}, fakeCatalog{}, fakeTools{}, executionharness.NewMemoryHistoryStore())
	if err != nil {
		t.Fatal(err)
	}
	registryStore, err := skillregistrypostgres.New(platformStore, "explorarte")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := skillregistry.NewManager(skillregistry.NewService(nil), registryStore, noopGate{})
	if err != nil {
		t.Fatal(err)
	}

	repoDir := t.TempDir()
	materializeDir := t.TempDir()

	// 1. Enabled=true + empty remote -> DENY
	bCfgEnabledEmptyRemote := bootstrap.Config{
		OrganizationID:      "explorarte",
		CanonicalDir:        canonicalDir,
		SkillSourceRepoRoot: repoDir,
		SkillRuntimeRoot:    materializeDir,
		PublishedRemoteURL:  "",
		Enabled:             true,
	}
	_, err = bootstrap.Open(bCfgEnabledEmptyRemote, platformStore, harness, harness, manager)
	if err == nil {
		t.Fatal("expected fail-closed error for Enabled=true with empty PublishedRemoteURL (Enabled=true + empty remote -> DENY)")
	}
	if !strings.Contains(err.Error(), "PublishedRemoteURL is required when enabled") {
		t.Fatalf("unexpected error message: %v", err)
	}
	t.Log("✓ Enabled=true + empty remote -> DENY PASS")

	// 2. Enabled=false + empty remote -> allowed only for non-operational inspection if needed
	bCfgDisabledEmptyRemote := bootstrap.Config{
		OrganizationID:      "explorarte",
		CanonicalDir:        canonicalDir,
		SkillSourceRepoRoot: repoDir,
		SkillRuntimeRoot:    materializeDir,
		PublishedRemoteURL:  "",
		Enabled:             false,
	}
	rtDisabled, err := bootstrap.Open(bCfgDisabledEmptyRemote, platformStore, nil, nil, manager)
	if err != nil {
		t.Fatalf("bootstrap.Open failed for Enabled=false with empty remote: %v", err)
	}
	if rtDisabled.Enabled {
		t.Fatal("expected rtDisabled.Enabled == false")
	}
	t.Log("✓ Enabled=false + empty remote -> PASS (allowed for inspection)")

	// 3. Enabled=true + configured remote -> PASS
	bCfgEnabledConfiguredRemote := bootstrap.Config{
		OrganizationID:      "explorarte",
		CanonicalDir:        canonicalDir,
		SkillSourceRepoRoot: repoDir,
		SkillRuntimeRoot:    materializeDir,
		PublishedRemoteURL:  "git@github.com:explorarte-org/skills.git",
		Enabled:             true,
	}
	rtEnabled, err := bootstrap.Open(bCfgEnabledConfiguredRemote, platformStore, harness, harness, manager)
	if err != nil {
		t.Fatalf("bootstrap.Open failed with configured remote: %v", err)
	}
	if !rtEnabled.Enabled {
		t.Fatal("expected rtEnabled.Enabled == true")
	}
	t.Log("✓ Enabled=true + configured remote -> PASS")
}
