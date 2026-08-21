package main

import (
	"context"
	"fmt"
	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	modeldispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"io"
	"os"
	"os/signal"
	"strconv"
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
	worker := coderunner.Worker{Queue: taskService, Reconciler: taskService, Executor: executor, Workspace: workspace, PlanGuardResolver: engineeringmission.GuardResolver{Tasks: taskService, Mission: mission}, WorkerID: workerID, HolderPrincipalID: principal, LeaseDuration: cfg.Tasks.DefaultLeaseDuration, RuntimeVersion: runtimeVersion}
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
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
}
