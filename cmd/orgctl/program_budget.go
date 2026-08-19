package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/staging/gitexec"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const programTargetRef = "refs/heads/v2/program-context-memory-001"

func validProgramTargetRef(ref string) bool {
	return ref == programTargetRef
}

func runProgram(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: orgctl program <budget|promotion> ...")
		return exitUsage
	}
	if args[0] == "promotion" {
		if len(args) >= 2 && args[1] == "worker" {
			return runProgramPromotionWorker(args[2:], stdout, stderr)
		}
		return runProgramPromotion(args[1:], stdout, stderr)
	}
	if args[0] != "budget" {
		return exitUsage
	}
	cfg, service, cleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	switch args[1] {
	case "attach":
		if len(args) != 4 || args[2] == "" || args[3] == "" {
			fmt.Fprintln(stderr, "usage: orgctl program budget attach ROOT_TASK_ID --file POLICY.json")
			return exitUsage
		}
		var root int64
		if _, err := fmt.Sscan(args[2], &root); err != nil || root <= 0 {
			return exitUsage
		}
		raw, err := os.ReadFile(args[3])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		var p programbudget.Policy
		if err = json.Unmarshal(raw, &p); err != nil || p.ProgramRootTaskID != root || p.SchemaVersion != programbudget.SchemaVersion || p.Validate() != nil {
			fmt.Fprintln(stderr, "invalid program budget policy")
			return exitUsage
		}
		var meta map[string]any
		if err = json.Unmarshal(raw, &meta); err != nil {
			return exitInternal
		}
		_, err = service.RecordEvidence(ctx, tasks.RecordEvidenceCommand{TaskID: root, Type: tasks.RequirementResult, Reference: fmt.Sprintf("program-model-budget://%d", root), RecordedBy: "operator", Metadata: meta})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInternal
		}
		writeValue(stdout, true, map[string]any{"root_task_id": root, "schema": programbudget.SchemaVersion})
		return exitOK
	case "show":
		if len(args) != 3 {
			return exitUsage
		}
		var root int64
		if _, err := fmt.Sscan(args[2], &root); err != nil {
			return exitUsage
		}
		p, err := (programbudget.Resolver{Tasks: service}).Policy(ctx, root)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInternal
		}
		if p.SchemaVersion == "" {
			fmt.Fprintln(stderr, "program budget policy not found")
			return exitInternal
		}
		writeValue(stdout, true, p)
		return exitOK
	default:
		return exitUsage
	}
}

// runProgramPromotion is the only application surface allowed to apply an
// approved candidate. It is deliberately separate from engineeringmission,
// and its target is a single non-production program ref.
func runProgramPromotion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 5 || args[0] != "apply" || args[2] != "--actor-role" || strings.TrimSpace(args[3]) == "" {
		fmt.Fprintln(stderr, "usage: orgctl program promotion apply PROMOTION_ID --actor-role ROLE [--json]")
		return exitUsage
	}
	id, err := positiveID(args[1], "promotion")
	if err != nil || args[4] != "--json" {
		return exitUsage
	}
	cfg, runtime, cleanup, code := openStagingRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Staging.CommandTimeout)
	defer cancel()
	promotion, err := runtime.Service.GetPromotion(ctx, id)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInternal
	}
	workspace, err := runtime.Service.GetWorkspace(ctx, promotion.WorkspaceID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInternal
	}
	if !validProgramTargetRef(promotion.TargetRef) {
		fmt.Fprintln(stderr, "program promotion target denied")
		return exitDenied
	}
	if promotion.Status != staging.PromotionApproved || workspace.Status != staging.WorkspaceSealed || promotion.CandidateCommit == "" || promotion.ExpectedBaseCommit != workspace.BaseCommit || workspace.ActorRoleID == args[3] || promotion.ApprovedByRoleID == nil || *promotion.ApprovedByRoleID == workspace.ActorRoleID {
		fmt.Fprintln(stderr, "program promotion preconditions denied")
		return exitDenied
	}
	result, err := runtime.Service.ApplyPromotion(ctx, staging.ApplyPromotionCommand{PromotionID: id, ActorRoleID: args[3]})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInternal
	}
	writeValue(stdout, true, result)
	return exitOK
}

const programPromotionEvidenceSchema = "program-promotion-controller/v1"

func programPromotionEvidenceReference(promotionID int64, outcome string) string {
	return fmt.Sprintf(
		"program-promotion-controller://promotion/%d/%s",
		promotionID,
		outcome,
	)
}

func hasProgramPromotionEvidence(
	ctx context.Context,
	taskService *tasks.Service,
	promotion staging.Promotion,
	outcome string,
) (bool, error) {
	detail, err := taskService.GetTask(ctx, promotion.TaskID)
	if err != nil {
		return false, err
	}
	ref := programPromotionEvidenceReference(promotion.ID, outcome)
	for _, evidence := range detail.Evidence {
		if evidence.Reference == ref {
			return true, nil
		}
	}
	return false, nil
}

