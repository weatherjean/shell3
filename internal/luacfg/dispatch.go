package luacfg

import (
	"context"
	"encoding/json"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// lockVM takes the VM mutex and marks it held, returning an unlock func that
// clears the flag before releasing. Callers use it as `defer c.lockVM()()`.
func (c *LoadedConfig) lockVM() func() {
	c.mu.Lock()
	c.vmLockHeld = true
	return func() { c.vmLockHeld = false; c.mu.Unlock() }
}

// toolContext returns the turn context stored on the VM by CallTool/WrapBash
// (via L.SetContext), so IO bindings honor turn cancellation. On the top-level
// DoFile path no context is set, so it falls back to context.Background().
func toolContext(L *lua.LState) context.Context {
	if ctx := L.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// CallTool invokes a custom tool's Lua handler with JSON args, returning the
// handler's string result. Holds the VM mutex; IO bindings release it.
// The built-in "skill" tool is handled before any Lua dispatch.
func (c *LoadedConfig) CallTool(ctx context.Context, name, argsJSON string) (string, error) {
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("tool %q: bad args json: %w", name, err)
		}
	}

	if name == "skill" {
		sn, _ := args["name"].(string)
		for _, s := range c.Skills {
			if s.Name == sn {
				return s.Body, nil
			}
		}
		return "", fmt.Errorf("unknown skill %q", sn)
	}

	// Stub tools (shell3.stub_tools): a hallucinated tool name returns its fixed
	// redirect message verbatim. A stub call must never error — it is a nudge,
	// not a failure. Checked before the custom-tool lookup; a real tool with the
	// same name always wins because agentsetup skips colliding stubs, so this map
	// only ever holds names that are not real tools.
	if msg, ok := c.StubTools[name]; ok {
		return msg, nil
	}

	tool, ok := c.Tools[name]
	if !ok {
		return "", fmt.Errorf("unknown custom tool %q", name)
	}
	defer c.lockVM()()
	c.L.SetContext(ctx)
	argsT := goToLua(c.L, args)
	if err := c.L.CallByParam(lua.P{Fn: tool.handler, NRet: 1, Protect: true}, argsT); err != nil {
		return "", fmt.Errorf("tool %q handler: %w", name, err)
	}
	ret := c.L.Get(-1)
	c.L.Pop(1)
	if _, ok := ret.(lua.LString); !ok {
		return "", fmt.Errorf("tool %q: handler must return a string, got %s", name, ret.Type().String())
	}
	return ret.String(), nil
}
