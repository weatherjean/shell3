package luacfg

import (
	"context"
	"os/exec"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// withIOUnlock releases the VM mutex around blocking IO so other tools can
// proceed when luaBash/luaHTTP is called from within a CallTool/WrapBash
// handler that holds c.mu. Lock ownership is tracked explicitly via c.vmLockHeld
// (set by CallTool/WrapBash) rather than inferred from the mutex, because
// sync.Mutex has no ownership and a failed TryLock cannot distinguish
// "held by me" from "held by another goroutine".
//
// INVARIANT: luacfg is single-agent; the VM runs on one goroutine at a time,
// so c.vmLockHeld reliably reflects whether THIS goroutine holds c.mu.
func (c *LoadedConfig) withIOUnlock(f func()) {
	if !c.vmLockHeld {
		// Config top-level (DoFile during Load): no lock is held, so there is
		// nothing to release — just run the IO.
		f()
		return
	}
	// Inside CallTool/WrapBash: we hold c.mu. Release it around the blocking
	// IO so other operations can proceed, then reacquire.
	c.vmLockHeld = false
	c.mu.Unlock()
	f()
	c.mu.Lock()
	c.vmLockHeld = true
}

func (c *LoadedConfig) luaBash(L *lua.LState) int {
	cmd := L.CheckString(1)
	timeout := 10
	if opts, ok := L.Get(2).(*lua.LTable); ok {
		if n := optInt(opts, "timeout"); n > 0 {
			timeout = n
		}
	}
	if timeout > 600 {
		timeout = 600
	}

	var stdout, stderr []byte
	exit := 0

	c.withIOUnlock(func() {
		ctx, cancel := context.WithTimeout(toolContext(L), time.Duration(timeout)*time.Second)
		defer cancel()
		ec := exec.CommandContext(ctx, "bash", "-c", cmd)
		so, se := &captureBuf{}, &captureBuf{}
		ec.Stdout, ec.Stderr = so, se
		runErr := ec.Run()
		stdout, stderr = so.b, se.b
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else if runErr != nil {
			exit = -1
			stderr = append(stderr, []byte(runErr.Error())...)
		}
	})

	res := L.NewTable()
	res.RawSetString("exit", lua.LNumber(exit))
	res.RawSetString("stdout", lua.LString(string(stdout)))
	res.RawSetString("stderr", lua.LString(string(stderr)))
	L.Push(res)
	return 1
}

type captureBuf struct{ b []byte }

func (c *captureBuf) Write(p []byte) (int, error) { c.b = append(c.b, p...); return len(p), nil }

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
