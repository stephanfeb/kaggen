// Package tools provides tool implementations for the agent system.
package tools

import (
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/vfs"
)

// DefaultTools returns the default set of tools for an agent.
// All I/O is sandboxed through the provided VFS.
func DefaultTools(filesystem vfs.FS) []tool.Tool {
	base := []tool.Tool{
		newReadTool(filesystem),
		newWriteTool(filesystem),
	}
	// Include the Lua execution tool with access to the base tools.
	base = append(base, NewLuaTool(filesystem, base))
	return base
}
