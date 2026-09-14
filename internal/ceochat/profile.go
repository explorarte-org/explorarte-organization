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

// CAMPAIGN EXECUTION BOUNDARY (architectural contract, not yet implemented
// behavior -- see CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_V1):
//
// A ceochat turn task is NOT, and must never become, a campaign execution
// root. The CEO conversation is the organization's DIRECTION interface: the
// owner talks to it, it reads bounded state, and -- once a future round
// adds write capability -- it will be able to PROPOSE or SUBMIT an action
// (e.g. "promote this campaign") through a canonical Executive/Campaign API
// call. It will never itself become the thing that runs a campaign to
// completion.
//
// Concretely, that means a future "submit campaign" tool must open its OWN
// execution graph (its own task tree, its own attempts, its own dispatcher
// assignments) rooted at whatever canonical entry point Executive/Campaign
// machinery already uses for that work -- department planning, budget
// approval, worker dispatch, MemoryOS, closeout -- not extend the current
// chat turn's task/attempt/lease/DispatcherAssignment to cover it.
//
// Why this matters here specifically: this file's DispatcherAssignment
// quota (MaxTurns model invocations, all within ONE bounded Harness run) is
// sized for "answer one conversational turn," not "drive a multi-day
// campaign." A campaign that tried to run inside a ceochat turn would
// either blow through this bounded quota (by design) or require raising it
// to something effectively unbounded -- which is exactly the unbounded
// authority this round's whole design exists to prevent. A long-running
// campaign has its own lifecycle, its own authority, and its own audit
// trail; it must never borrow this turn's.
