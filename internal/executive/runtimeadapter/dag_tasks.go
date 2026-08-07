package runtimeadapter

import (
	"context"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// DAGTasks guarantees that an executive worker is pending from the instant it
// is materialized. R22 adds worker-to-worker dependency edges after CreateTask;
// without this guard a dependency-free insert is transiently ready and can be
// claimed concurrently. Every worker already has a logical source planning or
// review task in CausationID. Making that source a real dependency is both
// semantically correct and closes the ready-window: the source cannot complete
// until R22 finishes materializing the whole proposed DAG.
type DAGTasks struct {
	executive.TaskCoordinator
}

func (d DAGTasks) CreateTask(ctx context.Context, command executive.CreateTaskCommand) (executive.TaskRecord, bool, error) {
	if strings.Contains(command.IdempotencyKey, ":worker:") {
		if sourceID, ok := sourceTaskID(command.CausationID); ok && !containsTaskID(command.Dependencies, sourceID) {
			command.Dependencies = append([]int64{sourceID}, command.Dependencies...)
		}
	}
	return d.TaskCoordinator.CreateTask(ctx, command)
}

func sourceTaskID(causation string) (int64, bool) {
	const prefix = "task:"
	if !strings.HasPrefix(causation, prefix) {
		return 0, false
	}
	value := strings.TrimPrefix(causation, prefix)
	if strings.Contains(value, ":") {
		return 0, false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}

func containsTaskID(values []int64, id int64) bool {
	for _, value := range values {
		if value == id {
			return true
		}
	}
	return false
}

var _ executive.TaskCoordinator = DAGTasks{}
