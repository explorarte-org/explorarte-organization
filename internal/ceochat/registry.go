package ceochat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

// AccessMode classifies whether a capability is read-only or mutative.
type AccessMode string

const (
	AccessReadOnly AccessMode = "read_only"
	AccessMutating AccessMode = "mutating"
)

// ToolEffect classifies the effect of a capability.
type ToolEffect string

const (
	ToolEffectRead  ToolEffect = "read"
	ToolEffectWrite ToolEffect = "write"
)

// DataClassification is a coarse, host-owned label for what kind of data a
// capability's result carries. It exists so a future round can add
// per-classification handling (redaction, retention, export policy)
// without renegotiating every tool's contract.
type DataClassification string

const (
	DataClassInternal DataClassification = "internal"
	DataClassPublic   DataClassification = "public"
)

// ToolLimits bounds one capability's execution. The registry enforces these
// itself -- a handler cannot opt out of them, and a model-supplied "limit"
// argument is validated against MaxRows before the handler ever runs (see
// each family's argument validator).
type ToolLimits struct {
	MaxRows        int
	MaxResultBytes int
	Timeout        time.Duration
}

// ToolDescriptor is the host-owned identity and contract of one capability.
// The wire name a model sees (ToolDefinition.Name) is ID; Version is
// separate metadata, not encoded into the wire name, so the contract can be
// versioned without the model ever parsing a version out of a string.
type ToolDescriptor struct {
	ID          string
	Version     string
	Description string

	InputSchema  json.RawMessage
	OutputSchema json.RawMessage

	Access       AccessMode
	Effect       ToolEffect
	RequiredRole string

	Limits ToolLimits

	DataClass DataClassification
}

func (d ToolDescriptor) IsMutating() bool {
	return d.Access == AccessMutating || d.Effect == ToolEffectWrite
}

func (d ToolDescriptor) validate() error {
	switch {
	case strings.TrimSpace(d.ID) == "":
		return fmt.Errorf("%w: tool descriptor requires an ID", ErrInvalidInput)
	case strings.TrimSpace(d.Version) == "":
		return fmt.Errorf("%w: tool %q requires a version", ErrInvalidInput, d.ID)
	case strings.TrimSpace(d.Description) == "":
		return fmt.Errorf("%w: tool %q requires a description", ErrInvalidInput, d.ID)
	case len(d.InputSchema) == 0:
		return fmt.Errorf("%w: tool %q requires an input schema", ErrInvalidInput, d.ID)
	case d.Access != AccessReadOnly && d.Access != AccessMutating:
		return fmt.Errorf("%w: tool %q must declare access=read_only or access=mutating", ErrInvalidInput, d.ID)
	case d.Effect != ToolEffectRead && d.Effect != ToolEffectWrite:
		return fmt.Errorf("%w: tool %q must declare effect=read or effect=write", ErrInvalidInput, d.ID)
	case d.Access == AccessReadOnly && d.Effect != ToolEffectRead:
		return fmt.Errorf("%w: tool %q with access=read_only must declare effect=read", ErrInvalidInput, d.ID)
	case d.Access == AccessMutating && d.Effect != ToolEffectWrite:
		return fmt.Errorf("%w: tool %q with access=mutating must declare effect=write", ErrInvalidInput, d.ID)
	case strings.TrimSpace(d.RequiredRole) == "":
		return fmt.Errorf("%w: tool %q requires an authorized role", ErrInvalidInput, d.ID)
	case d.Limits.MaxResultBytes <= 0:
		// A tool with no row concept (e.g. a single-record get) may leave
		// MaxRows at 0, but EVERY tool must bound its byte size -- this
		// check does not depend on MaxRows at all, because
		// RegistryToolExecutor.Execute only enforces MaxResultBytes when
		// it is itself positive: a descriptor that slipped through with
		// MaxResultBytes<=0 (however MaxRows was set) would run with no
		// result-size bound whatsoever, silently reintroducing exactly
		// the unbounded-read risk this round's BOUNDED_RESULTS section
		// exists to close.
		return fmt.Errorf("%w: tool %q requires MaxResultBytes > 0", ErrInvalidInput, d.ID)
	case d.Limits.Timeout <= 0:
		return fmt.Errorf("%w: tool %q requires a positive timeout", ErrInvalidInput, d.ID)
	case d.DataClass == "":
		return fmt.Errorf("%w: tool %q requires a data classification", ErrInvalidInput, d.ID)
	}
	return nil
}

