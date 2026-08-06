package decisiongraphtrace

// schemaVersion identifies this package's canonical trace payload shape,
// independent of decisiongraph's own internal schema/hash versioning.
const schemaVersion = "decisiongraph-trace/v1"

// canonicalTrace is the deterministic, content-hashed representation of a
// terminal decisiongraph run this package produces. Field order here does
// not affect the hash (json.Marshal on a struct is already
// order-independent across runs for the same Go type); node and edge
// slices are explicitly sorted before marshaling so the hash does not
// depend on database storage or scan order.
type canonicalTrace struct {
	SchemaVersion      string            `json:"schema_version"`
	RunID              int64             `json:"run_id"`
	OrganizationID     string            `json:"organization_id"`
	TaskID             int64             `json:"task_id"`
	AttemptID          int64             `json:"attempt_id"`
	TerminalReasonCode *string           `json:"terminal_reason_code,omitempty"`
	Nodes              []canonicalNode   `json:"nodes"`
	Edges              []canonicalEdge   `json:"edges"`
	Decision           canonicalDecision `json:"decision"`
}

// canonicalNode carries only content hashes and typed state, never a
// node's raw payload: decisiongraph's own privacy invariant (no private
// chain-of-thought persisted) carries through to this package's trace.
type canonicalNode struct {
	LogicalNodeID  int64  `json:"logical_node_id"`
	NodeType       string `json:"node_type"`
	BranchState    string `json:"branch_state"`
	ExecutionState string `json:"execution_state"`
	Depth          int    `json:"depth"`
	PayloadHash    string `json:"payload_hash"`
}

type canonicalEdge struct {
	FromLogicalNodeID int64  `json:"from_logical_node_id"`
	ToLogicalNodeID   int64  `json:"to_logical_node_id"`
	EdgeType          string `json:"edge_type"`
}

type canonicalDecision struct {
	SelectedCandidateLogicalNodeID int64  `json:"selected_candidate_logical_node_id"`
	DecisionHash                   string `json:"decision_hash"`
	VerificationLabel              string `json:"verification_label"`
}
