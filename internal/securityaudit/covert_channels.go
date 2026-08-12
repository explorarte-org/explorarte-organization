// Package securityaudit provides automated detection of covert channels in the
// organization's agent communication and data persistence layers. It implements
// a deterministic catalog of all surfaces where one agent can write data that
// another agent can read, and identifies gaps in authorization boundaries.
package securityaudit

import (
	"strconv"
	"strings"
)

// Channel represents a potential covert channel surface. Each channel describes
// who can write, who can read, what authorization exists, and whether it can
// influence context or grant capabilities to agents.
type Channel struct {
	Name              string   // Unique identifier for the channel
	Writers           []string // Role types that can write (e.g., "specialist", "execution_service")
	Readers           []string // Role types that can read
	AuthBoundary      string   // Authorization mechanism ("capability_gate", "task_workflow", "none", "principal_binding", etc.)
	OrgScope          string   // Organization-level isolation type ("org_isolated", "global")
	RoleScope         string   // Role-level scope description
	DataClass         string   // Data classification support ("public", "sanitized", "organizational", "secret", "clinical")
	SizeBoundBytes    int      // Maximum payload size in bytes (0 = unbounded)
	Durable           bool     // Persists across invocations
	InfluencesContext bool     // Can modify what the LLM sees in context
	GrantsCapability  bool     // Can grant LLM capabilities via content
	AuditProvenance   string   // How access is logged/audited
}

// Rule defines a detection rule for identifying covert channel violations.
type Rule struct {
	Name        string             // e.g., "missing-auth-boundary"
	Severity    string             // "critical", "high", "medium", "low"
	Description string             // Human-readable explanation of the violation
	Check       func(Channel) bool // Returns true if violation detected
}

