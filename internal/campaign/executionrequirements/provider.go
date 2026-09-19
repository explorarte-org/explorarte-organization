// Package executionrequirements derives Campaign's execution budget floor
// (campaign.ExecutionBudgetRequirements) from the host's own canonical facts.
//
// It is a composition seam, not a second source of truth. Campaign owns the
// contract and the validation; this package only asks the authorities:
//
//   - the minimal execution TOPOLOGY (stages, depths, child and model-call
//     counts) comes from internal/executive (MinimalCampaignTopology);
//   - the models a stage's role can route to come from Model Runtime's own
//     canonical routing state (modelruntime.PossibleRoutes);
//   - the input-token estimate is Model Runtime's own dispatch rule
//     (modelruntime.EstimateInputTokens), applied to the canonical envelope
//     encoding (modelruntime.SingleShotModelInputCeilingBytes);
//   - the price and the worst-case reservation come from CostGate itself
//     (modelruntime.CostReservationEstimator, implemented by costgate.Gate
//     from the same code path Reserve uses);
//   - the output ceiling is Executive's own Limits.MaxOutputTokensFor.
//
// Nothing here calls a provider, reserves a wallet, or consumes an
// AgentBudget.
package executionrequirements

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

// Config is everything the provider reads. All of it is read-only.
type Config struct {
	Registry registry.Reader
	// Routes is Model Runtime's canonical routing state.
	Routes modelruntime.RegistryStore
	// Costs prices a worst-case reservation exactly as CostGate does.
	Costs modelruntime.CostReservationEstimator
	// Limits is the Executive limit set the campaign will run under; its
	// MaxOutputTokensFor is the output ceiling every dispatch reserves.
	Limits executive.Limits
	// ContextMaxTotalBytes is the Context Engine's configured bound on one
	// snapshot's rendered context (config.Context.MaxTotalBytes). It is the
	// canonical ceiling on how large a call's context can be.
	ContextMaxTotalBytes int
	Clock                func() time.Time
}

// Provider implements campaign.ExecutionRequirementsProvider.
type Provider struct{ cfg Config }

var _ campaign.ExecutionRequirementsProvider = (*Provider)(nil)

