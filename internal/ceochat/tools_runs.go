package ceochat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

const (
	ToolRunsListRecent = "runs.list_recent"
	ToolRunsGet        = "runs.get"

	runsListRecentVersion = "v1"
	runsGetVersion        = "v1"

	maxRunsListRows     = 30
	defaultRunsListRows = 10

	// runStatusInProgress is a host-presentation label, not an
	// executionharness.RunStatus: a run with no terminal event yet has no
	// canonical terminal status at all, and inventing one there would make
	// this the second place that decides what "still running" means.
	runStatusInProgress = "in_progress"
)

var runsListRecentSchema = json.RawMessage(fmt.Sprintf(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "task_id": {
      "type": "integer",
      "minimum": 1,
      "description": "Positive integer task ID to filter runs by. Omit this field when not filtering by task."
    },
    "execution_profile_id": {
      "type": "string",
      "maxLength": 240,
      "description": "Filter runs by execution profile ID. Omit this field when not filtering by profile."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": %d,
      "description": "Maximum runs to return. Omit to use the host default (%d). Must be between 1 and %d."
    },
    "cursor": {
      "type": "string",
      "maxLength": 400,
      "description": "Pagination cursor from a previous page. Omit this field for the first page."
    }
  }
}`, maxRunsListRows, defaultRunsListRows, maxRunsListRows))

var runsGetSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["run_id"],
  "properties": {
    "run_id": {
      "type": "string",
      "maxLength": 200,
      "description": "Canonical run identifier."
    }
  }
}`)

type runsListRecentArgs struct {
	TaskID             *int64  `json:"task_id,omitempty"`
	ExecutionProfileID *string `json:"execution_profile_id,omitempty"`
	Limit              *int    `json:"limit,omitempty"`
	Cursor             *string `json:"cursor,omitempty"`
}

type runsGetArgs struct {
	RunID string `json:"run_id"`
}

type runListView struct {
	RunID              string `json:"run_id"`
	TaskID             int64  `json:"task_id"`
	AttemptID          int64  `json:"attempt_id"`
	ExecutionProfileID string `json:"execution_profile_id"`
	Status             string `json:"status"`
	CreatedAt          string `json:"created_at"`
}

type runDetailView struct {
	RunID              string `json:"run_id"`
	TaskID             int64  `json:"task_id"`
	AttemptID          int64  `json:"attempt_id"`
	RoleID             string `json:"role_id"`
	ExecutionProfileID string `json:"execution_profile_id"`
	MaxTurns           int    `json:"max_turns"`
	MaxToolCalls       int    `json:"max_tool_calls"`
	Status             string `json:"status"`
	ErrorCode          string `json:"error_code,omitempty"`
	Reason             string `json:"reason,omitempty"`
	TurnsUsed          int    `json:"turns_used"`
	ToolCallsUsed      int    `json:"tool_calls_used"`
}

