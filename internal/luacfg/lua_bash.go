package luacfg

import (
	"context"
	"os/exec"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// withIOUnlock releases the VM mutex around blocking IO so other tools can
// proceed when luaBash (or luaHTTP) is called from within a CallTool handler
// that already holds c.mu. When called at config top-level (during DoFile,
// before any lock is held), TryLock succeeds — we acquired the lock ourselves,
// so we release it, run f, and return without re-locking (we were never locked
// to begin with).
func (c *LoadedConfig) withIOUnlock(f func()) {
	locked := c.mu.TryLock() // returns false if already held by CallTool
	if locked {
		// Not under CallTool: we just acquired it; release for IO, then return.
		// (The caller—DoFile top-level—did not hold the lock, so no re-lock.)
		c.mu.Unlock()
		f()
		return
	}
	// Under CallTool: caller holds the lock; release around IO, then reacquire.
	c.mu.Unlock()
	f()
	c.mu.Lock()
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
