package luacfg

import (
	"context"

	lua "github.com/yuin/gopher-lua"
)

// luaWrapBash binds shell3.wrap_bash(fn): the single Lua hook the bash/bash_bg
// tools pass their command through before execution. fn(cmd) returns either a
// string (the command to run, possibly rewritten), a table of strings (the argv
// to exec directly), or nil/false[, reason] to block. There is no "ask": there
// is no approval engine, so no approver to suspend for. A second call replaces
// the first (last writer wins): wrap_bash is a single global hook, not a chain.
func (c *LoadedConfig) luaWrapBash(L *lua.LState) int {
	fn := L.CheckFunction(1)
	c.wrapBash = fn
	return 0
}

// WrapBash runs the registered shell3.wrap_bash hook against cmd and returns the
// argv to exec, whether it is allowed, and an optional block reason. The hook may
// return: a string (run under `bash -c <string>`), a table of strings (the argv
// to exec directly — a runner swap, e.g. {"docker","exec","c","bash","-c",cmd}),
// or nil/false[, reason] to block. It locks the VM and honors ctx via
// L.SetContext. Callers skip WrapBash entirely when no hook is declared.
//
// FAIL CLOSED. wrap_bash is the ONLY bash safety surface, so a broken hook must
// block, not run: any Lua error, a wrong return type, an empty argv table, or a
// table with a non-string element returns allowed=false with a reason. On a
// block the returned argv is nil.
func (c *LoadedConfig) WrapBash(ctx context.Context, cmd string) (argv []string, allowed bool, reason string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.L.SetContext(ctx)
	if e := c.L.CallByParam(lua.P{Fn: c.wrapBash, NRet: 2, Protect: true}, lua.LString(cmd)); e != nil {
		// Fail closed: a hook that errors blocks rather than runs.
		return nil, false, "wrap_bash error: " + e.Error(), nil
	}
	// Two return values: the verdict (string | table | nil | false) and an
	// optional reason.
	r2 := c.L.Get(-1)
	r1 := c.L.Get(-2)
	c.L.Pop(2)
	switch v := r1.(type) {
	case lua.LString:
		// A string runs under bash -c.
		return []string{"bash", "-c", string(v)}, true, "", nil
	case *lua.LTable:
		// A list of strings is the argv to exec directly (the runner swap).
		list, ok := luaStringList(v)
		if !ok {
			return nil, false, "wrap_bash error: argv table must be a non-empty list of strings", nil
		}
		return list, true, "", nil
	case *lua.LNilType:
		// nil → block; second return (if a string) is the reason.
		return nil, false, optReason(r2), nil
	case lua.LBool:
		if bool(v) {
			// true is not a command — a hook must return the command string or an
			// argv table to allow. Fail closed on this misuse.
			return nil, false, "wrap_bash error: returned boolean true (return a command string or an argv table to allow, nil+reason to block)", nil
		}
		// false → block; second return (if a string) is the reason.
		return nil, false, optReason(r2), nil
	default:
		// Any other type (number, function, …) is a broken hook: fail closed.
		return nil, false, "wrap_bash error: hook returned " + r1.Type().String() + " (must return a command string, an argv table, or nil/false[+reason])", nil
	}
}

// optReason returns v as a string when it is a Lua string, else "". Used for the
// optional second return of a wrap_bash block verdict.
func optReason(v lua.LValue) string {
	if s, ok := v.(lua.LString); ok {
		return string(s)
	}
	return ""
}

// luaStringList converts a Lua list table to []string. It returns ok=false when
// the table is empty or any element 1..N is not a string (a hole reads as nil,
// which is not a string, so it is rejected). This is fail-closed input
// validation for the wrap_bash argv shape — a map-style table has Len()==0 and
// is rejected as empty.
func luaStringList(t *lua.LTable) ([]string, bool) {
	n := t.Len()
	if n == 0 {
		return nil, false
	}
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		s, ok := t.RawGetInt(i).(lua.LString)
		if !ok {
			return nil, false
		}
		out = append(out, string(s))
	}
	return out, true
}
