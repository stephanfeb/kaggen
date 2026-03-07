package lua

import (
	"encoding/json"
	"fmt"
	"sort"

	lua "github.com/yuin/gopher-lua"
)

// installToolBridge registers the "agent" module with a call() function
// that lets Lua scripts invoke agent tools by name.
func installToolBridge(L *lua.LState, caller ToolCaller) {
	mod := L.NewTable()
	L.SetField(mod, "call", L.NewFunction(agentCall(L, caller)))
	L.SetGlobal("agent", mod)
}

// agentCall returns a Lua function implementing agent.call(tool_name, args_table).
// Returns (result_table, nil) on success or (nil, error_string) on failure.
func agentCall(_ *lua.LState, caller ToolCaller) lua.LGFunction {
	return func(L *lua.LState) int {
		toolName := L.CheckString(1)
		argsTable := L.OptTable(2, nil)

		var argsJSON []byte
		var err error
		if argsTable != nil {
			argsJSON, err = luaTableToJSON(argsTable)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(fmt.Sprintf("failed to encode args: %s", err)))
				return 2
			}
		} else {
			argsJSON = []byte("{}")
		}

		ctx := L.Context()
		resultJSON, err := caller.Call(ctx, toolName, argsJSON)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		resultVal, err := jsonToLuaValue(L, resultJSON)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(fmt.Sprintf("failed to decode result: %s", err)))
			return 2
		}

		L.Push(resultVal)
		return 1
	}
}

// luaTableToJSON converts a Lua table to a JSON byte slice.
// Tables with sequential integer keys starting at 1 become JSON arrays;
// tables with string keys become JSON objects.
func luaTableToJSON(tbl *lua.LTable) ([]byte, error) {
	return json.Marshal(luaValueToInterface(tbl))
}

// luaValueToInterface converts a Lua value to a Go interface{} suitable for
// json.Marshal.
func luaValueToInterface(value lua.LValue) interface{} {
	switch v := value.(type) {
	case *lua.LTable:
		return luaTableToInterface(v)
	case lua.LBool:
		return bool(v)
	case lua.LNumber:
		return float64(v)
	case *lua.LNilType:
		return nil
	default:
		return v.String()
	}
}

// luaTableToInterface converts a Lua table to either a map or a slice.
func luaTableToInterface(tbl *lua.LTable) interface{} {
	// Check if the table is array-like (sequential integer keys from 1).
	maxN := tbl.MaxN()
	if maxN > 0 {
		// Verify there are no string keys.
		hasStringKeys := false
		tbl.ForEach(func(key, _ lua.LValue) {
			if _, ok := key.(lua.LNumber); !ok {
				hasStringKeys = true
			}
		})
		if !hasStringKeys {
			arr := make([]interface{}, 0, maxN)
			for i := 1; i <= maxN; i++ {
				arr = append(arr, luaValueToInterface(tbl.RawGetInt(i)))
			}
			return arr
		}
	}

	// Object (string keys).
	obj := make(map[string]interface{})
	tbl.ForEach(func(key, val lua.LValue) {
		obj[fmt.Sprintf("%v", key)] = luaValueToInterface(val)
	})
	return obj
}

// jsonToLuaValue converts a JSON byte slice to a Lua value.
func jsonToLuaValue(L *lua.LState, data []byte) (lua.LValue, error) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		// If it's not valid JSON, return as string.
		return lua.LString(string(data)), nil
	}
	return goToLuaValue(L, raw), nil
}

// goToLuaValue converts a Go value (from json.Unmarshal) to a Lua value.
func goToLuaValue(L *lua.LState, val interface{}) lua.LValue {
	switch v := val.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(v)
	case float64:
		return lua.LNumber(v)
	case string:
		return lua.LString(v)
	case []interface{}:
		tbl := L.NewTable()
		for _, item := range v {
			tbl.Append(goToLuaValue(L, item))
		}
		return tbl
	case map[string]interface{}:
		tbl := L.NewTable()
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			L.SetField(tbl, k, goToLuaValue(L, v[k]))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", v))
	}
}
