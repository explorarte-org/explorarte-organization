//go:build integration

// CEO_CONVERSATIONAL_FULL_STACK_PREMERGE_CLOSURE_V1 BLOCKER 1: proves the
// exact production composition cmd/orgctl/executive_chat.go's
// openCeoChatRuntime performs -- open internal/executive/bootstrap.Runtime,
// then internal/ceochat/bootstrap.Runtime sharing its Model Runtime --
// constructs exactly ONE Model Runtime, never two. Pointer identity is the
// proof: modelbootstrap.Open builds a brand new *modelbootstrap.Runtime
// (new provider adapters, new router, new circuit breakers, new egress
// client) on every call, so two independently-opened runtimes can never be
// pointer-equal by coincidence -- only actual sharing produces that.
package ceochat_test

import (
	"context"
	"os"
	"testing"
	"time"

	ceochatbootstrap "github.com/Mireuz13/explorarte-organization/internal/ceochat/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	executivebootstrap "github.com/Mireuz13/explorarte-organization/internal/executive/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// TestCEOChatSharesExecutiveModelRuntime proves BLOCKER 1's whole claim,
// against real PostgreSQL, using the exact bootstrap calls
// cmd/orgctl/executive_chat.go's openCeoChatRuntime makes:
//
//  1. WithModelRuntime shares the Executive runtime's Model Runtime by
//     pointer -- ceochatRuntime.ModelRuntime == executiveRuntime.Models,
//     not merely deeply equal.
//  2. Omitting WithModelRuntime (every existing independent ceochat
//     caller, and every ceochat test fixture, does exactly this) still
//     opens its own canonical Model Runtime -- old behavior is preserved
//     for callers that never asked to share.
func TestCEOChatSharesExecutiveModelRuntime(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	// modelbootstrap.Open reads ExecutionPrincipalKey (and other model-
	// runtime-specific settings) via a direct os.LookupEnv call, not
	// through config.LoadFrom's map below -- t.Setenv is required here,
	// matching every other ceochat fixture in this package.
	t.Setenv("ORG_MODEL_EXECUTION_PRINCIPAL_KEY", chatTestDispatchPrincipalKey)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":           "test",
			"ORG_DATABASE_URL":          databaseURL,
			"ORG_DATABASE_MAX_CONNS":    "16",
			"ORG_DATABASE_MIN_CONNS":    "0",
			"ORG_CANONICAL_DIR":         "/src/docs/canonical",
			"ORG_CONTEXT_SOURCE_ROOT":   "/src",
			"ORG_TASKS_ORGANIZATION_ID": chatTestOrganization,
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "ceochat-model-runtime-sharing")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(cfg.Registry.CanonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, chatTestOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || (!result.Applied && !result.NoOp) {
		t.Fatalf("sync canonical registry: result=%+v err=%v", result, syncErr)
	}
	registerChatTestDispatchPrincipal(t, ctx, store, func(format string, args ...any) { t.Fatalf(format, args...) })

	// 1. The exact production sequence: Executive first, ceochat sharing it.
	executiveRuntime, err := executivebootstrap.Open(cfg, store)
	if err != nil {
		t.Fatalf("open executive runtime: %v", err)
	}
	if executiveRuntime.Models == nil {
		t.Fatal("executive runtime has a nil Model Runtime")
	}

	sharedCEOChatRuntime, err := ceochatbootstrap.Open(cfg, store,
		ceochatbootstrap.WithModelRuntime(executiveRuntime.Models),
		ceochatbootstrap.WithExecutiveSubmitter(executiveRuntime.Orchestrator),
	)
	if err != nil {
		t.Fatalf("open ceochat runtime sharing the executive model runtime: %v", err)
	}
	if sharedCEOChatRuntime.ModelRuntime != executiveRuntime.Models {
		t.Fatalf("ceochat's ModelRuntime (%p) is not the executive runtime's Models (%p) -- BLOCKER 1 not fixed: two Model Runtimes exist in one process composition",
			sharedCEOChatRuntime.ModelRuntime, executiveRuntime.Models)
	}
	t.Logf("PASS: ceochatRuntime.ModelRuntime == executiveRuntime.Models (%p) -- exactly one Model Runtime in this composition", executiveRuntime.Models)

	// 2. Existing independent ceochat callers (every ceochat test fixture,
	// and any future caller that never opts in) must keep opening their
	// own canonical Model Runtime -- never silently start sharing, and
	// never fail merely because sharing is now possible.
	independentCEOChatRuntime, err := ceochatbootstrap.Open(cfg, store)
	if err != nil {
		t.Fatalf("open independent ceochat runtime (no WithModelRuntime): %v", err)
	}
	if independentCEOChatRuntime.ModelRuntime == executiveRuntime.Models {
		t.Fatal("independent ceochat runtime (no WithModelRuntime option) unexpectedly shares the executive runtime's Model Runtime -- old behavior regressed")
	}
	if independentCEOChatRuntime.ModelRuntime == sharedCEOChatRuntime.ModelRuntime {
		t.Fatal("independent ceochat runtime unexpectedly shares the FIRST ceochat runtime's Model Runtime")
	}
	t.Logf("PASS: independent ceochatbootstrap.Open (no WithModelRuntime) opens its own distinct Model Runtime (%p), old behavior preserved", independentCEOChatRuntime.ModelRuntime)
}
