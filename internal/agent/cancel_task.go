package agent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type cancelTaskRequest struct {
	TaskID string `json:"task_id" jsonschema:"required,description=The task ID to cancel"`
}

type cancelTaskResponse struct {
	Cancelled bool   `json:"cancelled"`
	Message   string `json:"message"`
}

type cancelTaskHandler struct {
	store *InFlightStore
}

func (h *cancelTaskHandler) cancel(_ context.Context, req cancelTaskRequest) (cancelTaskResponse, error) {
	if req.TaskID == "" {
		return cancelTaskResponse{}, fmt.Errorf("task_id is required")
	}

	if h.store.Cancel(req.TaskID) {
		return cancelTaskResponse{
			Cancelled: true,
			Message:   fmt.Sprintf("Task %s has been cancelled.", req.TaskID),
		}, nil
	}

	// Check if the task exists at all.
	t, ok := h.store.Get(req.TaskID)
	if !ok {
		return cancelTaskResponse{Message: fmt.Sprintf("Task %s not found.", req.TaskID)}, nil
	}
	return cancelTaskResponse{
		Message: fmt.Sprintf("Task %s is already %s.", req.TaskID, t.Status),
	}, nil
}

// NewCancelTaskTool creates a tool that cancels a running async task.
func NewCancelTaskTool(store *InFlightStore) tool.Tool {
	h := &cancelTaskHandler{store: store}
	return function.NewFunctionTool(
		h.cancel,
		function.WithName("cancel_task"),
		function.WithDescription("Cancel a running async task. Use task_status to find the task ID first."),
	)
}
