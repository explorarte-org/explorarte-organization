package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("agentbudget store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
}

var _ agentbudget.Ledger = (*Store)(nil)

const budgetColumns = `id, organization_id, root_task_id, task_id, role_id, parent_budget_id,
max_usd_nanos, max_tokens, max_model_calls, max_wall_time_ms, max_depth, max_retries, max_subagents,
used_usd_nanos, used_tokens, used_model_calls, used_wall_time_ms, depth, used_retries, used_subagents, version`

func scanBudget(row pgx.Row) (agentbudget.Budget, error) {
	var b agentbudget.Budget
	var parentID *int64
	if err := row.Scan(
		&b.ID, &b.OrganizationID, &b.RootTaskID, &b.TaskID, &b.RoleID, &parentID,
		&b.Limits.MaxUSD, &b.Limits.MaxTokens, &b.Limits.MaxModelCalls, &b.Limits.MaxWallTimeMS, &b.Limits.MaxDepth, &b.Limits.MaxRetries, &b.Limits.MaxSubagents,
		&b.Usage.UsedUSD, &b.Usage.UsedTokens, &b.Usage.UsedModelCalls, &b.Usage.UsedWallTimeMS, &b.Usage.Depth, &b.Usage.UsedRetries, &b.Usage.UsedSubagents, &b.Version,
	); err != nil {
		return agentbudget.Budget{}, err
	}
	b.ParentBudgetID = parentID
	return b, nil
}

func (s *Store) GetBudget(ctx context.Context, budgetID int64) (agentbudget.Budget, error) {
	b, err := scanBudget(s.pool.QueryRow(ctx, `SELECT `+budgetColumns+` FROM agent_budgets WHERE id=$1`, budgetID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentbudget.Budget{}, agentbudget.ErrBudgetNotFound
		}
		return agentbudget.Budget{}, err
	}
	return b, nil
}

