package ceochat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const (
	ToolTasksList         = "tasks.list"
	ToolTasksGet          = "tasks.get"
	ToolTasksListAttempts = "tasks.list_attempts"

	tasksListVersion         = "v1"
	tasksGetVersion          = "v1"
	tasksListAttemptsVersion = "v1"

	maxTasksListRows     = 50
	maxTaskAttemptsRows  = 20
	defaultTasksListRows = 20
)

var tasksListSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "status": {"type": "string"},
    "assigned_role_id": {"type": "string", "maxLength": 240},
    "limit": {"type": "integer"},
    "cursor": {"type": "string", "maxLength": 400}
  }
}`)

var tasksGetSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["task_id"],
  "properties": {
    "task_id": {"type": "integer"}
  }
}`)

var tasksListAttemptsSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["task_id"],
  "properties": {
    "task_id": {"type": "integer"},
    "limit": {"type": "integer"},
    "cursor": {"type": "string", "maxLength": 400}
  }
}`)

type tasksListArgs struct {
	Status         *string `json:"status,omitempty"`
	AssignedRoleID *string `json:"assigned_role_id,omitempty"`
	Limit          *int    `json:"limit,omitempty"`
	Cursor         *string `json:"cursor,omitempty"`
}

type taskIDArgs struct {
	TaskID int64 `json:"task_id"`
}

type tasksListAttemptsArgs struct {
	TaskID int64   `json:"task_id"`
	Limit  *int    `json:"limit,omitempty"`
	Cursor *string `json:"cursor,omitempty"`
}

// taskListView and friends are host-designed projections: enough for the
// CEO to reason about a task, never the full repository row (no
// organization_revision_id, no request_hash, no internal FK identifiers
// beyond the ones that ARE the CEO-meaningful identity).
type taskListView struct {
	TaskID       int64  `json:"task_id"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	TaskClass    string `json:"task_class"`
	AssignedRole string `json:"assigned_role_id"`
	AttemptCount int    `json:"attempt_count"`
	CreatedAt    string `json:"created_at"`
}

