package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	"github.com/jackc/pgx/v5"
)

// ProjectHarnessRun reads durable database facts for a single Harness run,
// deterministically projects them into an Episode, and persists it.
func (s *Store) ProjectHarnessRun(ctx context.Context, harnessRunID string) (episode.Episode, error) {
	if err := ctx.Err(); err != nil {
		return episode.Episode{}, err
	}
	harnessRunID = strings.TrimSpace(harnessRunID)
	if harnessRunID == "" {
		return episode.Episode{}, errors.New("memoryos postgres: harness_run_id is required")
	}

	// 1. Read Execution Run Descriptor
	var d episode.RunDescriptor
	var frozenToolsJSON []byte
	var contextSnapshotID int64
	err := s.pool.QueryRow(ctx, `
		SELECT
			organization_id, harness_run_id, task_id, attempt_id, role_id,
			execution_principal_id, context_id, context_version, context_digest,
			execution_profile_id, model_policy_ref, build_ref, max_turns,
			max_tool_calls, frozen_tools, identity_digest
		FROM execution_run_descriptors
		WHERE organization_id = $1 AND harness_run_id = $2
	`, s.organizationID, harnessRunID).Scan(
		&d.OrganizationID, &d.RunID, &d.TaskID, &d.AttemptID, &d.RoleID,
		&d.ExecutionPrincipalID, &d.ContextID, &d.ContextVersion, &d.ContextDigest,
		&d.ExecutionProfileID, &d.ModelPolicyRef, &d.BuildRef, &d.MaxTurns,
		&d.MaxToolCalls, &frozenToolsJSON, &d.IdentityDigest,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return episode.Episode{}, fmt.Errorf("memoryos postgres: run descriptor for %s not found", harnessRunID)
	}
	if err != nil {
		return episode.Episode{}, mapError(err)
	}
	_ = json.Unmarshal(frozenToolsJSON, &d.FrozenTools)
	contextSnapshotID, _ = strconv.ParseInt(d.ContextID, 10, 64)

	// 2. Read TaskClass from tasks
	var taskClass string
	err = s.pool.QueryRow(ctx, `
		SELECT task_class
		FROM tasks
		WHERE id = $1 AND organization_id = $2
	`, d.TaskID, s.organizationID).Scan(&taskClass)
	if errors.Is(err, pgx.ErrNoRows) {
		return episode.Episode{}, fmt.Errorf("memoryos postgres: task %d not found", d.TaskID)
	}
	if err != nil {
		return episode.Episode{}, mapError(err)
	}

	// 3. Read Context Snapshot and View
	contextUse := episode.ContextUse{
		SnapshotID:            d.ContextID,
		SnapshotVersion:       d.ContextVersion,
		SnapshotDigest:        d.ContextDigest,
		ProviderVisibleDigest: d.ContextDigest,
		TaskClass:             taskClass,
	}

	var snapshotStatus string
	_ = s.pool.QueryRow(ctx, `
		SELECT status
		FROM context_snapshots
		WHERE id = $1 AND organization_id = $2
	`, contextSnapshotID, s.organizationID).Scan(&snapshotStatus)
	contextUse.Status = snapshotStatus

	var viewID int64
	var providerVisibleDigest, execPurpose string
	err = s.pool.QueryRow(ctx, `
		SELECT id, provider_visible_digest, selection_kind
		FROM execution_context_views
		WHERE context_snapshot_id = $1 AND organization_id = $2
	`, contextSnapshotID, s.organizationID).Scan(&viewID, &providerVisibleDigest, &execPurpose)
	if err == nil {
		contextUse.ExecutionContextViewID = &viewID
		if providerVisibleDigest != "" {
			contextUse.ProviderVisibleDigest = providerVisibleDigest
		}
		contextUse.ExecutionPurpose = execPurpose
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return episode.Episode{}, mapError(err)
	}
	if contextUse.ExecutionPurpose == "" {
		contextUse.ExecutionPurpose = "general.execution"
	}

	// 4. Read Skill segments from context_segments
	skills := make([]episode.SkillFact, 0)
	skillRows, err := s.pool.Query(ctx, `
		SELECT source_reference, source_version, content_hash, included
		FROM context_segments
		WHERE snapshot_id = $1 AND organization_id = $2 AND source_kind = 'approved_skill'
		ORDER BY ordinal ASC
	`, contextSnapshotID, s.organizationID)
	if err == nil {
		defer skillRows.Close()
		for skillRows.Next() {
			var ref, ver, hash string
			var inc bool
			if scanErr := skillRows.Scan(&ref, &ver, &hash, &inc); scanErr == nil {
				skills = append(skills, episode.SkillFact{
					SkillID:     ref,
					Version:     ver,
					ContentHash: hash,
					Included:    inc,
				})
			}
		}
	}

	// 5. Read Events from execution_run_events
	events := make([]episode.EventFact, 0)
	eventRows, err := s.pool.Query(ctx, `
		SELECT sequence, event_type, terminal_status, payload, recorded_at
		FROM execution_run_events
		WHERE organization_id = $1 AND run_id = $2
		ORDER BY sequence ASC
	`, s.organizationID, harnessRunID)
	if err != nil {
		return episode.Episode{}, mapError(err)
	}
	defer eventRows.Close()

	for eventRows.Next() {
		var seq int64
		var evType, termStatus string
		var payloadBytes []byte
		var recAt time.Time
		if scanErr := eventRows.Scan(&seq, &evType, &termStatus, &payloadBytes, &recAt); scanErr != nil {
			return episode.Episode{}, mapError(scanErr)
		}
		var harnessEv executionharness.Event
		_ = json.Unmarshal(payloadBytes, &harnessEv)

		fact := episode.EventFact{
			Sequence:       uint64(seq),
			Type:           evType,
			TerminalStatus: termStatus,
			RecordedAt:     recAt.UTC(),
			ErrorCode:      harnessEv.ErrorCode,
			ToolProvenance: harnessEv.ToolProvenance,
		}
		if harnessEv.ModelResult != nil && harnessEv.ModelResult.InvocationRef != "" {
			fact.InvocationRef = harnessEv.ModelResult.InvocationRef
		} else if harnessEv.InvocationRef != "" {
			fact.InvocationRef = harnessEv.InvocationRef
		}
		if harnessEv.ToolRequest != nil {
			fact.ToolCallID = harnessEv.ToolRequest.ToolCallID
			fact.ToolName = harnessEv.ToolRequest.ToolName
		}
		events = append(events, fact)
	}

	// 6. Read Model Invocations and Usage
	invocations := make([]episode.InvocationFact, 0)
	costs := make([]episode.CostFact, 0)
	invIDs := make([]int64, 0)

	invRows, err := s.pool.Query(ctx, `
		SELECT id, provider_id, provider_model_id, status, created_at, terminal_at
		FROM model_invocations
		WHERE organization_id = $1 AND task_id = $2 AND attempt_id = $3
		ORDER BY id ASC
	`, s.organizationID, d.TaskID, d.AttemptID)
	if err == nil {
		defer invRows.Close()
		for invRows.Next() {
			var id int64
			var providerID, modelID, status string
			var crAt time.Time
			var termAt *time.Time
			if scanErr := invRows.Scan(&id, &providerID, &modelID, &status, &crAt, &termAt); scanErr == nil {
				invocations = append(invocations, episode.InvocationFact{
					InvocationID:    id,
					ProviderID:      providerID,
					ProviderModelID: modelID,
					Status:          status,
					CreatedAt:       crAt.UTC(),
					TerminalAt:      termAt,
				})
				invIDs = append(invIDs, id)
			}
		}
	}

	if len(invIDs) > 0 {
		// Read tokens from model_invocation_usage
		usageRows, err := s.pool.Query(ctx, `
			SELECT invocation_id, input_tokens, output_tokens, reasoning_tokens
			FROM model_invocation_usage
			WHERE invocation_id = ANY($1)
		`, invIDs)
		if err == nil {
			defer usageRows.Close()
			usageMap := make(map[int64][3]int64)
			for usageRows.Next() {
				var id, inTok, outTok int64
				var reasonTok *int64
				if scanErr := usageRows.Scan(&id, &inTok, &outTok, &reasonTok); scanErr == nil {
					var r int64
					if reasonTok != nil {
						r = *reasonTok
					}
					usageMap[id] = [3]int64{inTok, outTok, r}
				}
			}
			for i := range invocations {
				if u, ok := usageMap[invocations[i].InvocationID]; ok {
					inV := u[0]
					outV := u[1]
					rV := u[2]
					invocations[i].InputTokens = &inV
					invocations[i].OutputTokens = &outV
					invocations[i].ReasoningTokens = &rV
				}
			}
		}

		// Read costs from provider_wallet_events
		walletRows, err := s.pool.Query(ctx, `
			SELECT invocation_id, kind, amount_usd_nanos, cost_provenance, financial_outcome
			FROM provider_wallet_events
			WHERE invocation_id = ANY($1)
		`, invIDs)
		if err == nil {
			defer walletRows.Close()
			costMap := make(map[int64]*episode.CostFact)
			for walletRows.Next() {
				var id int64
				var kind, prov, outcome string
				var amt int64
				if scanErr := walletRows.Scan(&id, &kind, &amt, &prov, &outcome); scanErr == nil {
					cf, ok := costMap[id]
					if !ok {
						cf = &episode.CostFact{InvocationID: id}
						costMap[id] = cf
					}
					if kind == "committed" && (prov == "actual_provider_reported" || outcome == "actual") {
						a := amt
						cf.ActualUSDNanos = &a
					} else if prov == "estimated_locally" || kind == "reserved" {
						e := amt
						cf.EstimatedUSDNanos = &e
					}
				}
			}
			for _, cf := range costMap {
				costs = append(costs, *cf)
			}
		}
	}

	// 7. Read DecisionRunID & Verification
	var decisionRunID *int64
	_ = s.pool.QueryRow(ctx, `
		SELECT id
		FROM decision_graph_runs
		WHERE organization_id = $1 AND task_id = $2 AND attempt_id = $3
		ORDER BY id DESC LIMIT 1
	`, s.organizationID, d.TaskID, d.AttemptID).Scan(&decisionRunID)

	var verification *episode.VerificationSummary
	var obsDigest, obsVerdict string
	var obsTime time.Time
	var obsObligationsJSON []byte
	err = s.pool.QueryRow(ctx, `
		SELECT observation_digest, verdict, verified_at, obligations
		FROM memoryos_completion_observations
		WHERE organization_id = $1 AND task_id = $2 AND attempt_id = $3
		ORDER BY verified_at DESC, id DESC LIMIT 1
	`, s.organizationID, d.TaskID, d.AttemptID).Scan(&obsDigest, &obsVerdict, &obsTime, &obsObligationsJSON)
	if err == nil {
		var obligations []episode.ObligationObservation
		_ = json.Unmarshal(obsObligationsJSON, &obligations)
		var refs []string
		if decisionRunID != nil {
			refs = []string{fmt.Sprintf("decisiongraph:run:%d", *decisionRunID)}
		}
		vt := obsTime.UTC()
		verification = &episode.VerificationSummary{
			Verdict:       obsVerdict,
			Scope:         episode.VerificationScopeAttempt,
			VerifiedAt:    &vt,
			DecisionRunID: decisionRunID,
			EvidenceRefs:  refs,
			Obligations:   obligations,
		}
	} else if decisionRunID != nil {
		// Fall back to decision_verifications if present
		verifRows, verifErr := s.pool.Query(ctx, `
			SELECT label, verifier_ref, verifier_version, evidence_set_hash
			FROM decision_verifications
			WHERE organization_id = $1 AND run_id = $2
			ORDER BY id ASC
		`, s.organizationID, *decisionRunID)
		if verifErr == nil {
			defer verifRows.Close()
			obligations := make([]episode.ObligationObservation, 0)
			for verifRows.Next() {
				var lbl, ref, ver, evHash string
				if scanErr := verifRows.Scan(&lbl, &ref, &ver, &evHash); scanErr == nil {
					obligations = append(obligations, episode.ObligationObservation{
						Key:             ref,
						Kind:            "decision_node",
						Label:           lbl,
						VerifierRef:     ref,
						VerifierVersion: ver,
						EvidenceDigest:  evHash,
					})
				}
			}
			if len(obligations) > 0 {
				verdict := "pass"
				for _, ob := range obligations {
					if ob.Label == "contradicted" {
						verdict = "fail"
						break
					}
				}
				now := time.Now().UTC()
				verification = &episode.VerificationSummary{
					Verdict:       verdict,
					Scope:         episode.VerificationScopeAttempt,
					VerifiedAt:    &now,
					DecisionRunID: decisionRunID,
					EvidenceRefs:  []string{fmt.Sprintf("decisiongraph:run:%d", *decisionRunID)},
					Obligations:   obligations,
				}
			}
		}
	}

	// 8. Project
	input := episode.ProjectionInput{
		Descriptor:   d,
		TaskClass:    taskClass,
		Context:      contextUse,
		Events:       events,
		Skills:       skills,
		Invocations:  invocations,
		Costs:        costs,
		Verification: verification,
	}

	ep, err := episode.Project(input)
	if err != nil {
		return episode.Episode{}, fmt.Errorf("memoryos postgres: project episode: %w", err)
	}

	// 9. Persist
	saved, _, err := s.SaveEpisode(ctx, ep)
	if err != nil {
		return episode.Episode{}, fmt.Errorf("memoryos postgres: save episode: %w", err)
	}
	return saved, nil
}
