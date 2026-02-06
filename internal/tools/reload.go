package tools

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// SkillReloader is an interface for triggering skill reloads.
// Implemented by AgentFactory.
type SkillReloader interface {
	RequestReload() error
	WaitReloadDone() error
}

// reloadSkillsArgs defines the input arguments for the reload_skills tool.
type reloadSkillsArgs struct {
	// No arguments needed - just triggers a reload
}

// reloadSkillsResult defines the output of the reload_skills tool.
type reloadSkillsResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

const reloadTimeout = 30 * time.Second

// ReloadSkillsTool creates a tool that triggers skill reload, or nil if no reloader provided.
func ReloadSkillsTool(reloader SkillReloader) tool.Tool {
	if reloader == nil {
		return nil
	}
	return newReloadSkillsTool(reloader)
}

func newReloadSkillsTool(reloader SkillReloader) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args reloadSkillsArgs) (*reloadSkillsResult, error) {
			return executeReloadSkills(ctx, reloader)
		},
		function.WithName("reload_skills"),
		function.WithDescription("Reload all skills from disk. Use this after creating a new skill with skill-builder to make it immediately available."),
	)
}

func executeReloadSkills(ctx context.Context, reloader SkillReloader) (*reloadSkillsResult, error) {
	// Request reload
	if err := reloader.RequestReload(); err != nil {
		return &reloadSkillsResult{
			Success: false,
			Message: fmt.Sprintf("Failed to request reload: %v", err),
		}, nil
	}

	// Wait for reload to complete with timeout
	done := make(chan error, 1)
	go func() {
		done <- reloader.WaitReloadDone()
	}()

	select {
	case err := <-done:
		if err != nil {
			return &reloadSkillsResult{
				Success: false,
				Message: fmt.Sprintf("Reload failed: %v", err),
			}, nil
		}
		return &reloadSkillsResult{
			Success: true,
			Message: "Skills reloaded successfully",
		}, nil
	case <-time.After(reloadTimeout):
		return &reloadSkillsResult{
			Success: false,
			Message: "Reload timed out after 30 seconds",
		}, nil
	case <-ctx.Done():
		return &reloadSkillsResult{
			Success: false,
			Message: fmt.Sprintf("Reload cancelled: %v", ctx.Err()),
		}, nil
	}
}
