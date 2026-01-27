package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
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
	// Create temp directory with a test file
	tmpDir, err := os.MkdirTemp("", "kaggen-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testContent := "Hello, World!\nThis is a test file."
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	readTool := newReadTool(tmpDir)

	// Test reading with absolute path
	result, err := callTool(t, readTool, ReadArgs{Path: testFile})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}
	// Result is JSON with content field containing the file content
	if !strings.Contains(result, "Hello, World!") {
		t.Errorf("expected result to contain file content, got %q", result)
	}

	// Test reading with relative path
	result, err = callTool(t, readTool, ReadArgs{Path: "test.txt"})
	if err != nil {
		t.Fatalf("execute read relative: %v", err)
	}
	if !strings.Contains(result, "Hello, World!") {
		t.Errorf("expected result to contain file content, got %q", result)
	}
}

func TestReadTool_MaxLines(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create file with many lines
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "Line content here")
	}
	content := strings.Join(lines, "\n")

	testFile := filepath.Join(tmpDir, "large.txt")
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	readTool := newReadTool(tmpDir)

	maxLines := 10
	result, err := callTool(t, readTool, ReadArgs{Path: testFile, MaxLines: &maxLines})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}

	if !strings.Contains(result, "truncated") {
		t.Error("expected truncation message")
	}
}

func TestWriteTool(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writeTool := newWriteTool(tmpDir)

	// Test writing new file
	result, err := callTool(t, writeTool, WriteArgs{Path: "new.txt", Content: "New content"})
	if err != nil {
		t.Fatalf("execute write: %v", err)
	}
	if !strings.Contains(result, "Wrote") {
		t.Errorf("expected write confirmation, got %q", result)
	}

	// Verify file was created
	content, err := os.ReadFile(filepath.Join(tmpDir, "new.txt"))
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

	content, _ = os.ReadFile(filepath.Join(tmpDir, "new.txt"))
	if string(content) != "New content appended" {
		t.Errorf("expected %q, got %q", "New content appended", string(content))
	}
}

func TestWriteTool_CreatesDirectories(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writeTool := newWriteTool(tmpDir)

	// Write to nested path
	_, err = callTool(t, writeTool, WriteArgs{Path: "nested/dir/file.txt", Content: "Nested content"})
	if err != nil {
		t.Fatalf("execute write nested: %v", err)
	}

	// Verify file exists
	content, err := os.ReadFile(filepath.Join(tmpDir, "nested/dir/file.txt"))
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(content) != "Nested content" {
		t.Errorf("expected %q, got %q", "Nested content", string(content))
	}
}

func TestExecTool(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execTool := newExecTool(tmpDir)

	// Test simple command
	result, err := callTool(t, execTool, ExecArgs{Command: "echo hello"})
	if err != nil {
		t.Fatalf("execute exec: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected output containing 'hello', got %q", result)
	}

	// Test command with working directory
	// Create a file in tmpDir
	os.WriteFile(filepath.Join(tmpDir, "marker.txt"), []byte("exists"), 0644)

	result, err = callTool(t, execTool, ExecArgs{Command: "ls marker.txt"})
	if err != nil {
		t.Fatalf("execute ls: %v", err)
	}
	if !strings.Contains(result, "marker.txt") {
		t.Errorf("expected output containing 'marker.txt', got %q", result)
	}
}

func TestExecTool_Timeout(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kaggen-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	execTool := newExecTool(tmpDir)

	// Test command that would timeout
	timeoutSeconds := 1
	result, err := callTool(t, execTool, ExecArgs{Command: "sleep 10", TimeoutSeconds: &timeoutSeconds})
	if err != nil {
		t.Fatalf("execute with timeout: %v", err)
	}
	if !strings.Contains(result, "timed out") {
		t.Errorf("expected timeout message, got %q", result)
	}
}

func TestDefaultTools(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "kaggen-test-*")
	defer os.RemoveAll(tmpDir)

	tools := DefaultTools(tmpDir)

	if len(tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(tools))
	}

	// Verify tool names
	names := make(map[string]bool)
	for _, tl := range tools {
		names[tl.Declaration().Name] = true
	}

	for _, expected := range []string{"read", "write", "exec"} {
		if !names[expected] {
			t.Errorf("expected tool %q to be present", expected)
		}
	}
}
