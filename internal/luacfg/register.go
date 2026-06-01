package luacfg

import lua "github.com/yuin/gopher-lua"

func registerShell3(c *LoadedConfig) {
	L := c.L
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	L.SetField(tbl, "model", L.NewFunction(c.luaModel))
	L.SetField(tbl, "agent", L.NewFunction(c.luaAgent))
	// tool, skill, guards, env, http, bash, urlencode added in later tasks.
}

var modelKeys = map[string]bool{
	"base_url": true, "api_key": true, "model": true, "context_window": true,
	"reasoning": true, "max_tokens": true, "temperature": true, "extra": true,
}

func (c *LoadedConfig) luaModel(L *lua.LState) int {
	name := L.CheckString(1)
	opts := L.CheckTable(2)
	if err := checkKeys(opts, "model", modelKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	m := Model{
		Name:          name,
		BaseURL:       optStr(opts, "base_url"),
		APIKey:        optStr(opts, "api_key"),
		ModelID:       optStr(opts, "model"),
		ContextWindow: optInt(opts, "context_window"),
		Reasoning:     optStr(opts, "reasoning"),
		MaxTokens:     optInt(opts, "max_tokens"),
		Temperature:   optFloatPtr(opts, "temperature"),
	}
	if m.BaseURL == "" || m.APIKey == "" || m.ModelID == "" {
		L.RaiseError("model %q: base_url, api_key, model are required", name)
	}
	if ex, ok := opts.RawGetString("extra").(*lua.LTable); ok {
		m.Extra = tableToMap(ex)
	}
	c.Models = append(c.Models, m)
	return 0
}

// luaAgent is a minimal stub here; fully implemented in later tasks.
func (c *LoadedConfig) luaAgent(L *lua.LState) int {
	opts := L.CheckTable(1)
	c.Agent.Name = optStr(opts, "name")
	c.Agent.ModelName = optStr(opts, "model")
	c.Agent.Prompt = optStr(opts, "prompt")
	return 0
}
