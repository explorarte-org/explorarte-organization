// Package smoke proves the CEO -> leader -> worker -> leader -> CEO
// executive messaging/principal chain against a real, live Postgres —
// including production — without ever touching the durable task engine's
// executable path.
//
// It exercises exactly the same real components production wiring uses
// (agentmessagingpostgres.Store as the Ledger, modeldispatch/postgres.Store
// as the PrincipalStore, runtimeadapter.AgentMessages as the adapter), so a
// passing smoke run is evidence about the real code path, not a simulation
// of it.
//
// The one deliberate departure from a real business task is how its three
// support tasks are created: internal/tasks/postgres.Store.Create can only
// insert a task into a non-terminal status (there is no supported way to
// ask the durable task engine's own Create/Finalize state machine for a
// task that is terminal from birth — Finalize's no_action outcome requires
// the task to already be awaiting_verification, which itself requires
// passing through ready/leased/running first). A smoke support task must
// NEVER be visible to any reader in ready/leased/running, not even for one
// transaction's duration, so this package inserts directly with
// status=no_action and terminal_at populated in the same statement that
// creates the row — the row never exists in any other status. See
// createSupportTask for the exact statement.
package smoke

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	agentmessagingpostgres "github.com/Mireuz13/explorarte-organization/internal/agentmessaging/postgres"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	modeldispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	"github.com/jackc/pgx/v5/pgxpool"
)

// agentMessageRateLimitMax/Window mirror the exact values production
// bootstrap uses (internal/executive/bootstrap/runtime.go) — the smoke must
// exercise the same rate-limit configuration real traffic does, not a
// looser one that would hide a real production issue.
const (
	agentMessageRateLimitMax    = 200
	agentMessageRateLimitWindow = time.Hour
	agentMessageMaxAttempts     = 10
)

// Wire constructs the exact same real components production bootstrap uses
// for executive messaging (internal/executive/bootstrap/runtime.go) — the
// same Ledger and PrincipalStore concrete types, the same authorizer, the
// same rate limits — so a smoke run exercises production's real code path,
// not a parallel reimplementation of it that could drift.
func Wire(cfg config.Config, store *platformpostgres.Store) (runtimeadapter.AgentMessages, error) {
	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		return runtimeadapter.AgentMessages{}, fmt.Errorf("create registry repository: %w", err)
	}
	authorizationRuntime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		return runtimeadapter.AgentMessages{}, fmt.Errorf("open authorization runtime: %w", err)
	}
	agentMessageLedger, err := agentmessagingpostgres.New(store, registryRepository, authorizationRuntime.Authorizer, agentMessageRateLimitMax, agentMessageRateLimitWindow)
	if err != nil {
		return runtimeadapter.AgentMessages{}, fmt.Errorf("create agent message ledger: %w", err)
	}
	principalStore, err := modeldispatchpostgres.New(store)
	if err != nil {
		return runtimeadapter.AgentMessages{}, fmt.Errorf("create principal store: %w", err)
	}
	return runtimeadapter.AgentMessages{
		Ledger:         agentMessageLedger,
		MaxAttempts:    agentMessageMaxAttempts,
		PrincipalStore: principalStore,
		OrganizationID: cfg.Tasks.OrganizationID,
	}, nil
}

// Roles names the three real, already-registered production roles a smoke
// run exercises. These must be real roles the organization's registry
// already knows about — the smoke never creates or syncs registry state.
type Roles struct {
	CEO    string
	Leader string
	Worker string
}

// Result is what one smoke run produced: the three support tasks, the four
// messages sent, and whether each hop's role-bound principal already
// existed or had to be lazily provisioned by this run.
type Result struct {
	CorrelationID string
	CEOTask       executive.TaskRecord
	LeaderTask    executive.TaskRecord
	WorkerTask    executive.TaskRecord
	Hops          []HopOutcome
}

// HopOutcome records one of the four sends.
type HopOutcome struct {
	Label                string // "ceo_to_leader_delegation", etc.
	SenderRoleID         string
	RecipientRoleID      string
	MessageType          string
	PrincipalWasExisting bool // true if the role-bound principal already existed before this run
}

