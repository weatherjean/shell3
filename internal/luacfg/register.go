package luacfg

import lua "github.com/yuin/gopher-lua"

func registerShell3(c *LoadedConfig) {
	L := c.L
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	L.SetField(tbl, "model", L.NewFunction(c.luaModel))
	L.SetField(tbl, "skill", L.NewFunction(c.luaSkill))
	L.SetField(tbl, "tool", L.NewFunction(c.luaTool))
	L.SetField(tbl, "agent", L.NewFunction(c.luaAgent))
	L.SetField(tbl, "urlencode", L.NewFunction(luaURLEncode))
	env := L.NewTable()
	L.SetField(env, "secret", L.NewFunction(c.luaSecret))
	L.SetField(tbl, "env", env)
	L.SetField(tbl, "bash", L.NewFunction(c.luaBash))
	httpT := L.NewTable()
	L.SetField(httpT, "request", L.NewFunction(c.luaHTTPRequest))
	L.SetField(httpT, "get", L.NewFunction(c.luaHTTPGet))
	L.SetField(httpT, "post", L.NewFunction(c.luaHTTPPost))
	L.SetField(tbl, "http", httpT)
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
	if _, exists := c.Model(name); exists {
		L.RaiseError("model %q: already declared (model names must be unique)", name)
	}
	if ex, ok := opts.RawGetString("extra").(*lua.LTable); ok {
		m.Extra = tableToMap(ex)
	}
	c.Models = append(c.Models, m)
	return 0
}

var skillKeys = map[string]bool{"name": true, "description": true, "body": true}

func (c *LoadedConfig) luaSkill(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "skill", skillKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	s := Skill{Name: optStr(opts, "name"), Description: optStr(opts, "description"), Body: optStr(opts, "body")}
	if s.Name == "" || s.Description == "" || s.Body == "" {
		L.RaiseError("skill: name, description, body all required")
	}
	c.Skills = append(c.Skills, s)
	// Return a handle table carrying a sentinel + the name.
	h := L.NewTable()
	h.RawSetString("__skill", lua.LString(s.Name))
	L.Push(h)
	return 1
}

var toolKeys = map[string]bool{"name": true, "description": true, "parameters": true, "handler": true}

var agentKeys = map[string]bool{
	"name": true, "model": true, "prompt": true, "tools": true, "skills": true,
	"on_tool_call": true,
}

var toolGateKeys = map[string]bool{
	"bash": true, "bash_bg": true, "shell_interactive": true, "edit": true,
	"history": true, "custom": true, "skill": true,
	"prune": true, "compact": true,
}

func (c *LoadedConfig) luaTool(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "tool", toolKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	ct := CustomTool{Name: optStr(opts, "name"), Description: optStr(opts, "description")}
	if fn, ok := opts.RawGetString("handler").(*lua.LFunction); ok {
		ct.handler = fn
	} else {
		L.RaiseError("tool %q: handler function required", ct.Name)
	}
	if p, ok := opts.RawGetString("parameters").(*lua.LTable); ok {
		ct.Parameters = tableToMap(p)
	}
	c.Tools[ct.Name] = ct
	h := L.NewTable()
	h.RawSetString("__tool", lua.LString(ct.Name))
	L.Push(h)
	return 1
}

// luaAgent parses name/model/prompt, skills, and the tools struct (gates + custom).
func (c *LoadedConfig) luaAgent(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "agent", agentKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	a := Agent{
		Name:      optStr(opts, "name"),
		ModelName: optStr(opts, "model"),
		Prompt:    optStr(opts, "prompt"),
	}
	if a.Name == "" {
		L.RaiseError("agent: name is required")
	}
	for _, ex := range c.agents {
		if ex.Name == a.Name {
			L.RaiseError("agent %q: already declared (agent names must be unique)", a.Name)
		}
	}
	if sk, ok := opts.RawGetString("skills").(*lua.LTable); ok {
		a.Skills = handleNames(sk, "__skill")
	}
	if tt, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		if err := checkKeys(tt, "agent.tools", toolGateKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		a.Gates = ToolGates{
			Bash:             optBool(tt, "bash"),
			BashBg:           optBool(tt, "bash_bg"),
			ShellInteractive: optBool(tt, "shell_interactive"),
			Edit:             optBool(tt, "edit"),
			History:          optBool(tt, "history"),
			Prune:            optBool(tt, "prune"),
			Compact:          optBool(tt, "compact"),
		}
		if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
			a.CustomTools = handleNames(cu, "__tool")
		}
		if tt.RawGetString("skill") == lua.LFalse {
			a.SkillsDisabled = true
		}
	}
	if g, ok := opts.RawGetString("on_tool_call").(*lua.LTable); ok {
		g.ForEach(func(_, v lua.LValue) {
			if fn, ok := v.(*lua.LFunction); ok {
				a.Guard = append(a.Guard, GuardEntry{fn: fn})
			}
		})
	}
	c.agents = append(c.agents, a)
	return 0
}
