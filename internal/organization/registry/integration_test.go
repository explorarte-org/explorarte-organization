//go:build integration

package registry_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
	"gopkg.in/yaml.v3"
)

func TestCanonicalRegistryAgainstPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store := openStore(t, ctx)
	defer store.Close()
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err = store.Pool().Exec(ctx, `TRUNCATE organization_reporting_lines, organization_registry_revision_documents, organization_roles, organizational_units, organizations, organization_registry_revisions, audit_events RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("clean registry: %v", err)
	}
	repository, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repository, "explorarte", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.SynchronizeCanonical(ctx, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !first.Applied || first.NoOp || first.Revision == nil {
		t.Fatalf("unexpected first sync: %+v", first)
	}
	org, err := service.GetOrganization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if org.ID != "explorarte" {
		t.Fatalf("organization=%q", org.ID)
	}
	units, err := service.ListUnits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	operational := 0
	leaderless := map[string]bool{}
	for _, unit := range units {
		if unit.Operational {
			operational++
		}
		leaderless[unit.ID] = unit.Leaderless
	}
	if operational != 7 || !leaderless["empresa"] || !leaderless["investigacion"] {
		t.Fatalf("units=%+v", units)
	}
	roles, err := service.ListRoles(ctx, registry.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 48 {
		t.Fatalf("roles=%d", len(roles))
	}
	imported, proposed := 0, 0
	for _, role := range roles {
		switch role.SourceStatus {
		case "imported_source":
			imported++
		case "proposed_profile_required":
			proposed++
			if role.Enabled || role.Executable {
				t.Fatalf("proposed role enabled: %+v", role)
			}
		}
	}
	if imported != 45 || proposed != 3 {
		t.Fatalf("imported=%d proposed=%d", imported, proposed)
	}
	canonicalSnapshot, _, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range canonicalSnapshot.Units {
		if !unit.Operational {
			continue
		}
		if unit.LeaderRoleID == nil {
			t.Fatalf("canonical operational unit %s has no leader", unit.ID)
		}
		got, e := service.GetLeader(ctx, unit.ID)
		if e != nil || got.ID != *unit.LeaderRoleID {
			t.Fatalf("leader %s=%+v want=%s err=%v", unit.ID, got, *unit.LeaderRoleID, e)
		}
	}
	workers, err := service.ListWorkers(ctx, "ingenieria_ia")
	if err != nil || len(workers) < 10 {
		t.Fatalf("engineering workers=%d err=%v", len(workers), err)
	}
	if _, err = service.GetRole(ctx, "ingenieria_ia/orquestador"); err != nil {
		t.Fatal(err)
	}
	var reporting int
	if err = store.Pool().QueryRow(ctx, `SELECT count(*) FROM organization_reporting_lines WHERE revision_id=$1`, first.Revision.ID).Scan(&reporting); err != nil || reporting == 0 {
		t.Fatalf("reporting=%d err=%v", reporting, err)
	}
	var audits int
	if err = store.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE event_type='organization.registry_synced'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	second, err := service.SynchronizeCanonical(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !second.NoOp || second.Applied {
		t.Fatalf("second sync=%+v", second)
	}
	if err = store.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE event_type='organization.registry_synced'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("duplicate audit event: %d err=%v", audits, err)
	}

	modifiedDir := copyCanonical(t)
	mutateRoleDisplayName(t, modifiedDir, "ingenieria_ia/qa", "QA modificado")
	modifiedLoader, _ := registry.NewLoader(modifiedDir)
	modifiedService, _ := registry.NewService(modifiedLoader, repository, "explorarte", 30*time.Second)
	comparison, err := modifiedService.CompareCanonical(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Diff.HasChanges() || len(comparison.Diff.Roles.Updated) != 1 {
		t.Fatalf("modified diff=%+v", comparison.Diff)
	}

	retiredDir := copyCanonical(t)
	removeLeafRole(t, retiredDir, "ingenieria_ia/qa")
	retiredLoader, _ := registry.NewLoader(retiredDir)
	retiredService, _ := registry.NewService(retiredLoader, repository, "explorarte", 30*time.Second)
	retired, err := retiredService.SynchronizeCanonical(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired.Diff.Roles.Retired) != 1 || retired.Diff.Roles.Retired[0] != "ingenieria_ia/qa" {
		t.Fatalf("retired diff=%+v", retired.Diff)
	}
	var retiredAt *time.Time
	if err = store.Pool().QueryRow(ctx, `SELECT retired_at FROM organization_roles WHERE organization_id='explorarte' AND id='ingenieria_ia/qa'`).Scan(&retiredAt); err != nil || retiredAt == nil {
		t.Fatalf("logical retirement=%v err=%v", retiredAt, err)
	}

	beforeRevision, _ := service.GetCurrentRevision(ctx)
	badDir := copyCanonical(t)
	mutateLeaderToMissing(t, badDir)
	badLoader, _ := registry.NewLoader(badDir)
	badService, _ := registry.NewService(badLoader, repository, "explorarte", 30*time.Second)
	if _, err = badService.SynchronizeCanonical(ctx, true); err == nil {
		t.Fatal("invalid canonical sync succeeded")
	}
	var validation *registry.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("wanted validation error, got %v", err)
	}
	afterRevision, _ := service.GetCurrentRevision(ctx)
	if beforeRevision.ID != afterRevision.ID {
		t.Fatalf("validation failure changed revision: %d -> %d", beforeRevision.ID, afterRevision.ID)
	}

	var revisionCountBefore int
	if err = store.Pool().QueryRow(ctx, `SELECT count(*) FROM organization_registry_revisions`).Scan(&revisionCountBefore); err != nil {
		t.Fatal(err)
	}
	invalidSQLSnapshot := canonicalSnapshot
	invalidSQLSnapshot.CanonicalHash = strings.Repeat("f", 64)
	invalidSQLSnapshot.Organization.DisplayName = ""
	if _, err = repository.Apply(ctx, invalidSQLSnapshot); err == nil {
		t.Fatal("SQL constraint failure was expected")
	}
	var revisionCountAfter int
	if err = store.Pool().QueryRow(ctx, `SELECT count(*) FROM organization_registry_revisions`).Scan(&revisionCountAfter); err != nil {
		t.Fatal(err)
	}
	if revisionCountBefore != revisionCountAfter {
		t.Fatalf("SQL failure committed a partial revision: %d -> %d", revisionCountBefore, revisionCountAfter)
	}
}

func openStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required")
	}
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": url, "ORG_DATABASE_MAX_CONNS": "4", "ORG_DATABASE_MIN_CONNS": "0"}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "registry-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func canonicalDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "docs", "canonical")
}
func copyCanonical(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(canonicalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(canonicalDir(t), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, entry.Name()), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func loadRoleCatalog(t *testing.T, dir string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "role-catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = yaml.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
func saveRoleCatalog(t *testing.T, dir string, doc map[string]any) {
	t.Helper()
	body, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "role-catalog.yaml"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}
func mutateRoleDisplayName(t *testing.T, dir, id, name string) {
	doc := loadRoleCatalog(t, dir)
	roles := doc["roles"].([]any)
	for _, raw := range roles {
		role := raw.(map[string]any)
		if role["id"] == id {
			role["display_name"] = name
			saveRoleCatalog(t, dir, doc)
			return
		}
	}
	t.Fatalf("role %s not found", id)
}
func removeLeafRole(t *testing.T, dir, id string) {
	doc := loadRoleCatalog(t, dir)
	roles := doc["roles"].([]any)
	filtered := make([]any, 0, len(roles)-1)
	for _, raw := range roles {
		role := raw.(map[string]any)
		if role["id"] != id {
			filtered = append(filtered, raw)
		}
	}
	doc["roles"] = filtered
	saveRoleCatalog(t, dir, doc)
	orgPath := filepath.Join(dir, "organization.yaml")
	body, _ := os.ReadFile(orgPath)
	var org map[string]any
	_ = yaml.Unmarshal(body, &org)
	counts := org["counts"].(map[string]any)
	counts["imported_profiles"] = 44
	body, _ = yaml.Marshal(org)
	_ = os.WriteFile(orgPath, body, 0o600)
	mapPath := filepath.Join(dir, "leader-worker-map.yaml")
	body, _ = os.ReadFile(mapPath)
	var leader map[string]any
	_ = yaml.Unmarshal(body, &leader)
	for _, raw := range leader["departments"].([]any) {
		department := raw.(map[string]any)
		workers := department["workers"].([]any)
		kept := workers[:0]
		for _, worker := range workers {
			if worker != id {
				kept = append(kept, worker)
			}
		}
		department["workers"] = kept
	}
	body, _ = yaml.Marshal(leader)
	_ = os.WriteFile(mapPath, body, 0o600)
}
func mutateLeaderToMissing(t *testing.T, dir string) {
	path := filepath.Join(dir, "organization.yaml")
	body, _ := os.ReadFile(path)
	var doc map[string]any
	_ = yaml.Unmarshal(body, &doc)
	doc["operational_departments"].([]any)[0].(map[string]any)["leader_role_id"] = "comunicaciones/no_existe"
	body, _ = yaml.Marshal(doc)
	_ = os.WriteFile(path, body, 0o600)
}
