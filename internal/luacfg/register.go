package luacfg

import lua "github.com/yuin/gopher-lua"

func registerShell3(c *LoadedConfig) {
	L := c.L
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	L.SetField(tbl, "model", L.NewFunction(c.luaModel))
	L.SetField(tbl, "skill", L.NewFunction(c.luaSkill))
	L.SetField(tbl, "tool", L.NewFunction(c.luaTool))
	L.SetField(tbl, "mcp", L.NewFunction(c.luaMCP))
	L.SetField(tbl, "agent", L.NewFunction(c.luaAgent))
	L.SetField(tbl, "subagent", L.NewFunction(c.luaSubagent))
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
	"run_proxy": true,
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
		RunProxy:      optStr(opts, "run_proxy"),
		ContextWindow: optInt(opts, "context_window"),
		Reasoning:     optStr(opts, "reasoning"),
		MaxTokens:     optInt(opts, "max_tokens"),
		Temperature:   optFloatPtr(opts, "temperature"),
	}
	// api_key is optional: a local proxy (e.g. run_proxy) can handle auth, so an
	// empty key is valid. base_url and model are always required.
	if m.BaseURL == "" || m.ModelID == "" {
		L.RaiseError("model %q: base_url and model are required", name)
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
	"prune": true, "compact": true, "mcp": true, "media": true,
	"subagents": true,
}

var mcpKeys = map[string]bool{
	"name": true, "command": true, "args": true, "env": true, "tools": true,
}

func (c *LoadedConfig) luaMCP(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "mcp", mcpKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	srv := MCPServer{Name: optStr(opts, "name"), Command: optStr(opts, "command")}
	if srv.Name == "" || srv.Command == "" {
		L.RaiseError("mcp: name and command are required")
	}
	if a, ok := opts.RawGetString("args").(*lua.LTable); ok {
		srv.Args = stringList(a)
	}
	if e, ok := opts.RawGetString("env").(*lua.LTable); ok {
		srv.Env = map[string]string{}
		e.ForEach(func(k, v lua.LValue) {
			if ks, ok := k.(lua.LString); ok {
				srv.Env[string(ks)] = v.String()
			}
		})
	}
	if tools, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		srv.Tools = stringList(tools)
	}
	c.MCPServers[srv.Name] = srv

	h := L.NewTable()
	h.RawSetString("__mcp", lua.LString(srv.Name))
	L.Push(h)
	return 1
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

// parseGates reads the boolean tool gates from a tools table.
func parseGates(tt *lua.LTable) ToolGates {
	return ToolGates{
		Bash:             optBool(tt, "bash"),
		BashBg:           optBool(tt, "bash_bg"),
		ShellInteractive: optBool(tt, "shell_interactive"),
		Edit:             optBool(tt, "edit"),
		History:          optBool(tt, "history"),
		Prune:            optBool(tt, "prune"),
		Compact:          optBool(tt, "compact"),
		Media:            optBool(tt, "media"),
	}
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
	for _, ex := range c.subagents {
		if ex.Name == a.Name {
			L.RaiseError("config: %q is declared as both an agent and a subagent", a.Name)
		}
	}
	if sk, ok := opts.RawGetString("skills").(*lua.LTable); ok {
		a.Skills = handleNames(sk, "__skill")
	}
	if tt, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		if err := checkKeys(tt, "agent.tools", toolGateKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		a.Gates = parseGates(tt)
		if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
			a.CustomTools = handleNames(cu, "__tool")
		}
		if mc, ok := tt.RawGetString("mcp").(*lua.LTable); ok {
			a.MCPServerNames = handleNames(mc, "__mcp")
		}
		if tt.RawGetString("skill") == lua.LFalse {
			a.SkillsDisabled = true
		}
		if sg, ok := tt.RawGetString("subagents").(*lua.LTable); ok {
			a.Subagents = handleNames(sg, "__subagent")
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

var subagentKeys = map[string]bool{
	"name": true, "description": true, "model": true, "prompt": true,
	"tools": true, "skills": true, "on_tool_call": true,
}

func (c *LoadedConfig) luaSubagent(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "subagent", subagentKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	s := Subagent{
		Name:        optStr(opts, "name"),
		Description: optStr(opts, "description"),
		ModelName:   optStr(opts, "model"),
		Prompt:      optStr(opts, "prompt"),
	}
	if s.Name == "" || s.Description == "" {
		L.RaiseError("subagent: name and description are required")
	}
	// Reject collision with already-declared agents.
	for _, ex := range c.agents {
		if ex.Name == s.Name {
			L.RaiseError("config: %q is declared as both an agent and a subagent", s.Name)
		}
	}
	for _, ex := range c.subagents {
		if ex.Name == s.Name {
			L.RaiseError("subagent %q: already declared (subagent names must be unique)", s.Name)
		}
	}
	if sk, ok := opts.RawGetString("skills").(*lua.LTable); ok {
		s.Skills = handleNames(sk, "__skill")
	}
	if tt, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		if err := checkKeys(tt, "subagent.tools", toolGateKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		if tt.RawGetString("subagents") != lua.LNil {
			L.RaiseError("subagent %q: a subagent may not declare its own subagents (depth limit 1)", s.Name)
		}
		s.Gates = parseGates(tt)
		if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
			s.CustomTools = handleNames(cu, "__tool")
		}
		if mc, ok := tt.RawGetString("mcp").(*lua.LTable); ok {
			s.MCPServerNames = handleNames(mc, "__mcp")
		}
		if tt.RawGetString("skill") == lua.LFalse {
			s.SkillsDisabled = true
		}
	}
	if g, ok := opts.RawGetString("on_tool_call").(*lua.LTable); ok {
		g.ForEach(func(_, v lua.LValue) {
			if fn, ok := v.(*lua.LFunction); ok {
				s.Guard = append(s.Guard, GuardEntry{fn: fn})
			}
		})
	}
	c.subagents = append(c.subagents, s)
	h := L.NewTable()
	h.RawSetString("__subagent", lua.LString(s.Name))
	L.Push(h)
	return 1
}
