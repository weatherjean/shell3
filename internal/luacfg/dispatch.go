package luacfg

import (
	"context"
	"encoding/json"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// Decision is the result of a guard evaluation.
type Decision int

const (
	DecisionAllow  Decision = iota // proceed normally
	DecisionBlock                  // deny this call; model may retry
	DecisionCancel                 // abort the entire turn
	// DecisionAsk suspends the call pending host approval: the front-end's
	// approver (Approve in chat.TurnConfig) decides allow or deny. With no
	// approver registered the engine treats ask as block (fail closed).
	DecisionAsk
)

// OnToolCallFor runs the given agent's guard chain in order; first non-allow
// short-circuits. The agent is passed in (not read from global state) so
// concurrent sessions with different active agents never race.
func (c *LoadedConfig) OnToolCallFor(a Agent, ctx context.Context, tool string, params map[string]any) (Decision, string, error) {
	for _, g := range a.Guard {
		d, reason, err := c.runLuaGuard(ctx, g.fn, tool, params)
		if err != nil {
			// Fail closed: a broken guard must block rather than silently
			// allow whatever it was meant to stop.
			return DecisionBlock, "guard execution error: " + err.Error(), nil
		}
		if d != DecisionAllow {
			return d, reason, nil
		}
	}
	return DecisionAllow, "", nil
}

// lockVM takes the VM mutex and marks it held, returning an unlock func that
// clears the flag before releasing. Callers use it as `defer c.lockVM()()`.
func (c *LoadedConfig) lockVM() func() {
	c.mu.Lock()
	c.vmLockHeld = true
	return func() { c.vmLockHeld = false; c.mu.Unlock() }
}

// runLuaGuard calls a single Lua guard function, locking the VM mutex.
func (c *LoadedConfig) runLuaGuard(ctx context.Context, fn *lua.LFunction, tool string, params map[string]any) (Decision, string, error) {
	defer c.lockVM()()
	c.L.SetContext(ctx)
	call := c.L.NewTable()
	call.RawSetString("tool", lua.LString(tool))
	call.RawSetString("params", goToLua(c.L, anyMap(params)))
	if err := c.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, call); err != nil {
		return DecisionAllow, "", err
	}
	ret := c.L.Get(-1)
	c.L.Pop(1)
	rt, ok := ret.(*lua.LTable)
	if !ok {
		return DecisionAllow, "", nil
	}
	return parseAction(optStr(rt, "action")), optStr(rt, "reason"), nil
}

// toolContext returns the turn context stored on the VM by CallTool/runLuaGuard
// (via L.SetContext), so IO bindings honor turn cancellation. On the top-level
// DoFile path no context is set, so it falls back to context.Background().
func toolContext(L *lua.LState) context.Context {
	if ctx := L.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func parseAction(s string) Decision {
	switch s {
	case "block":
		return DecisionBlock
	case "cancel":
		return DecisionCancel
	case "ask":
		return DecisionAsk
	default:
		return DecisionAllow
	}
}

func anyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
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