// Catalog returns the complete set of known covert channels in the system.
func Catalog() []Channel {
	return []Channel{
		// Agent messaging channels
		{
			Name:              "agent_messages_send",
			Writers:           []string{"executive", "department_leadership", "specialist"},
			Readers:           []string{"inbox_recipients"},
			AuthBoundary:      "capability_gate+principal_binding+topology_check",
			OrgScope:          "org_isolated",
			RoleScope:         "sender_role matches dispatch_actor + recipient_role in topology edge",
			DataClass:         "structured_payload",
			SizeBoundBytes:    1024,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "ledger_operation_logged",
		},
		{
			Name:              "agent_messages_claim",
			Writers:           []string{"executive", "department_leadership", "specialist"},
			Readers:           []string{"already_claimed_inbox_owners"},
			AuthBoundary:      "token_verification+principal_verification",
			OrgScope:          "org_isolated",
			RoleScope:         "dispatch_actor_role_id == claimed_by",
			DataClass:         "structured_payload",
			SizeBoundBytes:    1024,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "token_settlement_logged",
		},
		{
			// Was "bypassable_via_colliding_key" / AuthBoundary "key_only":
			// Store.Send() used to reach the idempotency-key lookup (and
			// return an existing message on a hit) WITHOUT first checking
			// topology, execution-principal identity, or task ownership --
			// a colliding idempotency key alone could surface a message
			// authored by a different, unvalidated path. Send() now runs
			// principal validation, task ownership, and topology.
			// ValidateEdge unconditionally BEFORE the idempotency-key
			// lookup (see store.go), so a colliding key can no longer skip
			// any of those checks. Updated here to keep this catalog
			// truthful about the current implementation, not the pre-fix one.
			Name:              "agent_messages_idempotency_collision",
			Writers:           []string{"attacker_via_key_replay"},
			Readers:           []string{"prior_message_recipient"},
			AuthBoundary:      "key_only+principal_binding+task_ownership+topology_check",
			OrgScope:          "org_isolated",
			RoleScope:         "sender_role matches dispatch_actor + recipient_role in topology edge, checked before idempotency lookup",
			DataClass:         "structured_payload",
			SizeBoundBytes:    1024,
			Durable:           true,
			InfluencesContext: false,
			GrantsCapability:  false,
			AuditProvenance:   "idempotency_collision_tracked_if_detected",
		},
		{
			Name:         "agent_messages_payload_smuggling",
			Writers:      []string{"sending_principal"},
			Readers:      []string{"inbox_reader"},
			AuthBoundary: "dataclass_scan",
			OrgScope:     "org_isolated",
			RoleScope:    "sender->recipient per topology",
			// Not a scanner: the V1 payload schema is closed and carries a
			// single integer field, and unknown fields are rejected, so there
			// is no free-text field for a secret to travel in.
			DataClass:         "closed_schema_no_free_text",
			SizeBoundBytes:    1024,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "dataclass_scan_results_logged",
		},
		{
			Name:              "agent_messages_topology_bypass",
			Writers:           []string{"any_well-formed_request"},
			Readers:           []string{"forged_recipient"},
			AuthBoundary:      "registry_derived_topology",
			OrgScope:          "org_isolated",
			RoleScope:         "enforced by registry edges (V1: ceo->dept_leader, dept_leader->worker, worker->dept_leader)",
			DataClass:         "structured_payload",
			SizeBoundBytes:    1024,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "topology_check_logged",
		},

		// Task-related channels
		{
			Name:         "tasks_instructions",
			Writers:      []string{"task_creator", "department_leader", "ceo"},
			Readers:      []string{"assigned_worker"},
			AuthBoundary: "task_state_machine",
			OrgScope:     "org_isolated",
			RoleScope:    "assigned_role_id",
			// Free text, but credential material is refused at ingress by
			// ValidateCreateRequest (see internal/secretscan). Sensitive
			// non-credential data -- personal, clinical, commercial -- is
			// deliberately carried and governed by classification instead.
			DataClass:         "free_text_with_secret_rejection",
			SizeBoundBytes:    65536,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "outbox_events_logged",
		},
		{
			Name:              "task_results",
			Writers:           []string{"worker"},
			Readers:           []string{"department_reviewer", "ceo"},
			AuthBoundary:      "workflow_verdict_gate",
			OrgScope:          "org_isolated",
			RoleScope:         "task hierarchy traversal",
			DataClass:         "structured_result",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "result_requirement_evidence_logged",
		},
		{
			Name:              "task_evidence",
			Writers:           []string{"worker"},
			Readers:           []string{"reviewer"},
			AuthBoundary:      "evidence_acceptance_gate",
			OrgScope:          "org_isolated",
			RoleScope:         "assigned role with reviewer capability",
			DataClass:         "references_with_digests",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "evidence_refs_persisted",
		},

		// Memory channels
		{
			Name:              "organizational_memory_get",
			Writers:           []string{"manager_client"},
			Readers:           []string{"entry_reader"},
			AuthBoundary:      "memory.read_own_capability",
			OrgScope:          "org_isolated",
			RoleScope:         "actor_role must match entry.RoleID",
			DataClass:         "free_text_entry",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "manager_call_logged",
		},
		{
			Name:              "organizational_memory_list",
			Writers:           []string{"manager_client"},
			Readers:           []string{"all_org_entries"},
			AuthBoundary:      "memory.read_own_capability+role_filter",
			OrgScope:          "org_isolated",
			RoleScope:         "filtered by actor's role only",
			DataClass:         "free_text_entries",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "manager_call_logged",
		},
		{
			Name:              "organizational_memory_search",
			Writers:           []string{"actor_constrained"},
			Readers:           []string{"own_role_approved_entries"},
			AuthBoundary:      "self_role_check+memory.read_own_capability",
			OrgScope:          "org_isolated",
			RoleScope:         "actorRoleID == roleID enforced",
			DataClass:         "approved_free_text",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "search_request_logged",
		},

		// RAG channels
		{
			Name:              "rag_knowledge_chunks",
			Writers:           []string{"approved_publishers"},
			Readers:           []string{"queried_actors"},
			AuthBoundary:      "capability_gate(propose/publish/read)+namespace_match",
			OrgScope:          "org_isolated",
			RoleScope:         "per_namespace + read capability required",
			DataClass:         "chunked_text",
			SizeBoundBytes:    1200,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "publish_approval_logged",
		},

		// Context snapshot channels
		{
			Name:              "context_snapshots",
			Writers:           []string{"context_engine_service"},
			Readers:           []string{"invocation_consumers"},
			AuthBoundary:      "engine_controlled_resolution",
			OrgScope:          "org_isolated",
			RoleScope:         "resolution_role from task",
			DataClass:         "aggregated_all_sources",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "snapshot_build_logged",
		},

		// Decision graph channels
		{
			Name:              "decision_graph",
			Writers:           []string{"decision_recorder"},
			Readers:           []string{"verifiers"},
			AuthBoundary:      "run_lifecycle_workflow_state",
			OrgScope:          "org_isolated",
			RoleScope:         "participating roles only",
			DataClass:         "reasoning_nodes",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: false,
			GrantsCapability:  false,
			AuditProvenance:   "node_creation_logged",
		},

		// Staging/artifact channels
		{
			Name:              "staging_artifacts",
			Writers:           []string{"code_runners"},
			Readers:           []string{"promotion_reviewers"},
			AuthBoundary:      "staging_promotion_workflow",
			OrgScope:          "org_isolated",
			RoleScope:         "workspace_holders",
			DataClass:         "file_paths_metadata",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: false,
			GrantsCapability:  false,
			AuditProvenance:   "promotion_review_logged",
		},
		{
			Name:              "artifacts_metadata",
			Writers:           []string{"artifact_writers"},
			Readers:           []string{"artifact_readers"},
			AuthBoundary:      "staging_artifact_permissions",
			OrgScope:          "org_isolated",
			RoleScope:         "holder_roles",
			DataClass:         "file_metadata",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: false,
			GrantsCapability:  false,
			AuditProvenance:   "artifact_creation_logged",
		},

		// Web evidence channels
		{
			Name:              "web_evidence_ingest",
			Writers:           []string{"fetcher"},
			Readers:           []string{"same_task_consumers"},
			AuthBoundary:      "task_id_binding+ttl_expiration",
			OrgScope:          "org_isolated",
			RoleScope:         "same task only",
			DataClass:         "sanitized_chunks",
			SizeBoundBytes:    1200,
			Durable:           false,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "ingest_timestamp_expiry_logged",
		},

		// Model invocation channels
		{
			Name:              "model_invocation_result",
			Writers:           []string{"model_runtime_adapter"},
			Readers:           []string{"task_attempt_recorders"},
			AuthBoundary:      "task_attempt_binding",
			OrgScope:          "org_isolated",
			RoleScope:         "task assignee",
			DataClass:         "normalized_response",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: true,
			GrantsCapability:  false,
			AuditProvenance:   "invocation_record_logged",
		},
		{
			Name:              "model_invocation_output_metadata",
			Writers:           []string{"model_runtime"},
			Readers:           []string{"observers_reconcilers"},
			AuthBoundary:      "runtime_query_scope",
			OrgScope:          "org_isolated",
			RoleScope:         "authorized queries only",
			DataClass:         "outcome_cost_metadata",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: false,
			GrantsCapability:  false,
			AuditProvenance:   "cost_budget_logging",
		},

		// Audit event channels
		{
			Name:              "audit_events",
			Writers:           []string{"all_services"},
			Readers:           []string{"auditors_with_capability"},
			AuthBoundary:      "audit.read_sanitized_evidence_capability",
			OrgScope:          "org_isolated",
			RoleScope:         "audit capability required",
			DataClass:         "sanitized_event_data",
			SizeBoundBytes:    0,
			Durable:           true,
			InfluencesContext: false,
			GrantsCapability:  false,
			AuditProvenance:   "append_only_design",
		},
	}
}