func New(cfg Config) (*Provider, error) {
	if cfg.Registry == nil || cfg.Routes == nil || cfg.Costs == nil {
		return nil, errors.New("execution requirements provider needs a registry, canonical routes and a cost estimator")
	}
	if cfg.ContextMaxTotalBytes <= 0 {
		return nil, errors.New("execution requirements provider needs the context engine's positive MaxTotalBytes")
	}
	if cfg.Limits.MaxOutputTokens <= 0 {
		return nil, errors.New("execution requirements provider needs executive limits with a positive MaxOutputTokens")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Provider{cfg: cfg}, nil
}

// ExecutionBudgetRequirements derives the current floor.
//
// INPUT-SIZE TRADEOFF. Finance runs before the CEO-plan task or any of its
// context exists, so the true input size is unknowable. The floor sizes every
// dispatch's input at the CEILING of what one call can carry: the Context
// Engine's MaxTotalBytes of rendered context plus the purpose's host execution
// contract, measured through the real envelope encoding and converted with the
// dispatch's own bytes-to-tokens rule. That is deliberately far above a typical
// call (production's first CEO-plan input was 49,901 bytes against a 524,288
// byte ceiling) and it can also select a higher long-context price tier. The
// alternative -- guessing a typical size -- would let Finance recommend a budget
// the real reservation refuses, which is the defect this exists to end. The
// cost of the conservatism is only a higher required CEILING; actual spend is
// still settled to what the provider reports. It does not model JSON string
// escaping of the context body.
//
// WORST STAGE, NOT FIRST STAGE. Every unconditional stage reserves its own
// worst case before it runs, on the model its role routes to, so the floor is
// the maximum over all of them and over every model a stage's roles could
// route to. Flooring only the first dispatch would leave the next stage to
// fail the same way -- with a different model, at a higher price.
func (p *Provider) ExecutionBudgetRequirements(ctx context.Context, organizationID string) (campaign.ExecutionBudgetRequirements, error) {
	revision, err := p.cfg.Registry.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return campaign.ExecutionBudgetRequirements{}, fmt.Errorf("read current organization revision: %w", err)
	}
	if revision == nil {
		return campaign.ExecutionBudgetRequirements{}, fmt.Errorf("organization %q has no current revision", organizationID)
	}
	topology := executive.MinimalCampaignTopology()

	roles := &stageRoles{registry: p.cfg.Registry, organizationID: organizationID}
	routes := &policyRoutes{store: p.cfg.Routes, organizationID: organizationID, revisionID: revision.ID, byPolicy: map[string][]modelruntime.RoutableModel{}}
	now := p.cfg.Clock()

	requirements := campaign.ExecutionBudgetRequirements{
		MinModelCalls: topology.ModelCalls,
		MinSubagents:  topology.Subagents,
		MinDepth:      topology.Depth,
		// Neither dimension is consumed on the canonical Executive path (only
		// child allocations spend them and Executive never allocates), so the
		// floor is the AgentBudget validity minimum of 1 -- not an invented
		// operational number. Basis records that fact.
		MinWallTimeMS: 1,
		MinRetries:    1,
		Basis:         campaign.ExecutionBudgetRequirementsBasis{OrganizationRevisionID: revision.ID},
	}
	for _, stage := range topology.Stages {
		candidates, err := roles.forActor(ctx, stage.Actor)
		if err != nil {
			return campaign.ExecutionBudgetRequirements{}, fmt.Errorf("stage %s: %w", stage.Name, err)
		}
		inputBytes, err := modelruntime.SingleShotModelInputCeilingBytes(p.cfg.ContextMaxTotalBytes, executive.ExecutionContractBytes(stage.Purpose))
		if err != nil {
			return campaign.ExecutionBudgetRequirements{}, fmt.Errorf("stage %s: %w", stage.Name, err)
		}
		inputTokens := modelruntime.EstimateInputTokens(inputBytes)
		maxOutput := int64(p.cfg.Limits.MaxOutputTokensFor(stage.Purpose))

		worst, found := campaign.ExecutionStageBasis{}, false
		for _, role := range candidates {
			models, err := routes.forRole(ctx, role)
			if err != nil {
				return campaign.ExecutionBudgetRequirements{}, fmt.Errorf("stage %s: role %s: %w", stage.Name, role.ID, err)
			}
			for _, model := range models {
				estimate, err := p.cfg.Costs.EstimateReservation(ctx, modelruntime.ReservationEstimateRequest{
					ProviderID: model.ProviderID, ProviderModelID: model.ProviderModelID,
					EstimatedInputTokens: inputTokens, MaxOutputTokens: maxOutput,
				}, now)
				if err != nil {
					return campaign.ExecutionBudgetRequirements{}, fmt.Errorf("stage %s: role %s: %s/%s: %w", stage.Name, role.ID, model.ProviderID, model.ProviderModelID, err)
				}
				candidate := campaign.ExecutionStageBasis{
					Stage: stage.Name, Purpose: string(stage.Purpose), Depth: stage.Depth, RoleID: role.ID,
					ProviderID: model.ProviderID, ProviderModelID: model.ProviderModelID, PriceTier: estimate.PriceTier,
					InputBytes: inputBytes, EstimatedInputTokens: inputTokens, MaxOutputTokens: maxOutput,
					ReservationUSD: modelpricing.USDNanos(estimate.USDNanos), ReservationTokens: estimate.Tokens,
				}
				if !found || candidate.ReservationUSD > worst.ReservationUSD ||
					(candidate.ReservationUSD == worst.ReservationUSD && candidate.ReservationTokens > worst.ReservationTokens) {
					worst, found = candidate, true
				}
			}
		}
		if !found {
			return campaign.ExecutionBudgetRequirements{}, fmt.Errorf("stage %s: no enabled executable role with a model route can run it", stage.Name)
		}
		requirements.Basis.Stages = append(requirements.Basis.Stages, worst)
		if worst.ReservationUSD > requirements.MinUSD {
			requirements.MinUSD = worst.ReservationUSD
		}
		if worst.ReservationTokens > requirements.MinTokens {
			requirements.MinTokens = worst.ReservationTokens
		}
	}
	// A subscription-only route reserves $0; the AgentBudget still requires
	// a strictly positive ceiling, so the floor is one nano-dollar rather
	// than zero.
	if requirements.MinUSD <= 0 {
		requirements.MinUSD = 1
	}
	if err := requirements.Validate(); err != nil {
		return campaign.ExecutionBudgetRequirements{}, err
	}
	return requirements, nil
}

// stageRoles finds the roles that could run a stage, applying Executive's own
// eligibility rules (executive.CampaignLeaderEligible / CampaignWorkerEligible).
type stageRoles struct {
	registry       registry.Reader
	organizationID string
	units          []unitLeader
	loaded         bool
}

type unitLeader struct {
	unit   executive.UnitRef
	leader executive.RoleRef
}

