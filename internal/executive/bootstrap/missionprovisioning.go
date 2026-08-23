package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	stagingbootstrap "github.com/Mireuz13/explorarte-organization/internal/staging/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// Mission provisioning is wired here rather than assumed. The Executive holds
// the ports; without a concrete implementation behind them
// driveImplementationMission blocks the run with
// mission_provisioning_unavailable. That fail-closed behaviour is correct, but
// it means a deployment that intends to self-modify never can.
//
// The repository and target ref come from the SAME trusted configuration the
// CodeRunner worker already uses. Reusing those two variables is deliberate:
// the Executive that bases a mission on a ref and the worker that later
// promotes onto it must be reading the same ref, and a second pair of
// variables is how those two drift apart.
const (
	missionRepositoryEnv = "ORG_CODE_RUNNER_REPOSITORY_ID"
	missionTargetRefEnv  = "ORG_CODE_RUNNER_TARGET_REF"
)

// TargetReader is the narrow slice of the staging Git backend this needs. It
// is an interface so the resolver can be tested against a real repository
// without a database, and so nothing here can reach the mutating operations.
type TargetReader interface {
	ReadTarget(context.Context, staging.RepositoryConfig, string) (string, error)
}

// programTargetResolver answers "what commit is the promotion target at right
// now" by reading the governed LOCAL ref.
//
// It never reads HEAD, never consults origin, and never fetches. HEAD is
// whatever the working tree happens to be checked out at, and origin is a
// mirror that can lag or lead; the promotion target is the only thing a
// mission may be based on, because it is the only thing a promotion will be
// applied to.
type programTargetResolver struct {
	git        TargetReader
	repository staging.RepositoryConfig
	targetRef  string
}

func (r programTargetResolver) ResolveProgramTargetSHA(ctx context.Context) (string, error) {
	sha, err := r.git.ReadTarget(ctx, r.repository, r.targetRef)
	if err != nil {
		return "", fmt.Errorf("read program target %s: %w", r.targetRef, err)
	}
	return sha, nil
}

// missionCreator is the one operation the provisioner may perform.
//
// It is CreateIn rather than Create because a mission provisioned by the
// Executive always belongs to a campaign, and the campaign's identity has to
// be part of the creation itself rather than something attached afterwards.
type missionCreator interface {
	CreateIn(ctx context.Context, policy engineeringmission.MissionPolicy, plan string,
		origin engineeringmission.MissionOrigin, actorType, actorID string) (tasks.Task, error)
}

// missionProvisioner translates the Executive's command into a mission and
// returns its task id. That is its whole surface: it cannot execute
// CodeRunner, review, approve or promote, and it passes the policy through
// untouched -- it has no way to change a BaseSHA, widen AllowedPaths or drop a
// gate, because it never constructs a policy of its own.
//
// Create is already idempotent on the normalized policy's content digest, so
// this adds no bookkeeping. A second idempotency scheme layered on top is how
// two systems end up disagreeing about whether a mission exists.
type missionProvisioner struct {
	missions       missionCreator
	organizationID string
}

func (p missionProvisioner) ProvisionMission(ctx context.Context, command executive.MissionProvisionCommand) (executive.MissionRecord, error) {
	task, err := p.missions.CreateIn(ctx, command.Policy, string(command.PlanJSON),
		engineeringmission.MissionOrigin{
			OrganizationID:    p.organizationID,
			RequestedByRoleID: command.RequestedByRoleID,
			CorrelationID:     command.CorrelationID,
			CausationID:       command.CausationID,
		}, command.ActorType, command.ActorID)
	if err != nil {
		// Classified here because this is where the two vocabularies meet.
		// The task engine says "invalid input"; the Executive needs to know
		// whether coming back later could possibly help. For a malformed
		// request it cannot: the same policy and the same plan produce the
		// same refusal every time. Deciding that upstream would mean the
		// Executive re-deriving the task engine's own rule, and deciding it
		// downstream means never deciding it at all.
		if errors.Is(err, tasks.ErrInvalidInput) {
			return executive.MissionRecord{}, fmt.Errorf("%w: %w", executive.ErrMissionRejected, err)
		}
		return executive.MissionRecord{}, fmt.Errorf("provision engineering mission: %w", err)
	}
	return executive.MissionRecord{TaskID: task.ID}, nil
}

// missionProvisioningOptions returns the orchestrator option when this
// deployment is configured to provision missions, and none when it is not.
//
// Absence is a supported state: an Executive that only decides designs is a
// legitimate deployment, and staging being disabled must not stop it from
// starting. A root that then asks for an implementation mission still fails
// closed inside the phase.
func missionProvisioningOptions(cfg config.Config, store *platformpostgres.Store, taskService *tasks.Service) ([]executive.OrchestratorOption, error) {
	repositoryID := strings.TrimSpace(os.Getenv(missionRepositoryEnv))
	targetRef := strings.TrimSpace(os.Getenv(missionTargetRefEnv))
	if repositoryID == "" || targetRef == "" {
		return nil, nil
	}
	if !cfg.Staging.Enabled {
		// Configured to provision but unable to. Refusing here beats starting
		// an Executive that would spend the design and review budget on every
		// governed run and then block at the last step.
		return nil, fmt.Errorf("%s/%s are set but staging is disabled", missionRepositoryEnv, missionTargetRefEnv)
	}
	stagingRuntime, err := stagingbootstrap.Open(cfg, store)
	if err != nil {
		return nil, fmt.Errorf("open staging runtime for mission provisioning: %w", err)
	}
	resolver, err := newProgramTargetResolver(context.Background(), stagingRuntime.Git, stagingRuntime.Catalog, repositoryID, targetRef)
	if err != nil {
		return nil, err
	}
	provisioner := missionProvisioner{
		missions:       engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service},
		organizationID: cfg.Tasks.OrganizationID,
	}
	return []executive.OrchestratorOption{executive.WithMissionProvisioning(resolver, provisioner)}, nil
}

// newProgramTargetResolver resolves the repository through the catalog and
// refuses a target ref the repository does not already allow. A mission cannot
// introduce a new promotion target by naming one.
func newProgramTargetResolver(ctx context.Context, git TargetReader, catalog staging.RepositoryCatalog, repositoryID, targetRef string) (programTargetResolver, error) {
	if git == nil || catalog == nil {
		return programTargetResolver{}, fmt.Errorf("mission provisioning requires a staging git backend and catalog")
	}
	repository, _, err := catalog.Get(ctx, repositoryID)
	if err != nil {
		return programTargetResolver{}, fmt.Errorf("resolve mission repository %q: %w", repositoryID, err)
	}
	allowed := false
	for _, candidate := range repository.AllowedTargetRefs {
		if candidate == targetRef {
			allowed = true
			break
		}
	}
	if !allowed {
		return programTargetResolver{}, fmt.Errorf("target ref %q is not an allowed target of repository %q", targetRef, repositoryID)
	}
	return programTargetResolver{git: git, repository: repository, targetRef: targetRef}, nil
}