func decodeRunsListRecentArgs(body json.RawMessage) (runsListRecentArgs, error) {
	if len(body) == 0 {
		body = []byte("{}")
	}
	var args runsListRecentArgs
	if err := decodeStrict(body, &args); err != nil {
		return runsListRecentArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if args.TaskID != nil && *args.TaskID <= 0 {
		return runsListRecentArgs{}, fmt.Errorf("%w: task_id must be positive", ErrInvalidInput)
	}
	if args.Limit != nil && (*args.Limit < 1 || *args.Limit > maxRunsListRows) {
		return runsListRecentArgs{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxRunsListRows)
	}
	if args.Cursor != nil {
		if _, err := decodeOffsetCursor(*args.Cursor); err != nil {
			return runsListRecentArgs{}, err
		}
	}
	return args, nil
}

func decodeRunsGetArgs(body json.RawMessage) (runsGetArgs, error) {
	var args runsGetArgs
	if err := decodeStrict(body, &args); err != nil {
		return runsGetArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if strings.TrimSpace(args.RunID) == "" {
		return runsGetArgs{}, fmt.Errorf("%w: run_id is required", ErrInvalidInput)
	}
	return args, nil
}

// runOutcome derives a run's presentation status from its own durable
// trajectory: the same terminal Event fields (Type/TerminalStatus/
// ErrorCode/Reason) the Harness itself relies on to decide whether a run
// may be resumed, read the same way (Read's full event list), never a
// second notion of "done" computed some other way.
func runOutcome(events []executionharness.Event) (status, errorCode, reason string, turnsUsed, toolCallsUsed int) {
	status = runStatusInProgress
	for _, event := range events {
		switch event.Type {
		case executionharness.EventModelRequestPrepared:
			turnsUsed++
		case executionharness.EventToolResultRecorded:
			toolCallsUsed++
		case executionharness.EventRunCompleted, executionharness.EventRunFailed,
			executionharness.EventRunLimitReached, executionharness.EventRunCancelled:
			status = string(event.TerminalStatus)
			errorCode = event.ErrorCode
			reason = event.Reason
		}
	}
	return status, errorCode, reason, turnsUsed, toolCallsUsed
}

// registerRunTools adds runs.list_recent and runs.get. list_recent reads
// through RunLister (production-adapted from
// *executionharnesspostgres.Store.ListRunDescriptors) plus RunEventReader
// for each row's status; get reads the single descriptor straight from the
// canonical RunDescriptorStore Service.DescriptorStore already is, plus the
// same RunEventReader for outcome. No new store, no bypass, and no prompt
// or tool-body content ever leaves these handlers.
func RegisterRunTools(registry *ToolRegistry, organizationID string, runs RunLister, descriptors executionharness.RunDescriptorStore, history RunEventReader) error {
	if err := registry.Register(ToolDescriptor{
		ID: ToolRunsListRecent, Version: runsListRecentVersion,
		Description: "List the most recent Harness runs, optionally filtered by task or execution profile.",
		InputSchema: runsListRecentSchema, Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxRows: maxRunsListRows, MaxResultBytes: 32 << 10, Timeout: defaultToolTimeout},
		DataClass: DataClassInternal,
	}, func(body json.RawMessage) error { _, err := decodeRunsListRecentArgs(body); return err },
		func(ctx context.Context, _ string, body json.RawMessage) (json.RawMessage, error) {
			args, err := decodeRunsListRecentArgs(body)
			if err != nil {
				return nil, err
			}
			offset := 0
			if args.Cursor != nil {
				offset, err = decodeOffsetCursor(*args.Cursor)
				if err != nil {
					return nil, err
				}
			}
			limit := defaultRunsListRows
			if args.Limit != nil {
				limit = *args.Limit
			}
			filter := RunDescriptorFilter{Limit: limit, Offset: offset}
			if args.TaskID != nil {
				filter.TaskID = *args.TaskID
			}
			if args.ExecutionProfileID != nil {
				filter.ExecutionProfileID = *args.ExecutionProfileID
			}
			records, err := runs.ListRunDescriptors(ctx, filter)
			if err != nil {
				return nil, err
			}
			views := make([]runListView, 0, len(records))
			for _, record := range records {
				events, err := history.Read(ctx, record.RunID)
				if err != nil {
					return nil, err
				}
				status, _, _, _, _ := runOutcome(events)
				views = append(views, runListView{
					RunID: record.RunID, TaskID: record.TaskID, AttemptID: record.AttemptID,
					ExecutionProfileID: record.ExecutionProfileID, Status: status,
					CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339),
				})
			}
			nextCursor := ""
			if len(records) == limit {
				nextCursor = encodeOffsetCursor(offset + limit)
			}
			return json.Marshal(struct {
				Runs       []runListView `json:"runs"`
				NextCursor string        `json:"next_cursor,omitempty"`
			}{Runs: views, NextCursor: nextCursor})
		}); err != nil {
		return err
	}

	return registry.Register(ToolDescriptor{
		ID: ToolRunsGet, Version: runsGetVersion,
		Description: "Get one Harness run's descriptor and outcome by run ID.",
		InputSchema: runsGetSchema, Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxResultBytes: 16 << 10, Timeout: defaultToolTimeout},
		DataClass: DataClassInternal,
	}, func(body json.RawMessage) error { _, err := decodeRunsGetArgs(body); return err },
		func(ctx context.Context, _ string, body json.RawMessage) (json.RawMessage, error) {
			args, err := decodeRunsGetArgs(body)
			if err != nil {
				return nil, err
			}
			descriptor, err := descriptors.ReadRunDescriptor(ctx, organizationID, args.RunID)
			if err != nil {
				if errors.Is(err, executionharness.ErrRunDescriptorNotFound) {
					return nil, fmt.Errorf("%w: run %q not found", ErrInvalidInput, args.RunID)
				}
				return nil, err
			}
			events, err := history.Read(ctx, args.RunID)
			if err != nil {
				return nil, err
			}
			status, errorCode, reason, turnsUsed, toolCallsUsed := runOutcome(events)
			view := runDetailView{
				RunID: descriptor.RunID, TaskID: descriptor.TaskID, AttemptID: descriptor.AttemptID,
				RoleID: descriptor.RoleID, ExecutionProfileID: descriptor.ExecutionProfileID,
				MaxTurns: descriptor.MaxTurns, MaxToolCalls: descriptor.MaxToolCalls,
				Status: status, ErrorCode: errorCode, Reason: reason,
				TurnsUsed: turnsUsed, ToolCallsUsed: toolCallsUsed,
			}
			return json.Marshal(view)
		})
}
