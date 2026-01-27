// Package tools provides tool implementations for the agent system.
package tools

import (
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/embedding"
	"github.com/yourusername/kaggen/internal/memory"
)

// DefaultTools returns the default set of tools for an agent.
func DefaultTools(workspace string) []tool.Tool {
	return []tool.Tool{
		newReadTool(workspace),
		newWriteTool(workspace),
		newExecTool(workspace),
	}
}

// MemoryTools returns tools for semantic memory search and write.
func MemoryTools(embedder embedding.Embedder, index *memory.VectorIndex, indexer *memory.Indexer, workspace string) []tool.Tool {
	return []tool.Tool{
		newMemorySearchTool(embedder, index),
		newMemoryWriteTool(index, indexer, workspace),
	}
}

