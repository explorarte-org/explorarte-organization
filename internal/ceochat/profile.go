package ceochat

// Execution profile constants for the CEO conversational surface.
//
// This is a SEPARATE execution profile from executive/typed-task/v1, not a
// relaxation of it. The typed-task profile stays MaxTurns=1/MaxToolCalls=0/
// Tools=nil -- one question, one JSON answer, no model-selected tool -- and
// nothing here changes that. A chat turn is a different kind of work: the
// model may call a bounded number of read-only tools before answering, so it
// gets its own frozen ceiling, versioned independently so a future profile
// change cannot silently alter an in-flight typed-task run's identity (the
// two profiles never share a digest).
const (
	ExecutionProfileID = "executive/chat/v1"
	ModelPolicyRef     = "executive/chat-text/v1"

	// MaxTurns bounds model round-trips within ONE Harness run (one owner
	// message). It is not a per-conversation ceiling: each new owner
	// message starts a fresh task/attempt/run with its own budget.
	MaxTurns = 8
	// MaxToolCalls bounds tool calls within the same single run. The model
	// cannot raise either ceiling, add a tool, change schemas, or choose a
	// different provider/model/execution principal -- RunSpec freezes all
	// of that before the Harness ever sees the run.
	MaxToolCalls = 6

	// CEORoleID is the fixed subject role of every chat turn's Harness run.
	// It is the same role Executive drives its own CEO-authored work under,
	// so the two surfaces route to the identical model-routing.yaml policy
	// (executive.ceo) without ceochat naming a provider or model itself.
	CEORoleID = "empresa/ceo"

	// TaskClass classifies a chat turn's durable task. It carries no
	// authority; ClaimTaskByID and the lease it returns are the only
	// authority a turn has.
	TaskClass = "executive.ceo_chat_turn"
)