// Rules returns the set of detection rules for identifying covert channel violations.
func Rules() []Rule {
	return []Rule{
		{
			Name:        "missingAuthBoundary",
			Severity:    "critical",
			Description: "Channel has writer and reader defined but no effective authorization boundary",
			Check: func(c Channel) bool {
				if c.AuthBoundary == "none" || strings.TrimSpace(c.AuthBoundary) == "" {
					return true
				}
				return false
			},
		},
		{
			Name:        "crossOrgReadWrite",
			Severity:    "critical",
			Description: "Reader scope extends beyond organization isolation boundary",
			Check: func(c Channel) bool {
				if c.OrgScope != "org_isolated" && len(c.Readers) > 0 {
					// Check if readers include non-org-scoped entities
					for _, r := range c.Readers {
						if strings.Contains(r, "global") || strings.Contains(r, "*") {
							return true
						}
					}
				}
				return false
			},
		},
		{
			Name:        "untrustedAsAuthority",
			Severity:    "high",
			Description: "Untrusted data renders as authoritative instruction in context",
			Check: func(c Channel) bool {
				// If influences context AND grants capability OR has untrusted data class rendered as authority
				if c.InfluencesContext && (c.GrantsCapability || c.DataClass == "untrusted_rendered_authority") {
					return true
				}
				return false
			},
		},
		{
			Name:        "unboundedDurableSurface",
			Severity:    "medium",
			Description: "Persistent surface without application-layer size bound",
			Check: func(c Channel) bool {
				if c.Durable && c.SizeBoundBytes == 0 && !strings.Contains(c.Name, "metadata") && !strings.Contains(c.Name, "audit") {
					// Exclude metadata/audit channels which are expected to be large
					return true
				}
				return false
			},
		},
		{
			Name:        "messagingTopologyBypass",
			Severity:    "critical",
			Description: "Agent messaging allows topology edges not in canonical registry",
			Check: func(c Channel) bool {
				if !strings.Contains(c.Name, "agent_messages") {
					return false
				}
				// Check for any topology bypass indicators
				if strings.Contains(c.RoleScope, "forged") || strings.Contains(c.RoleScope, "bypass") {
					return true
				}
				return false
			},
		},
		{
			Name:        "memoryCrossRoleBypass",
			Severity:    "critical",
			Description: "Memory Get/List allows reading arbitrary role's memory",
			Check: func(c Channel) bool {
				if strings.Contains(c.Name, "organizational_memory_get") && c.AuthBoundary == "none" {
					return true
				}
				if strings.Contains(c.Name, "organizational_memory_list") && c.AuthBoundary == "none" {
					return true
				}
				return false
			},
		},
		{
			Name:        "contextInjectsUntrustedAsAuthority",
			Severity:    "high",
			Description: "Provider render mixes untrusted content without structural differentiation",
			Check: func(c Channel) bool {
				if strings.Contains(c.Name, "context_snapshots") {
					// V1 may have this issue; v2 should resolve it
					// This rule detects when v1 behavior persists
					return false // Will be addressed by ProviderRender v2 implementation
				}
				return false
			},
		},
		{
			Name:        "tasksInstructionsUnbounded",
			Severity:    "medium",
			Description: "Task instructions free-form text influences LLM with no payload size bound",
			Check: func(c Channel) bool {
				if strings.Contains(c.Name, "tasks_instructions") && c.SizeBoundBytes == 0 {
					return true
				}
				return false
			},
		},
		{
			Name:        "secretSmugglingViaPayload",
			Severity:    "critical",
			Description: "Secret/clinical data can be smuggled via message/instructions payload",
			Check: func(c Channel) bool {
				// Payload channels are mitigated by one of two real controls,
				// each backed by executable evidence in the securityaudit
				// tests. A label alone never clears this rule -- that is what
				// ORG-04 was raised for.
				mitigated := map[string]bool{
					"closed_schema_no_free_text":      true,
					"free_text_with_secret_rejection": true,
				}
				payloadChannels := []string{"agent_messages_payload_smuggling", "tasks_instructions"}
				for _, pc := range payloadChannels {
					if c.Name == pc && !mitigated[c.DataClass] {
						return true
					}
				}
				return false
			},
		},
	}
}

