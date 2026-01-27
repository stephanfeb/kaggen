package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// WriteArgs defines the input arguments for the write tool.
type WriteArgs struct {
	Path    string `json:"path" jsonschema:"required,description=The path to the file to write. Can be absolute or relative to the workspace."`
	Content string `json:"content" jsonschema:"required,description=The content to write to the file."`
	Append  bool   `json:"append,omitempty" jsonschema:"description=If true append to the file instead of overwriting. Defaults to false."`
}

// WriteResult defines the output of the write tool.
type WriteResult struct {
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	Message string `json:"message"`
}

// newWriteTool creates a new write tool using trpc-agent-go's function tool.
func newWriteTool(workspace string) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args WriteArgs) (*WriteResult, error) {
			return executeWrite(workspace, args)
		},
		function.WithName("write"),
		function.WithDescription("Write content to a file. Creates the file if it doesn't exist, or overwrites it if it does. Creates parent directories as needed."),
	)
}

// executeWrite performs the actual file write operation.
func executeWrite(workspace string, args WriteArgs) (*WriteResult, error) {
	result := &WriteResult{
		Path: args.Path,
	}

	if args.Path == "" {
		result.Message = "Error: path is required"
		return result, fmt.Errorf("path is required")
	}

	// Resolve path
	resolvedPath := resolvePath(workspace, args.Path)

	// Ensure parent directory exists
	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		result.Message = fmt.Sprintf("Error: failed to create directory: %v", err)
		return result, fmt.Errorf("failed to create directory: %w", err)
	}

	// Write file
	if args.Append {
		f, err := os.OpenFile(resolvedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			result.Message = fmt.Sprintf("Error: failed to open file for append: %v", err)
			return result, fmt.Errorf("failed to open file for append: %w", err)
		}
		defer f.Close()

		n, err := f.WriteString(args.Content)
		if err != nil {
			result.Message = fmt.Sprintf("Error: failed to append to file: %v", err)
			return result, fmt.Errorf("failed to append to file: %w", err)
		}
		result.Bytes = n
		result.Message = fmt.Sprintf("Appended %d bytes to %s", n, resolvedPath)
		return result, nil
	}

	// Atomic write: write to temp file, then rename
	tmpFile := resolvedPath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(args.Content), 0644); err != nil {
		result.Message = fmt.Sprintf("Error: failed to write temp file: %v", err)
		return result, fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpFile, resolvedPath); err != nil {
		os.Remove(tmpFile) // Clean up temp file on error
		result.Message = fmt.Sprintf("Error: failed to rename temp file: %v", err)
		return result, fmt.Errorf("failed to rename temp file: %w", err)
	}

	result.Bytes = len(args.Content)
	result.Message = fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), resolvedPath)
	return result, nil
}
