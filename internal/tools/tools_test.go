package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"github.com/yourusername/kaggen/internal/vfs"
)

// callTool is a helper to call a tool with JSON arguments.
func callTool(t *testing.T, tl tool.Tool, args any) (string, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	callable, ok := tl.(tool.CallableTool)
	if !ok {
		t.Fatalf("tool is not callable")
	}

	result, err := callable.Call(context.Background(), argsJSON)
	if err != nil {
		return "", err
	}

	// Convert result to JSON string for comparison
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(resultJSON), nil
}

func TestReadTool(t *testing.T) {
	fs := vfs.NewMemFS()
	testContent := "Hello, World!\nThis is a test file."
	fs.WriteFile("test.txt", []byte(testContent), 0644)

	readTool := newReadTool(fs)

	// Test reading file
	result, err := callTool(t, readTool, ReadArgs{Path: "test.txt"})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}
	if !strings.Contains(result, "Hello, World!") {
		t.Errorf("expected result to contain file content, got %q", result)
	}
}

func TestReadTool_MaxLines(t *testing.T) {
	fs := vfs.NewMemFS()

	// Create file with many lines
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "Line content here")
	}
	content := strings.Join(lines, "\n")
	fs.WriteFile("large.txt", []byte(content), 0644)

	readTool := newReadTool(fs)

	maxLines := 10
	result, err := callTool(t, readTool, ReadArgs{Path: "large.txt", MaxLines: &maxLines})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}

	if !strings.Contains(result, "truncated") {
		t.Error("expected truncation message")
	}
}

func TestReadTool_Directory(t *testing.T) {
	fs := vfs.NewMemFS()
	fs.MkdirAll("mydir", 0755)
	fs.WriteFile("mydir/file.txt", []byte("content"), 0644)

	readTool := newReadTool(fs)

	result, err := callTool(t, readTool, ReadArgs{Path: "mydir"})
	if err != nil {
		t.Fatalf("execute read dir: %v", err)
	}
	if !strings.Contains(result, "file.txt") {
		t.Errorf("expected directory listing, got %q", result)
	}
}

func TestWriteTool(t *testing.T) {
	fs := vfs.NewMemFS()
	writeTool := newWriteTool(fs)

	// Test writing new file
	result, err := callTool(t, writeTool, WriteArgs{Path: "new.txt", Content: "New content"})
	if err != nil {
		t.Fatalf("execute write: %v", err)
	}
	if !strings.Contains(result, "Wrote") {
		t.Errorf("expected write confirmation, got %q", result)
	}

	// Verify file was created
	content, err := fs.ReadFile("new.txt")
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(content) != "New content" {
		t.Errorf("expected %q, got %q", "New content", string(content))
	}

	// Test append mode
	_, err = callTool(t, writeTool, WriteArgs{Path: "new.txt", Content: " appended", Append: true})
	if err != nil {
		t.Fatalf("execute append: %v", err)
	}

	content, _ = fs.ReadFile("new.txt")
	if string(content) != "New content appended" {
		t.Errorf("expected %q, got %q", "New content appended", string(content))
	}
}

func TestWriteTool_CreatesDirectories(t *testing.T) {
	fs := vfs.NewMemFS()
	writeTool := newWriteTool(fs)

	// Write to nested path
	_, err := callTool(t, writeTool, WriteArgs{Path: "nested/dir/file.txt", Content: "Nested content"})
	if err != nil {
		t.Fatalf("execute write nested: %v", err)
	}

	// Verify file exists
	content, err := fs.ReadFile("nested/dir/file.txt")
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(content) != "Nested content" {
		t.Errorf("expected %q, got %q", "Nested content", string(content))
	}
}

func TestWriteTool_EmptyContent(t *testing.T) {
	fs := vfs.NewMemFS()
	writeTool := newWriteTool(fs)

	_, err := callTool(t, writeTool, WriteArgs{Path: "empty.txt", Content: ""})
	if err == nil {
		t.Error("expected error for empty content write")
	}
}

func TestDefaultTools(t *testing.T) {
	fs := vfs.NewMemFS()
	tools := DefaultTools(fs)

	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}

	// Verify tool names
	names := make(map[string]bool)
	for _, tl := range tools {
		names[tl.Declaration().Name] = true
	}

	for _, expected := range []string{"read", "write"} {
		if !names[expected] {
			t.Errorf("expected tool %q to be present", expected)
		}
	}

	// Verify exec tool is NOT present
	if names["exec"] {
		t.Error("exec tool should not be present — agents have no shell access")
	}
}
