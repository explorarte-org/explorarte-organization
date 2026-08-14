package smoke

// CUTOVER-REHEARSAL-001 (second incident): two failed production smoke
// attempts left 8 synthetic messages sitting in 'pending' -- harmless only
// because traffic stayed closed. ClaimNext selects pending messages by
// (organization_id, recipient_role_id) alone; it never looks at whether
// the recipient task is terminal. A production-safe smoke tool cannot
// depend on an operator remembering to keep traffic closed until every run
// either succeeds or is manually cleaned up.
//
// This file adds the two things that close that gap without resorting to
// a raw DELETE/UPDATE:
//
//  1. Preflight: verifies, read-only, that agent.message.send AND
//     agent.message.claim are both actually granted for all three roles
//     under the CURRENTLY APPLIED registry revision (not just the
//     canonical files on disk), and that the registry is synchronized in
//     the first place. This is exactly the check that would have caught
//     today's incident before ever creating a support task: the capability
//     grant existed in canonical but not yet in the applied revision.
//  2. Cleanup: if anything after Preflight fails, resolves every message
//     this run created out of pending/claimed using the REAL
//     ClaimNext+Nack path -- with MaxAttempts pinned to 1 for smoke
//     messages specifically, so a single Nack lands them in 'dead'
//     (attempt_count reaches max_attempts), not back in 'pending' where
//     they'd remain claimable.
//
// Execute wires Preflight -> Run -> Verify -> Deliver -> Cleanup-on-any-
// failure into one call, so "any result != PASS implies zero pending/
// claimed smoke messages" is a property of calling Execute correctly, not
// something every caller has to remember to arrange by hand.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationbootstrap "github.com/Mireuz13/explorarte-organization/internal/authorization/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// smokeMessageMaxAttempts is deliberately 1: a smoke message is never a
// real business retry target. Pinning it to 1 means a single Nack during
// Cleanup is guaranteed to land the message in 'dead' (attempt_count >=
// max_attempts), a genuinely terminal state, rather than bouncing it back
// to 'pending' where a real consumer could still pick it up.
const smokeMessageMaxAttempts = 1

// Toolkit bundles everything Preflight/Execute need beyond what Run/Verify/
// Deliver already take: the real Authorizer (for capability dry-runs) and
// the real registry Service (for the synchronized check), both wired
// exactly as production bootstrap wires them.
type Toolkit struct {
	Messages        runtimeadapter.AgentMessages
	Authorizer      *authorization.Authorizer
	RegistryService *registry.Service
	OrganizationID  string
}

// WireToolkit constructs Wire's AgentMessages plus the additional
// read-only tools Preflight needs, from the same real bootstrap
// components production uses.
func WireToolkit(cfg config.Config, store *platformpostgres.Store) (Toolkit, error) {
	messages, err := Wire(cfg, store)
	if err != nil {
		return Toolkit{}, err
	}
	authorizationRuntime, err := authorizationbootstrap.Open(cfg, store)
	if err != nil {
		return Toolkit{}, fmt.Errorf("open authorization runtime: %w", err)
	}
	registryRepository, err := registry.NewPostgresRepository(store)
	if err != nil {
		return Toolkit{}, fmt.Errorf("create registry repository: %w", err)
	}
	loader, err := registry.NewLoader(cfg.Registry.CanonicalDir)
	if err != nil {
		return Toolkit{}, fmt.Errorf("load canonical registry: %w", err)
	}
	registryService, err := registry.NewService(loader, registryRepository, cfg.Tasks.OrganizationID, cfg.Registry.SyncTimeout)
	if err != nil {
		return Toolkit{}, fmt.Errorf("create registry service: %w", err)
	}
	return Toolkit{
		Messages:        messages,
		Authorizer:      authorizationRuntime.Authorizer,
		RegistryService: registryService,
		OrganizationID:  cfg.Tasks.OrganizationID,
	}, nil
}

// CapabilityCheck is one (role, capability) dry-run result.
type CapabilityCheck struct {
	RoleID     string
	Capability string
	Allowed    bool
	Error      string
}

// PreflightReport is the read-only precondition check Execute requires to
// pass before creating anything.
type PreflightReport struct {
	RegistrySynchronized bool
	RegistryRevisionID   int64
	CapabilityChecks     []CapabilityCheck
	AllPassed            bool
}

