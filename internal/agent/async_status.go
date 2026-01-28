package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// taskStatusRequest is the input schema for the task_status tool.
type taskStatusRequest struct {
	TaskID string `json:"task_id,omitempty" jsonschema:"description=Optional task ID to check. If empty returns all tasks."`
	Status string `json:"status,omitempty" jsonschema:"description=Optional status filter: running or completed or failed"`
}

// taskStatusResponse is the output schema for the task_status tool.
type taskStatusResponse struct {
	Tasks []*TaskState `json:"tasks"`
}

type taskStatusChecker struct {
	store *InFlightStore
}

func (c *taskStatusChecker) check(_ context.Context, req taskStatusRequest) (taskStatusResponse, error) {
	if req.TaskID != "" {
		t, ok := c.store.Get(req.TaskID)
		if !ok {
			return taskStatusResponse{}, nil
		}
		return taskStatusResponse{Tasks: []*TaskState{t}}, nil
	}

	tasks := c.store.List(TaskStatus(req.Status))
	return taskStatusResponse{Tasks: tasks}, nil
}

// NewTaskStatusTool creates a tool that reports on in-flight and completed async tasks.
func NewTaskStatusTool(store *InFlightStore) tool.Tool {
	c := &taskStatusChecker{store: store}
	return function.NewFunctionTool(
		c.check,
		function.WithName("task_status"),
		function.WithDescription("Check the status of dispatched async tasks. Returns task IDs, status (running/completed/failed), and results."),
	)
}