// ToolHandler is the canonical-service call a capability makes once the
// registry has validated its arguments and authorized its caller. It
// receives only the actor's role ID and the raw, already-schema-valid
// arguments -- never a lease token, a database handle, or anything else a
// handler could use to reach outside its own canonical service.
type ToolHandler func(ctx context.Context, actorRoleID string, args json.RawMessage) (json.RawMessage, error)

// ArgumentValidator decodes and bounds-checks one tool's arguments. It is
// the ONLY place a model-supplied limit/cursor/filter is trusted, and it
// must reject anything it cannot make sense of rather than clamp silently
// -- see each family's own validator for the "reject, don't clamp" rule
// this round requires.
type ArgumentValidator func(args json.RawMessage) error

type registeredTool struct {
	Descriptor ToolDescriptor
	Validate   ArgumentValidator
	Handle     ToolHandler
}

// ToolRegistry is the single, host-owned, fail-closed catalog every
// ceochat capability is registered into. The model can neither register a
// tool nor read or alter a descriptor: Register happens once, at process
// bootstrap (or in a test fixture), never in response to a model turn.
type ToolRegistry struct {
	tools map[string]registeredTool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]registeredTool)}
}

// Register adds one capability. A second Register call for the same ID is a
// deterministic, immediate error -- there is no "last one wins": a
// conflicting registration is a bootstrap/test bug, not a runtime policy
// decision.
func (r *ToolRegistry) Register(descriptor ToolDescriptor, validate ArgumentValidator, handle ToolHandler) error {
	if err := descriptor.validate(); err != nil {
		return err
	}
	if validate == nil || handle == nil {
		return fmt.Errorf("%w: tool %q requires both a validator and a handler", ErrInvalidInput, descriptor.ID)
	}
	if _, exists := r.tools[descriptor.ID]; exists {
		return fmt.Errorf("%w: tool %q is already registered", ErrDuplicateToolRegistration, descriptor.ID)
	}
	r.tools[descriptor.ID] = registeredTool{Descriptor: descriptor, Validate: validate, Handle: handle}
	return nil
}

// Lookup returns the descriptor for a registered tool ID, or false. This is
// the ONLY way to learn whether a name is known; there is no wildcard and
// no fallback to an implementation-shaped guess.
func (r *ToolRegistry) Lookup(id string) (ToolDescriptor, bool) {
	tool, ok := r.tools[id]
	return tool.Descriptor, ok
}

// Definitions returns every registered tool as an executionharness tool
// definition, in a stable (ID-sorted) order. Exposing a SUBSET of these in
// a RunSpec.Tools slice is what actually grants a model access to a
// capability; being registered here only makes a tool KNOWN, not exposed
// (see ToolCatalog.Lookup vs the Harness's own allowedTool check).
func (r *ToolRegistry) Definitions() []executionharness.ToolDefinition {
	ids := make([]string, 0, len(r.tools))
	for id := range r.tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	definitions := make([]executionharness.ToolDefinition, 0, len(ids))
	for _, id := range ids {
		descriptor := r.tools[id].Descriptor
		definitions = append(definitions, executionharness.ToolDefinition{
			Name: descriptor.ID, Description: descriptor.Description, InputSchema: descriptor.InputSchema,
		})
	}
	return definitions
}

// DefinitionsFor returns the ToolDefinitions for exactly the given IDs, in
// the order given, failing closed if any ID is unknown. A caller building a
// RunSpec.Tools slice uses this so "exposed to this run" and "known to the
// registry" can never silently diverge into two different tool sets.
func (r *ToolRegistry) DefinitionsFor(ids []string) ([]executionharness.ToolDefinition, error) {
	definitions := make([]executionharness.ToolDefinition, 0, len(ids))
	for _, id := range ids {
		tool, ok := r.tools[id]
		if !ok {
			return nil, fmt.Errorf("%w: tool %q is not registered", ErrInvalidInput, id)
		}
		definitions = append(definitions, executionharness.ToolDefinition{
			Name: tool.Descriptor.ID, Description: tool.Descriptor.Description, InputSchema: tool.Descriptor.InputSchema,
		})
	}
	return definitions, nil
}

// RegistryToolCatalog adapts a *ToolRegistry to executionharness.ToolCatalog.
// It performs schema validation (registry §architecture step 3) and nothing
// else: capability authorization, authority/lease checks, and execution all
// happen elsewhere in the pipeline, exactly as the Harness's own boundary
// already separates "is this call well-formed" from "may it run".
type RegistryToolCatalog struct{ Registry *ToolRegistry }

func (c RegistryToolCatalog) Lookup(_ context.Context, name string) (executionharness.ToolDefinition, bool) {
	descriptor, ok := c.Registry.Lookup(name)
	if !ok {
		return executionharness.ToolDefinition{}, false
	}
	return executionharness.ToolDefinition{Name: descriptor.ID, Description: descriptor.Description, InputSchema: descriptor.InputSchema}, true
}

