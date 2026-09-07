package contextprovider_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry/contextprovider"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
)

func openTestStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL not set; skipping postgres divergence test")
	}

	cfg := config.DatabaseConfig{
		URL:               url,
		SSLMode:           "disable",
		MaxConns:          25,
		MinConns:          1,
		MaxConnLifetime:   time.Minute,
		MaxConnIdleTime:   time.Minute,
		HealthCheckPeriod: time.Second,
		ConnectTimeout:    5 * time.Second,
		PingTimeout:       5 * time.Second,
		StatementTimeout:  30 * time.Second,
		LockTimeout:       5 * time.Second,
	}

	store, err := platformpostgres.Open(ctx, cfg, "contextprovider-divergence-test")
	if err != nil {
		t.Fatalf("open platform store: %v", err)
	}

	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("testdbguard check failed: %v", err)
	}

	return store
}

func TestPostgresDivergenceRecorderDurable(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	defer store.Close()

	orgID := "explorarte"
	roleID := "empresa/ceo"

	_, err := store.Pool().Exec(ctx, "DELETE FROM skill_provider_divergences WHERE organization_id = $1 AND role_id = $2", orgID, roleID)
	if err != nil {
		t.Fatalf("failed to clean test table: %v", err)
	}

	recorder := contextprovider.NewPostgresDivergenceRecorder(store, orgID)

	rec := contextprovider.DivergenceRecord{
		OrganizationID:    orgID,
		RoleID:            roleID,
		SkillID:           "test-skill",
		Operation:         "GetActiveForRole",
		Field:             "source_hash",
		PrimaryValue:      "hash-a",
		ShadowValue:       "hash-b",
		PrimaryVersion:    "1",
		ShadowVersion:     "1",
		PrimarySourceHash: "hash-a",
		ShadowSourceHash:  "hash-b",
		Reason:            "source hash mismatch",
		ObservedAt:        time.Now().UTC(),
	}

	// 1. Record divergence
	if err := recorder.RecordDivergence(ctx, rec); err != nil {
		t.Fatalf("failed to record divergence: %v", err)
	}

	// 2. Query divergence
	list, err := recorder.ListDivergences(ctx, orgID, 10)
	if err != nil {
		t.Fatalf("failed to list divergences: %v", err)
	}
	found := false
	for _, d := range list {
		if d.RoleID == roleID && d.SkillID == "test-skill" {
			found = true
			if d.PrimarySourceHash != "hash-a" || d.ObservationCount != 1 {
				t.Fatalf("unexpected row contents: %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("expected divergence row not found in db")
	}

	// 3. Restart recorder: simulate new process/recorder instance
	recorderRestarted := contextprovider.NewPostgresDivergenceRecorder(store, orgID)
	list2, err := recorderRestarted.ListDivergences(ctx, orgID, 10)
	if err != nil {
		t.Fatalf("failed to list divergences after restart: %v", err)
	}
	foundRestart := false
	for _, d := range list2 {
		if d.RoleID == roleID && d.SkillID == "test-skill" {
			foundRestart = true
		}
	}
	if !foundRestart {
		t.Fatal("expected row to remain durable after restart")
	}
}

func TestPostgresDivergenceRecorderAtomicConcurrent(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	defer store.Close()

	orgID := "explorarte"
	roleID := "ingenieria/qa_concurrent"
	_, err := store.Pool().Exec(ctx, "DELETE FROM skill_provider_divergences WHERE organization_id = $1 AND role_id = $2", orgID, roleID)
	if err != nil {
		t.Fatalf("failed to clean test table: %v", err)
	}

	recorder := contextprovider.NewPostgresDivergenceRecorder(store, orgID)

	// Launch N concurrent goroutines attempting to record the exact same divergence
	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			rec := contextprovider.DivergenceRecord{
				OrganizationID:    orgID,
				RoleID:            roleID,
				SkillID:           "concurrent-skill",
				Operation:         "GetActiveForRole",
				Field:             "presence",
				PrimaryValue:      "present",
				ShadowValue:       "absent",
				PrimaryVersion:    "1",
				PrimarySourceHash: "hash-c",
				Reason:            "skill present in primary but absent in shadow",
				ObservedAt:        time.Now().UTC(),
			}
			_ = recorder.RecordDivergence(ctx, rec)
		}()
	}

	wg.Wait()

	// Verify atomic aggregation:
	// Exactly ONE row exists for this divergence_key
	// ObservationCount must equal numGoroutines
	list, err := recorder.ListDivergences(ctx, orgID, 100)
	if err != nil {
		t.Fatalf("failed to list divergences: %v", err)
	}
	var targetRow *contextprovider.DivergenceRecord
	for _, d := range list {
		if d.RoleID == roleID && d.SkillID == "concurrent-skill" {
			targetRow = &d
			break
		}
	}
	if targetRow == nil {
		t.Fatal("expected aggregated divergence row not found")
	}
	if targetRow.ObservationCount != numGoroutines {
		t.Fatalf("expected observation_count %d, got %d", numGoroutines, targetRow.ObservationCount)
	}
}

func TestPostgresDivergenceRecorderDistinctKeys(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, ctx)
	defer store.Close()

	orgID := "explorarte"
	roleID := "empresa/cfo"
	_, err := store.Pool().Exec(ctx, "DELETE FROM skill_provider_divergences WHERE organization_id = $1 AND role_id = $2", orgID, roleID)
	if err != nil {
		t.Fatalf("failed to clean test table: %v", err)
	}

	recorder := contextprovider.NewPostgresDivergenceRecorder(store, orgID)

	rec1 := contextprovider.DivergenceRecord{
		OrganizationID:    orgID,
		RoleID:            roleID,
		SkillID:           "skill-1",
		Operation:         "GetActiveForRole",
		Field:             "source_hash",
		PrimaryValue:      "hash-1a",
		ShadowValue:       "hash-1b",
		PrimaryVersion:    "1",
		ShadowVersion:     "1",
		PrimarySourceHash: "hash-1a",
		ShadowSourceHash:  "hash-1b",
		Reason:            "mismatch 1",
		ObservedAt:        time.Now().UTC(),
	}
	rec2 := contextprovider.DivergenceRecord{
		OrganizationID:    orgID,
		RoleID:            roleID,
		SkillID:           "skill-2", // different skill
		Operation:         "GetActiveForRole",
		Field:             "source_hash",
		PrimaryValue:      "hash-2a",
		ShadowValue:       "hash-2b",
		PrimaryVersion:    "1",
		ShadowVersion:     "1",
		PrimarySourceHash: "hash-2a",
		ShadowSourceHash:  "hash-2b",
		Reason:            "mismatch 2",
		ObservedAt:        time.Now().UTC(),
	}

	if err := recorder.RecordDivergence(ctx, rec1); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordDivergence(ctx, rec2); err != nil {
		t.Fatal(err)
	}

	list, err := recorder.ListDivergences(ctx, orgID, 10)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, d := range list {
		if d.RoleID == roleID {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 distinct rows for 2 distinct divergence keys, got %d", count)
	}
}
