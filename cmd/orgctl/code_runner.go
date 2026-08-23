package main

import (
	"context"
	"fmt"
	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	costledgerpostgres "github.com/Mireuz13/explorarte-organization/internal/costledger/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	modeldispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	modelruntimepostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// codeRunnerOperationTimeout and codeRunnerPlanOutputBudget are trusted
// runtime configuration read from the worker's own environment, never from
// a claimed task's instructions. They are deliberately separate from lease
// TTL: lease TTL is authority liveness, this is an execution resource
// boundary.
const (
	defaultCodeRunnerOperationTimeout = 5 * time.Minute
	defaultCodeRunnerPlanOutputBudget = int64(64 << 20) // 64 MiB aggregate real bytes per plan
)

func codeRunnerOperationTimeout() time.Duration {
	if raw := os.Getenv("ORG_CODE_RUNNER_OPERATION_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultCodeRunnerOperationTimeout
}

func codeRunnerPlanOutputBudget() int64 {
	if raw := os.Getenv("ORG_CODE_RUNNER_PLAN_OUTPUT_BUDGET_BYTES"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return defaultCodeRunnerPlanOutputBudget
}

const (
	// recoverySweepInterval keeps an idle runner from re-deciding the same
	// dead letters twice a second. Recovery is about failures that already
	// happened; nothing about them changes between two passes.
	recoverySweepInterval = 60 * time.Second
	recoverySweepBatch    = 200
	// defaultCodeRunnerMaxRecoveryEpisodes permits ONE successor per
	// original failure by default. A single successor is what turns a
	// transient failure against a since-fixed target into completed work;
	// a chain longer than that is a policy decision a deployment has to
	// make deliberately.
	defaultCodeRunnerMaxRecoveryEpisodes = 1
)

// codeRunnerMaxRecoveryEpisodes bounds how many successor episodes one
// original failure may spawn. Zero disables recovery entirely.
//
// An unset variable takes the default. A SET but unparseable variable
// disables recovery instead of falling back to the default: the operator
// clearly meant to say something about this limit, and the one outcome that
// must never follow from a typo is leaving autonomous succession switched on
// for someone who believed they had turned it off.
func codeRunnerMaxRecoveryEpisodes(stderr io.Writer) int {
	raw, present := os.LookupEnv("ORG_CODE_RUNNER_MAX_RECOVERY_EPISODES")
	if !present {
		return defaultCodeRunnerMaxRecoveryEpisodes
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		fmt.Fprintln(stderr, "ORG_CODE_RUNNER_MAX_RECOVERY_EPISODES is not a non-negative integer; recovery is disabled")
		return 0
	}
	return n
}

func runCodeRunner(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "worker" || args[1] != "run" {
		fmt.Fprintln(stderr, "usage: orgctl code-runner worker run")
		return exitUsage
	}
	cfg, stagingRuntime, stagingCleanup, code := openStagingRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer stagingCleanup()
	_, taskService, taskCleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer taskCleanup()
	principal := os.Getenv("ORG_CODE_RUNNER_PRINCIPAL_ID")
	workerID := os.Getenv("ORG_CODE_RUNNER_WORKER_ID")
	repo := os.Getenv("ORG_CODE_RUNNER_REPOSITORY_ID")
	base := os.Getenv("ORG_CODE_RUNNER_BASE_COMMIT")
	target := os.Getenv("ORG_CODE_RUNNER_TARGET_REF")
	if principal == "" || workerID == "" || repo == "" || base == "" || target == "" {
		fmt.Fprintln(stderr, "code-runner trusted configuration is incomplete")
		return exitUsage
	}
	pid, err := strconv.ParseInt(principal, 10, 64)
	if err != nil || pid <= 0 {
		return exitUsage
	}
	checkCtx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	pstore, runner, code := openDatabase(checkCtx, cfg, stderr, "code-runner-principal")
	if code != exitOK {
		return code
	}
	defer pstore.Close()
	status, err := runner.Status(checkCtx)
	if err != nil || !status.Ready {
		return exitDrift
	}
	principalStore, err := modeldispatchpostgres.New(pstore)
	if err != nil {
		return exitInternal
	}
	canonical, err := principalStore.GetPrincipal(checkCtx, pid)
	if err != nil || string(canonical.Status) != "active" || canonical.OrganizationID != cfg.Tasks.OrganizationID || canonical.DispatchActorRoleID != coderunner.RoleID {
		fmt.Fprintln(stderr, "code-runner principal is not active/canonical")
		return exitDenied
	}
	executor := &coderunner.Executor{Workspace: "", MaxOutput: 1 << 20, OperationTimeout: codeRunnerOperationTimeout(), PlanOutputBudget: codeRunnerPlanOutputBudget()}
	runtimeVersion := os.Getenv("ORG_CODE_RUNNER_RUNTIME_VERSION")
	mission := engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service}
	workspace := coderunner.StagingAdapter{Service: stagingRuntime.Service, Tasks: taskService, WorkspaceRoot: cfg.Staging.WorkspaceRoot, RepositoryID: repo, BaseCommit: base, TargetRef: target, IntentResolver: engineeringmission.WorkspaceResolver{Tasks: taskService, Mission: mission, RepositoryID: repo, TargetRef: target}}
	// Recovery by succession: a mission that exhausted its attempts stays
	// dead-lettered forever, and a NEW mission is opened for it only when
	// the runtime itself classified the failure retryable AND the target
	// has moved past the commit that mission was pinned to.
	//
	// The head is read through the runtime's OWN catalog and git backend
	// (see bootstrap.Runtime.Git) so that "what the target points at" has a
	// single definition in this process.
	ledgerStore, err := costledgerpostgres.New(pstore)
	if err != nil {
		return exitInternal
	}
	invocationStore, err := modelruntimepostgres.New(pstore)
	if err != nil {
		return exitInternal
	}
	recovery := engineeringmission.Recovery{
		Tasks:      taskService,
		Mission:    mission,
		Head:       engineeringmission.StagingHead{Catalog: stagingRuntime.Catalog, Backend: stagingRuntime.Git},
		Workspaces: stagingRuntime.Service,
		Ambiguity:  invocationStore,
		Budget: engineeringmission.ProgramBudgetAdmission{
			Programs: programbudget.Resolver{Tasks: taskService},
			Spend:    ledgerStore,
		},

		MaxRecoveryEpisodes: codeRunnerMaxRecoveryEpisodes(stderr),
		Settled:             map[int64]engineeringmission.RecoveryReason{},
		// No fallback requester: a mission whose own requester was
		// never recorded is left unattributed rather than credited to
		// whatever role happened to be in scope here.
		RequestedBy: "",
		ActorType:   "system",
		ActorID:     workerID,
	}
	worker := coderunner.Worker{Queue: taskService, Reconciler: taskService, Executor: executor, Workspace: workspace, PlanGuardResolver: engineeringmission.GuardResolver{Tasks: taskService, Mission: mission}, WorkerID: workerID, HolderPrincipalID: principal, LeaseDuration: cfg.Tasks.DefaultLeaseDuration, RuntimeVersion: runtimeVersion}
	var lastRecoverySweep time.Time
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stdout, "code-runner worker starting: role=%s worker=%s\n", coderunner.RoleID, workerID)
	for {
		select {
		case <-ctx.Done():
			return exitOK
		default:
			n, err := worker.RunOnce(ctx)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitInternal
			}
			if n == 0 {
				// Recovery runs only on an idle pass, and never
				// blocks claiming work. It is a sweep over failures
				// that already happened, so being a few seconds
				// late costs nothing, while running it ahead of
				// live work would delay every mission behind it.
				//
				// Its errors are reported and dropped for the same
				// reason the queue reconciler's are: refusing to
				// serve the queue because a recovery decision could
				// not be reached would turn one unrecoverable dead
				// letter into a stalled runner.
				if recovery.MaxRecoveryEpisodes > 0 && time.Since(lastRecoverySweep) >= recoverySweepInterval {
					lastRecoverySweep = time.Now()
					decisions, recoverErr := recovery.RecoverPending(ctx, recoverySweepBatch)
					if recoverErr != nil {
						fmt.Fprintf(stderr, "recovery sweep: %v\n", recoverErr)
					}
					// A sweep that refuses everything must still say so.
					// Otherwise the only evidence that recovery runs at
					// all is the absence of successors -- and an absent
					// successor is equally consistent with a sweep that
					// refused every candidate and one that never ran.
					// For a mechanism whose whole job is deciding when
					// autonomous work may be created, "I declined and
					// told nobody" is not an acceptable resting state.
					counts := map[engineeringmission.RecoveryReason]int{}
					for _, decision := range decisions {
						counts[decision.Reason]++
						if decision.Eligible() {
							fmt.Fprintf(stdout, "recovery successor opened: dead_letter=%d recovered_task=%d change=%q\n",
								decision.DeadLetterID, decision.TaskID, decision.ObservedChange)
						}
					}
					if len(decisions) > 0 {
						reasons := make([]string, 0, len(counts))
						for reason, count := range counts {
							reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
						}
						sort.Strings(reasons)
						fmt.Fprintf(stdout, "recovery sweep: considered=%d %s\n",
							len(decisions), strings.Join(reasons, " "))
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
}
