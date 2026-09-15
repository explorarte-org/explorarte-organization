package ceochat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

const (
	ToolMemorySearch    = "memory.search"
	memorySearchVersion = "v1"

	maxMemorySearchRows     = 20
	defaultMemorySearchRows = 10
)

// memory.get_episode and memory.get_semantic_entry are NOT registered this
// round: they were explicitly optional, and this round did not need to go
// past memory.search (the mandatory minimum) to satisfy the multi-tool E2E
// scenario. A future round may add them against memory.Manager's own
// Get-by-ID seams once a concrete need names what they should return.

var memorySearchSchema = json.RawMessage(fmt.Sprintf(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["query"],
  "properties": {
    "query": {
      "type": "string",
      "maxLength": 2000,
      "description": "Query text to search prior role memories and corrections for."
    },
    "task_id": {
      "type": "integer",
      "minimum": 1,
      "description": "Positive integer task ID to scope search to. Omit this field when searching across all tasks."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": %d,
      "description": "Maximum memory entries to return. Omit to use default (%d). Must be between 1 and %d."
    }
  }
}`, maxMemorySearchRows, defaultMemorySearchRows, maxMemorySearchRows))

type memorySearchArgs struct {
	Query  string `json:"query"`
	TaskID *int64 `json:"task_id,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

type memoryEntryView struct {
	ID         string `json:"id"`
	Category   string `json:"category"`
	Problem    string `json:"problem"`
	Correction string `json:"correction"`
	SourceKind string `json:"source_kind"`
	CreatedAt  string `json:"created_at"`
}

func decodeMemorySearchArgs(body json.RawMessage) (memorySearchArgs, error) {
	var args memorySearchArgs
	if err := decodeStrict(body, &args); err != nil {
		return memorySearchArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return memorySearchArgs{}, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	if args.TaskID != nil && *args.TaskID <= 0 {
		return memorySearchArgs{}, fmt.Errorf("%w: task_id must be positive", ErrInvalidInput)
	}
	if args.Limit != nil && (*args.Limit < 1 || *args.Limit > maxMemorySearchRows) {
		return memorySearchArgs{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxMemorySearchRows)
	}
	return args, nil
}

// MemorySearcher is the narrow seam memory.search needs from MemoryOS.
// *memory.Manager satisfies it directly -- ceochat never queries pgvector,
// the memory repository, or any embedding store on its own.
type MemorySearcher interface {
	Search(ctx context.Context, request memory.SearchRequest) ([]memory.Entry, error)
}

// registerMemoryTools adds memory.search. actorRoleID is fixed to CEORoleID
// at Execute time (RegistryToolExecutor's own defense-in-depth check
// already proves the caller's identity.RoleID == CEORoleID before this
// handler runs): SearchRequest.RoleID is set to the SAME value, never a
// model-supplied namespace, which is what keeps this tool inside R29's own
// "search your own role's memory" boundary -- memory.search cannot be
// asked to widen into another role's memory, because the handler never
// reads a role argument from the model at all.
func RegisterMemoryTools(registry *ToolRegistry, organizationID string, searcher MemorySearcher) error {
	return registry.Register(ToolDescriptor{
		ID: ToolMemorySearch, Version: memorySearchVersion,
		Description: "Search the CEO's own role memory for prior corrections relevant to a query, optionally scoped to a task.",
		InputSchema: memorySearchSchema, Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxRows: maxMemorySearchRows, MaxResultBytes: 32 << 10, Timeout: 10 * time.Second},
		DataClass: DataClassInternal,
	}, func(body json.RawMessage) error { _, err := decodeMemorySearchArgs(body); return err },
		func(ctx context.Context, actorRoleID string, body json.RawMessage) (json.RawMessage, error) {
			args, err := decodeMemorySearchArgs(body)
			if err != nil {
				return nil, err
			}
			limit := defaultMemorySearchRows
			if args.Limit != nil {
				limit = *args.Limit
			}
			entries, err := searcher.Search(ctx, memory.SearchRequest{
				OrganizationID: organizationID,
				ActorRoleID:    actorRoleID,
				RoleID:         actorRoleID,
				QueryText:      args.Query,
				TaskID:         args.TaskID,
				Limit:          limit,
			})
			if err != nil {
				return nil, err
			}
			views := make([]memoryEntryView, 0, len(entries))
			for _, entry := range entries {
				views = append(views, memoryEntryView{
					ID: entry.ID, Category: entry.Category, Problem: entry.Problem,
					Correction: entry.Correction, SourceKind: string(entry.SourceKind),
					CreatedAt: entry.CreatedAt.UTC().Format(time.RFC3339),
				})
			}
			return json.Marshal(struct {
				Entries []memoryEntryView `json:"entries"`
			}{Entries: views})
		})
}
