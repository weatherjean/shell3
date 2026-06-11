package luacfg

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

func registerShell3(c *LoadedConfig) {
	L := c.L
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	L.SetField(tbl, "model", L.NewFunction(c.luaModel))
	L.SetField(tbl, "telegram", L.NewFunction(c.luaTelegram))
	L.SetField(tbl, "cron", L.NewFunction(c.luaCron))
	L.SetField(tbl, "skill", L.NewFunction(c.luaSkill))
	L.SetField(tbl, "tool", L.NewFunction(c.luaTool))
	L.SetField(tbl, "stub_tools", L.NewFunction(c.luaStubTools))
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

var telegramKeys = map[string]bool{"token": true, "chat_id": true, "agent": true, "workdir": true, "dashboard": true}
var telegramDashboardKeys = map[string]bool{"enabled": true, "addr": true, "url": true}

func (c *LoadedConfig) luaTelegram(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "telegram", telegramKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	tg := TelegramConfig{
		Token:   optStr(opts, "token"),
		ChatID:  optStr(opts, "chat_id"),
		Agent:   optStr(opts, "agent"),
		WorkDir: optStr(opts, "workdir"),
	}
	if d, ok := opts.RawGetString("dashboard").(*lua.LTable); ok {
		if err := checkKeys(d, "telegram.dashboard", telegramDashboardKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		tg.Dashboard = DashboardConfig{
			Enabled: optBool(d, "enabled"),
			Addr:    optStr(d, "addr"),
			URL:     optStr(d, "url"),
		}
	}
	c.telegram = tg
	return 0
}

var cronKeys = map[string]bool{"jobs": true}
var cronJobKeys = map[string]bool{
	"name": true, "schedule": true, "agent": true, "prompt": true, "workdir": true, "notify": true,
}

func (c *LoadedConfig) luaCron(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "cron", cronKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	jobsT, ok := opts.RawGetString("jobs").(*lua.LTable)
	if !ok {
		return 0 // no jobs
	}
	n := 0
	jobsT.ForEach(func(_, v lua.LValue) {
		jt, ok := v.(*lua.LTable)
		if !ok {
			return
		}
		n++
		if err := checkKeys(jt, "cron.job", cronJobKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		job := CronJob{
			Name:     optStr(jt, "name"),
			Schedule: optStr(jt, "schedule"),
			Agent:    optStr(jt, "agent"),
			Prompt:   optStr(jt, "prompt"),
			WorkDir:  optStr(jt, "workdir"),
			Notify:   true, // default
		}
		if v := jt.RawGetString("notify"); v != lua.LNil {
			job.Notify = lua.LVAsBool(v)
		}
		if job.Name == "" {
			job.Name = fmt.Sprintf("job-%d", n)
		}
		c.cron = append(c.cron, job)
	})
	return 0
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
	"custom": true, "skill": true,
	"prune": true, "compact": true, "media": true,
	"subagents": true,
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

// luaStubTools registers name-only stub tools from a string→string table:
// tool-name → redirect message. Models trained on other harnesses reflexively
// call tools like read_file/grep/write_file; a stub returns the message verbatim
// (a self-correcting nudge toward bash/edit_file) instead of erroring. Stubs are
// config-GLOBAL — they apply to every agent. Multiple calls merge into the same
// map; a later key overwrites an earlier one. Values must be strings.
func (c *LoadedConfig) luaStubTools(L *lua.LState) int {
	t := L.CheckTable(1)
	t.ForEach(func(k, v lua.LValue) {
		name, ok := k.(lua.LString)
		if !ok {
			L.RaiseError("stub_tools: keys must be strings (tool names), got %s", k.Type().String())
		}
		msg, ok := v.(lua.LString)
		if !ok {
			L.RaiseError("stub_tools[%q]: value must be a string (redirect message), got %s", string(name), v.Type().String())
		}
		c.StubTools[string(name)] = string(msg)
	})
	return 0
}

// subagentHandleNames is like handleNames for the "__subagent" sentinel, but
// fails fast (via L.RaiseError) on any array element that is not a valid
// subagent handle instead of silently dropping it. A bare string, number, or a
// table missing the sentinel all raise. agentName is used in the error message.
func subagentHandleNames(L *lua.LState, list *lua.LTable, agentName string) []string {
	var out []string
	for i := 1; i <= list.Len(); i++ {
		v := list.RawGetInt(i)
		ht, ok := v.(*lua.LTable)
		if !ok {
			L.RaiseError("agent %q: tools.subagents[%d] is not a subagent handle", agentName, i)
		}
		s, ok := ht.RawGetString("__subagent").(lua.LString)
		if !ok {
			L.RaiseError("agent %q: tools.subagents[%d] is not a subagent handle", agentName, i)
		}
		out = append(out, string(s))
	}
	return out
}

// parseGates reads the boolean tool gates from a tools table.
func parseGates(tt *lua.LTable) ToolGates {
	return ToolGates{
		Bash:             optBool(tt, "bash"),
		BashBg:           optBool(tt, "bash_bg"),
		ShellInteractive: optBool(tt, "shell_interactive"),
		Edit:             optBool(tt, "edit"),
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
		if tt.RawGetString("skill") == lua.LFalse {
			a.SkillsDisabled = true
		}
		if sgv := tt.RawGetString("subagents"); sgv != lua.LNil {
			sg, ok := sgv.(*lua.LTable)
			if !ok {
				L.RaiseError("agent %q: tools.subagents must be a list of subagent handles, got %s", a.Name, sgv.Type().String())
			}
			a.Subagents = subagentHandleNames(L, sg, a.Name)
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
		if _, isTable := tt.RawGetString("subagents").(*lua.LTable); isTable {
			L.RaiseError("subagent %q: a subagent may not declare its own subagents (depth limit 1)", s.Name)
		}
		s.Gates = parseGates(tt)
		if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
			s.CustomTools = handleNames(cu, "__tool")
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
