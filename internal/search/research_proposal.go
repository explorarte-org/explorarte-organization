package search

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// =============================================================================
// Topic Proposal Policy
//
// The researcher LLM may PROPOSE topics; only this host-side deterministic
// policy may turn a proposal into a productive agenda topic. The LLM can
// never insert arbitrary vigilance into the organization.
//
// Autoaccept requires ALL of:
//   - department is known to the agenda;
//   - parent need (if declared) exists and belongs to the same department;
//   - parent topic (if declared) exists;
//   - not a duplicate of an existing topic in the department;
//   - cadence respects the class floor;
//   - intents are canonical;
//   - priority is not critical-tier (>= 0.95 goes to review).
//
// Review (pending) when: cadence faster than floor, priority >= 0.95, or a
// DEEP class proposal (budgeted work needs explicit capacity).
// =============================================================================

// DepartmentResolver reports whether a department ID is known to the
// organization. Production wiring backs this with the canonical kernel role/
// unit catalog; tests use a static set. The policy never trusts a
// model-declared department beyond what this resolver confirms.
type DepartmentResolver interface {
	KnownDepartment(departmentID string) bool
}

// StaticDepartments is a DepartmentResolver over an explicit allow-list.
type StaticDepartments map[string]bool

// KnownDepartment implements DepartmentResolver.
func (s StaticDepartments) KnownDepartment(departmentID string) bool { return s[departmentID] }

// ProposalPolicyConfig centralizes the acceptance thresholds.
type ProposalPolicyConfig struct {
	// CriticalPriorityThreshold: proposals at/above go to review, never
	// autoaccepted (critical vigilance is an organizational decision).
	CriticalPriorityThreshold float64
	// ReviewBeforeFloor: proposals whose cadence is below the class floor
	// but above HalfFloor go to review; below that they are rejected.
	ReviewBeforeFloor map[ResearchClass]time.Duration
}

// DefaultProposalPolicyConfig returns conservative V1 thresholds.
func DefaultProposalPolicyConfig() ProposalPolicyConfig {
	return ProposalPolicyConfig{
		CriticalPriorityThreshold: 0.95,
		ReviewBeforeFloor: map[ResearchClass]time.Duration{
			ResearchWatch: 5 * time.Minute,
			ResearchTrack: 3 * time.Hour,
			ResearchDeep:  12 * time.Hour,
		},
	}
}

// TopicProposalPolicy is the deterministic host-side gate.
type TopicProposalPolicy struct {
	Agenda      *MemoryAgenda
	Departments DepartmentResolver
	Floors      map[ResearchClass]time.Duration
	Cfg         ProposalPolicyConfig
	Now         func() time.Time
}

// NewTopicProposalPolicy validates deps and returns the policy.
func NewTopicProposalPolicy(agenda *MemoryAgenda, departments DepartmentResolver, floors map[ResearchClass]time.Duration) (*TopicProposalPolicy, error) {
	if agenda == nil {
		return nil, fmt.Errorf("%w: proposal policy requires an agenda", ErrInvalidRequest)
	}
	if departments == nil {
		return nil, fmt.Errorf("%w: proposal policy requires a department resolver", ErrInvalidRequest)
	}
	if floors == nil {
		floors = DefaultSchedulerConfig().CadenceFloors
	}
	return &TopicProposalPolicy{
		Agenda:      agenda,
		Departments: departments,
		Floors:      floors,
		Cfg:         DefaultProposalPolicyConfig(),
		Now:         func() time.Time { return time.Now().UTC() },
	}, nil
}

