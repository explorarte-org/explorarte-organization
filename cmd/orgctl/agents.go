package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	agentbudgetpostgres "github.com/Mireuz13/explorarte-organization/internal/agentbudget/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

func runAgents(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printAgentsUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "tree":
		return runAgentsTree(args[1:], stdout, stderr, false)
	case "status":
		return runAgentsTree(args[1:], stdout, stderr, true)
	default:
		fmt.Fprintf(stderr, "unknown agents subcommand %q\n", args[0])
		printAgentsUsage(stderr)
		return exitUsage
	}
}

type agentTreeNode struct {
	TaskID        int64  `json:"task_id"`
	AssignedRole  string `json:"assigned_role_id"`
	Status        string `json:"status"`
	CausationID   string `json:"causation_id"`
	CorrelationID string `json:"correlation_id"`
}

type agentTreeResult struct {
	RootTaskID int64               `json:"root_task_id"`
	Tasks      []agentTreeNode     `json:"tasks"`
	Budget     *agentbudget.Budget `json:"budget,omitempty"`
}

func runAgentsTree(args []string, stdout, stderr io.Writer, includeBudget bool) int {
	name := "agents tree"
	if includeBudget {
		name = "agents status"
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	taskID := flags.Int64("task", 0, "any task id in the tree (the root, or a delegated task)")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *taskID <= 0 || flags.NArg() != 0 {
		fmt.Fprintf(stderr, "usage: orgctl %s --task <id> [--json]\n", name)
		return exitUsage
	}

	cfg, service, cleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()

	detail, err := service.GetTask(ctx, *taskID)
	if err != nil {
		fmt.Fprintf(stderr, "get task %d: %v\n", *taskID, err)
		return exitInternal
	}
	if detail.Task.CorrelationID == nil {
		fmt.Fprintf(stderr, "task %d has no correlation id\n", *taskID)
		return exitInternal
	}
	correlated, err := service.ListTasks(ctx, tasks.TaskFilter{OrganizationID: cfg.Tasks.OrganizationID, CorrelationID: *detail.Task.CorrelationID, Limit: 500})
	if err != nil {
		fmt.Fprintf(stderr, "list correlated tasks: %v\n", err)
		return exitInternal
	}

	result := agentTreeResult{RootTaskID: *taskID, Tasks: make([]agentTreeNode, 0, len(correlated))}
	for _, t := range correlated {
		causation := ""
		if t.CausationID != nil {
			causation = *t.CausationID
		}
		result.Tasks = append(result.Tasks, agentTreeNode{
			TaskID: t.ID, AssignedRole: t.AssignedRoleID, Status: string(t.Status),
			CausationID: causation, CorrelationID: *t.CorrelationID,
		})
	}

	if includeBudget {
		store, runner, dbCode := openDatabase(ctx, cfg, stderr, "agents")
		if dbCode != exitOK {
			return dbCode
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
		ledger, err := agentbudgetpostgres.New(store)
		if err != nil {
			fmt.Fprintf(stderr, "create agent budget ledger: %v\n", err)
			return exitInternal
		}
		budget, err := ledger.ResolveBudgetForTask(ctx, *taskID)
		if err != nil && !errors.Is(err, agentbudget.ErrBudgetNotFound) {
			fmt.Fprintf(stderr, "resolve budget: %v\n", err)
			return exitInternal
		}
		if err == nil {
			result.Budget = &budget
		}
	}

	writeValue(stdout, *jsonOutput, result)
	return exitOK
}

func printAgentsUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl agents <tree|status> --task <id> [--json]

  tree --task <id> [--json]
      List every task sharing the given task's correlation id (its whole
      CEO->coordinador->worker execution tree), with role, status, and
      causation.

  status --task <id> [--json]
      Same as tree, plus the multidimensional budget that task's tree
      resolves to, if any.`)
}
