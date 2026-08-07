package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/shadowverifier"
	shadowpostgres "github.com/Mireuz13/explorarte-organization/internal/shadowverifier/postgres"
)

// shadowProbeDigest is a fixed, valid action digest used to satisfy
// authorization evaluation input validation. The shadow comparison only
// exercises the policy core (grants/hard denies/approval), which never
// inspects the digest value.
var shadowProbeDigest = func() string {
	sum := sha256.Sum256([]byte("shadow-verifier:probe"))
	return hex.EncodeToString(sum[:])
}()

func printShadowUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, `usage: orgctl shadow <command> [options]
commands:
   verify  [--sample N] [--max-comparisons N] [--json]   exhaustive parity run over the current fact space
   replay  [--limit N] [--json]                           replay recorded authorization_requests traffic
   report  [--run ID] [--limit N] [--json]                read persisted runs and findings`)
}

// runShadow is Rama 17's composition root. It is the one place allowed to
// wire both sides of the comparison: the shadow service (whose derivations
// never import internal/organization/registry or internal/authorization) and
// the real engines, exposed behind shadowverifier.GroundTruth. Enforcement
// stays observe_and_compare_only — every subcommand only reports and persists
// findings to shadowverifier's own tables.
func runShadow(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printShadowUsage(stderr)
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Authorization.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "shadow")
	if code != exitOK {
		return code
	}
	defer store.Close()
	status, err := runner.Status(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return exitInternal
	}
	if !status.Ready {
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return exitDrift
	}

	matrix, err := shadowverifier.LoadMatrix(cfg.Registry.CanonicalDir)
	if err != nil {
		fmt.Fprintf(stderr, "load capability matrix: %v\n", err)
		return exitInvalid
	}
	leaderMap, err := shadowverifier.LoadLeaderWorkerMap(cfg.Registry.CanonicalDir)
	if err != nil {
		fmt.Fprintf(stderr, "load leader-worker-map: %v\n", err)
		return exitInvalid
	}

	reader, err := shadowpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "create shadow verifier store: %v\n", err)
		return exitInternal
	}

	if args[0] == "report" {
		return shadowReport(ctx, reader, args[1:], stdout, stderr)
	}

	runtime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "open authorization runtime: %v\n", err)
		return exitInternal
	}
	loader, err := registry.NewLoader(cfg.Registry.CanonicalDir)
	if err != nil {
		fmt.Fprintf(stderr, "create canonical loader: %v\n", err)
		return exitInternal
	}
	repository, err := registry.NewPostgresRepository(store)
	if err != nil {
		fmt.Fprintf(stderr, "create registry repository: %v\n", err)
		return exitInternal
	}
	registryService, err := registry.NewService(loader, repository, cfg.Tasks.OrganizationID, cfg.Registry.SyncTimeout)
	if err != nil {
		fmt.Fprintf(stderr, "create registry service: %v\n", err)
		return exitInternal
	}
	ground := &shadowGround{registry: registryService, authorizer: runtime.Authorizer, organizationID: cfg.Tasks.OrganizationID}

	switch args[0] {
	case "verify":
		return shadowVerify(ctx, reader, ground, matrix, leaderMap, cfg, args[1:], stdout, stderr)
	case "replay":
		return shadowReplay(ctx, reader, ground, matrix, leaderMap, cfg, args[1:], stdout, stderr)
	default:
		printShadowUsage(stderr)
		return exitUsage
	}
}

func newShadowService(reader *shadowpostgres.Store, ground shadowverifier.GroundTruth, matrix shadowverifier.MatrixIndex, leaderMap shadowverifier.LeaderWorkerMapFact, cfg config.Config, sampleRate, maxComparisons, replayLimit int) (*shadowverifier.Service, error) {
	return shadowverifier.NewService(reader, reader, reader, ground, matrix, leaderMap, cfg.Tasks.OrganizationID, sampleRate, maxComparisons, replayLimit, nil)
}

func shadowVerify(ctx context.Context, reader *shadowpostgres.Store, ground shadowverifier.GroundTruth, matrix shadowverifier.MatrixIndex, leaderMap shadowverifier.LeaderWorkerMapFact, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("shadow verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sampleRate := flags.Int("sample", 1, "compare a deterministic 1-in-N sample of capability pairs")
	maxComparisons := flags.Int("max-comparisons", 0, "cap capability pair comparisons (0 = default)")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		return exitUsage
	}
	service, err := newShadowService(reader, ground, matrix, leaderMap, cfg, *sampleRate, *maxComparisons, 0)
	if err != nil {
		fmt.Fprintf(stderr, "create shadow verifier service: %v\n", err)
		return exitInternal
	}
	report, err := service.VerifyExhaustive(ctx)
	return shadowFinishRun(report, err, *jsonOutput, stdout, stderr)
}

