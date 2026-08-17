package main

import (
	"context"
	"fmt"
	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	modeldispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

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
	executor := &coderunner.Executor{Workspace: "", MaxOutput: 1 << 20}
	worker := coderunner.Worker{Queue: taskService, Executor: executor, Workspace: coderunner.StagingAdapter{Service: stagingRuntime.Service, WorkspaceRoot: cfg.Staging.WorkspaceRoot, RepositoryID: repo, BaseCommit: base, TargetRef: target}, WorkerID: workerID, HolderPrincipalID: principal, LeaseDuration: cfg.Tasks.DefaultLeaseDuration}
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
