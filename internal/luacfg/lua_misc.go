package luacfg

import (
	lua "github.com/yuin/gopher-lua"
)

func (c *LoadedConfig) luaSecret(L *lua.LState) int {
	key := L.CheckString(1)
	v, ok := c.Secrets[key]
	if !ok {
		L.RaiseError("config: secret %q not found in .env", key)
	}
	L.Push(lua.LString(v))
	return 1
}
