package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

type Evaluator interface {
	Evaluate(context.Context, authorization.EvaluationRequest) (authorization.Evaluation, error)
}

type RevisionReader interface {
	GetCurrentRevision(context.Context, string) (*registry.Revision, error)
}

type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type Gate struct {
	evaluator      Evaluator
	revisions      RevisionReader
	clock          Clock
	organizationID string
}

func New(evaluator Evaluator, revisions RevisionReader, organizationID string) (*Gate, error) {
	if evaluator == nil {
		return nil, errors.New("skill registry authorization gate requires evaluator")
	}
	if revisions == nil {
		return nil, errors.New("skill registry authorization gate requires revision reader")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("skill registry authorization gate requires organization ID")
	}
	return &Gate{evaluator: evaluator, revisions: revisions, clock: systemClock{}, organizationID: organizationID}, nil
}

var _ skillregistry.AuthorizationGate = (*Gate)(nil)

func (g *Gate) AuthorizeProposal(ctx context.Context, organizationID, actorRoleID, skillID string) (skillregistry.GovernanceEvidence, error) {
	return g.authorize(ctx, organizationID, actorRoleID, skillregistry.CapabilityPropose, "skill", skillID, []string{skillID, "propose"})
}

func (g *Gate) AuthorizeLifecycleChange(ctx context.Context, organizationID, actorRoleID, skillID string, from, to skillregistry.Lifecycle) (skillregistry.GovernanceEvidence, error) {
	capability := skillregistry.CapabilityPropose
	switch to {
	case skillregistry.LifecycleActive, skillregistry.LifecycleSuspended, skillregistry.LifecycleRetired:
		capability = skillregistry.CapabilityActivate
	}
	return g.authorize(ctx, organizationID, actorRoleID, capability, "skill_version", skillID, []string{skillID, string(from), string(to)})
}

func (g *Gate) AuthorizeAssignmentChange(ctx context.Context, organizationID, actorRoleID, roleID, skillID, action string) (skillregistry.GovernanceEvidence, error) {
	resourceID := skillID + ":" + roleID
	return g.authorize(ctx, organizationID, actorRoleID, skillregistry.CapabilityActivate, "skill_assignment", resourceID, []string{skillID, roleID, action})
}

func (g *Gate) authorize(ctx context.Context, organizationID, actorRoleID, capabilityID, resourceType, resourceID string, digestParts []string) (skillregistry.GovernanceEvidence, error) {
	organizationID = strings.TrimSpace(organizationID)
	actorRoleID = strings.TrimSpace(actorRoleID)
	if organizationID != g.organizationID {
		return skillregistry.GovernanceEvidence{}, fmt.Errorf("%w: skill registry organization mismatch", authorization.ErrCapabilityDenied)
	}
	revision, err := g.revisions.GetCurrentRevision(ctx, g.organizationID)
	if err != nil {
		return skillregistry.GovernanceEvidence{}, fmt.Errorf("resolve skill registry authorization revision: %w", err)
	}
	if revision == nil || revision.ID <= 0 {
		return skillregistry.GovernanceEvidence{}, fmt.Errorf("%w: no active organization revision", authorization.ErrPolicyRevisionMismatch)
	}
	result, err := g.evaluator.Evaluate(ctx, authorization.EvaluationRequest{
		OrganizationID:         g.organizationID,
		OrganizationRevisionID: revision.ID,
		ActorRoleID:            actorRoleID,
		CapabilityID:           capabilityID,
		ResourceType:           resourceType,
		ResourceID:             resourceID,
		ActionDigest:           actionDigest(capabilityID, digestParts),
	})
	if err != nil {
		return skillregistry.GovernanceEvidence{}, err
	}
	switch result.Effect {
	case authorization.EffectAllow:
		return skillregistry.GovernanceEvidence{
			DecisionRef: fmt.Sprintf("authz:%s:%s:%d", capabilityID, resourceID, revision.ID),
			ActorRoleID: actorRoleID,
			DecidedAt:   g.clock.Now().UTC(),
		}, nil
	case authorization.EffectApprovalRequired:
		return skillregistry.GovernanceEvidence{}, fmt.Errorf("%w: %s", authorization.ErrApprovalRequired, result.ReasonCode)
	case authorization.EffectDeny:
		return skillregistry.GovernanceEvidence{}, fmt.Errorf("%w: %s", authorization.ErrCapabilityDenied, result.ReasonCode)
	default:
		return skillregistry.GovernanceEvidence{}, fmt.Errorf("%w: unexpected authorization effect %q", authorization.ErrCapabilityDenied, result.Effect)
	}
}

func actionDigest(capabilityID string, parts []string) string {
	body := "skill-registry-authz.v1|" + capabilityID + "|" + strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
