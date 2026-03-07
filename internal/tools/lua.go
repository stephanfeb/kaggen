package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	sandboxlua "github.com/yourusername/kaggen/internal/lua"
	"github.com/yourusername/kaggen/internal/vfs"
)

const luaToolDescription = `Execute a Lua 5.1 script in a sandboxed virtual machine. Use this for data transformation, text processing, conditional logic, iteration, and multi-step file manipulation — anything that benefits from procedural code rather than multiple LLM turns.

Each invocation creates a fresh VM — no state persists between calls. Use file I/O to persist data across invocations.

OUTPUT: print() output is captured and returned in the "output" field (capped at 64KB). The script's return value (if any) is returned in the "result" field.

AVAILABLE STANDARD LIBRARIES:
- string: find, format, gsub, gmatch, match, sub, rep, reverse, upper, lower, byte, char, len
- table: insert, remove, sort, concat, maxn
- math: abs, ceil, floor, max, min, sqrt, sin, cos, tan, exp, log, random, randomseed, pi, huge
- coroutine: create, resume, yield, status, wrap
- base globals: print, type, tostring, tonumber, pcall, xpcall, error, assert, select, pairs, ipairs, next, unpack, rawget, rawset, rawequal, setmetatable, getmetatable

FILE I/O (all paths are relative to the workspace, sandboxed by VFS):
- io.open(path, mode) — open a file. Modes: "r" (read, default), "w" (write/create/truncate), "a" (append/create), "r+" (read-write), "w+" (read-write/create/truncate), "a+" (read-write/append/create). Returns file_handle, nil on success or nil, error_string on failure. Parent directories are created automatically for write modes.
- io.lines(path) — returns an iterator over lines in the file.
- io.type(obj) — returns "file", "closed file", or nil.
- file_handle:read(format) — read from file. Formats: "*a" (entire file), "*l" (one line, default), "*n" (a number).
- file_handle:write(data, ...) — write strings to file. Returns the handle for chaining.
- file_handle:lines() — returns a line iterator.
- file_handle:close() — close the file handle.

OS FUNCTIONS (safe subset only):
- os.time() — current time as epoch number.
- os.date([format, time]) — formatted date string.
- os.clock() — CPU time used.
- os.difftime(t2, t1) — difference between two times.
- os.rename(old, new) — rename/move a file within the workspace.
- os.remove(path) — delete a file.

AGENT TOOL BRIDGE:
- agent.call(tool_name, args_table) — invoke another agent tool synchronously. Returns result_table on success, or nil, error_string on failure. The args_table is converted to JSON and passed to the tool; the JSON result is converted back to a Lua table.
  Example: local result, err = agent.call("read", {path = "data.txt"})
  Example: local result, err = agent.call("write", {path = "out.txt", content = "hello"})

NOT AVAILABLE (blocked for security):
- require, dofile, loadfile (no module loading)
- os.execute, os.exit, os.getenv, os.tmpname, os.setlocale (no shell access or env inspection)
- debug library (no introspection)
- package library (no module system)
- io.popen (no process spawning)
- Raw filesystem access outside the workspace
- Recursive run_lua calls (agent.call("run_lua", ...) is blocked)`

// RunLuaArgs defines the input arguments for the run_lua tool.
type RunLuaArgs struct {
	Script  string `json:"script" jsonschema:"required,description=Lua 5.1 script source code to execute in the sandboxed VM."`
	Timeout int    `json:"timeout,omitempty" jsonschema:"description=Execution timeout in seconds. Default 30, maximum 120."`
}

// RunLuaResult defines the output of the run_lua tool.
type RunLuaResult struct {
	Output  string `json:"output"`
	Result  string `json:"result,omitempty"`
	Message string `json:"message"`
}

// NewLuaTool creates a sandboxed Lua execution tool backed by the given VFS.
// The availableTools are exposed to Lua scripts via agent.call().
func NewLuaTool(filesystem vfs.FS, availableTools []tool.Tool) tool.CallableTool {
	// Build the tool caller, excluding run_lua itself to prevent recursion.
	caller := newToolCallerImpl(availableTools)

	return function.NewFunctionTool(
		func(ctx context.Context, args RunLuaArgs) (*RunLuaResult, error) {
			return executeLua(ctx, filesystem, caller, args)
		},
		function.WithName("run_lua"),
		function.WithDescription(luaToolDescription),
	)
}

func executeLua(ctx context.Context, filesystem vfs.FS, caller sandboxlua.ToolCaller, args RunLuaArgs) (*RunLuaResult, error) {
	result := &RunLuaResult{}

	if args.Script == "" {
		result.Message = "Error: script is required"
		return result, fmt.Errorf("script is required")
	}

	timeout := 30
	if args.Timeout > 0 {
		timeout = args.Timeout
	}
	if timeout > 120 {
		timeout = 120
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	luaResult, err := sandboxlua.Execute(ctx, filesystem, caller, args.Script)
	if err != nil {
		result.Output = luaResult.Output
		result.Message = fmt.Sprintf("Error: %v", err)
		return result, err
	}

	result.Output = luaResult.Output
	result.Result = luaResult.ReturnValue
	result.Message = "Script executed successfully"
	return result, nil
}

// toolCallerImpl implements sandboxlua.ToolCaller by dispatching to agent tools.
type toolCallerImpl struct {
	tools map[string]tool.CallableTool
}

func newToolCallerImpl(available []tool.Tool) *toolCallerImpl {
	m := make(map[string]tool.CallableTool)
	for _, t := range available {
		name := t.Declaration().Name
		// Exclude run_lua itself to prevent recursive VM spawning.
		if name == "run_lua" {
			continue
		}
		if ct, ok := t.(tool.CallableTool); ok {
			m[name] = ct
		}
	}
	return &toolCallerImpl{tools: m}
}

func (tc *toolCallerImpl) Call(ctx context.Context, toolName string, argsJSON []byte) ([]byte, error) {
	ct, ok := tc.tools[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}

	result, err := ct.Call(ctx, argsJSON)
	if err != nil {
		return nil, fmt.Errorf("tool %s failed: %w", toolName, err)
	}

	// Marshal the result (which is any) to JSON.
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("tool %s: failed to marshal result: %w", toolName, err)
	}
	return data, nil
}
