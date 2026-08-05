package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RequestCancellation(ctx context.Context, id int64, actor, reason string, outboxMax int) (modelruntime.CancelResult, error) {
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if actor == "" {
		return modelruntime.CancelResult{}, fmt.Errorf("%w: cancellation actor is required", modelruntime.ErrInvalidRequest)
	}
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.CancelResult, error) {
		if err := lockInvocation(ctx, tx, id); err != nil {
			return modelruntime.CancelResult{}, err
		}
		inv, err := scanInvocation(tx.QueryRow(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return modelruntime.CancelResult{}, err
		}
		if inv.Status.Terminal() {
			return modelruntime.CancelResult{Invocation: inv, Immediate: true}, nil
		}
		switch inv.Status {
		case modelruntime.InvocationRequested:
			inv, err = scanInvocation(tx.QueryRow(ctx, `UPDATE model_invocations SET status='cancelled',cancel_requested_at=clock_timestamp(),error_code='cancelled_before_claim',updated_at=clock_timestamp(),terminal_at=clock_timestamp() WHERE id=$1 RETURNING `+invocationColumns, id))
			if err != nil {
				return modelruntime.CancelResult{}, err
			}
			if err = appendInvocationEvent(ctx, tx, inv, modelruntime.AuditInvocationCancelled, "role", actor, true, outboxMax, map[string]any{"reason_provided": reason != "", "phase": "requested"}); err != nil {
				return modelruntime.CancelResult{}, err
			}
			return modelruntime.CancelResult{Invocation: inv, Immediate: true}, nil
		case modelruntime.InvocationClaimed:
			var attemptID int64
			if err = tx.QueryRow(ctx, `SELECT id FROM model_dispatch_attempts WHERE invocation_id=$1 AND status='claimed' FOR UPDATE`, id).Scan(&attemptID); err != nil {
				return modelruntime.CancelResult{}, mapError(err)
			}
			if _, err = tx.Exec(ctx, `UPDATE model_dispatch_attempts SET status='failed_before_send',retry_safety='not_retryable',outcome_classification='cancelled_before_send',error_code='cancelled_before_send',finished_at=clock_timestamp() WHERE id=$1`, attemptID); err != nil {
				return modelruntime.CancelResult{}, mapError(err)
			}
			inv, err = scanInvocation(tx.QueryRow(ctx, `UPDATE model_invocations SET status='cancelled',cancel_requested_at=clock_timestamp(),error_code='cancelled_before_send',updated_at=clock_timestamp(),terminal_at=clock_timestamp() WHERE id=$1 RETURNING `+invocationColumns, id))
			if err != nil {
				return modelruntime.CancelResult{}, err
			}
			if err = appendInvocationEvent(ctx, tx, inv, modelruntime.AuditInvocationCancelled, "role", actor, true, outboxMax, map[string]any{"reason_provided": reason != "", "phase": "claimed"}); err != nil {
				return modelruntime.CancelResult{}, err
			}
			return modelruntime.CancelResult{Invocation: inv, Immediate: true}, nil
		case modelruntime.InvocationSendStarted, modelruntime.InvocationResponseReceived:
			inv, err = scanInvocation(tx.QueryRow(ctx, `UPDATE model_invocations SET cancel_requested_at=COALESCE(cancel_requested_at,clock_timestamp()),updated_at=clock_timestamp() WHERE id=$1 RETURNING `+invocationColumns, id))
			if err != nil {
				return modelruntime.CancelResult{}, err
			}
			if _, err = tx.Exec(ctx, `SELECT pg_notify('model_invocation_cancel',$1)`, strconv.FormatInt(id, 10)); err != nil {
				return modelruntime.CancelResult{}, mapError(err)
			}
			return modelruntime.CancelResult{Invocation: inv, AdapterCancellationRequested: true}, nil
		default:
			return modelruntime.CancelResult{}, fmt.Errorf("%w: cannot cancel status %s", modelruntime.ErrConflict, inv.Status)
		}
	})
}

func (s *Store) CancellationRequested(ctx context.Context, invocationID int64) (bool, error) {
	var requested bool
	if err := s.pool.QueryRow(ctx, `SELECT cancel_requested_at IS NOT NULL FROM model_invocations WHERE id=$1`, invocationID).Scan(&requested); err != nil {
		return false, mapError(err)
	}
	return requested, nil
}

func (s *Store) WatchCancellation(ctx context.Context, invocationID int64) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return mapError(err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, `LISTEN model_invocation_cancel`); err != nil {
		return mapError(err)
	}
	var requested bool
	if err = conn.QueryRow(ctx, `SELECT cancel_requested_at IS NOT NULL FROM model_invocations WHERE id=$1`, invocationID).Scan(&requested); err != nil {
		return mapError(err)
	}
	if requested {
		return nil
	}
	target := strconv.FormatInt(invocationID, 10)
	for {
		notification, waitErr := conn.Conn().WaitForNotification(ctx)
		if waitErr != nil {
			return waitErr
		}
		if notification.Payload != target {
			continue
		}
		if err = conn.QueryRow(ctx, `SELECT cancel_requested_at IS NOT NULL FROM model_invocations WHERE id=$1`, invocationID).Scan(&requested); err != nil {
			return mapError(err)
		}
		if requested {
			return nil
		}
	}
}
