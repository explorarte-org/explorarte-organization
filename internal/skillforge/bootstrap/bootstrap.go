package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/platform/skillpublisher"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge"
	needpostgres "github.com/Mireuz13/explorarte-organization/internal/skillforge/need/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	skillregistrypostgres "github.com/Mireuz13/explorarte-organization/internal/skillregistry/postgres"
)

// Config carries the deployment and environmental wiring for Skill Forge.
type Config struct {
	OrganizationID       string
	CanonicalDir         string
	SkillSourceRepoRoot  string
	SkillRuntimeRoot     string
	SourceRepositoryDir  string // alias for SkillSourceRepoRoot
	MaterializeRootDir   string // alias for SkillRuntimeRoot
	PublishedRemoteURL   string
	ExecutionProfileID   string
	RoleID               string
	Enabled              bool
	TaskID               int64
	AttemptID            int64
	ExecutionPrincipalID string
	LeaseToken           string
	ContextSnapshotID    int64
	ContextContent       string
	ContextDigest        string
}

// Runtime holds the productive composition root for Skill Forge.
type Runtime struct {
	Config           Config
	Engine           *skillforge.Engine
	NeedStore        *needpostgres.Store
	RegistryStore    *skillregistrypostgres.Store
	SkillRegistry    *skillregistry.Manager
	Publisher        source.SourcePublisher
	PinnedReader     source.PinnedSourceReader
	Materializer     source.Materializer
	GovernanceReader skillforge.SkillForgeGovernanceReader
	Authorer         *skillforge.HarnessAuthorer
	Evaluator        *skillforge.ForgeEvaluator
	Validator        *skillforge.StaticValidator
	Enabled          bool
}

