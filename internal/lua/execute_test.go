package lua

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yourusername/kaggen/internal/vfs"
)

func TestBasicExecution(t *testing.T) {
	fs := vfs.NewMemFS()
	result, err := Execute(context.Background(), fs, nil, `print("hello world")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "hello world") {
		t.Errorf("expected 'hello world' in output, got %q", result.Output)
	}
}

func TestReturnValue(t *testing.T) {
	fs := vfs.NewMemFS()
	result, err := Execute(context.Background(), fs, nil, `return 42`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ReturnValue != "42" {
		t.Errorf("expected return value '42', got %q", result.ReturnValue)
	}
}

func TestReturnString(t *testing.T) {
	fs := vfs.NewMemFS()
	result, err := Execute(context.Background(), fs, nil, `return "hello"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ReturnValue != "hello" {
		t.Errorf("expected return value 'hello', got %q", result.ReturnValue)
	}
}

func TestMultiplePrints(t *testing.T) {
	fs := vfs.NewMemFS()
	result, err := Execute(context.Background(), fs, nil, `
		print("line 1")
		print("line 2")
		print("line 3")
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(result.Output), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %q", len(lines), result.Output)
	}
}

func TestTimeout(t *testing.T) {
	fs := vfs.NewMemFS()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := Execute(ctx, fs, nil, `while true do end`)
	if err == nil {
		t.Fatal("expected error for infinite loop, got nil")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context error, got: %v", err)
	}
}

func TestBlockedGlobals(t *testing.T) {
	fs := vfs.NewMemFS()

	tests := []struct {
		name   string
		script string
	}{
		{"require", `require("os")`},
		{"dofile", `dofile("test.lua")`},
		{"loadfile", `loadfile("test.lua")`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Execute(context.Background(), fs, nil, tc.script)
			if err == nil {
				t.Errorf("expected error for blocked global %s", tc.name)
			}
		})
	}
}

func TestNoDebugLibrary(t *testing.T) {
	fs := vfs.NewMemFS()
	result, err := Execute(context.Background(), fs, nil, `return type(debug)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ReturnValue != "nil" {
		t.Errorf("expected debug to be nil, got type %q", result.ReturnValue)
	}
}

func TestNoOsExecute(t *testing.T) {
	fs := vfs.NewMemFS()
	result, err := Execute(context.Background(), fs, nil, `return type(os.execute)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ReturnValue != "nil" {
		t.Errorf("expected os.execute to be nil type, got %q", result.ReturnValue)
	}
}

func TestNoOsGetenv(t *testing.T) {
	fs := vfs.NewMemFS()
	result, err := Execute(context.Background(), fs, nil, `return type(os.getenv)`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ReturnValue != "nil" {
		t.Errorf("expected os.getenv to be nil type, got %q", result.ReturnValue)
	}
}

func TestVFSReadWrite(t *testing.T) {
	fs := vfs.NewMemFS()
	// Pre-populate a file.
	if err := fs.WriteFile("input.txt", []byte("hello from VFS"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	result, err := Execute(context.Background(), fs, nil, `
		local f = io.open("input.txt", "r")
		local content = f:read("*a")
		f:close()
		print(content)
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "hello from VFS") {
		t.Errorf("expected 'hello from VFS' in output, got %q", result.Output)
	}
}

func TestVFSWrite(t *testing.T) {
	fs := vfs.NewMemFS()

	_, err := Execute(context.Background(), fs, nil, `
		local f = io.open("output.txt", "w")
		f:write("written by lua")
		f:close()
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := fs.ReadFile("output.txt")
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if string(data) != "written by lua" {
		t.Errorf("expected 'written by lua', got %q", string(data))
	}
}

func TestVFSAppend(t *testing.T) {
	fs := vfs.NewMemFS()
	if err := fs.WriteFile("log.txt", []byte("line1\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := Execute(context.Background(), fs, nil, `
		local f = io.open("log.txt", "a")
		f:write("line2\n")
		f:close()
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := fs.ReadFile("log.txt")
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if string(data) != "line1\nline2\n" {
		t.Errorf("expected 'line1\\nline2\\n', got %q", string(data))
	}
}

func TestIOLines(t *testing.T) {
	fs := vfs.NewMemFS()
	if err := fs.WriteFile("lines.txt", []byte("alpha\nbeta\ngamma"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	result, err := Execute(context.Background(), fs, nil, `
		local lines = {}
		for line in io.lines("lines.txt") do
			table.insert(lines, line)
		end
		return #lines
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ReturnValue != "3" {
		t.Errorf("expected 3 lines, got %q", result.ReturnValue)
	}
}

func TestOsRename(t *testing.T) {
	fs := vfs.NewMemFS()
	if err := fs.WriteFile("old.txt", []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := Execute(context.Background(), fs, nil, `os.rename("old.txt", "new.txt")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := fs.Stat("new.txt"); err != nil {
		t.Errorf("expected new.txt to exist after rename: %v", err)
	}
	if _, err := fs.Stat("old.txt"); err == nil {
		t.Errorf("expected old.txt to be gone after rename")
	}
}

func TestOsRemove(t *testing.T) {
	fs := vfs.NewMemFS()
	if err := fs.WriteFile("delete.txt", []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := Execute(context.Background(), fs, nil, `os.remove("delete.txt")`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := fs.Stat("delete.txt"); err == nil {
		t.Errorf("expected delete.txt to be removed")
	}
}

func TestOsTimeFunctions(t *testing.T) {
	fs := vfs.NewMemFS()

	result, err := Execute(context.Background(), fs, nil, `
		local t = os.time()
		return type(t)
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ReturnValue != "number" {
		t.Errorf("expected os.time() to return number, got %q", result.ReturnValue)
	}
}

func TestToolBridge(t *testing.T) {
	fs := vfs.NewMemFS()
	caller := &mockToolCaller{
		result: []byte(`{"greeting":"hello from tool"}`),
	}

	result, err := Execute(context.Background(), fs, caller, `
		local r = agent.call("test_tool", {name = "world"})
		print(r.greeting)
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "hello from tool") {
		t.Errorf("expected 'hello from tool' in output, got %q", result.Output)
	}
	if caller.lastTool != "test_tool" {
		t.Errorf("expected tool name 'test_tool', got %q", caller.lastTool)
	}
}

func TestToolBridgeError(t *testing.T) {
	fs := vfs.NewMemFS()
	caller := &mockToolCaller{
		err: fmt.Errorf("tool not found"),
	}

	result, err := Execute(context.Background(), fs, caller, `
		local r, err = agent.call("missing_tool", {})
		if err then
			print("ERROR: " .. err)
		end
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "ERROR: tool not found") {
		t.Errorf("expected error message in output, got %q", result.Output)
	}
}

func TestOutputCap(t *testing.T) {
	fs := vfs.NewMemFS()
	// Generate output exceeding 64KB.
	result, err := Execute(context.Background(), fs, nil, `
		for i = 1, 10000 do
			print(string.rep("x", 100))
		end
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "truncated") {
		t.Errorf("expected truncation message in output")
	}
	// Output should be capped around 64KB.
	if len(result.Output) > 70*1024 { // some margin for the truncation message
		t.Errorf("output too large: %d bytes", len(result.Output))
	}
}

func TestStdLibAvailable(t *testing.T) {
	fs := vfs.NewMemFS()

	result, err := Execute(context.Background(), fs, nil, `
		-- Test string library
		local s = string.upper("hello")
		-- Test table library
		local t = {3, 1, 2}
		table.sort(t)
		-- Test math library
		local pi = math.pi
		return s .. ":" .. t[1] .. t[2] .. t[3] .. ":" .. tostring(pi ~= nil)
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ReturnValue != "HELLO:123:true" {
		t.Errorf("expected 'HELLO:123:true', got %q", result.ReturnValue)
	}
}

func TestScriptError(t *testing.T) {
	fs := vfs.NewMemFS()
	_, err := Execute(context.Background(), fs, nil, `error("intentional error")`)
	if err == nil {
		t.Fatal("expected error from script")
	}
	if !strings.Contains(err.Error(), "intentional error") {
		t.Errorf("expected 'intentional error' in error, got: %v", err)
	}
}

func TestSyntaxError(t *testing.T) {
	fs := vfs.NewMemFS()
	_, err := Execute(context.Background(), fs, nil, `this is not valid lua!!!`)
	if err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestIOOpenNonexistent(t *testing.T) {
	fs := vfs.NewMemFS()
	result, err := Execute(context.Background(), fs, nil, `
		local f, err = io.open("nonexistent.txt", "r")
		if not f then
			print("ERROR: " .. err)
		end
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "ERROR:") {
		t.Errorf("expected error in output for nonexistent file, got %q", result.Output)
	}
}

func TestSubdirectoryWrite(t *testing.T) {
	fs := vfs.NewMemFS()
	_, err := Execute(context.Background(), fs, nil, `
		local f = io.open("sub/dir/file.txt", "w")
		f:write("nested")
		f:close()
	`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := fs.ReadFile("sub/dir/file.txt")
	if err != nil {
		t.Fatalf("failed to read nested file: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

// mockToolCaller is a simple mock for testing the tool bridge.
type mockToolCaller struct {
	lastTool string
	lastArgs []byte
	result   []byte
	err      error
}

func (m *mockToolCaller) Call(_ context.Context, toolName string, argsJSON []byte) ([]byte, error) {
	m.lastTool = toolName
	m.lastArgs = argsJSON

	// Verify args are valid JSON.
	var v interface{}
	if err := json.Unmarshal(argsJSON, &v); err != nil {
		return nil, fmt.Errorf("invalid args JSON: %w", err)
	}

	return m.result, m.err
}
