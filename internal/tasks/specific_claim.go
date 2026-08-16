package tasks

import (
	"context"
	"fmt"
	"strings"
)

type SpecificClaimPersistence interface {
	ClaimSpecific(context.Context, int64, ClaimRequest, AssigneeValidator, int) (ClaimedTask, error)
}

func (s *Service) ClaimTaskByID(ctx context.Context, taskID int64, request ClaimRequest) (ClaimedTask, error) {
	if taskID <= 0 {
		return ClaimedTask{}, fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	request.OrganizationID = strings.TrimSpace(request.OrganizationID)
	if request.OrganizationID == "" {
		request.OrganizationID = s.cfg.OrganizationID
	}
	if !identifierPattern.MatchString(request.OrganizationID) {
		return ClaimedTask{}, fmt.Errorf("%w: organization_id is invalid", ErrInvalidInput)
	}
	request.WorkerID = strings.TrimSpace(request.WorkerID)
	request.HolderPrincipalID = strings.TrimSpace(request.HolderPrincipalID)
	request.AssignedRoleID = strings.TrimSpace(request.AssignedRoleID)
	if request.WorkerID == "" || len(request.WorkerID) > 200 {
		return ClaimedTask{}, fmt.Errorf("%w: worker_id is required and must be at most 200 bytes", ErrInvalidInput)
	}
	if len(request.HolderPrincipalID) > 200 {
		return ClaimedTask{}, fmt.Errorf("%w: holder_principal_id must be at most 200 bytes", ErrInvalidInput)
	}
	if request.AssignedRoleID != "" && !rolePattern.MatchString(request.AssignedRoleID) {
		return ClaimedTask{}, fmt.Errorf("%w: assigned_role_id is invalid", ErrInvalidInput)
	}
	if request.LeaseDuration == 0 {
		request.LeaseDuration = s.cfg.DefaultLeaseDuration
	}
	if request.LeaseDuration <= 0 || request.LeaseDuration > s.cfg.MaxLeaseDuration {
		return ClaimedTask{}, fmt.Errorf("%w: lease duration is invalid", ErrInvalidInput)
	}
	claimer, ok := s.persistence.(SpecificClaimPersistence)
	if !ok {
		return ClaimedTask{}, fmt.Errorf("%w: targeted claim persistence unavailable", ErrInvalidInput)
	}
	return claimer.ClaimSpecific(ctx, taskID, request, s.validateAssignee, s.cfg.OutboxMaxAttempts)
}
