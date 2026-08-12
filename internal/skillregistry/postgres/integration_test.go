//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	skillregistrypostgres "github.com/Mireuz13/explorarte-organization/internal/skillregistry/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	skillIntegrationOrganization = "explorarte"
	skillIntegrationOwnerRole    = "ingenieria_ia/frontend"
	skillIntegrationCreator      = "recursos_agenticos/disenador_skills"
	skillIntegrationHuman        = "empresa/human"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

func TestSkillRegistryPostgresRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openSkillStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Compared against the runner's own Latest (the real migration tip
	// baked into this binary via rootmigrations.Files), not a hardcoded
	// version number that goes stale the moment a new migration lands.
	if status, statusErr := runner.Status(ctx); statusErr != nil {
		t.Fatal(statusErr)
	} else if result.Current != status.Latest {
		t.Fatalf("current migration=%d, want latest=%d", result.Current, status.Latest)
	}
	resetSkillSchema(t, ctx, platform)
	t.Cleanup(func() { resetSkillSchema(t, context.Background(), platform) })
	syncSkillCanonical(t, ctx, platform)
	store, err := skillregistrypostgres.New(platform, skillIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	var _ skillregistry.Repository = store
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := &fixedClock{now: now}
	domain := skillregistry.NewService(clock)

	t.Run("draft creation is idempotent", func(t *testing.T) {
		_, version := proposeDraft(t, domain, clock, now, "skill-roundtrip")
		created, cVersion, reused, err := store.CreateSkill(ctx, mustSkill(t, domain, clock, now, "skill-roundtrip"), version, "idem-roundtrip", skillregistry.GovernanceEvidence{DecisionRef: "authz:1", ActorRoleID: skillIntegrationCreator, DecidedAt: now})
		if err != nil || reused {
			t.Fatalf("created=%+v version=%+v reused=%v err=%v", created, cVersion, reused, err)
		}
		again, againVersion, reused, err := store.CreateSkill(ctx, created, cVersion, "idem-roundtrip", skillregistry.GovernanceEvidence{DecisionRef: "authz:1", ActorRoleID: skillIntegrationCreator, DecidedAt: now})
		if err != nil || !reused || againVersion.ID != cVersion.ID || again.ID != created.ID {
			t.Fatalf("again=%+v version=%+v reused=%v err=%v", again, againVersion, reused, err)
		}
	})

	t.Run("lifecycle round trip to active and assignment", func(t *testing.T) {
		clock.now = now.Add(time.Minute)
		_, version := proposeDraft(t, domain, clock, now.Add(time.Minute), "skill-lifecycle")
		skill, version, _, err := store.CreateSkill(ctx, mustSkill(t, domain, clock, now.Add(time.Minute), "skill-lifecycle"), version, "idem-lifecycle", skillregistry.GovernanceEvidence{DecisionRef: "authz:2", ActorRoleID: skillIntegrationCreator, DecidedAt: clock.now})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Minute)
		approved, err := domain.HumanApprove(version, skillregistry.ApprovalEvidence{DecisionRef: "authz:owner", ApprovedBy: skillIntegrationHuman, ApprovedAt: clock.now})
		if err != nil {
			t.Fatal(err)
		}
		approved, err = store.SaveVersion(ctx, approved, 1, skillregistry.LifecycleEvent{OrganizationID: skillIntegrationOrganization, SkillID: skill.ID, SkillVersionID: version.ID, From: skillregistry.LifecycleDraft, To: skillregistry.LifecycleHumanApproved, ActorRoleID: skillIntegrationHuman, DecisionRef: "authz:owner", OccurredAt: clock.now})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Minute)
		candidate, err := domain.QualifyCandidate(approved, skillregistry.ValidationEvidence{SchemaValidationRef: "schema:1", CapabilityReviewRef: "capreview:1", InstructionSafetyRef: "safety:1", SourceRecordRef: "staging:artifact:41", ValidatedBy: skillIntegrationCreator, ValidatedAt: clock.now, CapabilitiesPass: true, InstructionSafetyPass: true})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err = store.SaveVersion(ctx, candidate, 2, skillregistry.LifecycleEvent{OrganizationID: skillIntegrationOrganization, SkillID: skill.ID, SkillVersionID: version.ID, From: skillregistry.LifecycleHumanApproved, To: skillregistry.LifecycleCandidate, ActorRoleID: skillIntegrationCreator, DecisionRef: "authz:validate", OccurredAt: clock.now})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SaveVersion(ctx, candidate, 1, skillregistry.LifecycleEvent{}); !errors.Is(err, skillregistry.ErrRevisionConflict) {
			t.Fatalf("stale save err=%v", err)
		}
		clock.now = clock.now.Add(time.Minute)
		active, err := domain.Activate(candidate, skillregistry.ApprovalEvidence{DecisionRef: "authz:activate", ApprovedBy: skillIntegrationHuman, ApprovedAt: clock.now})
		if err != nil {
			t.Fatal(err)
		}
		active, err = store.SaveVersion(ctx, active, 3, skillregistry.LifecycleEvent{OrganizationID: skillIntegrationOrganization, SkillID: skill.ID, SkillVersionID: version.ID, From: skillregistry.LifecycleCandidate, To: skillregistry.LifecycleActive, ActorRoleID: skillIntegrationHuman, DecisionRef: "authz:activate", OccurredAt: clock.now})
		if err != nil {
			t.Fatal(err)
		}
		if active.Lifecycle != skillregistry.LifecycleActive {
			t.Fatalf("active=%+v", active)
		}
		loaded, err := store.GetVersion(ctx, skillIntegrationOrganization, version.ID)
		if err != nil || loaded.Lifecycle != skillregistry.LifecycleActive || loaded.OwnerApproval == nil || loaded.Validation == nil || loaded.ActivationApproval == nil {
			t.Fatalf("loaded=%+v err=%v", loaded, err)
		}

		clock.now = clock.now.Add(time.Minute)
		assignment, err := domain.Assign(active, skillregistry.AssignCommand{AssignmentID: "assign-lifecycle", OrganizationID: skillIntegrationOrganization, RoleID: skillIntegrationOwnerRole, AssignedBy: skillIntegrationHuman, AssignmentDecisionRef: "authz:assign", CapabilityReviewRef: "role-capreview:1"})
		if err != nil {
			t.Fatal(err)
		}
		created, reused, err := store.CreateAssignment(ctx, assignment, "idem-assign", skillregistry.AssignmentEvent{OrganizationID: skillIntegrationOrganization, AssignmentID: assignment.ID, SkillID: assignment.SkillID, SkillVersionID: assignment.SkillVersionID, RoleID: assignment.RoleID, Action: "assign", ActorRoleID: skillIntegrationHuman, DecisionRef: "authz:assign", OccurredAt: assignment.AssignedAt})
		if err != nil || reused {
			t.Fatalf("created=%+v reused=%v err=%v", created, reused, err)
		}
		if _, _, err := store.CreateAssignment(ctx, assignment, "idem-assign-2", skillregistry.AssignmentEvent{}); !errors.Is(err, skillregistry.ErrAssignmentConflict) {
			t.Fatalf("duplicate active assignment err=%v", err)
		}
		active2, err := store.ListActiveAssignmentsForRole(ctx, skillIntegrationOrganization, skillIntegrationOwnerRole)
		if err != nil || len(active2) != 1 {
			t.Fatalf("active assignments=%+v err=%v", active2, err)
		}

		clock.now = clock.now.Add(time.Minute)
		revoked, err := domain.RevokeAssignment(created, "role_restructured")
		if err != nil {
			t.Fatal(err)
		}
		revoked, err = store.SaveAssignment(ctx, revoked, 1, skillregistry.AssignmentEvent{OrganizationID: skillIntegrationOrganization, AssignmentID: revoked.ID, SkillID: revoked.SkillID, SkillVersionID: revoked.SkillVersionID, RoleID: revoked.RoleID, Action: "revoke", ActorRoleID: skillIntegrationHuman, DecisionRef: "authz:revoke", ReasonCode: revoked.RevokeReason, OccurredAt: *revoked.RevokedAt})
		if err != nil {
			t.Fatal(err)
		}
		if revoked.Status != skillregistry.AssignmentRevoked {
			t.Fatalf("revoked=%+v", revoked)
		}
	})

	t.Run("immutable rows cannot mutate", func(t *testing.T) {
		for name, statement := range map[string]string{
			"content":      `UPDATE skill_registry_versions SET content_hash=repeat('b',64) WHERE organization_id='explorarte' AND version_id='skillver-skill-roundtrip'`,
			"event":        `UPDATE skill_registry_lifecycle_events SET decision_ref='tampered' WHERE organization_id='explorarte' AND version_id='skillver-skill-roundtrip'`,
			"idempotency":  `DELETE FROM skill_registry_skill_idempotency WHERE organization_id='explorarte' AND idempotency_key='idem-roundtrip'`,
			"skill_delete": `DELETE FROM skill_registry_versions WHERE organization_id='explorarte' AND version_id='skillver-skill-roundtrip'`,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := platform.Pool().Exec(ctx, statement); err == nil {
					t.Fatalf("%s mutation succeeded", name)
				}
			})
		}
	})

	t.Run("database rejects skipped lifecycle transitions", func(t *testing.T) {
		clock.now = now.Add(10 * time.Minute)
		_, version := proposeDraft(t, domain, clock, clock.now, "skill-skip")
		skill, version, _, err := store.CreateSkill(ctx, mustSkill(t, domain, clock, clock.now, "skill-skip"), version, "idem-skip", skillregistry.GovernanceEvidence{DecisionRef: "authz:3", ActorRoleID: skillIntegrationCreator, DecidedAt: clock.now})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Minute)
		if _, err := platform.Pool().Exec(ctx, `UPDATE skill_registry_versions SET lifecycle='active',revision=2,updated_at=$3 WHERE organization_id=$1 AND version_id=$2`, skillIntegrationOrganization, version.ID, clock.now); err == nil {
			t.Fatalf("skipped transition for skill %s succeeded", skill.ID)
		}
	})
}

