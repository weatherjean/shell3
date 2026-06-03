package luacfg

import (
	"context"
	"os/exec"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// withIOUnlock releases the VM mutex around blocking IO so other tools can
// proceed when luaBash/luaHTTP is called from within a CallTool/runLuaGuard
// handler that holds c.mu. Lock ownership is tracked explicitly via c.vmLockHeld
// (set by CallTool/runLuaGuard) rather than inferred from the mutex, because
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
	// Inside CallTool/runLuaGuard: we hold c.mu. Release it around the blocking
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
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
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