// NewCorrelationID returns a unique, clearly-tagged correlation id for one
// smoke run. The "smoke/" prefix is load-bearing: it is what lets a human
// or a query immediately tell a smoke-generated row apart from genuine
// business traffic (see POST_SMOKE_MESSAGE_SAFETY in the branch report).
func NewCorrelationID(now time.Time) string {
	return fmt.Sprintf("smoke/%s/%d", now.UTC().Format("20060102T150405Z"), now.UnixNano())
}

// Run creates the three support tasks and sends all four messages of the
// CEO -> leader -> worker -> leader -> CEO chain, using the real
// AgentMessages adapter unmodified — no special-cased "smoke mode" inside
// SendDelegation/SendCompletion, no altered idempotency-key contract. The
// only smoke-specific behavior lives here, in how the three support tasks
// are created.
func Run(ctx context.Context, pool *pgxpool.Pool, messages runtimeadapter.AgentMessages, organizationID string, roles Roles, correlationID string, now time.Time) (Result, error) {
	existingCEO, err := principalAlreadyActive(ctx, messages.PrincipalStore, organizationID, roles.CEO)
	if err != nil {
		return Result{}, fmt.Errorf("check existing CEO principal: %w", err)
	}
	existingLeader, err := principalAlreadyActive(ctx, messages.PrincipalStore, organizationID, roles.Leader)
	if err != nil {
		return Result{}, fmt.Errorf("check existing leader principal: %w", err)
	}
	existingWorker, err := principalAlreadyActive(ctx, messages.PrincipalStore, organizationID, roles.Worker)
	if err != nil {
		return Result{}, fmt.Errorf("check existing worker principal: %w", err)
	}

	ceoTask, err := createSupportTask(ctx, pool, organizationID, roles.CEO, correlationID, "ceo")
	if err != nil {
		return Result{}, fmt.Errorf("create CEO support task: %w", err)
	}
	leaderTask, err := createSupportTask(ctx, pool, organizationID, roles.Leader, correlationID, "leader")
	if err != nil {
		return Result{}, fmt.Errorf("create leader support task: %w", err)
	}
	workerTask, err := createSupportTask(ctx, pool, organizationID, roles.Worker, correlationID, "worker")
	if err != nil {
		return Result{}, fmt.Errorf("create worker support task: %w", err)
	}

	result := Result{CorrelationID: correlationID, CEOTask: ceoTask, LeaderTask: leaderTask, WorkerTask: workerTask}

	if err := messages.SendDelegation(ctx, ceoTask, leaderTask, now); err != nil {
		return result, fmt.Errorf("ceo -> leader delegation: %w", err)
	}
	result.Hops = append(result.Hops, HopOutcome{Label: "ceo_to_leader_delegation", SenderRoleID: roles.CEO, RecipientRoleID: roles.Leader, MessageType: "delegation", PrincipalWasExisting: existingCEO})

	if err := messages.SendDelegation(ctx, leaderTask, workerTask, now); err != nil {
		return result, fmt.Errorf("leader -> worker delegation: %w", err)
	}
	result.Hops = append(result.Hops, HopOutcome{Label: "leader_to_worker_delegation", SenderRoleID: roles.Leader, RecipientRoleID: roles.Worker, MessageType: "delegation", PrincipalWasExisting: existingLeader})

	if err := messages.SendCompletion(ctx, workerTask, leaderTask, now); err != nil {
		return result, fmt.Errorf("worker -> leader completion: %w", err)
	}
	result.Hops = append(result.Hops, HopOutcome{Label: "worker_to_leader_completion", SenderRoleID: roles.Worker, RecipientRoleID: roles.Leader, MessageType: "completion", PrincipalWasExisting: existingWorker})

	if err := messages.SendCompletion(ctx, leaderTask, ceoTask, now); err != nil {
		return result, fmt.Errorf("leader -> ceo completion: %w", err)
	}
	// The leader principal is used twice (hop 2 and hop 4); by hop 4 it
	// necessarily already exists (hop 2 either found it or provisioned it),
	// so this reports the state as of the START of this run, same as the
	// other three, not "existing by the time we get here" trivia.
	result.Hops = append(result.Hops, HopOutcome{Label: "leader_to_ceo_completion", SenderRoleID: roles.Leader, RecipientRoleID: roles.CEO, MessageType: "completion", PrincipalWasExisting: existingLeader})

	return result, nil
}

