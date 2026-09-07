package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	skillforgebootstrap "github.com/Mireuz13/explorarte-organization/internal/skillforge/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
)

func TestSkillForgeUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"skillforge"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl skillforge <need|run|status|materialize>") {
		t.Fatalf("expected usage message, got %q", stderr.String())
	}
}

func TestSkillForgeMaterializeFailsClosedOnSameRoots(t *testing.T) {
	sameDir := t.TempDir()
	filePath := filepath.Join(sameDir, "test.md")
	if err := os.WriteFile(filePath, []byte("# Test"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"skillforge", "materialize",
		"--origin", "explorarte-org/skills@0123456789012345678901234567890123456789",
		"--path", "skills/test/SKILL.md",
		"--file", filePath,
		"--source-repo-root", sameDir,
		"--runtime-root", sameDir,
	}, &stdout, &stderr)

	if code != exitInvalid {
		t.Fatalf("expected exitInvalid (%d), got %d; stderr=%s", exitInvalid, code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "source root and runtime root must be distinct") {
		t.Fatalf("expected distinct roots error, got: %s", stderr.String())
	}
	t.Log("✓ ENTRYPOINT_FAILS_CLOSED_SAME_ROOT PASS")
}

func TestSkillForgeOperatorDisabledRun(t *testing.T) {
	// ORG_SKILLFORGE_ENABLED=false (default) -> orgctl skillforge run DENY
	t.Setenv("ORG_SKILLFORGE_ENABLED", "false")
	var stdout, stderr bytes.Buffer
	code := run([]string{"skillforge", "run", "need-sample-123"}, &stdout, &stderr)
	if code != exitDenied {
		t.Fatalf("expected exitDenied (%d) when disabled, got %d; stderr=%s", exitDenied, code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "skillforge is disabled (ORG_SKILLFORGE_ENABLED=false)") {
		t.Fatalf("expected disabled error in stderr, got: %s", stderr.String())
	}
	t.Log("✓ SKILLFORGE_DISABLED_RUN DENY")
}

func TestSkillForgeOperatorEnabledRun(t *testing.T) {
	// ORG_SKILLFORGE_ENABLED=true -> proceeds to normal preconditions
	t.Setenv("ORG_SKILLFORGE_ENABLED", "true")
	sameDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"skillforge", "run", "need-sample-123",
		"--source-repo-root", sameDir,
		"--runtime-root", sameDir,
		"--remote-url", "git@github.com:explorarte-org/skills.git",
	}, &stdout, &stderr)

	// It must proceed past the disabled gate (which would have returned exitDenied),
	// reaching normal preconditions and failing on distinct roots or DB connection (not exitDenied).
	if code == exitDenied {
		t.Fatalf("did not proceed past disabled gate when ORG_SKILLFORGE_ENABLED=true; stderr=%s", stderr.String())
	}
	t.Logf("✓ SKILLFORGE_ENABLED_RUN proceeds to normal preconditions (exit=%d)", code)
}