// Violation indicates a detected covert channel issue.
type Violation struct {
	ChannelName string
	RuleName    string
	Severity    string
	Description string
	Details     map[string]string
}

// CheckViolations runs all detection rules against the channel catalog.
func CheckViolations() []Violation {
	var violations []Violation
	channels := Catalog()
	rules := Rules()

	for _, channel := range channels {
		for _, rule := range rules {
			if rule.Check(channel) {
				violations = append(violations, Violation{
					ChannelName: channel.Name,
					RuleName:    rule.Name,
					Severity:    rule.Severity,
					Description: rule.Description,
					Details: map[string]string{
						"auth_boundary":    channel.AuthBoundary,
						"org_scope":        channel.OrgScope,
						"size_bound_bytes": formatSize(channel.SizeBoundBytes),
						"durable":          formatBool(channel.Durable),
					},
				})
			}
		}
	}

	return violations
}

// Helper functions for formatting
func formatSize(bytes int) string {
	// string(rune(n)) previously converted the integer to the Unicode
	// CODE POINT at that value (e.g. bytes/1024==1 produced U+0001, a
	// control character, not the text "1") -- every non-zero, non-
	// "unbounded" size in every violation report was silently garbled.
	// strconv.Itoa is the correct decimal-text conversion.
	if bytes == 0 {
		return "unbounded"
	}
	if bytes >= 1048576 {
		return strconv.Itoa(bytes/1048576) + "MB"
	}
	if bytes >= 1024 {
		return strconv.Itoa(bytes/1024) + "KB"
	}
	return strconv.Itoa(bytes) + "B"
}

func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
