package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	stagingbootstrap "github.com/Mireuz13/explorarte-organization/internal/staging/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/staging/gitexec"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// Mission provisioning is wired here rather than assumed. The Executive holds
// the ports; without a concrete implementation behind them
// driveImplementationMission blocks the run with
// mission_provisioning_unavailable, which is honest but means a deployment
// that intends to self-modify never can.
//
// Both env values are trusted configuration, read from the process
// environment and never from a model or a task: they name WHICH repository
// and WHICH ref a mission may be based on.
const (
	missionRepositoryEnv = "ORG_MISSION_REPOSITORY_ID"
	missionTargetRefEnv  = "ORG_MISSION_TARGET_REF"
)

// programTargetResolver answers "what commit is the promotion target at right
// now" by reading the ref through the staging Git backend -- the same backend
// staging itself uses, so a mission's base and a promotion's expected base are
// read the same way and cannot disagree about what the ref means.
type programTargetResolver struct {
	backend    *gitexec.Backend
	repository staging.RepositoryConfig
	targetRef  string
}

func (r programTargetResolver) ResolveProgramTargetSHA(ctx context.Context) (string, error) {
	sha, err := r.backend.ReadTarget(ctx, r.repository, r.targetRef)
	if err != nil {
		return "", fmt.Errorf("read program target %s: %w", r.targetRef, err)
	}
	return sha, nil
}

// missionProvisioner is a thin pass-through onto engineeringmission.Service.
// Create is already idempotent on the normalized policy's content digest, so
// this adds no bookkeeping of its own -- deliberately, because a second
// idempotency scheme layered on top is how two systems end up disagreeing
// about whether a mission exists.
type missionProvisioner struct {
	service        engineeringmission.Service
	organizationID string
}

func (p missionProvisioner) ProvisionMission(ctx context.Context, command executive.MissionProvisionCommand) (executive.MissionRecord, error) {
	task, err := p.service.Create(ctx, command.Policy, string(command.PlanJSON),
		p.organizationID, command.RequestedByRoleID, command.ActorType, command.ActorID)
	if err != nil {
		return executive.MissionRecord{}, fmt.Errorf("provision engineering mission: %w", err)
	}
	return executive.MissionRecord{TaskID: task.ID}, nil
}

// missionProvisioningOptions returns the orchestrator option when this
// deployment is configured to provision missions, and nil when it is not.
//
// Absence is a supported state, not a failure: an Executive that only decides
// designs is a legitimate deployment, and staging being disabled must not stop
// it from starting. What must never happen is a run that believes it will
// implement something and quietly does not -- that case is caught in the
// phase, which blocks rather than skipping.
func missionProvisioningOptions(cfg config.Config, store *platformpostgres.Store, taskService *tasks.Service) ([]executive.OrchestratorOption, error) {
	repositoryID := strings.TrimSpace(os.Getenv(missionRepositoryEnv))
	targetRef := strings.TrimSpace(os.Getenv(missionTargetRefEnv))
	if repositoryID == "" || targetRef == "" {
		return nil, nil
	}
	if !cfg.Staging.Enabled {
		// Configured to provision but unable to: refusing here is better than
		// starting an Executive that would block every governed run at the
		// last step, after spending the design and review budget.
		return nil, fmt.Errorf("%s/%s are set but staging is disabled", missionRepositoryEnv, missionTargetRefEnv)
	}
	stagingRuntime, err := stagingbootstrap.Open(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open staging runtime for mission provisioning: %w", err)
	}
	repository, _, err := stagingRuntime.Catalog.Get(context.Background(), repositoryID)
	if err != nil {
		return nil, fmt.Errorf("resolve mission repository %q: %w", repositoryID, err)
	}
	// The target ref must be one the repository already permits. A mission
	// cannot introduce a new promotion target by naming one.
	allowed := false
	for _, candidate := range repository.AllowedTargetRefs {
		if candidate == targetRef {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("target ref %q is not an allowed target of repository %q", targetRef, repositoryID)
	}
	backend, err := gitexec.New(cfg.Staging.GitBinary, cfg.Staging.WorkspaceRoot, cfg.Staging.CommandTimeout)
	if err != nil {
		return nil, fmt.Errorf("create Git backend for mission provisioning: %w", err)
	}
	resolver := programTargetResolver{backend: backend, repository: repository, targetRef: targetRef}
	provisioner := missionProvisioner{
		service:        engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service},
		organizationID: cfg.Tasks.OrganizationID,
	}
	return []executive.OrchestratorOption{executive.WithMissionProvisioning(resolver, provisioner)}, nil
}