// Open constructs the complete productive composition root of Skill Forge.
// It wires the database repositories, governance gate, Git publisher, runtime materializer,
// ExecutionHarness authorer, and evaluator without bypassing any governance boundary.
func Open(
	cfg Config,
	platformStore *platformpostgres.Store,
	authorHarness *executionharness.Runtime,
	evalHarness *executionharness.Runtime,
	skillManager *skillregistry.Manager,
) (*Runtime, error) {
	if strings.TrimSpace(cfg.OrganizationID) == "" {
		return nil, errors.New("skillforge bootstrap: organization ID is required")
	}
	if platformStore == nil || platformStore.Pool() == nil {
		return nil, errors.New("skillforge bootstrap: PostgreSQL store is required")
	}

	// Verify Schema 67 requirement: Skill Forge cannot start without schema migration 67.
	var currentTip int64
	if err := platformStore.Pool().QueryRow(context.Background(), `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentTip); err != nil {
		return nil, fmt.Errorf("skillforge bootstrap: check schema_migrations: %w", err)
	}
	if currentTip < 67 {
		return nil, fmt.Errorf("skillforge bootstrap: schema migration 67 required, current tip is %d", currentTip)
	}

	sourceRoot := cfg.SkillSourceRepoRoot
	if sourceRoot == "" {
		sourceRoot = cfg.SourceRepositoryDir
	}
	runtimeRoot := cfg.SkillRuntimeRoot
	if runtimeRoot == "" {
		runtimeRoot = cfg.MaterializeRootDir
	}

	cleanSource := filepath.Clean(strings.TrimSpace(sourceRoot))
	cleanRuntime := filepath.Clean(strings.TrimSpace(runtimeRoot))
	if cleanSource == "" || cleanRuntime == "" || cleanSource == "." || cleanRuntime == "." {
		return nil, errors.New("skillforge bootstrap: SkillSourceRepoRoot and SkillRuntimeRoot must be non-empty valid paths")
	}
	if cleanSource == cleanRuntime {
		return nil, fmt.Errorf("skillforge bootstrap: source repository root and runtime root must be distinct: %s == %s", cleanSource, cleanRuntime)
	}
	cfg.SkillSourceRepoRoot = cleanSource
	cfg.SkillRuntimeRoot = cleanRuntime
	cfg.SourceRepositoryDir = cleanSource
	cfg.MaterializeRootDir = cleanRuntime

	// Fail-closed enforcement: if Enabled is requested, all productive dependencies must be present.
	if cfg.Enabled {
		if authorHarness == nil {
			return nil, errors.New("skillforge bootstrap: authorHarness is required when enabled")
		}
		if evalHarness == nil {
			return nil, errors.New("skillforge bootstrap: evalHarness is required when enabled")
		}
		if skillManager == nil {
			return nil, errors.New("skillforge bootstrap: skillManager is required when enabled")
		}
		if strings.TrimSpace(cfg.PublishedRemoteURL) == "" {
			return nil, errors.New("skillforge bootstrap: PublishedRemoteURL is required when enabled")
		}
	}

	if cfg.CanonicalDir == "" {
		cfg.CanonicalDir = filepath.Join("docs", "canonical")
		if _, err := os.Stat(cfg.CanonicalDir); os.IsNotExist(err) {
			cfg.CanonicalDir = filepath.Join("..", "..", "docs", "canonical")
		}
	}
	if cfg.ExecutionProfileID == "" {
		cfg.ExecutionProfileID = "worker/skill-forge/v1"
	}
	if cfg.RoleID == "" {
		cfg.RoleID = "recursos_agenticos/disenador_skills"
	}

	// 1. Need store
	needStore, err := needpostgres.New(platformStore, cfg.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("skillforge bootstrap need store: %w", err)
	}

	// 2. Skill Registry store
	registryStore, err := skillregistrypostgres.New(platformStore, cfg.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("skillforge bootstrap registry store: %w", err)
	}

	// 3. Governance Reader
	govReader, err := skillforge.NewCanonicalSkillForgeGovernanceReader(cfg.CanonicalDir)
	if err != nil {
		return nil, fmt.Errorf("skillforge bootstrap governance reader: %w", err)
	}

	// 4. Source publisher (Git)
	pubCfg := skillpublisher.GitPublisherConfig{
		RepoDir:           cleanSource,
		RemoteName:        "origin",
		ExpectedRemoteURL: cfg.PublishedRemoteURL,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     cfg.Enabled || strings.TrimSpace(cfg.PublishedRemoteURL) != "",
	}
	publisher, err := skillpublisher.NewGitPublisher(pubCfg)
	if err != nil {
		return nil, fmt.Errorf("skillforge bootstrap publisher: %w", err)
	}

	// 5. Pinned source reader & materializer
	pinnedReader, err := skillpublisher.NewGitPinnedSourceReader(cleanSource)
	if err != nil {
		return nil, fmt.Errorf("skillforge bootstrap pinned reader: %w", err)
	}
	materializer, err := source.NewLocalMaterializer(cleanRuntime, pinnedReader)
	if err != nil {
		return nil, fmt.Errorf("skillforge bootstrap materializer: %w", err)
	}

	// 6. Authorer (if harness provided)
	var authorer *skillforge.HarnessAuthorer
	if authorHarness != nil {
		profileGate := skillforge.CanonicalAuthoringProfileGate{
			Reader:       govReader,
			CanonicalDir: cfg.CanonicalDir,
			RoleID:       cfg.RoleID,
		}
		authorerCfg := skillforge.HarnessAuthorerConfig{
			ExecutionProfileID:   cfg.ExecutionProfileID,
			ModelPolicyRef:       "department.worker",
			MaxTurns:             1,
			ProfileGate:          profileGate,
			TaskID:               cfg.TaskID,
			AttemptID:            cfg.AttemptID,
			ExecutionPrincipalID: cfg.ExecutionPrincipalID,
			LeaseToken:           cfg.LeaseToken,
			ContextSnapshotID:    cfg.ContextSnapshotID,
			ContextContent:       cfg.ContextContent,
			ContextDigest:        cfg.ContextDigest,
		}
		authorer, err = skillforge.NewHarnessAuthorer(authorHarness, authorerCfg)
		if err != nil {
			return nil, fmt.Errorf("skillforge bootstrap authorer: %w", err)
		}
	}

	// 7. Evaluator (if harness provided)
	var evaluator *skillforge.ForgeEvaluator
	if evalHarness != nil {
		evalCfg := skillforge.ForgeEvaluatorConfig{
			ExecutionProfileID:   cfg.ExecutionProfileID,
			ModelPolicyRef:       "department.worker",
			AdversarialProfileID: "transversal/revisor-adversarial/v1",
			TaskID:               cfg.TaskID,
			AttemptID:            cfg.AttemptID,
			ExecutionPrincipalID: cfg.ExecutionPrincipalID,
			LeaseToken:           cfg.LeaseToken,
			ContextSnapshotID:    cfg.ContextSnapshotID,
		}
		evaluator = skillforge.NewForgeEvaluatorWithHarness(cleanRuntime, evalHarness, evalCfg)
	}

	// 8. Static Validator
	validator := skillforge.NewStaticValidator(cleanRuntime)

	// 9. Engine
	engine := skillforge.NewEngine(
		needStore,
		registryStore,
		skillManager,
		publisher,
		materializer,
		authorer,
		validator,
		evaluator,
	)

	return &Runtime{
		Config:           cfg,
		Engine:           engine,
		NeedStore:        needStore,
		RegistryStore:    registryStore,
		SkillRegistry:    skillManager,
		Publisher:        publisher,
		PinnedReader:     pinnedReader,
		Materializer:     materializer,
		GovernanceReader: govReader,
		Authorer:         authorer,
		Evaluator:        evaluator,
		Validator:        validator,
		Enabled:          cfg.Enabled,
	}, nil
}
