package postgres

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5"
)

// maxAncestorDepth bounds the causation walk. Real trees are three or four
// levels deep; the bound only exists so a corrupt cycle can never loop.
const maxAncestorDepth = 64

// blockingAncestorPredicate is a NOT EXISTS body correlated with the outer
// task alias t0: it is true when some ancestor of t0 (by the "task:<id>"
// causation chain) is in a status that withdraws its descendants and is of a
// class that scopes them. It takes the blocking statuses ($n) and the
// non-scoping classes ($m) so it is derived from the same definitions
// (tasks.BlocksDescendantClaim, tasks.TaskClassScopesDescendants) the in-
// transaction check evaluates.
func blockingAncestorPredicate(statusesArg, classesArg int) string {
	return `EXISTS (
		WITH RECURSIVE chain(id, causation_id, depth) AS (
			SELECT t0.id, t0.causation_id, 0
			UNION ALL
			SELECT p.id, p.causation_id, c.depth+1 FROM chain c
			JOIN tasks p ON p.organization_id = t0.organization_id
				AND p.id = NULLIF(substring(c.causation_id from '^task:([0-9]+)$'), '')::bigint
			WHERE c.depth < ` + itoa(maxAncestorDepth) + `
		)
		SELECT 1 FROM chain c JOIN tasks a ON a.id = c.id
		WHERE c.depth > 0 AND a.status = ANY($` + itoa(statusesArg) + `::text[]) AND a.task_class <> ALL($` + itoa(classesArg) + `::text[])
	)`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

// blockingAncestor decides, INSIDE the claim transaction, whether a blocking
// ancestor exists. It locks the ancestors FOR SHARE first: a concurrent
// BlockTask on any of them takes FOR UPDATE, so the two serialize. Either the
// block commits first (and this reads it, refusing the claim), or the claim
// commits first (and the block lands after work that was already legitimately
// started). No claim can begin after a blocking transition has committed.
//
// It returns nil when nothing blocks. It never mutates anything.
func blockingAncestor(ctx context.Context, tx pgx.Tx, task tasks.Task) (*tasks.AncestorBlock, error) {
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE chain(id, causation_id, depth) AS (
			SELECT id, causation_id, 0 FROM tasks WHERE id=$1 AND organization_id=$2
			UNION ALL
			SELECT p.id, p.causation_id, c.depth+1 FROM chain c
			JOIN tasks p ON p.organization_id=$2
				AND p.id = NULLIF(substring(c.causation_id from '^task:([0-9]+)$'), '')::bigint
			WHERE c.depth < `+itoa(maxAncestorDepth)+`
		)
		SELECT id FROM chain WHERE depth > 0 ORDER BY depth`, task.ID, task.OrganizationID)
	if err != nil {
		return nil, mapError(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	locked, err := tx.Query(ctx, `SELECT id, status, task_class FROM tasks WHERE id = ANY($1) ORDER BY id FOR SHARE`, ids)
	if err != nil {
		return nil, mapError(err)
	}
	defer locked.Close()
	var found *tasks.AncestorBlock
	for locked.Next() {
		var ancestor tasks.AncestorBlock
		var status string
		if err := locked.Scan(&ancestor.TaskID, &status, &ancestor.TaskClass); err != nil {
			return nil, mapError(err)
		}
		ancestor.Status = tasks.Status(status)
		if ancestor.Blocks() && (found == nil || ancestor.TaskID < found.TaskID) {
			candidate := ancestor
			found = &candidate
		}
	}
	if err := locked.Err(); err != nil {
		return nil, mapError(err)
	}
	return found, nil
}
