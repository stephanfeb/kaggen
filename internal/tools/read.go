package tools

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/vfs"
)

// ReadArgs defines the input arguments for the read tool.
type ReadArgs struct {
	Path     string `json:"path" jsonschema:"required,description=The path to the file to read relative to the workspace."`
	MaxLines *int   `json:"max_lines,omitempty" jsonschema:"description=Maximum number of lines to read. Defaults to 1000 if not specified."`
}

// ReadResult defines the output of the read tool.
type ReadResult struct {
	Content string `json:"content"`
	Message string `json:"message"`
}

// NewReadTool creates a new read tool backed by the given VFS.
// Exported so the coordinator can use it directly for investigation.
func NewReadTool(filesystem vfs.FS) tool.CallableTool {
	return newReadTool(filesystem)
}

func newReadTool(filesystem vfs.FS) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args ReadArgs) (*ReadResult, error) {
			return executeRead(filesystem, args)
		},
		function.WithName("read"),
		function.WithDescription("Read the contents of a file. Returns the file content as text. Use this to examine files in the workspace."),
	)
}

// executeRead performs the actual file read operation.
func executeRead(filesystem vfs.FS, args ReadArgs) (*ReadResult, error) {
	result := &ReadResult{}

	if args.Path == "" {
		result.Message = "Error: path is required"
		return result, fmt.Errorf("path is required")
	}

	maxLines := 1000
	if args.MaxLines != nil {
		maxLines = *args.MaxLines
	}

	// Check if path exists and get info
	info, err := filesystem.Stat(args.Path)
	if err != nil {
		result.Message = fmt.Sprintf("Error: %v", err)
		return result, fmt.Errorf("stat path: %w", err)
	}

	// Handle directories by listing contents
	if info.IsDir() {
		entries, err := filesystem.ReadDir(args.Path)
		if err != nil {
			result.Message = fmt.Sprintf("Error: failed to read directory: %v", err)
			return result, fmt.Errorf("read directory: %w", err)
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Directory listing for: %s\n\n", args.Path))
		for _, entry := range entries {
			if entry.IsDir() {
				sb.WriteString(fmt.Sprintf("  [dir]  %s/\n", entry.Name()))
			} else {
				entryInfo, _ := entry.Info()
				size := int64(0)
				if entryInfo != nil {
					size = entryInfo.Size()
				}
				sb.WriteString(fmt.Sprintf("  [file] %s (%d bytes)\n", entry.Name(), size))
			}
		}
		result.Content = sb.String()
		result.Message = fmt.Sprintf("Listed %d entries in %s", len(entries), args.Path)
		return result, nil
	}

	// Read file
	data, err := filesystem.ReadFile(args.Path)
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