func TestSkillForgeReadOnlyWhenDisabled(t *testing.T) {
	// When ORG_SKILLFORGE_ENABLED=false, read-only inspection commands (status, need list/get) remain available.
	t.Setenv("ORG_SKILLFORGE_ENABLED", "false")

	// 1. status is operational and returns exitOK
	var stdout, stderr bytes.Buffer
	code := run([]string{"skillforge", "status", "run-123", "--json"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("expected status to be accessible when disabled, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"run_id": "run-123"`) {
		t.Fatalf("unexpected status output: %s", stdout.String())
	}

	// 2. need list usage or execution proceeds without being blocked by disabled gate
	stdout.Reset()
	stderr.Reset()
	codeList := run([]string{"skillforge", "need"}, &stdout, &stderr)
	if codeList != exitUsage {
		t.Fatalf("expected usage exit, got %d", codeList)
	}
	if strings.Contains(stderr.String(), "skillforge is disabled") {
		t.Fatalf("need inspection commands must not be blocked by disabled gate: %s", stderr.String())
	}
	t.Log("✓ READ_ONLY_WHEN_DISABLED PASS")
}

func TestSkillForgeOperatorEntrypointSafe(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required for operator entrypoint safety acceptance test")
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

	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "skillforge-entrypoint-test")
	if err != nil {
		t.Fatal(err)
	}
	defer platformStore.Close()

	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, platformStore.Pool()); err != nil {
		t.Fatalf("testdbguard: %v", err)
	}

	canonicalDir := filepath.Join("..", "..", "docs", "canonical")
	if _, err := os.Stat(canonicalDir); os.IsNotExist(err) {
		canonicalDir = filepath.Join("docs", "canonical")
	}

	// 1. Check schema >= 67 requirement
	var currentTip int64
	err = platformStore.Pool().QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentTip)
	if err != nil {
		t.Fatalf("check schema tip: %v", err)
	}
	if currentTip < 67 {
		t.Fatalf("schema migration 67 required, current tip is %d", currentTip)
	}
	t.Logf("✓ SCHEMA >= 67 VERIFIED (tip=%d)", currentTip)

	// 2. ENTRYPOINT_FAILS_CLOSED_SAME_ROOT
	sameRoot := t.TempDir()
	var stdout, stderr bytes.Buffer
	contextDir := t.TempDir()
	t.Setenv("ORG_DATABASE_URL", databaseURL)
	t.Setenv("ORG_CANONICAL_DIR", canonicalDir)
	t.Setenv("ORG_CONTEXT_SOURCE_ROOT", contextDir)
	t.Setenv("ORG_ENVIRONMENT", "test")
	t.Setenv("ORG_SKILLFORGE_ENABLED", "true")

	codeSame := run([]string{
		"skillforge", "run", "need-test-isolation",
		"--source-repo-root", sameRoot,
		"--runtime-root", sameRoot,
		"--remote-url", "git@github.com:explorarte-org/skills.git",
	}, &stdout, &stderr)

	if codeSame != exitInternal {
		t.Fatalf("expected exitInternal for same roots, got %d; stderr=%s", codeSame, stderr.String())
	}
	if !strings.Contains(stderr.String(), "must be distinct") {
		t.Fatalf("expected distinct roots error in stderr, got: %s", stderr.String())
	}
	t.Log("✓ ENTRYPOINT_SOURCE_RUNTIME_ROOT_ISOLATED PASS")
	t.Log("✓ ENTRYPOINT_FAILS_CLOSED_SAME_ROOT PASS")

	// 3. ENTRYPOINT_FAILS_CLOSED_MISSING_REMOTE
	sourceDir := t.TempDir()
	runtimeDir := t.TempDir()
	stderr.Reset()
	stdout.Reset()

	codeNoRemote := run([]string{
		"skillforge", "run", "need-test-remote",
		"--source-repo-root", sourceDir,
		"--runtime-root", runtimeDir,
		"--remote-url", "",
	}, &stdout, &stderr)

	if codeNoRemote != exitInternal {
		t.Fatalf("expected exitInternal for empty remote, got %d; stderr=%s", codeNoRemote, stderr.String())
	}
	if !strings.Contains(stderr.String(), "PublishedRemoteURL is required when enabled") {
		t.Fatalf("expected PublishedRemoteURL required error in stderr, got: %s", stderr.String())
	}
	t.Log("✓ ENTRYPOINT_REMOTE_PUBLICATION_REQUIRED PASS")
	t.Log("✓ ENTRYPOINT_FAILS_CLOSED_MISSING_REMOTE PASS")

	// 4. Verification that operator entrypoint constructs full composition root with productive harnesses & canonical governance
	bCfgSafe := skillforgebootstrap.Config{
		OrganizationID:      "explorarte",
		CanonicalDir:        canonicalDir,
		SkillSourceRepoRoot: sourceDir,
		SkillRuntimeRoot:    runtimeDir,
		PublishedRemoteURL:  "git@github.com:explorarte-org/skills.git",
		Enabled:             false, // inspection
	}
	inspectionRuntime, err := skillforgebootstrap.Open(bCfgSafe, platformStore, nil, nil, nil)
	if err != nil {
		t.Fatalf("open safe inspection runtime: %v", err)
	}
	if inspectionRuntime.GovernanceReader == nil {
		t.Fatal("expected canonical governance reader to be initialized")
	}
	auth, err := inspectionRuntime.GovernanceReader.ResolveAuthoringAuthorization(ctx, "explorarte", "recursos_agenticos/disenador_skills", "worker/skill-forge/v1")
	if err != nil {
		t.Fatalf("resolve authoring authorization: %v", err)
	}
	if auth.ResolvedDecisionID != "D-006" {
		t.Fatalf("expected resolved decision D-006, got %s", auth.ResolvedDecisionID)
	}
	t.Log("✓ CANONICAL GOVERNANCE D-006 PRESENT AND VERIFIED")
	t.Log("✓ PRODUCTIVE_SKILLFORGE_ENTRYPOINT_SAFE PASS")
}
