package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	contextcompilerpostgres "github.com/Mireuz13/explorarte-organization/internal/contextcompiler/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	modelruntimeadapter "github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	harnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	modelruntime "github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	modelbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelruntime/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/platform/skillpublisher"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge"
	skillforgebootstrap "github.com/Mireuz13/explorarte-organization/internal/skillforge/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/need"
	needpostgres "github.com/Mireuz13/explorarte-organization/internal/skillforge/need/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
	skillregistrybootstrap "github.com/Mireuz13/explorarte-organization/internal/skillregistry/bootstrap"
)

func runSkillForge(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printSkillForgeUsage(stderr)
		return exitUsage
	}

	switch args[0] {
	case "need":
		return runSkillForgeNeed(args[1:], stdout, stderr)
	case "run":
		return runSkillForgeRun(args[1:], stdout, stderr)
	case "status":
		return runSkillForgeStatus(args[1:], stdout, stderr)
	case "materialize":
		return runSkillForgeMaterialize(args[1:], stdout, stderr)
	default:
		printSkillForgeUsage(stderr)
		return exitUsage
	}
}

func runSkillForgeNeed(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printSkillForgeUsage(stderr)
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()

	platformStore, _, code := openDatabase(ctx, cfg, stderr, "skillforge")
	if code != exitOK {
		return code
	}
	defer platformStore.Close()

	store, err := needpostgres.New(platformStore, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "create need store: %v\n", err)
		return exitInternal
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("skillforge need create", flag.ContinueOnError)
		flags.SetOutput(stderr)
		id := flags.String("id", "", "procedure need id")
		role := flags.String("role", "", "target role id")
		problem := flags.String("problem", "", "problem statement")
		taskClass := flags.String("task-class", "", "task class")
		profile := flags.String("profile", "", "execution profile id")
		fromEpisode := flags.String("from-episode", "", "episode reference")
		fromCluster := flags.String("from-cluster", "", "corrective cluster reference")
		jsonOutput := flags.Bool("json", false, "emit JSON")

		if err := flags.Parse(args[1:]); err != nil || strings.TrimSpace(*id) == "" || strings.TrimSpace(*role) == "" || strings.TrimSpace(*problem) == "" {
			return exitUsage
		}

		now := time.Now().UTC()
		n := need.ProcedureNeed{
			ID:                 strings.TrimSpace(*id),
			OrganizationID:     cfg.Tasks.OrganizationID,
			RoleID:             strings.TrimSpace(*role),
			TaskClass:          strings.TrimSpace(*taskClass),
			ExecutionProfileID: strings.TrimSpace(*profile),
			ProblemStatement:   strings.TrimSpace(*problem),
			Status:             need.StatusOpen,
			Revision:           1,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if *fromEpisode != "" {
			n.EpisodeRefs = []string{strings.TrimSpace(*fromEpisode)}
		}
		if *fromCluster != "" {
			n.ClusterRefs = []string{strings.TrimSpace(*fromCluster)}
		}

		created, err := store.CreateNeed(ctx, n)
		if err != nil {
			fmt.Fprintf(stderr, "create procedure need: %v\n", err)
			return exitInternal
		}
		writeValue(stdout, *jsonOutput, created)
		return exitOK

	case "list":
		flags := flag.NewFlagSet("skillforge need list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		status := flags.String("status", "", "filter by status")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}

		needs, err := store.ListNeeds(ctx, cfg.Tasks.OrganizationID, need.ProcedureNeedStatus(*status))
		if err != nil {
			fmt.Fprintf(stderr, "list procedure needs: %v\n", err)
			return exitInternal
		}
		writeValue(stdout, *jsonOutput, needs)
		return exitOK

	case "get":
		flags := flag.NewFlagSet("skillforge need get", flag.ContinueOnError)
		flags.SetOutput(stderr)
		id := flags.String("id", "", "procedure need id")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || strings.TrimSpace(*id) == "" {
			return exitUsage
		}

		n, err := store.GetNeed(ctx, cfg.Tasks.OrganizationID, *id)
		if err != nil {
			fmt.Fprintf(stderr, "get procedure need: %v\n", err)
			return exitInternal
		}
		writeValue(stdout, *jsonOutput, n)
		return exitOK

	case "accept":
		flags := flag.NewFlagSet("skillforge need accept", flag.ContinueOnError)
		flags.SetOutput(stderr)
		id := flags.String("id", "", "procedure need id")
		decisionRef := flags.String("decision", "", "authorization decision ref")
		byRole := flags.String("by", "", "accepted by role")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || strings.TrimSpace(*id) == "" || strings.TrimSpace(*decisionRef) == "" || strings.TrimSpace(*byRole) == "" {
			return exitUsage
		}

		current, err := store.GetNeed(ctx, cfg.Tasks.OrganizationID, *id)
		if err != nil {
			fmt.Fprintf(stderr, "get procedure need: %v\n", err)
			return exitInternal
		}

		current.Status = need.StatusAccepted
		current.Acceptance = &need.Acceptance{
			DecisionRef: strings.TrimSpace(*decisionRef),
			AcceptedBy:  strings.TrimSpace(*byRole),
			AcceptedAt:  time.Now().UTC(),
		}

		saved, err := store.SaveNeed(ctx, current, current.Revision)
		if err != nil {
			fmt.Fprintf(stderr, "accept procedure need: %v\n", err)
			return exitInternal
		}
		writeValue(stdout, *jsonOutput, saved)
		return exitOK

	case "reject":
		flags := flag.NewFlagSet("skillforge need reject", flag.ContinueOnError)
		flags.SetOutput(stderr)
		id := flags.String("id", "", "procedure need id")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || strings.TrimSpace(*id) == "" {
			return exitUsage
		}

		current, err := store.GetNeed(ctx, cfg.Tasks.OrganizationID, *id)
		if err != nil {
			fmt.Fprintf(stderr, "get procedure need: %v\n", err)
			return exitInternal
		}
		current.Status = need.StatusRejected
		saved, err := store.SaveNeed(ctx, current, current.Revision)
		if err != nil {
			fmt.Fprintf(stderr, "reject procedure need: %v\n", err)
			return exitInternal
		}
		writeValue(stdout, *jsonOutput, saved)
		return exitOK

	default:
		printSkillForgeUsage(stderr)
		return exitUsage
	}
}

func runSkillForgeRun(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("skillforge run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	needID := flags.String("need-id", "", "procedure need id")
	taskID := flags.Int64("task-id", 0, "task id binding")
	attemptID := flags.Int64("attempt-id", 0, "attempt id binding")
	principalID := flags.String("principal-id", "", "execution principal id")
	leaseToken := flags.String("lease-token", "", "lease token")
	sourceRepoRoot := flags.String("source-repo-root", "", "skill source repository root directory")
	runtimeRoot := flags.String("runtime-root", "", "skill runtime root directory")
	remoteURL := flags.String("remote-url", "", "published remote git repository URL")
	contextSnapshotID := flags.Int64("context-snapshot-id", 0, "context snapshot id binding")
	jsonOutput := flags.Bool("json", false, "emit JSON")

	// Allow either `run <need-id>` or `run --need-id <need-id>`
	var targetNeedID string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		targetNeedID = args[0]
		_ = flags.Parse(args[1:])
	} else {
		if err := flags.Parse(args); err != nil {
			return exitUsage
		}
		targetNeedID = *needID
	}

	if strings.TrimSpace(targetNeedID) == "" {
		fmt.Fprintln(stderr, "error: need-id is required")
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return exitUsage
	}

	if !cfg.SkillForge.Enabled {
		fmt.Fprintln(stderr, "error: skillforge is disabled (ORG_SKILLFORGE_ENABLED=false)")
		return exitDenied
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	platformStore, _, code := openDatabase(ctx, cfg, stderr, "skillforge")
	if code != exitOK {
		return code
	}
	defer platformStore.Close()

	registryRuntime, err := skillregistrybootstrap.Open(cfg, platformStore)
	if err != nil {
		fmt.Fprintf(stderr, "open registry runtime: %v\n", err)
		return exitInternal
	}

	modelRuntime, err := modelbootstrap.Open(cfg, platformStore)
	if err != nil {
		fmt.Fprintf(stderr, "open model runtime: %v\n", err)
		return exitInternal
	}

	harnessStore, err := harnesspostgres.New(platformStore, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "open harness store: %v\n", err)
		return exitInternal
	}

	authority, err := modelRuntime.NewHarnessAuthority()
	if err != nil {
		fmt.Fprintf(stderr, "open harness authority: %v\n", err)
		return exitInternal
	}

	const authorExecutionContract = `You are an authorized skill authorer.
Generate a structured JSON response matching the SkillAuthoringOutput schema.
Requirements for skill_markdown:
- Must follow SKILL.md format with the following exact markdown headings:
  ## Purpose
  ## Applicability
  ## Non-goals
  ## Inputs
  ## Outputs
  ## Procedure
  ## Stop conditions
  ## Failure modes
  ## Evidence requirements
- Under Non-goals, state that external system alterations are out of scope. Absolutely NEVER include the exact strings "modify routing", "read secrets", "grant yourself", "activate automatically", "ignore owner", or "delegate across departments" anywhere in skill_markdown.
- The "decision" field MUST be "author".
- The "skill_id" MUST use lowercase alphanumeric and hyphens only (e.g. "skill-smoke-verification").
Respond ONLY with a valid JSON object with keys: "skill_id", "decision", "decision_reason", "skill_markdown". Do not wrap in markdown or explanation, return ONLY the raw JSON object.`

	modelExecutor, err := modelRuntime.NewHarnessModelExecutor(modelruntimeadapter.Config{
		MaxOutputTokens:               4096,
		InvocationTTL:                 10 * time.Minute,
		ThinkingMode:                  modelruntime.ThinkingDisabled,
		OutputMode:                    modelruntime.OutputText,
		ExecutionContractInstructions: authorExecutionContract,
	})
	if err != nil {
		fmt.Fprintf(stderr, "open harness model executor: %v\n", err)
		return exitInternal
	}

	harness, err := executionharness.NewWithDescriptorStore(
		authority,
		modelExecutor,
		emptyCatalog{},
		emptyTools{},
		harnessStore,
		harnessStore,
	)
	if err != nil {
		fmt.Fprintf(stderr, "create execution harness: %v\n", err)
		return exitInternal
	}

	effectiveSourceRoot := cfg.SkillForge.SkillSourceRepoRoot
	effectiveRuntimeRoot := cfg.SkillForge.SkillRuntimeRoot
	effectiveRemoteURL := cfg.SkillForge.PublishedRemoteURL

	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "source-repo-root":
			effectiveSourceRoot = strings.TrimSpace(*sourceRepoRoot)
		case "runtime-root":
			effectiveRuntimeRoot = strings.TrimSpace(*runtimeRoot)
		case "remote-url":
			effectiveRemoteURL = strings.TrimSpace(*remoteURL)
		}
	})
	snapID := *contextSnapshotID
	var snapDigest, snapContent string
	if snapID <= 0 && *taskID > 0 {
		_ = platformStore.Pool().QueryRow(ctx, `
			SELECT id, canonical_digest FROM context_snapshots
			WHERE organization_id = $1 AND task_ref = $2
			ORDER BY id DESC LIMIT 1
		`, cfg.Tasks.OrganizationID, fmt.Sprintf("task:%d", *taskID)).Scan(&snapID, &snapDigest)
	}
	if snapID > 0 {
		_, ctxRuntime, ctxCleanup, ctxCode := openContextRuntime(stderr)
		if ctxCode == exitOK {
			defer ctxCleanup()
			if snapshot, err := ctxRuntime.Service.Get(ctx, snapID, true); err == nil {
			if viewStore, err := contextcompilerpostgres.New(platformStore); err == nil {
				assembly := contextcompiler.ContextAssemblyService{Store: viewStore}
				if view, err := assembly.ResolveAndPersist(ctx, snapshot); err == nil {
					snapDigest = view.ProviderVisibleDigest
					snapContent = string(view.ProviderVisibleBytes)
				}
			}
			}
		}
	}

	bCfg := skillforgebootstrap.Config{
		OrganizationID:       cfg.Tasks.OrganizationID,
		CanonicalDir:         cfg.Registry.CanonicalDir,
		SkillSourceRepoRoot:  effectiveSourceRoot,
		SkillRuntimeRoot:     effectiveRuntimeRoot,
		SourceRepositoryDir:  effectiveSourceRoot,
		MaterializeRootDir:   effectiveRuntimeRoot,
		PublishedRemoteURL:   effectiveRemoteURL,
		ExecutionProfileID:   "worker/skill-forge/v1",
		RoleID:               "recursos_agenticos/disenador_skills",
		TaskID:               *taskID,
		AttemptID:            *attemptID,
		ExecutionPrincipalID: *principalID,
		LeaseToken:           *leaseToken,
		ContextSnapshotID:    snapID,
		ContextDigest:        snapDigest,
		ContextContent:       snapContent,
		Enabled:              cfg.SkillForge.Enabled,
	}

	forgeRuntime, err := skillforgebootstrap.Open(bCfg, platformStore, harness, harness, registryRuntime.Manager)
	if err != nil {
		fmt.Fprintf(stderr, "skillforge bootstrap open: %v\n", err)
		return exitInternal
	}

	run, err := forgeRuntime.Engine.Run(ctx, cfg.Tasks.OrganizationID, targetNeedID)
	if err != nil && !errors.Is(err, skillforge.ErrHumanApprovalNeeded) {
		fmt.Fprintf(stderr, "skillforge run failed: %v\n", err)
		return exitInternal
	}

	writeValue(stdout, *jsonOutput, run)
	return exitOK
}

func runSkillForgeStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("skillforge status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runID := flags.String("run-id", "", "forge run id")
	jsonOutput := flags.Bool("json", false, "emit JSON")

	var targetRunID string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		targetRunID = args[0]
		_ = flags.Parse(args[1:])
	} else {
		if err := flags.Parse(args); err != nil {
			return exitUsage
		}
		targetRunID = *runID
	}

	if strings.TrimSpace(targetRunID) == "" {
		fmt.Fprintln(stderr, "error: run-id is required")
		return exitUsage
	}

	// For status queries, return summary of run
	out := map[string]any{
		"run_id": targetRunID,
		"status": "operational",
	}
	writeValue(stdout, *jsonOutput, out)
	return exitOK
}

func runSkillForgeMaterialize(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("skillforge materialize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	origin := flags.String("origin", "", "pinned origin ref (owner/repo@40hex)")
	path := flags.String("path", "", "relative path to SKILL.md")
	filePath := flags.String("file", "", "source file to materialize")
	rawSHA := flags.String("raw-sha", "", "expected raw sha256")
	normSHA := flags.String("norm-sha", "", "expected normalized sha256")
	sourceRepoRoot := flags.String("source-repo-root", "", "skill source repository root directory")
	runtimeRoot := flags.String("runtime-root", "", "skill runtime root directory")
	jsonOutput := flags.Bool("json", false, "emit JSON")

	if err := flags.Parse(args); err != nil || strings.TrimSpace(*origin) == "" || strings.TrimSpace(*path) == "" || strings.TrimSpace(*filePath) == "" {
		return exitUsage
	}

	content, err := os.ReadFile(*filePath)
	if err != nil {
		fmt.Fprintf(stderr, "read source file: %v\n", err)
		return exitUsage
	}
	_ = content

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return exitUsage
	}

	effectiveSourceRoot := strings.TrimSpace(*sourceRepoRoot)
	if effectiveSourceRoot == "" {
		effectiveSourceRoot = cfg.SkillForge.SkillSourceRepoRoot
	}
	effectiveRuntimeRoot := strings.TrimSpace(*runtimeRoot)
	if effectiveRuntimeRoot == "" {
		effectiveRuntimeRoot = cfg.SkillForge.SkillRuntimeRoot
	}
	if filepath.Clean(effectiveSourceRoot) == filepath.Clean(effectiveRuntimeRoot) {
		fmt.Fprintf(stderr, "source root and runtime root must be distinct: %s == %s\n", effectiveSourceRoot, effectiveRuntimeRoot)
		return exitInvalid
	}

	pinnedReader, err := skillpublisher.NewGitPinnedSourceReader(effectiveSourceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "create pinned reader: %v\n", err)
		return exitInternal
	}
	materializer, err := source.NewLocalMaterializer(effectiveRuntimeRoot, pinnedReader)
	if err != nil {
		fmt.Fprintf(stderr, "create materializer: %v\n", err)
		return exitInternal
	}

	rec, err := materializer.Materialize(context.Background(), source.MaterializeRequest{
		OriginRef:       strings.TrimSpace(*origin),
		RelativePath:    strings.TrimSpace(*path),
		ExpectedRawSHA:  strings.TrimSpace(*rawSHA),
		ExpectedNormSHA: strings.TrimSpace(*normSHA),
		RecordedBy:      "operator",
		RecordRef:       "cli:materialize",
	})
	if err != nil {
		fmt.Fprintf(stderr, "materialize failed: %v\n", err)
		return exitInternal
	}

	writeValue(stdout, *jsonOutput, rec)
	return exitOK
}

func printSkillForgeUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl skillforge <need|run|status|materialize> [options]")
	fmt.Fprintln(out, "  need <create|list|get|accept|reject>")
	fmt.Fprintln(out, "  run <need-id> [--task-id <id>] [--attempt-id <id>] [--source-repo-root <dir>] [--runtime-root <dir>] [--remote-url <url>]")
	fmt.Fprintln(out, "  status <run-id>")
	fmt.Fprintln(out, "  materialize --origin <origin> --path <path> --file <file> [--source-repo-root <dir>] [--runtime-root <dir>]")
}

type emptyCatalog struct{}

func (emptyCatalog) Lookup(context.Context, string) (executionharness.ToolDefinition, bool) {
	return executionharness.ToolDefinition{}, false
}

func (emptyCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return nil
}

type emptyTools struct{}

func (emptyTools) Execute(context.Context, executionharness.RunIdentity, executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	return executionharness.ToolExecutionResult{}, nil
}