func shadowReplay(ctx context.Context, reader *shadowpostgres.Store, ground shadowverifier.GroundTruth, matrix shadowverifier.MatrixIndex, leaderMap shadowverifier.LeaderWorkerMapFact, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("shadow replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", 0, "maximum recorded requests to replay (0 = default)")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		return exitUsage
	}
	service, err := newShadowService(reader, ground, matrix, leaderMap, cfg, 1, 0, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "create shadow verifier service: %v\n", err)
		return exitInternal
	}
	report, err := service.ReplayRecorded(ctx)
	return shadowFinishRun(report, err, *jsonOutput, stdout, stderr)
}

func shadowFinishRun(report shadowverifier.RunReport, err error, jsonOutput bool, stdout, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintf(stderr, "shadow verification failed: %v\n", err)
		if errors.Is(err, shadowverifier.ErrOrganizationRetired) {
			return exitInvalid
		}
		if registry.IsDatabaseUnavailable(err) {
			return exitDatabase
		}
		return exitInternal
	}
	writeValue(stdout, jsonOutput, report)
	if report.Divergences() > 0 {
		return exitShadowDivergence
	}
	return exitOK
}

func shadowReport(ctx context.Context, reader *shadowpostgres.Store, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("shadow report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runID := flags.Int64("run", 0, "read one run with its findings")
	limit := flags.Int("limit", 20, "runs to list")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		return exitUsage
	}
	if *runID != 0 {
		record, summary, err := reader.GetRun(ctx, *runID)
		if err != nil {
			return shadowReportError(stderr, err)
		}
		findings, err := reader.RunFindings(ctx, *runID)
		if err != nil {
			return shadowReportError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, map[string]any{"run": record, "summary": summary, "findings": findings})
		return exitOK
	}
	runs, err := reader.ListRuns(ctx, *limit)
	if err != nil {
		return shadowReportError(stderr, err)
	}
	writeValue(stdout, *jsonOutput, runs)
	return exitOK
}

func shadowReportError(stderr io.Writer, err error) int {
	if errors.Is(err, shadowverifier.ErrRunNotFound) {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	if registry.IsDatabaseUnavailable(err) {
		fmt.Fprintf(stderr, "shadow verifier database unavailable: %v\n", err)
		return exitDatabase
	}
	fmt.Fprintf(stderr, "shadow report failed: %v\n", err)
	return exitInternal
}

// shadowGround implements shadowverifier.GroundTruth with the real engines.
// It exists only here, at the composition root: the shadow derivations
// themselves never touch these packages.
type shadowGround struct {
	registry       *registry.Service
	authorizer     *authorization.Authorizer
	organizationID string
	revision       int64
}

func (g *shadowGround) RoleExists(ctx context.Context, roleID string) (bool, error) {
	_, err := g.registry.GetRole(ctx, roleID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, registry.ErrNotFound) {
		return false, nil
	}
	return false, err
}

func (g *shadowGround) DepartmentExists(ctx context.Context, unitID string) (bool, error) {
	_, err := g.registry.GetUnit(ctx, unitID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, registry.ErrNotFound) {
		return false, nil
	}
	return false, err
}

func (g *shadowGround) LeaderOf(ctx context.Context, unitID string) (string, bool, error) {
	role, err := g.registry.GetLeader(ctx, unitID)
	if err == nil {
		return role.ID, true, nil
	}
	if errors.Is(err, registry.ErrNotFound) {
		return "", false, nil
	}
	return "", false, err
}

func (g *shadowGround) EvaluateCapability(ctx context.Context, roleID, capabilityID string) (string, string, error) {
	if g.revision == 0 {
		revision, err := g.registry.GetCurrentRevision(ctx)
		if err != nil {
			return "", "", err
		}
		if revision == nil {
			return "", "", errors.New("organization has no applied revision")
		}
		g.revision = revision.ID
	}
	resourceType := "shadow_probe"
	if capabilityID == "model.invoke" {
		resourceType = "model_invocation"
	}
	result, err := g.authorizer.Evaluate(ctx, authorization.EvaluationRequest{
		OrganizationID:         g.organizationID,
		OrganizationRevisionID: g.revision,
		ActorRoleID:            roleID,
		CapabilityID:           capabilityID,
		ResourceType:           resourceType,
		ResourceID:             roleID + ":" + capabilityID,
		ActionDigest:           shadowProbeDigest,
	})
	if err != nil {
		return "", "", err
	}
	return string(result.Effect), string(result.ReasonCode), nil
}

// CanonicalReportingClosed reuses the canonical loader's static validation —
// the same validateReportingGraph pass that runs at sync time — as the
// reference verdict for dependency_closed.
func (g *shadowGround) CanonicalReportingClosed(ctx context.Context) (bool, []string, error) {
	_, report, err := g.registry.ValidateCanonical()
	if err != nil {
		var validation *registry.ValidationError
		if !errors.As(err, &validation) {
			return false, nil, err
		}
		report = validation.Report
	}
	var issues []string
	for _, issue := range report.Errors {
		if strings.HasPrefix(issue.Code, "reporting.") {
			issues = append(issues, issue.Code+"@"+issue.Path)
		}
	}
	return len(issues) == 0, issues, nil
}
