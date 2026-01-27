package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ReadArgs defines the input arguments for the read tool.
type ReadArgs struct {
	Path     string `json:"path" jsonschema:"required,description=The path to the file to read. Can be absolute or relative to the workspace."`
	MaxLines *int   `json:"max_lines,omitempty" jsonschema:"description=Maximum number of lines to read. Defaults to 1000 if not specified."`
}

// ReadResult defines the output of the read tool.
type ReadResult struct {
	Content string `json:"content"`
	Message string `json:"message"`
}

// newReadTool creates a new read tool using trpc-agent-go's function tool.
func newReadTool(workspace string) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args ReadArgs) (*ReadResult, error) {
			return executeRead(workspace, args)
		},
		function.WithName("read"),
		function.WithDescription("Read the contents of a file. Returns the file content as text. Use this to examine files in the workspace or filesystem."),
	)
}

// executeRead performs the actual file read operation.
func executeRead(workspace string, args ReadArgs) (*ReadResult, error) {
	result := &ReadResult{}

	if args.Path == "" {
		result.Message = "Error: path is required"
		return result, fmt.Errorf("path is required")
	}

	maxLines := 1000
	if args.MaxLines != nil {
		maxLines = *args.MaxLines
	}

	// Resolve path
	resolvedPath := resolvePath(workspace, args.Path)

	// Read file
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		result.Message = fmt.Sprintf("Error: failed to read file: %v", err)
		return result, fmt.Errorf("failed to read file: %w", err)
	}

	content := string(data)

	// Limit lines if necessary
	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	if totalLines > maxLines {
		lines = lines[:maxLines]
		content = strings.Join(lines, "\n") + fmt.Sprintf("\n... (truncated, showing %d of %d lines)", maxLines, totalLines)
	}

	result.Content = content
	result.Message = fmt.Sprintf("Successfully read %s (%d lines)", args.Path, min(totalLines, maxLines))
	return result, nil
}

// resolvePath converts a path to an absolute path.
func resolvePath(workspace, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	// Expand ~ if present
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return filepath.Join(workspace, path)
}