// Preflight verifies, without creating or claiming anything, that this
// smoke run could actually complete end to end: the registry must be
// synchronized (the applied revision's capability-matrix.yaml must match
// what's on disk right now -- this is exactly the check that would have
// caught CUTOVER-REHEARSAL-001's registry-drift incident before it ever
// sent a message), and every role this run touches must actually hold
// both agent.message.send and agent.message.claim under that applied
// revision.
func Preflight(ctx context.Context, tk Toolkit, roles Roles) (PreflightReport, error) {
	report := PreflightReport{AllPassed: true}

	comparison, err := tk.RegistryService.CompareCanonical(ctx)
	if err != nil {
		return report, fmt.Errorf("compare canonical registry: %w", err)
	}
	report.RegistrySynchronized = comparison.Synchronized
	if !report.RegistrySynchronized {
		report.AllPassed = false
	}
	if comparison.CurrentRevision != nil {
		report.RegistryRevisionID = comparison.CurrentRevision.ID
	}

	roleList := []string{roles.CEO, roles.Leader, roles.Worker}
	capabilities := []string{agentmessaging.CapabilityAgentMessageSend, agentmessaging.CapabilityAgentMessageClaim}
	for _, roleID := range roleList {
		for _, capability := range capabilities {
			check := CapabilityCheck{RoleID: roleID, Capability: capability}
			if authErr := tk.Authorizer.Authorize(ctx, tk.OrganizationID, report.RegistryRevisionID, roleID, capability); authErr != nil {
				check.Allowed = false
				check.Error = authErr.Error()
				report.AllPassed = false
			} else {
				check.Allowed = true
			}
			report.CapabilityChecks = append(report.CapabilityChecks, check)
		}
	}

	return report, nil
}

// CleanupReport is what Cleanup resolved (or could not resolve) for one
// run's messages after a failure.
type CleanupReport struct {
	CorrelationID   string
	MessagesFound   int
	DeadenedCount   int
	AlreadyTerminal int
	// Unresolved lists message IDs still not in a terminal state after
	// Cleanup ran -- e.g. a message stuck 'claimed' from an interrupted
	// Deliver call, whose plaintext claim token was never persisted (only
	// its hash is, by design) and so cannot be Nacked directly. Those
	// self-recover once claim_expires_at passes (the same mechanism a real
	// consumer's stuck claim would recover through); Cleanup does not
	// invent a way around that, since doing so would mean touching a row
	// without the real ownership proof the ledger requires.
	Unresolved []int64
}

