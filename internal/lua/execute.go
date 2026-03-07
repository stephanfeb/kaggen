// Package lua provides a sandboxed Lua 5.1 runtime for agent script execution.
// All filesystem I/O is routed through the VFS interface, and agent tools are
// accessible via the agent.call() bridge function.
package lua

import (
	"context"
	"fmt"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"github.com/yourusername/kaggen/internal/vfs"
)

// ToolCaller allows Lua scripts to invoke agent tools by name.
type ToolCaller interface {
	Call(ctx context.Context, toolName string, argsJSON []byte) ([]byte, error)
}

// Result holds the output of a Lua script execution.
type Result struct {
	Output      string // captured print() output
	ReturnValue string // tostring of the script's return value
}

// Execute runs a Lua script in a sandboxed VM with VFS I/O and tool access.
// The caller argument may be nil if tool bridging is not needed.
func Execute(ctx context.Context, filesystem vfs.FS, caller ToolCaller, script string) (*Result, error) {
	buf := newCappedBuffer(64 * 1024)

	L := newSandboxedState(ctx, filesystem, caller, buf)
	defer L.Close()

	if err := L.DoString(script); err != nil {
		return &Result{Output: buf.String()}, fmt.Errorf("lua execution error: %w", err)
	}

	retVal := ""
	if L.GetTop() > 0 {
		retVal := L.Get(-1)
		if retVal != lua.LNil {
			return &Result{
				Output:      buf.String(),
				ReturnValue: retVal.String(),
			}, nil
		}
	}

	return &Result{
		Output:      buf.String(),
		ReturnValue: retVal,
	}, nil
}

// cappedBuffer is a thread-safe, size-limited writer for capturing print output.
type cappedBuffer struct {
	mu      sync.Mutex
	buf     []byte
	max     int
	capped  bool
}

func newCappedBuffer(max int) *cappedBuffer {
	return &cappedBuffer{max: max}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capped {
		return len(p), nil // silently discard
	}
	remaining := b.max - len(b.buf)
	if remaining <= 0 {
		b.capped = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
		b.capped = true
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := string(b.buf)
	if b.capped {
		s += "\n... [output truncated at 64KB]"
	}
	return s
}
