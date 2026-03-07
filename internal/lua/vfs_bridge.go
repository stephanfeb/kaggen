package lua

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	lua "github.com/yuin/gopher-lua"

	"github.com/yourusername/kaggen/internal/vfs"
)

const luaFileHandleType = "VFSFile"

// installVFSIO registers a VFS-backed "io" module in the Lua state.
func installVFSIO(L *lua.LState, filesystem vfs.FS) {
	// Register the file handle metatable.
	mt := L.NewTypeMetatable(luaFileHandleType)
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), fileHandleMethods))
	L.SetField(mt, "__tostring", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString("file"))
		return 1
	}))
	L.SetField(mt, "__gc", L.NewFunction(fileClose))

	// Build the io module table.
	mod := L.NewTable()
	L.SetField(mod, "open", L.NewFunction(ioOpen(filesystem)))
	L.SetField(mod, "lines", L.NewFunction(ioLines(filesystem)))
	L.SetField(mod, "type", L.NewFunction(ioType))
	L.SetGlobal("io", mod)
}

// fileHandle wraps a VFS file for use as Lua userdata.
type fileHandle struct {
	file   io.ReadWriteCloser
	reader *bufio.Reader
	closed bool
}

func newFileHandleUD(L *lua.LState, fh *fileHandle) *lua.LUserData {
	ud := L.NewUserData()
	ud.Value = fh
	L.SetMetatable(ud, L.GetTypeMetatable(luaFileHandleType))
	return ud
}

func checkFileHandle(L *lua.LState) *fileHandle {
	ud := L.CheckUserData(1)
	fh, ok := ud.Value.(*fileHandle)
	if !ok {
		L.ArgError(1, "file handle expected")
		return nil
	}
	if fh.closed {
		L.ArgError(1, "attempt to use a closed file")
		return nil
	}
	return fh
}

// ioOpen implements io.open(path [, mode]).
func ioOpen(filesystem vfs.FS) lua.LGFunction {
	return func(L *lua.LState) int {
		path := L.CheckString(1)
		mode := L.OptString(2, "r")

		flag, perm := parseLuaMode(mode)

		if flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
			// Write/append mode — ensure parent directory exists.
			dir := dirOf(path)
			if dir != "" && dir != "." {
				_ = filesystem.MkdirAll(dir, 0755)
			}
			wf, err := filesystem.OpenFile(path, flag, perm)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			fh := &fileHandle{file: wf}
			L.Push(newFileHandleUD(L, fh))
			return 1
		}

		// Read mode.
		f, err := filesystem.Open(path)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		fh := &fileHandle{file: readCloserWrapper{f}, reader: bufio.NewReader(f)}
		L.Push(newFileHandleUD(L, fh))
		return 1
	}
}

// readCloserWrapper adapts fs.File (which has no Write) to io.ReadWriteCloser.
type readCloserWrapper struct {
	fs.File
}

func (r readCloserWrapper) Write([]byte) (int, error) {
	return 0, fmt.Errorf("file not opened for writing")
}

// ioLines implements io.lines(path) as a stateful iterator.
func ioLines(filesystem vfs.FS) lua.LGFunction {
	return func(L *lua.LState) int {
		path := L.CheckString(1)
		f, err := filesystem.Open(path)
		if err != nil {
			L.RaiseError("io.lines: %s", err.Error())
			return 0
		}
		scanner := bufio.NewScanner(f)
		L.Push(L.NewFunction(func(L *lua.LState) int {
			if scanner.Scan() {
				L.Push(lua.LString(scanner.Text()))
				return 1
			}
			f.Close()
			if err := scanner.Err(); err != nil {
				L.RaiseError("io.lines: %s", err.Error())
			}
			return 0
		}))
		return 1
	}
}

// ioType implements io.type(obj) — returns "file", "closed file", or nil.
func ioType(L *lua.LState) int {
	v := L.Get(1)
	ud, ok := v.(*lua.LUserData)
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	fh, ok := ud.Value.(*fileHandle)
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	if fh.closed {
		L.Push(lua.LString("closed file"))
	} else {
		L.Push(lua.LString("file"))
	}
	return 1
}

// fileHandleMethods are the methods available on file handle userdata.
var fileHandleMethods = map[string]lua.LGFunction{
	"read":  fileRead,
	"write": fileWrite,
	"close": fileClose,
	"lines": fileLines,
}