func (c RegistryToolCatalog) ValidateArguments(_ context.Context, definition executionharness.ToolDefinition, args []byte) error {
	tool, ok := c.Registry.tools[definition.Name]
	if !ok {
		return fmt.Errorf("%w: tool %q is not registered", ErrInvalidInput, definition.Name)
	}
	return tool.Validate(args)
}

var _ executionharness.ToolCatalog = RegistryToolCatalog{}

// RegistryToolExecutor adapts a *ToolRegistry to executionharness.ToolExecutor.
// This is the pipeline's capability-authorization and bounded-result step:
// by the time Execute runs, the Harness has already proven the call is
// known, exposed, schema-valid, and authority/lease-checked -- this adapter
// adds the tool-specific RequiredRole check, a hard execution timeout, and
// a hard result-size ceiling, then calls exactly one canonical-service
// handler and nothing else.
type RegistryToolExecutor struct{ Registry *ToolRegistry }

func (e RegistryToolExecutor) Execute(ctx context.Context, identity executionharness.RunIdentity, request executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	tool, ok := e.Registry.tools[request.ToolName]
	if !ok {
		return executionharness.ToolExecutionResult{}, fmt.Errorf("%w: tool %q is not registered", ErrInvalidInput, request.ToolName)
	}
	// Defense in depth: RunSpec exposure + the Harness's own denial path
	// already gate which tools a run may call, but a capability that
	// declares a required role must never trust "it reached the executor"
	// as proof of who is asking. This check is redundant with a correctly
	// composed RunSpec and is exactly the kind of check that must never be
	// the ONLY one -- it is here because relying on a single layer is
	// itself the failure mode this rule exists to prevent.
	if tool.Descriptor.RequiredRole != "" && identity.RoleID != tool.Descriptor.RequiredRole {
		return executionharness.ToolExecutionResult{}, fmt.Errorf("%w: tool %q requires role %q, caller is %q", ErrUnauthorizedActor, request.ToolName, tool.Descriptor.RequiredRole, identity.RoleID)
	}
	timeout := tool.Descriptor.Limits.Timeout
	if timeout <= 0 {
		timeout = defaultToolTimeout
	}
	callCtx := WithToolCallContext(ctx, ToolCallContext{
		RunIdentity: identity,
		ToolCallID:  request.ToolCallID,
	})
	callCtx, cancel := context.WithTimeout(callCtx, timeout)
	defer cancel()
	result, err := tool.Handle(callCtx, identity.RoleID, request.Arguments)
	if err != nil {
		return executionharness.ToolExecutionResult{}, err
	}
	if maxBytes := tool.Descriptor.Limits.MaxResultBytes; maxBytes > 0 && len(result) > maxBytes {
		return executionharness.ToolExecutionResult{}, fmt.Errorf("%w: tool %q result is %d bytes, exceeds the %d-byte bound", ErrToolResultTooLarge, request.ToolName, len(result), maxBytes)
	}
	return executionharness.ToolExecutionResult{Content: result, Provenance: "ceochat/" + tool.Descriptor.ID + "/" + tool.Descriptor.Version}, nil
}

var _ executionharness.ToolExecutor = RegistryToolExecutor{}

const defaultToolTimeout = 5 * time.Second

type turnContextKey struct{}
type toolCallContextKey struct{}

// TurnContext carries the host-authoritative context of the active chat turn.
type TurnContext struct {
	OrganizationID         string
	OrganizationRevisionID int64
	ConversationID         int64
	OwnerRoleID            string
	OwnerMessageID         int64
	TaskID                 int64
	AttemptID              int64
	ActorRoleID            string
}

func WithTurnContext(ctx context.Context, tc TurnContext) context.Context {
	return context.WithValue(ctx, turnContextKey{}, tc)
}

func TurnContextFrom(ctx context.Context) (TurnContext, bool) {
	tc, ok := ctx.Value(turnContextKey{}).(TurnContext)
	return tc, ok
}

// ToolCallContext carries execution run identity and tool call ID.
type ToolCallContext struct {
	RunIdentity executionharness.RunIdentity
	ToolCallID  string
}

func WithToolCallContext(ctx context.Context, tcc ToolCallContext) context.Context {
	return context.WithValue(ctx, toolCallContextKey{}, tcc)
}

func ToolCallContextFrom(ctx context.Context) (ToolCallContext, bool) {
	tcc, ok := ctx.Value(toolCallContextKey{}).(ToolCallContext)
	return tcc, ok
}
