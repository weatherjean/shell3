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
	registerGuards(c, tbl)
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
	c.Agent.Name = optStr(opts, "name")
	c.Agent.ModelName = optStr(opts, "model")
	c.Agent.Prompt = optStr(opts, "prompt")
	if sk, ok := opts.RawGetString("skills").(*lua.LTable); ok {
		c.Agent.Skills = handleNames(sk, "__skill")
	}
	if tt, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		c.Agent.Gates = ToolGates{
			Bash:             optBool(tt, "bash"),
			BashBg:           optBool(tt, "bash_bg"),
			ShellInteractive: optBool(tt, "shell_interactive"),
			Edit:             optBool(tt, "edit"),
			Memory:           optBool(tt, "memory"),
			History:          optBool(tt, "history"),
			Docs:             optBool(tt, "docs"),
		}
		if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
			c.Agent.CustomTools = handleNames(cu, "__tool")
		}
	}
	if g, ok := opts.RawGetString("on_tool_call").(*lua.LTable); ok {
		g.ForEach(func(_, v lua.LValue) {
			switch x := v.(type) {
			case *lua.LFunction:
				c.Agent.Guard = append(c.Agent.Guard, GuardEntry{fn: x})
			case *lua.LTable:
				if b, ok := x.RawGetString("__guard").(lua.LString); ok {
					c.Agent.Guard = append(c.Agent.Guard, GuardEntry{
						Builtin: string(b), prompt: optBool(x, "prompt"),
					})
				}
			}
		})
	}
	return 0
}