func (s *stageRoles) forActor(ctx context.Context, actor executive.CampaignStageActor) ([]registry.Role, error) {
	switch actor {
	case executive.CampaignActorCEO:
		role, err := s.registry.GetRole(ctx, s.organizationID, executive.CEORoleID)
		if err != nil {
			return nil, fmt.Errorf("read CEO role: %w", err)
		}
		return []registry.Role{role}, nil
	case executive.CampaignActorLeader:
		if err := s.load(ctx); err != nil {
			return nil, err
		}
		var out []registry.Role
		for _, u := range s.units {
			role, err := s.registry.GetRole(ctx, s.organizationID, u.leader.ID)
			if err != nil {
				return nil, fmt.Errorf("read leader %s: %w", u.leader.ID, err)
			}
			out = append(out, role)
		}
		return out, nil
	case executive.CampaignActorWorker:
		if err := s.load(ctx); err != nil {
			return nil, err
		}
		var out []registry.Role
		for _, u := range s.units {
			all, err := s.registry.ListRoles(ctx, s.organizationID, registry.RoleFilter{UnitID: u.unit.ID, EnabledOnly: true})
			if err != nil {
				return nil, fmt.Errorf("list roles of %s: %w", u.unit.ID, err)
			}
			for _, role := range all {
				if executive.CampaignWorkerEligible(toRoleRef(role)) {
					out = append(out, role)
				}
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown campaign stage actor %q", actor)
	}
}

func (s *stageRoles) load(ctx context.Context) error {
	if s.loaded {
		return nil
	}
	units, err := s.registry.ListUnits(ctx, s.organizationID)
	if err != nil {
		return fmt.Errorf("list organization units: %w", err)
	}
	for _, unit := range units {
		leaderID := ""
		if unit.LeaderRoleID != nil {
			leaderID = *unit.LeaderRoleID
		}
		ref := executive.UnitRef{ID: unit.ID, Operational: unit.Operational, Leaderless: unit.Leaderless, LeaderRoleID: leaderID, Retired: unit.RetiredAt != nil}
		if ref.Retired || !ref.Operational || ref.Leaderless || leaderID == "" {
			continue
		}
		leader, err := s.registry.GetLeader(ctx, s.organizationID, unit.ID)
		if err != nil {
			return fmt.Errorf("read leader of %s: %w", unit.ID, err)
		}
		if executive.CampaignLeaderEligible(ref, toRoleRef(leader)) {
			s.units = append(s.units, unitLeader{unit: ref, leader: toRoleRef(leader)})
		}
	}
	sort.Slice(s.units, func(i, j int) bool { return s.units[i].unit.ID < s.units[j].unit.ID })
	s.loaded = true
	return nil
}

func toRoleRef(role registry.Role) executive.RoleRef {
	return executive.RoleRef{
		ID: role.ID, UnitID: role.UnitID, Enabled: role.Enabled, Executable: role.Executable,
		Retired: role.RetiredAt != nil, CanonicalLeader: role.CanonicalLeader, AuthorityClass: role.AuthorityClass,
	}
}

// policyRoutes resolves a role's routable models through Model Runtime's own
// PossibleRoutes, memoized per policy (many roles share one).
type policyRoutes struct {
	store          modelruntime.RegistryStore
	organizationID string
	revisionID     int64
	byPolicy       map[string][]modelruntime.RoutableModel
}

// forRole returns the models the role can dispatch to, or none when it has no
// model policy or no canonical binding -- such a role can never reach a
// provider, so it can impose no reservation. Any other failure is returned:
// an unreadable route is never treated as a free one.
func (r *policyRoutes) forRole(ctx context.Context, role registry.Role) ([]modelruntime.RoutableModel, error) {
	if role.ModelPolicy == nil || *role.ModelPolicy == "" {
		return nil, nil
	}
	policy := *role.ModelPolicy
	// Pool policies are keyed by policy alone; a static policy resolves
	// through the ROLE's own binding, so it is keyed by role.
	key := policy + "\x00" + role.ID
	if models, ok := r.byPolicy[key]; ok {
		return models, nil
	}
	models, err := modelruntime.PossibleRoutes(ctx, r.store, modelruntime.RouteResolutionRequest{
		OrganizationID: r.organizationID, OrganizationRevisionID: r.revisionID, SubjectRoleID: role.ID, PolicyID: policy,
	})
	if err != nil {
		if errors.Is(err, modelruntime.ErrBindingNotFound) {
			r.byPolicy[key] = nil
			return nil, nil
		}
		return nil, err
	}
	r.byPolicy[key] = models
	return models, nil
}
