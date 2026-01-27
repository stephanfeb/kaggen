// Package tools provides tool implementations for the agent system.
package tools

import (
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// DefaultTools returns the default set of tools for an agent.
func DefaultTools(workspace string) []tool.Tool {
	return []tool.Tool{
		newReadTool(workspace),
		newWriteTool(workspace),
		newExecTool(workspace),
	}
}
