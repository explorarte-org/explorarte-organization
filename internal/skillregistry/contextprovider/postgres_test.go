package contextprovider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
)

func TestPostgresDivergenceRecorderDurable(t *testing.T) {
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL not set; skipping live postgres divergence recorder test")
	}

	ctx := context.Background()
	cfg := config.DatabaseConfig{
		URL:               url,
		SSLMode:           "disable",
		MaxConns:          10,
		MinConns:          1,
		MaxConnLifetime:   time.Minute,
		MaxConnIdleTime:   time.Minute,
		HealthCheckPeriod: time.Second,
		ConnectTimeout:    5 * time.Second,
		PingTimeout:       5 * time.Second,
		StatementTimeout:  30 * time.Second,
		LockTimeout:       5 * time.Second,
	}
	platformStore, err := platformpostgres.Open(ctx, cfg, "contextprovider-divergence-test")
	if err != nil {
		t.Fatalf("open platform store: %v", err)
	}
	defer platformStore.Close()

	if err := testdbguard.RequireTestDatabase(ctx, url, platformStore.Pool()); err != nil {
		t.Fatalf("testdbguard check failed: %v", err)
	}

	orgID := "explorarte"
	roleID := "ingenieria_ia/qa"

	// Ensure clean table state for this role in test
	_, _ = platformStore.Pool().Exec(ctx, "DELETE FROM skill_provider_divergences WHERE organization_id = $1 AND role_id = $2", orgID, roleID)

	rec1 := NewPostgresDivergenceRecorder(platformStore, orgID)

	// 1. Initial state: 0 divergences
	initialDivs, err := rec1.ListDivergences(ctx, orgID, 100)
	if err != nil {
		t.Fatalf("ListDivergences: %v", err)
	}
	initialCount := 0
	for _, d := range initialDivs {
		if d.RoleID == roleID {
			initialCount++
		}
	}
	if initialCount != 0 {
		t.Fatalf("expected 0 initial divergences for role, got %d", initialCount)
	}

	// 2. Record divergence
	divergence := DivergenceRecord{
		OrganizationID:    orgID,
		RoleID:            roleID,
		SkillID:           "skill-diff-v",
		Operation:         "ListActiveForRole",
		PrimaryVersion:    "registry-v2",
		ShadowVersion:     "canonical-import-v1",
		PrimarySourceHash: "hash-pri-1111",
		ShadowSourceHash:  "hash-sha-2222",
		Reason:            "Version mismatch between primary and shadow: registry-v2 vs canonical-import-v1",
		ObservedAt:        time.Now().UTC(),
	}
	if err := rec1.RecordDivergence(ctx, divergence); err != nil {
		t.Fatalf("RecordDivergence failed: %v", err)
	}

	// 3. Restart recorder: create brand new recorder instance and verify row remains durable & queryable
	rec2 := NewPostgresDivergenceRecorder(platformStore, orgID)
	divs, err := rec2.ListDivergences(ctx, orgID, 100)
	if err != nil {
		t.Fatalf("ListDivergences on new instance failed: %v", err)
	}
	found := false
	for _, d := range divs {
		if d.RoleID == roleID && d.SkillID == "skill-diff-v" {
			found = true
			if d.PrimaryVersion != "registry-v2" || d.ShadowVersion != "canonical-import-v1" {
				t.Fatalf("corrupted versions in row: %+v", d)
			}
			if d.PrimarySourceHash != "hash-pri-1111" || d.ShadowSourceHash != "hash-sha-2222" {
				t.Fatalf("corrupted hashes in row: %+v", d)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected divergence row to be durable and queryable after recorder restart")
	}

	// 4. Same identical observation -> deterministic deduplication skips duplicate insert
	if err := rec2.RecordDivergence(ctx, divergence); err != nil {
		t.Fatalf("RecordDivergence dedupe test failed: %v", err)
	}
	divsAfterDedupe, err := rec2.ListDivergences(ctx, orgID, 100)
	if err != nil {
		t.Fatal(err)
	}
	countForSkill := 0
	for _, d := range divsAfterDedupe {
		if d.RoleID == roleID && d.SkillID == "skill-diff-v" {
			countForSkill++
		}
	}
	if countForSkill != 1 {
		t.Fatalf("expected deterministic dedupe to keep 1 row, got %d", countForSkill)
	}
}
