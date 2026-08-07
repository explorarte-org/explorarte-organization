package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

type Evaluator interface {
	Evaluate(context.Context, authorization.EvaluationRequest) (authorization.Evaluation, error)
}

type RevisionReader interface {
	GetCurrentRevision(context.Context, string) (*registry.Revision, error)
}

type Gate struct {
	evaluator      Evaluator
	revisions      RevisionReader
	organizationID string
}

func New(evaluator Evaluator, revisions RevisionReader, organizationID string) (*Gate, error) {
	if evaluator == nil {
		return nil, errors.New("memory authorization gate requires evaluator")
	}
	if revisions == nil {
		return nil, errors.New("memory authorization gate requires revision reader")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("memory authorization gate requires organization ID")
	}
	return &Gate{evaluator: evaluator, revisions: revisions, organizationID: organizationID}, nil
}

func (g *Gate) Authorize(ctx context.Context, request memory.AuthorizationRequest) error {
	if request.OrganizationID != g.organizationID {
		return fmt.Errorf("%w: memory organization mismatch", authorization.ErrCapabilityDenied)
	}
	revision, err := g.revisions.GetCurrentRevision(ctx, g.organizationID)
	if err != nil {
		return fmt.Errorf("resolve memory authorization revision: %w", err)
	}
	if revision == nil || revision.ID <= 0 {
		return fmt.Errorf("%w: no active organization revision", authorization.ErrPolicyRevisionMismatch)
	}
	result, err := g.evaluator.Evaluate(ctx, authorization.EvaluationRequest{
		OrganizationID:         g.organizationID,
		OrganizationRevisionID: revision.ID,
		ActorRoleID:            strings.TrimSpace(request.ActorRoleID),
		CapabilityID:           strings.TrimSpace(request.CapabilityID),
		ResourceType:           strings.TrimSpace(request.ResourceType),
		ResourceID:             strings.TrimSpace(request.ResourceID),
		ActionDigest:           strings.TrimSpace(request.ActionDigest),
	})
	if err != nil {
		return err
	}
	switch result.Effect {
	case authorization.EffectAllow:
		return nil
	case authorization.EffectApprovalRequired:
		return fmt.Errorf("%w: %s", authorization.ErrApprovalRequired, result.ReasonCode)
	case authorization.EffectDeny:
		return fmt.Errorf("%w: %s", authorization.ErrCapabilityDenied, result.ReasonCode)
	default:
		return fmt.Errorf("%w: unexpected authorization effect %q", authorization.ErrCapabilityDenied, result.Effect)
	}
}