func boundedProgramPromotionReason(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 4000 {
		value = value[:4000]
	}
	return value
}

func recordProgramPromotionEvidence(
	ctx context.Context,
	taskService *tasks.Service,
	promotion staging.Promotion,
	actor string,
	outcome string,
	reason string,
) error {
	exists, err := hasProgramPromotionEvidence(
		ctx,
		taskService,
		promotion,
		outcome,
	)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	metadata := map[string]any{
		"schema_version":   programPromotionEvidenceSchema,
		"promotion_id":     promotion.ID,
		"mission_task_id":  promotion.TaskID,
		"previous_commit":  promotion.ExpectedBaseCommit,
		"candidate_commit": promotion.CandidateCommit,
		"target_ref":       promotion.TargetRef,
		"outcome":          outcome,
	}
	if reason = boundedProgramPromotionReason(reason); reason != "" {
		metadata["reason"] = reason
	}

	_, err = taskService.RecordEvidence(
		ctx,
		tasks.RecordEvidenceCommand{
			TaskID:     promotion.TaskID,
			Type:       tasks.RequirementResult,
			Reference:  programPromotionEvidenceReference(promotion.ID, outcome),
			RecordedBy: actor,
			Metadata:   metadata,
		},
	)
	return err
}

func runProgramPostApplySmoke(
	ctx context.Context,
	workspaceRoot string,
	timeout time.Duration,
	workspace staging.Workspace,
) error {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	smokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	workspacePath := filepath.Join(
		workspaceRoot,
		workspace.WorkspaceKey,
	)

	// Deliberately hard-coded and non-model-controlled. This is an
	// independent post-apply core smoke, not one of the mission-selected
	// RequiredGates.
	command := exec.CommandContext(
		smokeCtx,
		"go",
		"test",
		"./cmd/orgctl",
		"./internal/staging/...",
		"./internal/engineeringmission/...",
	)
	command.Dir = workspacePath
	command.Env = os.Environ()

	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"post-apply smoke failed: %w: %s",
			err,
			boundedProgramPromotionReason(string(output)),
		)
	}
	return nil
}

func rollbackProgramPromotion(
	ctx context.Context,
	backend *gitexec.Backend,
	catalog staging.RepositoryCatalog,
	promotion staging.Promotion,
) error {
	repository, _, err := catalog.Get(
		ctx,
		promotion.RepositoryID,
	)
	if err != nil {
		return err
	}

	// Reverse the exact successful apply using the same safe CAS primitive:
	//
	// candidate -> previous
	//
	// If somebody moved the ref after the candidate was applied, this
	// refuses to overwrite that newer state.
	result, err := backend.PromoteRef(
		ctx,
		staging.PromotionRefRequest{
			Repository:         repository,
			TargetRef:          promotion.TargetRef,
			CandidateCommit:    promotion.ExpectedBaseCommit,
			ExpectedBaseCommit: promotion.CandidateCommit,
		},
	)
	if err != nil {
		return err
	}
	if !result.Applied ||
		result.Current != promotion.ExpectedBaseCommit {
		return fmt.Errorf(
			"rollback CAS did not restore previous commit: current=%s",
			result.Current,
		)
	}
	return nil
}

func reconcileAppliedProgramPromotion(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	cfgWorkspaceRoot string,
	commandTimeout time.Duration,
	backend *gitexec.Backend,
	catalog staging.RepositoryCatalog,
	taskService *tasks.Service,
	actor string,
	promotion staging.Promotion,
	workspace staging.Workspace,
) error {
	pending, err := hasProgramPromotionEvidence(
		ctx,
		taskService,
		promotion,
		"pending",
	)
	if err != nil {
		return err
	}

	// Promotions created before this controller revision deliberately have
	// no pending receipt and are not retroactively re-executed.
	if !pending {
		return nil
	}

	for _, terminal := range []string{
		"smoke-passed",
		"rolled-back",
	} {
		done, err := hasProgramPromotionEvidence(
			ctx,
			taskService,
			promotion,
			terminal,
		)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}

	smokeErr := runProgramPostApplySmoke(
		ctx,
		cfgWorkspaceRoot,
		commandTimeout,
		workspace,
	)
	if smokeErr == nil {
		if err := recordProgramPromotionEvidence(
			ctx,
			taskService,
			promotion,
			actor,
			"smoke-passed",
			"",
		); err != nil {
			return err
		}

		fmt.Fprintf(
			stdout,
			"program promotion %d post-apply smoke passed candidate=%s\n",
			promotion.ID,
			promotion.CandidateCommit,
		)
		return nil
	}

	if err := rollbackProgramPromotion(
		ctx,
		backend,
		catalog,
		promotion,
	); err != nil {
		_ = recordProgramPromotionEvidence(
			ctx,
			taskService,
			promotion,
			actor,
			"rollback-failed",
			errors.Join(smokeErr, err).Error(),
		)
		return fmt.Errorf(
			"post-apply smoke failed and rollback failed: %w",
			errors.Join(smokeErr, err),
		)
	}

	if err := recordProgramPromotionEvidence(
		ctx,
		taskService,
		promotion,
		actor,
		"rolled-back",
		smokeErr.Error(),
	); err != nil {
		return err
	}

	fmt.Fprintf(
		stderr,
		"program promotion %d post-apply smoke failed; restored %s from candidate %s\n",
		promotion.ID,
		promotion.ExpectedBaseCommit,
		promotion.CandidateCommit,
	)
	return nil
}