// fileRead implements fh:read([format]).
// Supports "*a" (all), "*l" (line, default), and "*n" (number).
func fileRead(L *lua.LState) int {
	fh := checkFileHandle(L)
	if fh == nil {
		return 0
	}

	format := L.OptString(2, "*l")

	if fh.reader == nil {
		// Wrap file in a buffered reader on first read.
		if r, ok := fh.file.(io.Reader); ok {
			fh.reader = bufio.NewReader(r)
		} else {
			L.Push(lua.LNil)
			L.Push(lua.LString("file not readable"))
			return 2
		}
	}

	switch format {
	case "*a", "*all":
		data, err := io.ReadAll(fh.reader)
		if err != nil && err != io.EOF {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LString(string(data)))
		return 1

	case "*l", "*line":
		line, err := fh.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		if line == "" && err == io.EOF {
			L.Push(lua.LNil)
			return 1
		}
		// Strip trailing newline like Lua does.
		line = strings.TrimRight(line, "\n\r")
		L.Push(lua.LString(line))
		return 1

	case "*n":
		var n float64
		_, err := fmt.Fscan(fh.reader, &n)
		if err != nil {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LNumber(n))
		return 1

	default:
		L.ArgError(2, "invalid read format: "+format)
		return 0
	}
}

// fileWrite implements fh:write(data...).
func fileWrite(L *lua.LState) int {
	fh := checkFileHandle(L)
	if fh == nil {
		return 0
	}

	w, ok := fh.file.(io.Writer)
	if !ok {
		L.Push(lua.LNil)
		L.Push(lua.LString("file not opened for writing"))
		return 2
	}

	for i := 2; i <= L.GetTop(); i++ {
		s := L.Get(i).String()
		if _, err := w.Write([]byte(s)); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
	}

	// Return the file handle for chaining, like Lua does.
	L.Push(L.Get(1))
	return 1
}

// fileClose implements fh:close().
func fileClose(L *lua.LState) int {
	ud := L.CheckUserData(1)
	fh, ok := ud.Value.(*fileHandle)
	if !ok || fh.closed {
		return 0
	}
	fh.closed = true
	if err := fh.file.Close(); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LTrue)
	return 1
}

// fileLines implements fh:lines() as a stateful iterator.
func fileLines(L *lua.LState) int {
	fh := checkFileHandle(L)
	if fh == nil {
		return 0
	}
	if fh.reader == nil {
		if r, ok := fh.file.(io.Reader); ok {
			fh.reader = bufio.NewReader(r)
		} else {
			L.RaiseError("file not readable")
			return 0
		}
	}

	L.Push(L.NewFunction(func(L *lua.LState) int {
		line, err := fh.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			L.RaiseError("lines: %s", err.Error())
			return 0
		}
		if line == "" && err == io.EOF {
			return 0
		}
		line = strings.TrimRight(line, "\n\r")
		L.Push(lua.LString(line))
		return 1
	}))
	return 1
}

// installSafeOS registers a restricted "os" module backed by VFS.
func installSafeOS(L *lua.LState, filesystem vfs.FS) {
	mod := L.NewTable()

	// Safe time functions (use real implementations).
	L.SetField(mod, "clock", L.NewFunction(lua.OpenOs))
	// Re-implement the safe subset manually.
	L.Push(L.NewFunction(lua.OpenOs))
	L.Call(0, 0)
	realOS := L.GetGlobal("os")
	if tbl, ok := realOS.(*lua.LTable); ok {
		L.SetField(mod, "clock", tbl.RawGetString("clock"))
		L.SetField(mod, "date", tbl.RawGetString("date"))
		L.SetField(mod, "time", tbl.RawGetString("time"))
		L.SetField(mod, "difftime", tbl.RawGetString("difftime"))
	}

	// VFS-backed file operations.
	L.SetField(mod, "rename", L.NewFunction(func(L *lua.LState) int {
		oldpath := L.CheckString(1)
		newpath := L.CheckString(2)
		if err := filesystem.Rename(oldpath, newpath); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	L.SetField(mod, "remove", L.NewFunction(func(L *lua.LState) int {
		path := L.CheckString(1)
		if err := filesystem.Remove(path); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	L.SetGlobal("os", mod)
}

// parseLuaMode converts a Lua-style mode string to os flags and permissions.
func parseLuaMode(mode string) (int, fs.FileMode) {
	switch mode {
	case "w":
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC, 0644
	case "a":
		return os.O_WRONLY | os.O_CREATE | os.O_APPEND, 0644
	case "r+":
		return os.O_RDWR, 0644
	case "w+":
		return os.O_RDWR | os.O_CREATE | os.O_TRUNC, 0644
	case "a+":
		return os.O_RDWR | os.O_CREATE | os.O_APPEND, 0644
	default: // "r" or anything else
		return os.O_RDONLY, 0
	}
}

// dirOf returns the directory portion of a path, handling simple cases.
func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return ""
}