type taskDetailView struct {
	TaskID             int64    `json:"task_id"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	TaskClass          string   `json:"task_class"`
	AssignedRole       string   `json:"assigned_role_id"`
	AttemptCount       int      `json:"attempt_count"`
	MaxAttempts        int      `json:"max_attempts"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	StatusReasonCode   string   `json:"status_reason_code,omitempty"`
	StatusReason       string   `json:"status_reason,omitempty"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
}

type taskAttemptView struct {
	AttemptID     int64  `json:"attempt_id"`
	Ordinal       int    `json:"ordinal"`
	State         string `json:"state"`
	WorkerID      string `json:"worker_id"`
	ResultSummary string `json:"result_summary,omitempty"`
	FailureCode   string `json:"failure_code,omitempty"`
	CreatedAt     string `json:"created_at"`
}

func decodeTasksListArgs(body json.RawMessage) (tasksListArgs, error) {
	if len(body) == 0 {
		body = []byte("{}")
	}
	var args tasksListArgs
	if err := decodeStrict(body, &args); err != nil {
		return tasksListArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if args.Limit != nil && (*args.Limit < 1 || *args.Limit > maxTasksListRows) {
		return tasksListArgs{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxTasksListRows)
	}
	if args.Status != nil {
		if _, err := parseTaskStatus(*args.Status); err != nil {
			return tasksListArgs{}, err
		}
	}
	if args.Cursor != nil {
		if _, err := decodeOffsetCursor(*args.Cursor); err != nil {
			return tasksListArgs{}, err
		}
	}
	return args, nil
}

// parseTaskStatus accepts exactly the durable status vocabulary
// tasks.Status already defines. It is deliberately closed: a model asking
// for a status this Task Engine has never produced is a malformed request,
// not a query to run and return zero rows for.
func parseTaskStatus(raw string) (tasks.Status, error) {
	switch tasks.Status(raw) {
	case tasks.StatusPending, tasks.StatusReady, tasks.StatusLeased, tasks.StatusRunning,
		tasks.StatusAwaitingVerification, tasks.StatusBlocked, tasks.StatusRetryWait,
		tasks.StatusCompleted, tasks.StatusNoAction, tasks.StatusFailed, tasks.StatusDeadLetter,
		tasks.StatusRejected, tasks.StatusCancelled:
		return tasks.Status(raw), nil
	default:
		return "", fmt.Errorf("%w: unknown task status %q", ErrInvalidInput, raw)
	}
}

func decodeTaskIDArgs(body json.RawMessage) (taskIDArgs, error) {
	var args taskIDArgs
	if err := decodeStrict(body, &args); err != nil {
		return taskIDArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if args.TaskID <= 0 {
		return taskIDArgs{}, fmt.Errorf("%w: task_id must be positive", ErrInvalidInput)
	}
	return args, nil
}

func decodeTasksListAttemptsArgs(body json.RawMessage) (tasksListAttemptsArgs, error) {
	var args tasksListAttemptsArgs
	if err := decodeStrict(body, &args); err != nil {
		return tasksListAttemptsArgs{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if args.TaskID <= 0 {
		return tasksListAttemptsArgs{}, fmt.Errorf("%w: task_id must be positive", ErrInvalidInput)
	}
	if args.Limit != nil && (*args.Limit < 1 || *args.Limit > maxTaskAttemptsRows) {
		return tasksListAttemptsArgs{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxTaskAttemptsRows)
	}
	if args.Cursor != nil {
		if _, err := decodeOffsetCursor(*args.Cursor); err != nil {
			return tasksListAttemptsArgs{}, err
		}
	}
	return args, nil
}

// registerTaskTools adds tasks.list, tasks.get, and tasks.list_attempts.
// Every handler calls straight through to *tasks.Service -- the SAME
// canonical Task Engine ceochat.Service already drives turns through --
// and returns only the bounded, host-designed projections above.
func RegisterTaskTools(registry *ToolRegistry, reader TaskReader) error {
	if err := registry.Register(ToolDescriptor{
		ID: ToolTasksList, Version: tasksListVersion,
		Description: "List durable tasks, optionally filtered by status or assigned role.",
		InputSchema: tasksListSchema, Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxRows: maxTasksListRows, MaxResultBytes: 32 << 10, Timeout: defaultToolTimeout},
		DataClass: DataClassInternal,
	}, func(body json.RawMessage) error { _, err := decodeTasksListArgs(body); return err },
		func(ctx context.Context, _ string, body json.RawMessage) (json.RawMessage, error) {
			args, err := decodeTasksListArgs(body)
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
			limit := defaultTasksListRows
			if args.Limit != nil {
				limit = *args.Limit
			}
			filter := tasks.TaskFilter{Limit: limit, Offset: offset}
			if args.Status != nil {
				status, statusErr := parseTaskStatus(*args.Status)
				if statusErr != nil {
					return nil, statusErr
				}
				filter.Statuses = []tasks.Status{status}
			}
			if args.AssignedRoleID != nil {
				filter.AssignedRoleID = *args.AssignedRoleID
			}
			results, err := reader.ListTasks(ctx, filter)
			if err != nil {
				return nil, err
			}
			views := make([]taskListView, 0, len(results))
			for _, task := range results {
				views = append(views, taskListView{
					TaskID: task.ID, Title: task.Title, Status: string(task.Status), TaskClass: task.TaskClass,
					AssignedRole: task.AssignedRoleID, AttemptCount: task.AttemptCount,
					CreatedAt: task.CreatedAt.UTC().Format(time.RFC3339),
				})
			}
			nextCursor := ""
			if len(results) == limit {
				nextCursor = encodeOffsetCursor(offset + limit)
			}
			return json.Marshal(struct {
				Tasks      []taskListView `json:"tasks"`
				NextCursor string         `json:"next_cursor,omitempty"`
			}{Tasks: views, NextCursor: nextCursor})
		}); err != nil {
		return err
	}

	if err := registry.Register(ToolDescriptor{
		ID: ToolTasksGet, Version: tasksGetVersion,
		Description: "Get one durable task by ID.",
		InputSchema: tasksGetSchema, Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxResultBytes: 16 << 10, Timeout: defaultToolTimeout},
		DataClass: DataClassInternal,
	}, func(body json.RawMessage) error { _, err := decodeTaskIDArgs(body); return err },
		func(ctx context.Context, _ string, body json.RawMessage) (json.RawMessage, error) {
			args, err := decodeTaskIDArgs(body)
			if err != nil {
				return nil, err
			}
			detail, err := reader.GetTask(ctx, args.TaskID)
			if err != nil {
				return nil, err
			}
			view := taskDetailView{
				TaskID: detail.Task.ID, Title: detail.Task.Title, Status: string(detail.Task.Status),
				TaskClass: detail.Task.TaskClass, AssignedRole: detail.Task.AssignedRoleID,
				AttemptCount: detail.Task.AttemptCount, MaxAttempts: detail.Task.MaxAttempts,
				AcceptanceCriteria: detail.Task.AcceptanceCriteria,
				CreatedAt:          detail.Task.CreatedAt.UTC().Format(time.RFC3339),
				UpdatedAt:          detail.Task.UpdatedAt.UTC().Format(time.RFC3339),
			}
			if detail.Task.StatusReasonCode != nil {
				view.StatusReasonCode = *detail.Task.StatusReasonCode
			}
			if detail.Task.StatusReason != nil {
				view.StatusReason = *detail.Task.StatusReason
			}
			return json.Marshal(view)
		}); err != nil {
		return err
	}

	return registry.Register(ToolDescriptor{
		ID: ToolTasksListAttempts, Version: tasksListAttemptsVersion,
		Description: "List the attempts recorded for one task.",
		InputSchema: tasksListAttemptsSchema, Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits:    ToolLimits{MaxRows: maxTaskAttemptsRows, MaxResultBytes: 16 << 10, Timeout: defaultToolTimeout},
		DataClass: DataClassInternal,
	}, func(body json.RawMessage) error { _, err := decodeTasksListAttemptsArgs(body); return err },
		func(ctx context.Context, _ string, body json.RawMessage) (json.RawMessage, error) {
			args, err := decodeTasksListAttemptsArgs(body)
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
			limit := maxTaskAttemptsRows
			if args.Limit != nil {
				limit = *args.Limit
			}
			// A real SQL LIMIT/OFFSET (tasks.Service.ListAttemptsPage) --
			// never "fetch every attempt this task has ever had, then
			// slice," which would be an unbounded read for a task with a
			// pathologically long retry history.
			attempts, err := reader.ListAttemptsPage(ctx, args.TaskID, limit, offset)
			if err != nil {
				return nil, err
			}
			views := make([]taskAttemptView, 0, len(attempts))
			for _, attempt := range attempts {
				view := taskAttemptView{
					AttemptID: attempt.ID, Ordinal: attempt.Ordinal, State: string(attempt.State),
					WorkerID: attempt.WorkerID, CreatedAt: attempt.CreatedAt.UTC().Format(time.RFC3339),
				}
				if attempt.ResultSummary != nil {
					view.ResultSummary = *attempt.ResultSummary
				}
				if attempt.FailureCode != nil {
					view.FailureCode = *attempt.FailureCode
				}
				views = append(views, view)
			}
			nextCursor := ""
			if len(attempts) == limit {
				nextCursor = encodeOffsetCursor(offset + limit)
			}
			return json.Marshal(struct {
				Attempts   []taskAttemptView `json:"attempts"`
				NextCursor string            `json:"next_cursor,omitempty"`
			}{Attempts: views, NextCursor: nextCursor})
		})
}
