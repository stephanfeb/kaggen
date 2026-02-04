// Package tools provides tool implementations for the agent system.
package tools

import (
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/security"
)

// DefaultTools returns the default set of tools for an agent.
func DefaultTools(workspace string) []tool.Tool {
	return DefaultToolsWithConfig(workspace, nil)
}

// DefaultToolsWithConfig returns the default set of tools with security configuration.
func DefaultToolsWithConfig(workspace string, cfg *config.Config) []tool.Tool {
	// Create command sandbox from config
	var sandbox *security.CommandSandbox
	if cfg != nil && cfg.Security.CommandSandbox.Enabled {
		var err error
		sandbox, err = security.NewCommandSandbox(cfg.Security.CommandSandbox.BlockedPatterns, true)
		if err != nil {
			// Fall back to default patterns on error
			sandbox, _ = security.NewCommandSandbox(nil, true)
		}
	}

	return []tool.Tool{
		newReadTool(workspace),
		newWriteTool(workspace),
		newExecToolWithSandbox(workspace, sandbox),
	}
}
