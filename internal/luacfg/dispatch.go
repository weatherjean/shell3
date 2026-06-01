package luacfg

import (
	"context"
	"encoding/json"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

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