func principalAlreadyActive(ctx context.Context, store modeldispatch.PrincipalStore, organizationID, roleID string) (bool, error) {
	_, err := store.ResolveActiveForRole(ctx, organizationID, roleID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, modeldispatch.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// createSupportTask inserts a task row that is terminal (status=no_action,
// terminal_at populated) in the SAME statement that creates it. No other
// session can ever observe this row in pending/ready/leased/running: it
// simply does not exist in the database until it exists already-terminal.
//
// This deliberately bypasses internal/tasks/postgres.Store.Create (which
// can only insert into a non-terminal initial status) — using it here
// would put the row into 'pending', where a real reconciler polling the
// same database could theoretically claim it before this function's own
// follow-up finalize call ever ran. A synthetic smoke fixture must never
// create that window, so there is no follow-up call: the row is born done.
func createSupportTask(ctx context.Context, pool *pgxpool.Pool, organizationID, roleID, correlationID, label string) (executive.TaskRecord, error) {
	var revisionID int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(current_revision_id,0) FROM organizations WHERE id=$1 AND retired_at IS NULL`, organizationID).Scan(&revisionID); err != nil {
		return executive.TaskRecord{}, fmt.Errorf("resolve organization revision: %w", err)
	}
	var unitID string
	if err := pool.QueryRow(ctx, `SELECT unit_id FROM organization_roles WHERE organization_id=$1 AND id=$2 AND retired_at IS NULL`, organizationID, roleID).Scan(&unitID); err != nil {
		return executive.TaskRecord{}, fmt.Errorf("resolve unit for role %q: %w", roleID, err)
	}

	idempotencyKey := fmt.Sprintf("smoke-support-task:%s:%s", correlationID, label)
	requestHash := sha256Hex(fmt.Sprintf("smoke-support-task:%s:%s:%s", organizationID, roleID, correlationID))
	title := fmt.Sprintf("[smoke] %s support task", label)
	instructions := fmt.Sprintf(
		"Synthetic, non-executable support task created by the production-safe executive "+
			"messaging smoke (correlation %s). This task was born in a terminal status "+
			"(no_action) and must never be claimed, leased, or executed.",
		correlationID,
	)

	const query = `
		INSERT INTO tasks(
			organization_id, organization_revision_id, requested_by_role_id, assigned_role_id, assigned_unit_id,
			idempotency_key, request_hash, title, instructions, acceptance_criteria, status, priority, available_at,
			max_attempts, correlation_id, causation_id, terminal_at
		) VALUES ($1,$2,NULL,$3,$4,$5,$6,$7,$8,'[]'::jsonb,$9,0,NOW(),1,$10,$10,NOW())
		RETURNING id, organization_id, organization_revision_id, assigned_role_id, assigned_unit_id,
			idempotency_key, request_hash, title, instructions, status, priority, max_attempts, correlation_id, causation_id
	`
	var t executive.TaskRecord
	err := pool.QueryRow(ctx, query,
		organizationID, revisionID, roleID, unitID, idempotencyKey, requestHash, title, instructions,
		string(tasks.StatusNoAction), correlationID,
	).Scan(
		&t.ID, &t.OrganizationID, &t.OrganizationRevisionID, &t.AssignedRoleID, &t.AssignedUnitID,
		&t.IdempotencyKey, &t.RequestHash, &t.Title, &t.Instructions, &t.Status, &t.Priority, &t.MaxAttempts,
		&t.CorrelationID, &t.CausationID,
	)
	if err != nil {
		return executive.TaskRecord{}, fmt.Errorf("insert support task: %w", err)
	}
	return t, nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// MessageInvariant is the read-back, byte-level proof for one persisted
// agent_messages row: the invariant that
// agent_messages.sender_role_id == tasks.assigned_role_id ==
// model_execution_principals.dispatch_actor_role_id must hold for every
// hop, not just be true "by construction" of the code that wrote it.
type MessageInvariant struct {
	MessageID                    int64
	SenderTaskID                 int64
	SenderRoleIDOnMessage        string
	SenderRoleIDOnTask           string
	PrincipalDispatchActorRoleID string
	RecipientTaskID              int64
	RecipientRoleIDOnMessage     string
	CorrelationID                string
	MessageType                  string
	Identical                    bool
}

// VerifyReport is the full read-back verification of one smoke run.
type VerifyReport struct {
	CorrelationID    string
	Messages         []MessageInvariant
	AllFourPresent   bool
	AllCorrelated    bool
	AllIdentical     bool
	SupportTasksSafe bool // true if none of the 3 support tasks are in an executable status
}

// Verify re-reads the database (never trusting the in-process Result alone)
// and checks every invariant the production-safe smoke must prove:
//   - exactly 4 messages exist for this correlation id;
//   - every one of them carries this exact correlation id (not just "some
//     truthy value");
//   - for every message, sender_role_id (on the message) ==
//     assigned_role_id (on the sender task) ==
//     dispatch_actor_role_id (on the resolved principal);
//   - the three support tasks are still in a non-executable status.
func Verify(ctx context.Context, pool *pgxpool.Pool, organizationID, correlationID string) (VerifyReport, error) {
	report := VerifyReport{CorrelationID: correlationID}

	rows, err := pool.Query(ctx, `
		SELECT am.id, am.sender_task_id, am.sender_role_id, st.assigned_role_id,
		       am.recipient_task_id, am.recipient_role_id, am.correlation_id, am.message_type,
		       mep.dispatch_actor_role_id
		FROM agent_messages am
		JOIN tasks st ON st.id = am.sender_task_id
		LEFT JOIN model_execution_principals mep
		       ON mep.organization_id = st.organization_id
		      AND mep.dispatch_actor_role_id = st.assigned_role_id
		      AND mep.status = 'active'
		      AND mep.principal_key = $1 || st.assigned_role_id
		WHERE am.organization_id = $2 AND am.correlation_id = $3
		ORDER BY am.id
	`, modeldispatch.RoleBoundPrincipalKeyPrefix, organizationID, correlationID)
	if err != nil {
		return report, fmt.Errorf("query messages for correlation %q: %w", correlationID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var m MessageInvariant
		var principalRole *string
		if err := rows.Scan(&m.MessageID, &m.SenderTaskID, &m.SenderRoleIDOnMessage, &m.SenderRoleIDOnTask,
			&m.RecipientTaskID, &m.RecipientRoleIDOnMessage, &m.CorrelationID, &m.MessageType, &principalRole); err != nil {
			return report, fmt.Errorf("scan message invariant row: %w", err)
		}
		if principalRole != nil {
			m.PrincipalDispatchActorRoleID = *principalRole
		}
		m.Identical = m.SenderRoleIDOnMessage == m.SenderRoleIDOnTask && m.SenderRoleIDOnTask == m.PrincipalDispatchActorRoleID
		report.Messages = append(report.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate message invariant rows: %w", err)
	}

	report.AllFourPresent = len(report.Messages) == 4
	report.AllCorrelated = true
	report.AllIdentical = true
	for _, m := range report.Messages {
		if m.CorrelationID != correlationID {
			report.AllCorrelated = false
		}
		if !m.Identical {
			report.AllIdentical = false
		}
	}

	var executableCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM tasks
		WHERE organization_id = $1 AND correlation_id = $2
		  AND status IN ('pending','ready','leased','running','awaiting_verification','blocked','retry_wait')
	`, organizationID, correlationID).Scan(&executableCount); err != nil {
		return report, fmt.Errorf("check support task executability: %w", err)
	}
	report.SupportTasksSafe = executableCount == 0

	return report, nil
}
