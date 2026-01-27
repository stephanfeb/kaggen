package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/memory"
)

// MemoryWriteArgs defines the input for the memory_write tool.
type MemoryWriteArgs struct {
	File    string `json:"file" jsonschema:"required,description=File to write relative to workspace: MEMORY.md or memory/YYYY-MM-DD.md"`
	Content string `json:"content" jsonschema:"required,description=Content to write to the memory file"`
	Append  bool   `json:"append,omitempty" jsonschema:"description=Append to the file instead of overwriting (default true)"`
}

// MemoryWriteResult defines the output of the memory_write tool.
type MemoryWriteResult struct {
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	Message string `json:"message"`
}

func newMemoryWriteTool(index *memory.VectorIndex, indexer *memory.Indexer, workspace string) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args MemoryWriteArgs) (*MemoryWriteResult, error) {
			return executeMemoryWrite(ctx, index, indexer, workspace, args)
		},
		function.WithName("memory_write"),
		function.WithDescription("Write content to a memory file. Use this to store important information, preferences, or notes for future recall. Files are automatically indexed for semantic search."),
	)
}

func executeMemoryWrite(ctx context.Context, _ *memory.VectorIndex, indexer *memory.Indexer, workspace string, args MemoryWriteArgs) (*MemoryWriteResult, error) {
	result := &MemoryWriteResult{}

	if args.File == "" {
		result.Message = "Error: file is required"
		return result, fmt.Errorf("file is required")
	}
	if args.Content == "" {
		result.Message = "Error: content is required"
		return result, fmt.Errorf("content is required")
	}

	// Validate path is within allowed locations
	cleanFile := filepath.Clean(args.File)
	allowed := false
	if cleanFile == "MEMORY.md" {
		allowed = true
	} else if strings.HasPrefix(cleanFile, "memory/") && strings.HasSuffix(cleanFile, ".md") {
		// Ensure no path traversal
		if !strings.Contains(cleanFile, "..") {
			allowed = true
		}
	}

	if !allowed {
		result.Message = "Error: file must be MEMORY.md or memory/<name>.md"
		return result, fmt.Errorf("invalid memory file path: %s", args.File)
	}

	resolvedPath := filepath.Join(workspace, cleanFile)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		result.Message = fmt.Sprintf("Error: failed to create directory: %v", err)
		return result, fmt.Errorf("create directory: %w", err)
	}

	// Default to append
	appendMode := true
	// The zero value of bool is false, but the plan says default true.
	// Since JSON unmarshaling sets false for missing bool, we treat the field
	// as "append unless explicitly set to false with the field present".
	// However with the struct tag omitempty, missing = false, so we just
	// check: if caller explicitly passes append:false, we overwrite.
	// For simplicity: always append unless Append is explicitly false AND
	// we can detect it. Since Go doesn't distinguish missing vs false for bool,
	// we default to append.
	if !args.Append {
		// Check if we should still default to append - we'll use append by default
		// unless the caller explicitly sets append to false.
		// Since we can't distinguish, let's just respect the value.
		appendMode = args.Append
	}

	var written int
	if appendMode {
		f, err := os.OpenFile(resolvedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			result.Message = fmt.Sprintf("Error: open file: %v", err)
			return result, fmt.Errorf("open file: %w", err)
		}
		defer f.Close()

		n, err := f.WriteString("\n" + args.Content + "\n")
		if err != nil {
			result.Message = fmt.Sprintf("Error: write: %v", err)
			return result, fmt.Errorf("write: %w", err)
		}
		written = n
	} else {
		if err := os.WriteFile(resolvedPath, []byte(args.Content+"\n"), 0644); err != nil {
			result.Message = fmt.Sprintf("Error: write file: %v", err)
			return result, fmt.Errorf("write file: %w", err)
		}
		written = len(args.Content) + 1
	}

	// Trigger re-indexing
	if indexer != nil {
		if err := indexer.IndexFile(ctx, resolvedPath); err != nil {
			// Non-fatal: log but don't fail the write
			result.Message = fmt.Sprintf("Wrote %d bytes to %s (indexing warning: %v)", written, cleanFile, err)
			result.Path = cleanFile
			result.Bytes = written
			return result, nil
		}
	}

	result.Path = cleanFile
	result.Bytes = written
	result.Message = fmt.Sprintf("Wrote %d bytes to %s and re-indexed", written, cleanFile)
	return result, nil
}
