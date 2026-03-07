package lua

import (
	"context"
	"fmt"
	"strings"

	lua "github.com/yuin/gopher-lua"

	"github.com/yourusername/kaggen/internal/vfs"
)

// newSandboxedState creates a Lua VM with restricted standard libraries,
// VFS-backed I/O, and an optional tool bridge.
func newSandboxedState(ctx context.Context, filesystem vfs.FS, caller ToolCaller, output *cappedBuffer) *lua.LState {
	L := lua.NewState(lua.Options{
		SkipOpenLibs: true,
	})

	// Selectively open safe standard libraries.
	for _, lib := range safeLibs {
		lib.fn(L)
	}

	// Remove dangerous globals that base lib provides.
	for _, name := range blockedGlobals {
		L.SetGlobal(name, lua.LNil)
	}

	// Replace print() with output-capturing version.
	L.SetGlobal("print", L.NewFunction(capturedPrint(output)))

	// Install VFS-backed io and os modules.
	installVFSIO(L, filesystem)
	installSafeOS(L, filesystem)

	// Install agent tool bridge if a caller is provided.
	if caller != nil {
		installToolBridge(L, caller)
	}

	// Set context for timeout enforcement.
	L.SetContext(ctx)

	return L
}

// safeLib pairs a library name with its open function.
type safeLib struct {
	name string
	fn   func(*lua.LState)
}

// safeLibs lists the standard libraries that are safe to expose.
var safeLibs = []safeLib{
	{"base", func(L *lua.LState) { L.Push(L.NewFunction(lua.OpenBase)); L.Call(0, 0) }},
	{"table", func(L *lua.LState) { L.Push(L.NewFunction(lua.OpenTable)); L.Call(0, 0) }},
	{"string", func(L *lua.LState) { L.Push(L.NewFunction(lua.OpenString)); L.Call(0, 0) }},
	{"math", func(L *lua.LState) { L.Push(L.NewFunction(lua.OpenMath)); L.Call(0, 0) }},
	{"coroutine", func(L *lua.LState) { L.Push(L.NewFunction(lua.OpenCoroutine)); L.Call(0, 0) }},
}

// blockedGlobals are base-library globals that must be removed for sandboxing.
var blockedGlobals = []string{
	"dofile",
	"loadfile",
	"require",
}

// capturedPrint returns a Lua function that writes to the capped buffer
// instead of stdout, mimicking Lua's built-in print() behavior.
func capturedPrint(output *cappedBuffer) lua.LGFunction {
	return func(L *lua.LState) int {
		n := L.GetTop()
		var parts []string
		for i := 1; i <= n; i++ {
			parts = append(parts, fmt.Sprintf("%v", L.Get(i)))
		}
		line := strings.Join(parts, "\t") + "\n"
		output.Write([]byte(line))
		return 0
	}
}
