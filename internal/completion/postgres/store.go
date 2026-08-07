// Package postgres implements internal/completion's five read-only ports by
// querying internal/tasks, internal/staging, internal/authorization and
// internal/decisiongraph's own tables directly via SQL — the same
// read-another-branch's-tables-not-its-Go-package pattern established by
// internal/cellworker/postgres and internal/decisiongraphtrace. completion owns no
// tables and no migration of its own beyond this file's queries.
package postgres

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/completion"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("completion store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("completion store requires an organization id")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

var (
	_ completion.TaskReader            = (*Store)(nil)
	_ completion.ArtifactChecker       = (*Store)(nil)
	_ completion.CheckRunChecker       = (*Store)(nil)
	_ completion.ApprovalChecker       = (*Store)(nil)
	_ completion.DecisionBranchChecker = (*Store)(nil)
)

func (s *Store) TaskFacts(ctx context.Context, taskID int64) (completion.TaskFact, error) {
	var status string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1 AND organization_id=$2`, taskID, s.organizationID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return completion.TaskFact{}, completion.ErrTaskNotFound
		}
		return completion.TaskFact{}, err
	}

	rows, err := s.pool.Query(ctx, `
SELECT r.id, r.requirement_type, r.required, r.status, COALESCE(e.reference, ''), COALESCE(e.digest, '')
FROM task_requirements r
LEFT JOIN LATERAL (
    SELECT reference, digest FROM task_evidence WHERE requirement_id = r.id ORDER BY id DESC LIMIT 1
) e ON true
WHERE r.task_id = $1
ORDER BY r.id`, taskID)
	if err != nil {
		return completion.TaskFact{}, err
	}
	defer rows.Close()

	fact := completion.TaskFact{TaskID: taskID, Status: status}
	for rows.Next() {
		var (
			id              int64
			requirementType string
			required        bool
			reqStatus       string
			reference       string
			digest          string
		)
		if err := rows.Scan(&id, &requirementType, &required, &reqStatus, &reference, &digest); err != nil {
			return completion.TaskFact{}, err
		}
		fact.Requirements = append(fact.Requirements, completion.RequirementFact{
			RequirementID:  id,
			Type:           completion.RequirementType(requirementType),
			Required:       required,
			Satisfied:      reqStatus == "satisfied",
			EvidenceRef:    reference,
			EvidenceDigest: digest,
		})
	}
	if err := rows.Err(); err != nil {
		return completion.TaskFact{}, err
	}
	return fact, nil
}

// ArtifactDigest resolves the real, stored content digest for a staging artifact
// by its storage key — internal/staging's artifactfs.Store writes artifacts under
// storage_key format 'artifact://sha256/<hex>', and task_evidence.reference for an
// artifact-type requirement is expected to hold that same storage key.
func (s *Store) ArtifactDigest(ctx context.Context, reference string) (string, error) {
	var digest string
	err := s.pool.QueryRow(ctx, `SELECT digest FROM staging_artifacts WHERE storage_key=$1`, reference).Scan(&digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", completion.ErrArtifactNotFound
	}
	if err != nil {
		return "", err
	}
	return digest, nil
}

// CheckPassed reads Rama 05's own record of whether a check-type requirement's
// staging check actually ran and passed, independent of task_evidence's
// self-reported status.
func (s *Store) CheckPassed(ctx context.Context, taskID, requirementID int64) (bool, error) {
	var status string
	err := s.pool.QueryRow(ctx, `
SELECT status FROM staging_checks WHERE task_id=$1 AND requirement_id=$2 ORDER BY id DESC LIMIT 1`,
		taskID, requirementID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "passed", nil
}

// ApprovalConsumed independently confirms an authorization_requests row is
// consumed and its stored action digest matches. requestRef is expected to hold
// the ApprovalRequest ID as a base-10 string (task_evidence.reference for an
// approval-type requirement); a malformed reference is treated as "not consumed"
// rather than an error, since it can never resolve to a real request.
func (s *Store) ApprovalConsumed(ctx context.Context, requestRef, actionDigest string) (bool, error) {
	requestID, parseErr := strconv.ParseInt(strings.TrimSpace(requestRef), 10, 64)
	if parseErr != nil {
		return false, nil
	}
	var storedDigest string
	err := s.pool.QueryRow(ctx, `
SELECT action_digest FROM authorization_requests WHERE id=$1 AND organization_id=$2 AND status='consumed'`,
		requestID, s.organizationID).Scan(&storedDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return storedDigest == actionDigest, nil
}

// CurrentBranchStateForAttempt resolves the current branch_state of the terminal
// decision's selected candidate node for the most recent succeeded decision graph
// run matching (taskID, attemptID). found=false when no such run/decision exists
// yet — most tasks never touch the decision graph at all.
func (s *Store) CurrentBranchStateForAttempt(ctx context.Context, taskID, attemptID int64) (string, bool, error) {
	var state string
	err := s.pool.QueryRow(ctx, `
SELECT n.branch_state
FROM decision_graph_runs r
JOIN decision_records dr ON dr.run_id = r.id
JOIN decision_graph_nodes n ON n.id = dr.selected_candidate_node_id
WHERE r.task_id=$1 AND r.attempt_id=$2 AND r.organization_id=$3 AND r.status='succeeded'
ORDER BY r.id DESC LIMIT 1`, taskID, attemptID, s.organizationID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return state, true, nil
}
