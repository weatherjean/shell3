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
)

// OnToolCall runs the guard chain in order; first non-allow short-circuits.
func (c *LoadedConfig) OnToolCall(ctx context.Context, tool string, params map[string]any) (Decision, string, error) {
	for _, g := range c.Agent.Guard {
		var d Decision
		var reason string
		var err error
		if g.Builtin != "" {
			d, reason = runBuiltinGuard(g, tool, params)
		} else {
			d, reason, err = c.runLuaGuard(ctx, g.fn, tool, params)
		}
		if err != nil {
			return DecisionAllow, "", err
		}
		if d != DecisionAllow {
			return d, reason, nil
		}
	}
	return DecisionAllow, "", nil
}

// runLuaGuard calls a single Lua guard function, locking the VM mutex.
func (c *LoadedConfig) runLuaGuard(ctx context.Context, fn *lua.LFunction, tool string, params map[string]any) (Decision, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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

func parseAction(s string) Decision {
	switch s {
	case "block":
		return DecisionBlock
	case "cancel":
		return DecisionCancel
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
func (c *LoadedConfig) CallTool(ctx context.Context, name, argsJSON string) (string, error) {
	tool, ok := c.Tools[name]
	if !ok {
		return "", fmt.Errorf("unknown custom tool %q", name)
	}
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("tool %q: bad args json: %w", name, err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.L.SetContext(ctx)
	argsT := goToLua(c.L, args)
	if err := c.L.CallByParam(lua.P{Fn: tool.handler, NRet: 1, Protect: true}, argsT); err != nil {
		return "", fmt.Errorf("tool %q handler: %w", name, err)
	}
	ret := c.L.Get(-1)
	c.L.Pop(1)
	return ret.String(), nil
}
