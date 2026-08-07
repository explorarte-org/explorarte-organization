package completion

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Service struct {
	tasks     TaskReader
	artifacts ArtifactChecker
	checks    CheckRunChecker
	approvals ApprovalChecker
	branches  DecisionBranchChecker
	clock     Clock
}

func NewService(tasks TaskReader, artifacts ArtifactChecker, checks CheckRunChecker, approvals ApprovalChecker, branches DecisionBranchChecker, clock Clock) (*Service, error) {
	if tasks == nil || artifacts == nil || checks == nil || approvals == nil || branches == nil {
		return nil, errors.New("completion: all readers are required")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &Service{tasks: tasks, artifacts: artifacts, checks: checks, approvals: approvals, branches: branches, clock: clock}, nil
}

// Verify independently re-checks every reasoning-assurance.yaml Phase 2 obligation
// for one task attempt. It never mutates internal/tasks — callers must gate their
// own Finalize(FinalCompleted) call on the returned Verdict themselves.
func (s *Service) Verify(ctx context.Context, request VerificationRequest) (VerificationResult, error) {
	if request.TaskID <= 0 || request.AttemptID <= 0 {
		return VerificationResult{}, ErrInvalidRequest
	}

	task, err := s.tasks.TaskFacts(ctx, request.TaskID)
	if err != nil {
		return VerificationResult{}, err
	}

	var obligations []ObligationResult

	obligations = append(obligations, s.checkRequirementsSatisfied(task))

	for _, req := range task.Requirements {
		if !req.Required {
			continue
		}
		switch req.Type {
		case RequirementArtifact:
			obligations = append(obligations, s.checkArtifactExists(ctx, req))
		case RequirementCheck:
			obligations = append(obligations, s.checkChecksPassed(ctx, request.TaskID, req))
		case RequirementApproval:
			obligations = append(obligations, s.checkApprovalPresent(ctx, req))
		}
	}

	obligations = append(obligations, s.checkNoRejectedBranchReused(ctx, request.TaskID, request.AttemptID))

	return VerificationResult{
		TaskID:      request.TaskID,
		AttemptID:   request.AttemptID,
		Verdict:     aggregateVerdict(obligations),
		Obligations: obligations,
		VerifiedAt:  s.clock.Now(),
	}, nil
}

func (s *Service) checkRequirementsSatisfied(task TaskFact) ObligationResult {
	for _, req := range task.Requirements {
		if req.Required && !req.Satisfied {
			return ObligationResult{
				Obligation:    ObligationRequirementsSatisfied,
				Label:         LabelContradicted,
				Detail:        "required requirement " + itoa(req.RequirementID) + " is not satisfied",
				RequirementID: req.RequirementID,
			}
		}
	}
	return ObligationResult{Obligation: ObligationRequirementsSatisfied, Label: LabelVerified, Detail: "all required requirements satisfied"}
}

func (s *Service) checkArtifactExists(ctx context.Context, req RequirementFact) ObligationResult {
	base := ObligationResult{Obligation: ObligationArtifactExists, RequirementID: req.RequirementID}
	if req.EvidenceDigest == "" {
		base.Label, base.Detail = LabelUnknown, "no evidence digest recorded for artifact requirement "+itoa(req.RequirementID)
		return base
	}
	digest, err := s.artifacts.ArtifactDigest(ctx, req.EvidenceRef)
	switch {
	case errors.Is(err, ErrArtifactNotFound):
		base.Label, base.Detail = LabelContradicted, "artifact evidence references a path that does not exist in staging"
	case err != nil:
		base.Label, base.Detail = LabelUnknown, "could not read staging artifact: "+err.Error()
	case digest != req.EvidenceDigest:
		base.Label, base.Detail = LabelContradicted, "staging artifact digest does not match recorded evidence digest"
	default:
		base.Label, base.Detail = LabelVerified, "staging artifact digest matches recorded evidence"
	}
	return base
}

func (s *Service) checkChecksPassed(ctx context.Context, taskID int64, req RequirementFact) ObligationResult {
	base := ObligationResult{Obligation: ObligationChecksPassed, RequirementID: req.RequirementID}
	passed, err := s.checks.CheckPassed(ctx, taskID, req.RequirementID)
	switch {
	case err != nil:
		base.Label, base.Detail = LabelUnknown, "could not confirm check outcome: "+err.Error()
	case !passed:
		base.Label, base.Detail = LabelContradicted, "no passing check event found for this attempt"
	default:
		base.Label, base.Detail = LabelVerified, "check-passed event confirmed for this attempt"
	}
	return base
}

func (s *Service) checkApprovalPresent(ctx context.Context, req RequirementFact) ObligationResult {
	base := ObligationResult{Obligation: ObligationApprovalPresent, RequirementID: req.RequirementID}
	if req.EvidenceRef == "" {
		base.Label, base.Detail = LabelUnknown, "no approval request reference recorded"
		return base
	}
	consumed, err := s.approvals.ApprovalConsumed(ctx, req.EvidenceRef, req.EvidenceDigest)
	switch {
	case err != nil:
		base.Label, base.Detail = LabelUnknown, "could not confirm approval state: "+err.Error()
	case !consumed:
		base.Label, base.Detail = LabelContradicted, "referenced approval request is not consumed, or its action digest does not match"
	default:
		base.Label, base.Detail = LabelVerified, "referenced approval request is consumed with a matching action digest"
	}
	return base
}

func (s *Service) checkNoRejectedBranchReused(ctx context.Context, taskID, attemptID int64) ObligationResult {
	base := ObligationResult{Obligation: ObligationNoRejectedBranchReused}
	state, found, err := s.branches.CurrentBranchStateForAttempt(ctx, taskID, attemptID)
	switch {
	case err != nil:
		base.Label, base.Detail = LabelUnknown, "could not read decision graph branch state: "+err.Error()
	case !found:
		base.Label, base.Detail = LabelVerified, "no decision graph run exists for this attempt; nothing to reuse"
	case state == "selected":
		base.Label, base.Detail = LabelVerified, "selected candidate branch is still selected"
	case strings.HasPrefix(state, "rejected_by_") || state == "superseded":
		base.Label, base.Detail = LabelContradicted, "selected candidate branch was rejected or superseded since the decision was recorded: "+state
	default:
		base.Label, base.Detail = LabelUnknown, "selected candidate branch is in an unexpected state: "+state
	}
	return base
}

func aggregateVerdict(obligations []ObligationResult) Verdict {
	inconclusive := false
	for _, o := range obligations {
		switch o.Label {
		case LabelContradicted:
			return VerdictFail
		case LabelUnknown:
			inconclusive = true
		}
	}
	if inconclusive {
		return VerdictInconclusive
	}
	return VerdictPass
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
