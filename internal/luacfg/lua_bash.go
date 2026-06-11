package luacfg

import (
	"context"

	lua "github.com/yuin/gopher-lua"
)

// luaWrapBash binds shell3.wrap_bash(fn): the single Lua hook the bash/bash_bg
// tools pass their command through before execution. fn(cmd) returns either a
// string (the command to run, possibly rewritten) or nil/false[, reason] to
// block. There is no "ask" — the guard/approval engine was removed, so there is
// no approver to suspend for. A second call replaces the first (last writer
// wins): wrap_bash is a single global hook, not a chain.
func (c *LoadedConfig) luaWrapBash(L *lua.LState) int {
	fn := L.CheckFunction(1)
	c.wrapBash = fn
	return 0
}

// WrapBash runs the registered shell3.wrap_bash hook against cmd, returning the
// (possibly rewritten) command to run, whether it is allowed, and an optional
// block reason. It locks the VM (mirroring CallTool) and honors ctx via
// L.SetContext. When no hook is declared the caller skips WrapBash entirely
// (see chat.ToolConfig.WrapBash being nil); this method is only invoked when a
// hook exists.
//
// FAIL CLOSED. Because wrap_bash is the ONLY bash safety surface (the guard
// engine is gone), a broken hook must block, not silently run the command: any
// Lua error, a non-string/non-false return, or any other unexpected shape
// returns allowed=false with an explanatory reason. A safety hook that fails
// open is worse than no hook at all, so the failure mode is deny.
func (c *LoadedConfig) WrapBash(ctx context.Context, cmd string) (rewritten string, allowed bool, reason string, err error) {
	defer c.lockVM()()
	c.L.SetContext(ctx)
	if e := c.L.CallByParam(lua.P{Fn: c.wrapBash, NRet: 2, Protect: true}, lua.LString(cmd)); e != nil {
		// Fail closed: a hook that errors blocks rather than runs.
		return cmd, false, "wrap_bash error: " + e.Error(), nil
	}
	// Two return values: the verdict (string | nil | false) and an optional reason.
	r2 := c.L.Get(-1)
	r1 := c.L.Get(-2)
	c.L.Pop(2)
	switch v := r1.(type) {
	case lua.LString:
		// A string is the command to run (allow, possibly rewritten).
		return string(v), true, "", nil
	case *lua.LNilType:
		// nil → block; second return (if a string) is the reason.
		return cmd, false, optReason(r2), nil
	case lua.LBool:
		if bool(v) {
			// true is not a valid command to run — a hook must return the
			// command string to allow. Fail closed on this misuse.
			return cmd, false, "wrap_bash error: returned boolean true (return the command string to allow, nil+reason to block)", nil
		}
		// false → block; second return (if a string) is the reason.
		return cmd, false, optReason(r2), nil
	default:
		// Any other type (number, table, function, …) is a broken hook: fail closed.
		return cmd, false, "wrap_bash error: hook returned " + r1.Type().String() + " (must return a command string to allow, or nil/false[+reason] to block)", nil
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