func mustSkill(t *testing.T, domain *skillregistry.Service, clock *fixedClock, now time.Time, id string) skillregistry.Skill {
	t.Helper()
	skill, _ := proposeDraft(t, domain, clock, now, id)
	return skill
}

func proposeDraft(t *testing.T, domain *skillregistry.Service, clock *fixedClock, now time.Time, id string) (skillregistry.Skill, skillregistry.SkillVersion) {
	t.Helper()
	clock.now = now
	skill, version, err := domain.CreateDraft(skillregistry.CreateDraftCommand{
		SkillID: id, VersionID: "skillver-" + id, OrganizationID: skillIntegrationOrganization, Version: 1, CreatedByRole: skillIntegrationCreator,
		Manifest: skillregistry.Manifest{
			Name: id, Description: "Skill de integración durable para pruebas de round trip.", Department: "ingenieria_ia", OwnerRoleID: skillIntegrationOwnerRole, MemoryDomain: "ingenieria_ia", BaseProtocol: "verificacion_estado", RequiredCapabilities: []string{"code.run_tests"},
		},
		Source:      skillregistry.SourceRecord{Path: "skills/" + id + "/SKILL.md", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Origin: skillregistry.OriginInternal, RecordedBy: skillIntegrationCreator, RecordRef: "staging:artifact:41"},
		ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if err != nil {
		t.Fatal(err)
	}
	return skill, version
}

func openSkillStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "skillregistry-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetSkillSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `TRUNCATE organizations, organization_registry_revisions, audit_events RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func syncSkillCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, skillIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !res.Applied {
		t.Fatalf("sync=%+v err=%v", res, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, skillIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}