func (s *Store) CreateRootBudget(ctx context.Context, organizationID string, rootTaskID int64, roleID string, limits agentbudget.Limits, now time.Time) (agentbudget.Budget, error) {
	if err := limits.Validate(); err != nil {
		return agentbudget.Budget{}, err
	}
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return agentbudget.Budget{}, fmt.Errorf("begin create root budget: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
INSERT INTO agent_budgets (
    organization_id, root_task_id, task_id, role_id, parent_budget_id,
    max_usd_nanos, max_tokens, max_model_calls, max_wall_time_ms, max_depth, max_retries, max_subagents,
    depth, created_at, updated_at
) VALUES ($1,$2,$2,$3,NULL,$4,$5,$6,$7,$8,$9,$10,1,$11,$11)
ON CONFLICT (task_id) DO NOTHING`,
		organizationID, rootTaskID, roleID,
		int64(limits.MaxUSD), limits.MaxTokens, limits.MaxModelCalls, limits.MaxWallTimeMS, limits.MaxDepth, limits.MaxRetries, limits.MaxSubagents, now)
	if err != nil {
		return agentbudget.Budget{}, err
	}
	if tag.RowsAffected() > 0 {
		var budgetID int64
		if err := tx.QueryRow(ctx, `SELECT id FROM agent_budgets WHERE task_id=$1`, rootTaskID).Scan(&budgetID); err != nil {
			return agentbudget.Budget{}, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO agent_budget_events (budget_id, kind, idempotency_ref, usd_nanos_delta, tokens_delta, model_calls_delta, wall_time_ms_delta, depth_delta, retries_delta, subagents_delta, created_at)
VALUES ($1,'created',$2,0,0,0,0,1,0,0,$3)`, budgetID, rootTaskID, now); err != nil {
			return agentbudget.Budget{}, err
		}
	}
	b, err := scanBudget(tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM agent_budgets WHERE task_id=$1`, rootTaskID))
	if err != nil {
		return agentbudget.Budget{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return agentbudget.Budget{}, err
	}
	return b, nil
}

// InheritForChild's idempotency key is the (parentBudgetID, 'inherited',
// childTaskID) event row: it is inserted first, inside the same
// transaction as every other mutation this call makes, so whether it was
// newly inserted (RowsAffected>0) is the single source of truth for
// whether this attachment has already happened. A retried call for the
// same child never re-debits the parent or creates a second child row.
func (s *Store) InheritForChild(ctx context.Context, parentBudgetID int64, childTaskID int64, childRoleID string, childDepth int64, allocation *agentbudget.Limits, now time.Time) (agentbudget.Budget, error) {
	if childDepth <= 0 {
		return agentbudget.Budget{}, fmt.Errorf("%w: child depth must be positive", agentbudget.ErrInvalidRequest)
	}
	if allocation != nil {
		if err := allocation.Validate(); err != nil {
			return agentbudget.Budget{}, err
		}
	}
	now = now.UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return agentbudget.Budget{}, fmt.Errorf("begin inherit for child: %w", err)
	}
	defer tx.Rollback(ctx)

	parent, err := scanBudget(tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM agent_budgets WHERE id=$1 FOR UPDATE`, parentBudgetID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentbudget.Budget{}, agentbudget.ErrBudgetNotFound
		}
		return agentbudget.Budget{}, err
	}

	if childDepth > parent.Limits.MaxDepth {
		return agentbudget.Budget{}, fmt.Errorf("%w: child depth %d exceeds max %d", agentbudget.ErrBudgetExceeded, childDepth, parent.Limits.MaxDepth)
	}

	if allocation == nil {
		tag, err := tx.Exec(ctx, `
INSERT INTO agent_budget_events (budget_id, kind, idempotency_ref, usd_nanos_delta, tokens_delta, model_calls_delta, wall_time_ms_delta, depth_delta, retries_delta, subagents_delta, created_at)
VALUES ($1,'inherited',$2,0,0,0,0,0,0,1,$3)
ON CONFLICT (budget_id, kind, idempotency_ref) DO NOTHING`, parentBudgetID, childTaskID, now)
		if err != nil {
			return agentbudget.Budget{}, err
		}
		if tag.RowsAffected() == 0 {
			// Already recorded — return the parent's current state as-is.
			result, err := scanBudget(tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM agent_budgets WHERE id=$1`, parentBudgetID))
			if err != nil {
				return agentbudget.Budget{}, err
			}
			return result, tx.Commit(ctx)
		}
		if parent.Usage.UsedSubagents+1 > parent.Limits.MaxSubagents {
			return agentbudget.Budget{}, fmt.Errorf("%w: subagent count would exceed max %d", agentbudget.ErrBudgetExceeded, parent.Limits.MaxSubagents)
		}
		newDepth := parent.Usage.Depth
		if childDepth > newDepth {
			newDepth = childDepth
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_budgets SET depth=$1, used_subagents=used_subagents+1, version=version+1, updated_at=$2 WHERE id=$3`, newDepth, now, parentBudgetID); err != nil {
			return agentbudget.Budget{}, err
		}
		result, err := scanBudget(tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM agent_budgets WHERE id=$1`, parentBudgetID))
		if err != nil {
			return agentbudget.Budget{}, err
		}
		return result, tx.Commit(ctx)
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO agent_budget_events (budget_id, kind, idempotency_ref, usd_nanos_delta, tokens_delta, model_calls_delta, wall_time_ms_delta, depth_delta, retries_delta, subagents_delta, created_at)
VALUES ($1,'inherited',$2,$3,$4,$5,$6,0,$7,1,$8)
ON CONFLICT (budget_id, kind, idempotency_ref) DO NOTHING`,
		parentBudgetID, childTaskID, int64(allocation.MaxUSD), allocation.MaxTokens, allocation.MaxModelCalls, allocation.MaxWallTimeMS, allocation.MaxRetries, now)
	if err != nil {
		return agentbudget.Budget{}, err
	}
	if tag.RowsAffected() == 0 {
		child, err := scanBudget(tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM agent_budgets WHERE task_id=$1`, childTaskID))
		if err != nil {
			return agentbudget.Budget{}, err
		}
		return child, tx.Commit(ctx)
	}

	if parent.Usage.UsedSubagents+1 > parent.Limits.MaxSubagents {
		return agentbudget.Budget{}, fmt.Errorf("%w: subagent count would exceed max %d", agentbudget.ErrBudgetExceeded, parent.Limits.MaxSubagents)
	}
	remainingUSD := parent.Limits.MaxUSD - parent.Usage.UsedUSD
	remainingTokens := parent.Limits.MaxTokens - parent.Usage.UsedTokens
	remainingCalls := parent.Limits.MaxModelCalls - parent.Usage.UsedModelCalls
	remainingWallTime := parent.Limits.MaxWallTimeMS - parent.Usage.UsedWallTimeMS
	remainingRetries := parent.Limits.MaxRetries - parent.Usage.UsedRetries
	if allocation.MaxUSD > remainingUSD || allocation.MaxTokens > remainingTokens || allocation.MaxModelCalls > remainingCalls ||
		allocation.MaxWallTimeMS > remainingWallTime || allocation.MaxRetries > remainingRetries {
		return agentbudget.Budget{}, fmt.Errorf("%w: parent has insufficient remaining budget for this allocation", agentbudget.ErrParentExhausted)
	}

	var childBudgetID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO agent_budgets (
    organization_id, root_task_id, task_id, role_id, parent_budget_id,
    max_usd_nanos, max_tokens, max_model_calls, max_wall_time_ms, max_depth, max_retries, max_subagents,
    depth, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)
RETURNING id`,
		parent.OrganizationID, parent.RootTaskID, childTaskID, childRoleID, parentBudgetID,
		int64(allocation.MaxUSD), allocation.MaxTokens, allocation.MaxModelCalls, allocation.MaxWallTimeMS, allocation.MaxDepth, allocation.MaxRetries, allocation.MaxSubagents,
		childDepth, now,
	).Scan(&childBudgetID); err != nil {
		return agentbudget.Budget{}, err
	}

	newDepth := parent.Usage.Depth
	if childDepth > newDepth {
		newDepth = childDepth
	}
	if _, err := tx.Exec(ctx, `
UPDATE agent_budgets SET
    used_usd_nanos=used_usd_nanos+$1, used_tokens=used_tokens+$2, used_model_calls=used_model_calls+$3,
    used_wall_time_ms=used_wall_time_ms+$4, used_retries=used_retries+$5,
    depth=$6, used_subagents=used_subagents+1, version=version+1, updated_at=$7
WHERE id=$8`,
		int64(allocation.MaxUSD), allocation.MaxTokens, allocation.MaxModelCalls, allocation.MaxWallTimeMS, allocation.MaxRetries, newDepth, now, parentBudgetID); err != nil {
		return agentbudget.Budget{}, err
	}

	child, err := scanBudget(tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM agent_budgets WHERE id=$1`, childBudgetID))
	if err != nil {
		return agentbudget.Budget{}, err
	}
	return child, tx.Commit(ctx)
}

func (s *Store) ConsumeModelCall(ctx context.Context, budgetID int64, invocationID int64, delta agentbudget.Usage, now time.Time) error {
	now = now.UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin consume model call: %w", err)
	}
	defer tx.Rollback(ctx)

	current, err := scanBudget(tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM agent_budgets WHERE id=$1 FOR UPDATE`, budgetID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentbudget.ErrBudgetNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO agent_budget_events (budget_id, kind, idempotency_ref, usd_nanos_delta, tokens_delta, model_calls_delta, wall_time_ms_delta, depth_delta, retries_delta, subagents_delta, created_at)
VALUES ($1,'consumed',$2,$3,$4,$5,$6,0,$7,0,$8)
ON CONFLICT (budget_id, kind, idempotency_ref) DO NOTHING`,
		budgetID, invocationID, int64(delta.UsedUSD), delta.UsedTokens, delta.UsedModelCalls, delta.UsedWallTimeMS, delta.UsedRetries, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	next, err := agentbudget.Reserve(current.Usage, delta, current.Limits)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
UPDATE agent_budgets SET
    used_usd_nanos=$1, used_tokens=$2, used_model_calls=$3, used_wall_time_ms=$4, used_retries=$5,
    version=version+1, updated_at=$6
WHERE id=$7`,
		int64(next.UsedUSD), next.UsedTokens, next.UsedModelCalls, next.UsedWallTimeMS, next.UsedRetries, now, budgetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