// Evaluate applies the deterministic policy to a proposal. It records the
// decision on the proposal and returns it. When accepted, the productive
// topic is created in the agenda with a fresh identity and NextCheckAt=now.
func (p *TopicProposalPolicy) Evaluate(ctx context.Context, proposal ResearchTopicProposal) (ResearchTopicProposal, error) {
	if p.Now != nil {
		proposal.CreatedAt = p.Now().UTC()
	} else {
		proposal.CreatedAt = time.Now().UTC()
	}

	decision, reason := p.decide(ctx, proposal)
	proposal.Decision = decision
	proposal.RejectReason = reason
	now := p.Now()
	proposal.ReviewedAt = &now

	if decision == ProposalAccepted {
		topic := ResearchTopic{
			OrganizationID:        proposal.OrganizationID,
			DepartmentID:          proposal.DepartmentID,
			Title:                 proposal.Title,
			Description:           proposal.Description,
			Priority:              proposal.Priority,
			ResearchClass:         proposal.ResearchClass,
			Status:                TopicStatusActive,
			AllowedIntents:        proposal.AllowedIntents,
			Cadence:               proposal.Cadence,
			NextCheckAt:           now,
			NoveltyWindow:         7 * 24 * time.Hour,
			CreatedBy:             "researcher_proposal",
			Reason:                proposal.Reason,
			ParentKnowledgeNeedID: proposal.ParentKnowledgeNeedID,
			CreatedAt:             now,
		}
		if err := p.Agenda.SaveTopic(ctx, topic); err != nil {
			proposal.Decision = ProposalRejected
			proposal.RejectReason = RejectInvalidTopic
			return proposal, err
		}
	}
	return proposal, nil
}

// decide implements the deterministic acceptance rules.
func (p *TopicProposalPolicy) decide(ctx context.Context, proposal ResearchTopicProposal) (ProposalDecision, ProposalRejectReason) {
	// Unknown department: hard reject.
	if !p.Departments.KnownDepartment(proposal.DepartmentID) {
		return ProposalRejected, RejectUnknownDepartment
	}

	// Structural validity of the proposed topic shape.
	probe := ResearchTopic{
		DepartmentID:   proposal.DepartmentID,
		Title:          proposal.Title,
		ResearchClass:  proposal.ResearchClass,
		AllowedIntents: proposal.AllowedIntents,
		Cadence:        proposal.Cadence,
	}
	if err := probe.Validate(); err != nil {
		return ProposalRejected, RejectInvalidTopic
	}

	// Parent references must be real and consistent.
	if proposal.ParentKnowledgeNeedID != "" {
		need, err := p.Agenda.GetNeed(ctx, proposal.ParentKnowledgeNeedID)
		if err != nil || need.DepartmentID != proposal.DepartmentID {
			return ProposalRejected, RejectInvalidParent
		}
	}
	if proposal.ParentTopicID != "" {
		if _, err := p.Agenda.GetTopic(ctx, proposal.ParentTopicID); err != nil {
			return ProposalRejected, RejectInvalidParent
		}
	}

	// Duplicate in the same department: reject (merge is a later, explicit
	// operation; auto-duplicating vigilance is noise).
	existing, err := p.Agenda.ListTopics(ctx, proposal.DepartmentID)
	if err == nil {
		for _, topic := range existing {
			if strings.EqualFold(normalizeTopicTitle(topic.Title), normalizeTopicTitle(proposal.Title)) {
				return ProposalRejected, RejectDuplicate
			}
		}
	}

	// Critical-priority proposals always need review.
	if proposal.Priority >= p.Cfg.CriticalPriorityThreshold {
		return ProposalPending, RejectCriticalPriority
	}

	// DEEP work is budget-gated: never autoaccepted.
	if proposal.ResearchClass == ResearchDeep {
		return ProposalPending, RejectHighFrequency
	}

	// Cadence below floor: review if within tolerance, reject if extreme.
	floor, ok := p.Floors[proposal.ResearchClass]
	if !ok {
		return ProposalPending, RejectHighFrequency
	}
	if proposal.Cadence < floor {
		reviewFloor, ok := p.Cfg.ReviewBeforeFloor[proposal.ResearchClass]
		if !ok || proposal.Cadence < reviewFloor {
			return ProposalPending, RejectHighFrequency
		}
		return ProposalPending, RejectCadenceTooFast
	}

	return ProposalAccepted, ""
}
