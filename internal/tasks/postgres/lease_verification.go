package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

func (s *Store) VerifyActiveExecutionLease(ctx context.Context, command tasks.VerifyExecutionLeaseCommand) (tasks.ExecutionLeaseContext, error) {
	if command.TaskID <= 0 || command.AttemptID <= 0 || strings.TrimSpace(command.HolderID) == "" {
		return tasks.ExecutionLeaseContext{}, fmt.Errorf("%w: task, attempt and holder are required", tasks.ErrInvalidInput)
	}
	if strings.TrimSpace(command.LeaseToken) == "" {
		return tasks.ExecutionLeaseContext{}, tasks.ErrLeaseMismatch
	}
	var result tasks.ExecutionLeaseContext
	err := s.pool.QueryRow(ctx, `
		SELECT t.id,a.id,t.organization_id,t.organization_revision_id,t.assigned_role_id,t.assigned_unit_id,
		       t.status,a.state,l.holder_id,l.expires_at
		FROM tasks t
		JOIN task_attempts a ON a.task_id=t.id AND a.id=$2
		JOIN task_leases l ON l.task_id=t.id AND l.attempt_id=a.id
		WHERE t.id=$1
		  AND t.status='running'
		  AND a.state='running'
		  AND l.status='active'
		  AND l.holder_id=$3
		  AND l.token_hash=$4
		  AND clock_timestamp() < l.expires_at
	`, command.TaskID, command.AttemptID, command.HolderID, hashToken(command.LeaseToken)).Scan(
		&result.TaskID, &result.AttemptID, &result.OrganizationID, &result.OrganizationRevisionID,
		&result.AssignedRoleID, &result.AssignedUnitID, &result.TaskStatus, &result.AttemptState,
		&result.HolderID, &result.ExpiresAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return tasks.ExecutionLeaseContext{}, tasks.ErrLeaseMismatch
		}
		return tasks.ExecutionLeaseContext{}, mapError(err)
	}
	return result, nil
}

var _ tasks.ExecutionLeaseVerifier = (*Store)(nil)