func runProgramPromotionWorker(
	args []string,
	stdout, stderr io.Writer,
) int {
	if len(args) != 1 || args[0] != "run" {
		return exitUsage
	}

	actor := strings.TrimSpace(
		os.Getenv("ORG_PROGRAM_PROMOTION_ACTOR_ROLE"),
	)
	if actor == "" {
		fmt.Fprintln(
			stderr,
			"ORG_PROGRAM_PROMOTION_ACTOR_ROLE is required",
		)
		return exitUsage
	}

	cfg, runtime, cleanup, code := openStagingRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()

	_, taskService, taskCleanup, code := openTaskService(stderr)
	if code != exitOK {
		return code
	}
	defer taskCleanup()

	gitBackend, err := gitexec.New(
		cfg.Staging.GitBinary,
		cfg.Staging.WorkspaceRoot,
		cfg.Staging.CommandTimeout,
	)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInternal
	}

	interval := 5 * time.Second
	if raw := os.Getenv(
		"ORG_PROGRAM_PROMOTION_POLL_INTERVAL",
	); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			interval = d
		}
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	for {
		// First recover any promotion that was durably marked pending,
		// successfully applied, and then interrupted before its post-apply
		// smoke/rollback receipt was persisted.
		applied, err := runtime.Service.ListPromotions(
			ctx,
			staging.PromotionFilter{
				Status: staging.PromotionApplied,
				Limit:  100,
			},
		)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInternal
		}

		for _, promotion := range applied {
			if !validProgramTargetRef(promotion.TargetRef) {
				continue
			}

			pending, err := hasProgramPromotionEvidence(
				ctx,
				taskService,
				promotion,
				"pending",
			)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitInternal
			}
			if !pending {
				continue
			}

			workspace, err := runtime.Service.GetWorkspace(
				ctx,
				promotion.WorkspaceID,
			)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitInternal
			}

			if err := reconcileAppliedProgramPromotion(
				ctx,
				stdout,
				stderr,
				cfg.Staging.WorkspaceRoot,
				cfg.Staging.CommandTimeout,
				gitBackend,
				runtime.Catalog,
				taskService,
				actor,
				promotion,
				workspace,
			); err != nil {
				fmt.Fprintln(stderr, err)
				return exitInternal
			}
		}

		promotions, err := runtime.Service.ListPromotions(
			ctx,
			staging.PromotionFilter{
				Status: staging.PromotionApproved,
				Limit:  100,
			},
		)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInternal
		}

		for _, promotion := range promotions {
			if !validProgramTargetRef(promotion.TargetRef) {
				continue
			}

			workspace, err := runtime.Service.GetWorkspace(
				ctx,
				promotion.WorkspaceID,
			)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitInternal
			}

			if workspace.Status != staging.WorkspaceSealed ||
				promotion.CandidateCommit == "" ||
				promotion.ExpectedBaseCommit != workspace.BaseCommit ||
				promotion.ApprovedByRoleID == nil ||
				*promotion.ApprovedByRoleID == workspace.ActorRoleID ||
				actor == workspace.ActorRoleID {
				continue
			}

			// Persist previous/candidate/promotion/mission identity BEFORE
			// moving the ref. This is the restart boundary.
			if err := recordProgramPromotionEvidence(
				ctx,
				taskService,
				promotion,
				actor,
				"pending",
				"",
			); err != nil {
				fmt.Fprintln(stderr, err)
				return exitInternal
			}

			result, err := runtime.Service.ApplyPromotion(
				ctx,
				staging.ApplyPromotionCommand{
					PromotionID: promotion.ID,
					ActorRoleID: actor,
				},
			)
			if err != nil {
				_ = recordProgramPromotionEvidence(
					ctx,
					taskService,
					promotion,
					actor,
					"apply-failed",
					err.Error(),
				)
				fmt.Fprintln(stderr, err)
				continue
			}

			writeValue(stdout, true, result)

			if err := reconcileAppliedProgramPromotion(
				ctx,
				stdout,
				stderr,
				cfg.Staging.WorkspaceRoot,
				cfg.Staging.CommandTimeout,
				gitBackend,
				runtime.Catalog,
				taskService,
				actor,
				result,
				workspace,
			); err != nil {
				fmt.Fprintln(stderr, err)
				return exitInternal
			}
		}

		select {
		case <-ctx.Done():
			return exitOK
		case <-time.After(interval):
		}
	}
}
