package ceochat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/search"
)

// Tool names. Exactly these two exist in V1; nothing else is registered in
// the catalog, so any other name (shell.exec, git.push, ...) fails Lookup
// before RunSpec.Tools or an executor is ever consulted.
const (
	ToolListTopics   = "research.list_topics"
	ToolListFindings = "research.list_findings"
)

// TopicLister is the narrow read-only seam ceochat needs from the research
// system. internal/search/postgres.Store satisfies it directly.
type TopicLister interface {
	ListTopics(ctx context.Context, departmentID string) ([]search.ResearchTopic, error)
}

// FindingLister is the narrow read-only seam ceochat needs from the research
// system. internal/search/postgres.Store satisfies it directly.
type FindingLister interface {
	ListFindings(ctx context.Context, filter search.FindingFilter) ([]search.ResearchFinding, error)
}

const (
	defaultFindingsLimit = 10
	maxFindingsLimit     = 20
	defaultTopicsLimit   = 20
	maxTopicsLimit       = 50
)

var listFindingsSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "department_id": {"type": "string", "maxLength": 240},
    "topic_id": {"type": "string", "maxLength": 240},
    "important_only": {"type": "boolean"},
    "limit": {"type": "integer"}
  }
}`)

var listTopicsSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "department_id": {"type": "string", "maxLength": 240},
    "limit": {"type": "integer"}
  }
}`)

// ToolCatalog is the CEO chat's own, deliberately small tool catalog. It is
// NOT a second tool framework: Lookup/ValidateArguments satisfy
// executionharness.ToolCatalog directly, and every enforcement decision
// (unknown tool, not exposed, definition drift, invalid arguments, replay,
// max-tool-calls) is made by executionharness.Runtime, not here. This type
// only knows which definitions exist and how to validate their arguments.
type ToolCatalog struct {
	definitions map[string]executionharness.ToolDefinition
}

func NewToolCatalog() ToolCatalog {
	return ToolCatalog{definitions: map[string]executionharness.ToolDefinition{
		ToolListTopics: {
			Name:        ToolListTopics,
			Description: "List research topics tracked for the organization, optionally scoped to one department.",
			InputSchema: listTopicsSchema,
		},
		ToolListFindings: {
			Name:        ToolListFindings,
			Description: "List recent research findings, optionally scoped to a department or topic.",
			InputSchema: listFindingsSchema,
		},
	}}
}

// Definitions returns the full V1 tool set in a stable order, for building
// RunSpec.Tools. Production always exposes the whole catalog; a test may
// expose a narrower slice to exercise the visible-set denial path.
func (c ToolCatalog) Definitions() []executionharness.ToolDefinition {
	return []executionharness.ToolDefinition{
		c.definitions[ToolListTopics],
		c.definitions[ToolListFindings],
	}
}

func (c ToolCatalog) Lookup(_ context.Context, name string) (executionharness.ToolDefinition, bool) {
	definition, ok := c.definitions[name]
	return definition, ok
}

func (c ToolCatalog) ValidateArguments(_ context.Context, definition executionharness.ToolDefinition, args []byte) error {
	switch definition.Name {
	case ToolListTopics:
		_, err := decodeListTopicsArgs(args)
		return err
	case ToolListFindings:
		_, err := decodeListFindingsArgs(args)
		return err
	default:
		return fmt.Errorf("ceochat: unknown tool definition %q", definition.Name)
	}
}

var _ executionharness.ToolCatalog = ToolCatalog{}

type listTopicsArgs struct {
	DepartmentID *string `json:"department_id,omitempty"`
	Limit        *int    `json:"limit,omitempty"`
}

type listFindingsArgs struct {
	DepartmentID  *string `json:"department_id,omitempty"`
	TopicID       *string `json:"topic_id,omitempty"`
	ImportantOnly *bool   `json:"important_only,omitempty"`
	Limit         *int    `json:"limit,omitempty"`
}