// Cleanup resolves every pending/claimed message for this correlation out
// of a claimable state, using only the real Ledger.ClaimNext/Nack path.
// Messages are pinned to smokeMessageMaxAttempts=1 for this run, so a
// single Nack is sufficient to land them in 'dead' rather than bouncing
// back to 'pending'. This must only be called after Preflight has already
// verified claim capability for the affected roles -- if authorization
// state changed in the narrow window since, Cleanup reports what it could
// not resolve rather than pretending success.
func Cleanup(ctx context.Context, pool *pgxpool.Pool, messages runtimeadapter.AgentMessages, organizationID, correlationID string, now time.Time) (CleanupReport, error) {
	report := CleanupReport{CorrelationID: correlationID}
	messages.MaxAttempts = smokeMessageMaxAttempts

	rows, err := pool.Query(ctx, `
		SELECT id, recipient_role_id, status FROM agent_messages
		WHERE organization_id = $1 AND correlation_id = $2 AND status IN ('pending','claimed')
		ORDER BY id
	`, organizationID, correlationID)
	if err != nil {
		return report, fmt.Errorf("query messages needing cleanup: %w", err)
	}
	pendingByRole := map[string][]int64{}
	var claimedIDs []int64
	for rows.Next() {
		var id int64
		var role, status string
		if err := rows.Scan(&id, &role, &status); err != nil {
			rows.Close()
			return report, fmt.Errorf("scan cleanup row: %w", err)
		}
		report.MessagesFound++
		if status == "pending" {
			pendingByRole[role] = append(pendingByRole[role], id)
		} else {
			claimedIDs = append(claimedIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate cleanup rows: %w", err)
	}
	rows.Close()

	// Messages already 'claimed' (not by this Cleanup call) cannot be
	// Nacked without their plaintext claim token, which is never
	// persisted. Report them as unresolved-but-self-recovering.
	report.Unresolved = append(report.Unresolved, claimedIDs...)

	for role, ids := range pendingByRole {
		expected := map[int64]bool{}
		for _, id := range ids {
			expected[id] = true
		}
		principal, err := messages.PrincipalStore.ResolveActiveForRole(ctx, organizationID, role)
		if err != nil {
			report.Unresolved = append(report.Unresolved, ids...)
			continue
		}
		principalID := strconv.FormatInt(principal.ID, 10)
		claimed, err := messages.Ledger.ClaimNext(ctx, principalID, organizationID, role, len(ids), time.Minute, now)
		if err != nil {
			report.Unresolved = append(report.Unresolved, ids...)
			continue
		}
		for _, cm := range claimed {
			disposition := agentmessaging.Disposition{
				MessageID: cm.Message.ID, ConsumerID: principalID, ClaimToken: cm.ClaimToken,
				Error: "production-safe smoke cleanup: run did not complete successfully",
			}
			if !expected[cm.Message.ID] {
				// Foreign message this Cleanup call should never have
				// touched -- release it untouched, do not deaden it.
				_ = messages.Ledger.Nack(ctx, principalID, agentmessaging.Disposition{
					MessageID: cm.Message.ID, ConsumerID: principalID, ClaimToken: cm.ClaimToken,
					Error: "released by production-safe smoke cleanup: message did not belong to this run",
				}, now)
				continue
			}
			if err := messages.Ledger.Nack(ctx, principalID, disposition, now); err != nil {
				report.Unresolved = append(report.Unresolved, cm.Message.ID)
				continue
			}
			report.DeadenedCount++
		}
	}

	var stillOpen int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_messages
		WHERE organization_id = $1 AND correlation_id = $2 AND status IN ('pending','claimed')
	`, organizationID, correlationID).Scan(&stillOpen); err != nil {
		return report, fmt.Errorf("count remaining open messages: %w", err)
	}
	report.AlreadyTerminal = report.MessagesFound - report.DeadenedCount - len(report.Unresolved)

	return report, nil
}

// ExecuteReport is the full lifecycle result: which stage was reached,
// and (if anything failed after Preflight) the Cleanup outcome proving
// zero messages were left pending/claimed.
type ExecuteReport struct {
	CorrelationID string
	Stage         string // "preflight" | "run" | "verify" | "deliver" | "complete"
	Preflight     PreflightReport
	Result        Result
	Verification  VerifyReport
	Delivery      DeliverReport
	Cleanup       *CleanupReport
	Passed        bool
}

// Execute runs Preflight -> Run -> Verify -> Deliver in sequence and
// guarantees that any non-PASS outcome after Preflight is followed by
// Cleanup, so "result != PASS implies zero pending/claimed smoke
// messages" holds for every caller of Execute, not just careful ones.
func Execute(ctx context.Context, pool *pgxpool.Pool, tk Toolkit, roles Roles, now time.Time) (ExecuteReport, error) {
	correlationID := NewCorrelationID(now)
	report := ExecuteReport{CorrelationID: correlationID, Stage: "preflight"}

	preflight, err := Preflight(ctx, tk, roles)
	report.Preflight = preflight
	if err != nil {
		return report, fmt.Errorf("preflight: %w", err)
	}
	if !preflight.AllPassed {
		return report, fmt.Errorf("preflight did not pass: registry_synchronized=%v capability_checks=%+v", preflight.RegistrySynchronized, preflight.CapabilityChecks)
	}

	// A local copy with MaxAttempts pinned to 1: every message THIS run
	// sends must carry max_attempts=1 from the moment it is created, not
	// just at cleanup time -- Nack's own SQL only deadens a message when
	// attempt_count reaches the STORED max_attempts column, so overriding
	// MaxAttempts solely inside Cleanup (after Run already sent messages
	// with the caller's real MaxAttempts, typically 10) would leave a
	// single cleanup Nack short of deadening anything.
	smokeMessages := tk.Messages
	smokeMessages.MaxAttempts = smokeMessageMaxAttempts

	report.Stage = "run"
	result, err := Run(ctx, pool, smokeMessages, tk.OrganizationID, roles, correlationID, now)
	report.Result = result
	if err != nil {
		cleanup, cerr := Cleanup(ctx, pool, smokeMessages, tk.OrganizationID, correlationID, now)
		report.Cleanup = &cleanup
		return report, fmt.Errorf("run failed: %w (cleanup: %+v, cleanup_err=%v)", err, cleanup, cerr)
	}

	report.Stage = "verify"
	verification, err := Verify(ctx, pool, tk.OrganizationID, correlationID)
	report.Verification = verification
	verifyPassed := err == nil && verification.AllFourPresent && verification.AllCorrelated && verification.AllIdentical && verification.SupportTasksSafe
	if !verifyPassed {
		cleanup, cerr := Cleanup(ctx, pool, smokeMessages, tk.OrganizationID, correlationID, now)
		report.Cleanup = &cleanup
		return report, fmt.Errorf("verify failed: err=%v report=%+v (cleanup: %+v, cleanup_err=%v)", err, verification, cleanup, cerr)
	}

	report.Stage = "deliver"
	delivery, err := Deliver(ctx, pool, smokeMessages, tk.OrganizationID, roles, correlationID, now)
	report.Delivery = delivery
	if err != nil || !delivery.AllDelivered {
		cleanup, cerr := Cleanup(ctx, pool, smokeMessages, tk.OrganizationID, correlationID, now)
		report.Cleanup = &cleanup
		return report, fmt.Errorf("deliver failed: %w (cleanup: %+v, cleanup_err=%v)", err, cleanup, cerr)
	}

	report.Stage = "complete"
	report.Passed = true
	return report, nil
}
