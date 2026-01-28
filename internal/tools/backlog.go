package tools

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/backlog"
)

// BacklogTools returns the set of tools for managing the persistent work backlog.
func BacklogTools(store *backlog.Store) []tool.Tool {
	bt := &backlogToolSet{store: store}
	return []tool.Tool{
		bt.listTool(),
		bt.addTool(),
		bt.updateTool(),
		bt.completeTool(),
	}
}

type backlogToolSet struct {
	store *backlog.Store
}

// --- backlog_list ---

type backlogListRequest struct {
	Status   string `json:"status,omitempty" jsonschema:"description=Filter by status: pending or in_progress or completed or failed or blocked"`
	Priority string `json:"priority,omitempty" jsonschema:"description=Filter by priority: high or normal or low"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=Max number of items to return (default 50)"`
}

type backlogListResponse struct {
	Items []*backlog.Item `json:"items"`
	Count int             `json:"count"`
}

func (bt *backlogToolSet) list(_ context.Context, req backlogListRequest) (backlogListResponse, error) {
	items, err := bt.store.List(backlog.Filter{
		Status:   req.Status,
		Priority: req.Priority,
		Limit:    req.Limit,
	})
	if err != nil {
		return backlogListResponse{}, fmt.Errorf("list backlog: %w", err)
	}
	return backlogListResponse{Items: items, Count: len(items)}, nil
}

func (bt *backlogToolSet) listTool() tool.Tool {
	return function.NewFunctionTool(
		bt.list,
		function.WithName("backlog_list"),
		function.WithDescription("List tasks from the persistent work backlog. Optionally filter by status and priority."),
	)
}

// --- backlog_add ---

type backlogAddRequest struct {
	Title       string `json:"title" jsonschema:"required,description=Short title for the task"`
	Description string `json:"description,omitempty" jsonschema:"description=Detailed description of what needs to be done"`
	Priority    string `json:"priority,omitempty" jsonschema:"description=Priority: high or normal (default) or low"`
	Source      string `json:"source,omitempty" jsonschema:"description=Who created this task: user or coordinator or sub-agent"`
}

type backlogAddResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

func (bt *backlogToolSet) add(_ context.Context, req backlogAddRequest) (backlogAddResponse, error) {
	item := &backlog.Item{
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		Source:      req.Source,
	}
	if err := bt.store.Add(item); err != nil {
		return backlogAddResponse{}, fmt.Errorf("add backlog item: %w", err)
	}
	return backlogAddResponse{ID: item.ID, Message: "Added to backlog"}, nil
}

func (bt *backlogToolSet) addTool() tool.Tool {
	return function.NewFunctionTool(
		bt.add,
		function.WithName("backlog_add"),
		function.WithDescription("Add a new task to the persistent work backlog."),
	)
}

// --- backlog_update ---

type backlogUpdateRequest struct {
	ID          string  `json:"id" jsonschema:"required,description=ID of the backlog item to update"`
	Title       *string `json:"title,omitempty" jsonschema:"description=New title"`
	Description *string `json:"description,omitempty" jsonschema:"description=New description"`
	Priority    *string `json:"priority,omitempty" jsonschema:"description=New priority: high or normal or low"`
	Status      *string `json:"status,omitempty" jsonschema:"description=New status: pending or in_progress or completed or failed or blocked"`
}

type backlogUpdateResponse struct {
	Message string `json:"message"`
}

func (bt *backlogToolSet) update(_ context.Context, req backlogUpdateRequest) (backlogUpdateResponse, error) {
	err := bt.store.Update(req.ID, backlog.Update{
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		Status:      req.Status,
	})
	if err != nil {
		return backlogUpdateResponse{}, fmt.Errorf("update backlog item: %w", err)
	}
	return backlogUpdateResponse{Message: "Updated"}, nil
}

func (bt *backlogToolSet) updateTool() tool.Tool {
	return function.NewFunctionTool(
		bt.update,
		function.WithName("backlog_update"),
		function.WithDescription("Update an existing backlog item's title, description, priority, or status."),
	)
}

// --- backlog_complete ---

type backlogCompleteRequest struct {
	ID      string `json:"id" jsonschema:"required,description=ID of the backlog item to mark as completed"`
	Summary string `json:"summary" jsonschema:"required,description=Summary of what was accomplished"`
}

type backlogCompleteResponse struct {
	Message string `json:"message"`
}

func (bt *backlogToolSet) complete(_ context.Context, req backlogCompleteRequest) (backlogCompleteResponse, error) {
	if err := bt.store.Complete(req.ID, req.Summary); err != nil {
		return backlogCompleteResponse{}, fmt.Errorf("complete backlog item: %w", err)
	}
	return backlogCompleteResponse{Message: "Completed"}, nil
}

func (bt *backlogToolSet) completeTool() tool.Tool {
	return function.NewFunctionTool(
		bt.complete,
		function.WithName("backlog_complete"),
		function.WithDescription("Mark a backlog item as completed with a summary of what was done."),
	)
}