// decodeStrict rejects unknown fields and trailing values, the same
// discipline modelruntimeadapter.decodeExact applies to the Harness's own
// canonical bytes -- host-owned tool arguments get no looser a reading.
func decodeStrict(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func decodeListTopicsArgs(body []byte) (listTopicsArgs, error) {
	if len(body) == 0 {
		body = []byte("{}")
	}
	var args listTopicsArgs
	if err := decodeStrict(body, &args); err != nil {
		return listTopicsArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if args.Limit != nil && (*args.Limit < 1 || *args.Limit > maxTopicsLimit) {
		return listTopicsArgs{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxTopicsLimit)
	}
	return args, nil
}

func decodeListFindingsArgs(body []byte) (listFindingsArgs, error) {
	if len(body) == 0 {
		body = []byte("{}")
	}
	var args listFindingsArgs
	if err := decodeStrict(body, &args); err != nil {
		return listFindingsArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if args.Limit != nil && (*args.Limit < 1 || *args.Limit > maxFindingsLimit) {
		return listFindingsArgs{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxFindingsLimit)
	}
	return args, nil
}

// ToolExecutor performs the two V1 read-only research tools. It is deny-by-
// default: any tool name other than the two it recognizes fails loudly
// rather than being silently ignored, because reaching this type at all
// already means the Harness decided the call was known, exposed, and
// argument-valid -- an unrecognized name here is a wiring bug, not a policy
// decision.
type ToolExecutor struct {
	Topics   TopicLister
	Findings FindingLister
}

func (e ToolExecutor) Execute(ctx context.Context, _ executionharness.RunIdentity, request executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	switch request.ToolName {
	case ToolListTopics:
		return e.executeListTopics(ctx, request.Arguments)
	case ToolListFindings:
		return e.executeListFindings(ctx, request.Arguments)
	default:
		return executionharness.ToolExecutionResult{}, fmt.Errorf("ceochat: no executor for tool %q", request.ToolName)
	}
}

type topicView struct {
	ID           string `json:"id"`
	DepartmentID string `json:"department_id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
}

func (e ToolExecutor) executeListTopics(ctx context.Context, rawArgs []byte) (executionharness.ToolExecutionResult, error) {
	args, err := decodeListTopicsArgs(rawArgs)
	if err != nil {
		return executionharness.ToolExecutionResult{}, err
	}
	department := ""
	if args.DepartmentID != nil {
		department = *args.DepartmentID
	}
	limit := defaultTopicsLimit
	if args.Limit != nil {
		limit = *args.Limit
	}
	topics, err := e.Topics.ListTopics(ctx, department)
	if err != nil {
		return executionharness.ToolExecutionResult{}, err
	}
	if len(topics) > limit {
		topics = topics[:limit]
	}
	views := make([]topicView, 0, len(topics))
	for _, topic := range topics {
		views = append(views, topicView{ID: topic.ID, DepartmentID: topic.DepartmentID, Title: topic.Title, Status: string(topic.Status)})
	}
	content, err := json.Marshal(struct {
		Topics []topicView `json:"topics"`
	}{Topics: views})
	if err != nil {
		return executionharness.ToolExecutionResult{}, err
	}
	return executionharness.ToolExecutionResult{Content: content, Provenance: "ceochat/research.list_topics/v1"}, nil
}

type findingView struct {
	ID             string `json:"id"`
	TopicID        string `json:"topic_id"`
	DepartmentID   string `json:"department_id"`
	Classification string `json:"classification"`
	Summary        string `json:"summary"`
	CreatedAt      string `json:"created_at"`
}

func (e ToolExecutor) executeListFindings(ctx context.Context, rawArgs []byte) (executionharness.ToolExecutionResult, error) {
	args, err := decodeListFindingsArgs(rawArgs)
	if err != nil {
		return executionharness.ToolExecutionResult{}, err
	}
	filter := search.FindingFilter{Limit: defaultFindingsLimit}
	if args.DepartmentID != nil {
		filter.DepartmentID = *args.DepartmentID
	}
	if args.TopicID != nil {
		filter.TopicID = *args.TopicID
	}
	if args.ImportantOnly != nil {
		filter.MinImportance = *args.ImportantOnly
	}
	if args.Limit != nil {
		filter.Limit = *args.Limit
	}
	findings, err := e.Findings.ListFindings(ctx, filter)
	if err != nil {
		return executionharness.ToolExecutionResult{}, err
	}
	views := make([]findingView, 0, len(findings))
	for _, finding := range findings {
		views = append(views, findingView{
			ID: finding.ID, TopicID: finding.TopicID, DepartmentID: finding.DepartmentID,
			Classification: string(finding.Classification), Summary: finding.Summary,
			CreatedAt: finding.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	content, err := json.Marshal(struct {
		Findings []findingView `json:"findings"`
	}{Findings: views})
	if err != nil {
		return executionharness.ToolExecutionResult{}, err
	}
	return executionharness.ToolExecutionResult{Content: content, Provenance: "ceochat/research.list_findings/v1"}, nil
}

var _ executionharness.ToolExecutor = ToolExecutor{}

// registerResearchTools registers research.list_topics and
// research.list_findings into a *ToolRegistry, producing byte-identical
// output to ToolCatalog/ToolExecutor above -- this is the round's
// "migrate research into the common ToolRegistry with unchanged behavior"
// requirement. It is what production bootstrap wires; ToolCatalog/
// ToolExecutor remain in this file only for their own isolated unit tests
// and for a caller that wants a narrower, registry-free composition.
func RegisterResearchTools(registry *ToolRegistry, topics TopicLister, findings FindingLister) error {
	executor := ToolExecutor{Topics: topics, Findings: findings}
	if err := registry.Register(ToolDescriptor{
		ID: ToolListTopics, Version: "v1",
		Description: "List research topics tracked for the organization, optionally scoped to one department.",
		InputSchema: listTopicsSchema, Access: AccessReadOnly, Effect: ToolEffectRead, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxRows: maxTopicsLimit, MaxResultBytes: 32 << 10, Timeout: defaultToolTimeout},
		DataClass: DataClassInternal,
	}, func(args json.RawMessage) error { _, err := decodeListTopicsArgs(args); return err },
		func(ctx context.Context, _ string, args json.RawMessage) (json.RawMessage, error) {
			result, err := executor.executeListTopics(ctx, args)
			if err != nil {
				return nil, err
			}
			return result.Content, nil
		}); err != nil {
		return err
	}
	return registry.Register(ToolDescriptor{
		ID: ToolListFindings, Version: "v1",
		Description: "List recent research findings, optionally scoped to a department or topic.",
		InputSchema: listFindingsSchema, Access: AccessReadOnly, Effect: ToolEffectRead, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxRows: maxFindingsLimit, MaxResultBytes: 32 << 10, Timeout: defaultToolTimeout},
		DataClass: DataClassInternal,
	}, func(args json.RawMessage) error { _, err := decodeListFindingsArgs(args); return err },
		func(ctx context.Context, _ string, args json.RawMessage) (json.RawMessage, error) {
			result, err := executor.executeListFindings(ctx, args)
			if err != nil {
				return nil, err
			}
			return result.Content, nil
		})
}
