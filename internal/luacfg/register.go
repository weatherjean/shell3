package luacfg

import (
	"fmt"
	"regexp"

	lua "github.com/yuin/gopher-lua"
)

func registerShell3(c *LoadedConfig) {
	L := c.L
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	L.SetField(tbl, "model", L.NewFunction(c.luaModel))
	L.SetField(tbl, "telegram", L.NewFunction(c.luaTelegram))
	L.SetField(tbl, "skill", L.NewFunction(c.luaSkill))
	L.SetField(tbl, "tool", L.NewFunction(c.luaTool))
	L.SetField(tbl, "stub_tools", L.NewFunction(c.luaStubTools))
	L.SetField(tbl, "agent", L.NewFunction(c.luaAgent))
	L.SetField(tbl, "subagent", L.NewFunction(c.luaSubagent))
	env := L.NewTable()
	L.SetField(env, "secret", L.NewFunction(c.luaSecret))
	L.SetField(tbl, "env", env)
	L.SetField(tbl, "wrap_bash", L.NewFunction(c.luaWrapBash))
	L.SetField(tbl, "bash_safety", L.NewFunction(c.luaBashSafety))
}

var telegramKeys = map[string]bool{"token": true, "chat_id": true, "agent": true, "workdir": true, "dashboard": true, "cron": true}
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
	// cron jobs are nested under telegram{}: the scheduler is consumed only by
	// the Telegram host, so the config shape reflects that coupling. `cron` is a
	// flat list of job tables (no `jobs=` wrapper).
	if jobsT, ok := opts.RawGetString("cron").(*lua.LTable); ok {
		c.parseCronJobs(L, jobsT)
	}
	c.telegram = tg
	return 0
}

var cronJobKeys = map[string]bool{
	"name": true, "schedule": true, "agent": true, "prompt": true, "workdir": true, "notify": true,
}

// parseCronJobs reads a flat list of cron job tables and appends them to c.cron.
func (c *LoadedConfig) parseCronJobs(L *lua.LState, jobsT *lua.LTable) {
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
}

var modelKeys = map[string]bool{
	"base_url": true, "api_key": true, "model": true, "context_window": true,
	"compact_at": true,
	"reasoning":  true, "max_tokens": true, "temperature": true, "extra": true,
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
		CompactAt:     optInt(opts, "compact_at"),
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

var skillKeys = map[string]bool{"name": true, "description": true, "path": true}

func (c *LoadedConfig) luaSkill(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "skill", skillKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	s := Skill{Name: optStr(opts, "name"), Description: optStr(opts, "description"), Path: optStr(opts, "path")}
	if s.Name == "" || s.Description == "" || s.Path == "" {
		L.RaiseError("skill: name, description, and path are all required")
	}
	c.Skills = append(c.Skills, s)
	// Return a handle table carrying a sentinel + the name.
	h := L.NewTable()
	h.RawSetString("__skill", lua.LString(s.Name))
	L.Push(h)
	return 1
}

var toolKeys = map[string]bool{
	"name": true, "description": true, "parameters": true,
	"command": true, "secrets": true, "background": true, "timeout": true,
}

var agentKeys = map[string]bool{
	"name": true, "model": true, "prompt": true, "prompt_cmd": true, "tools": true, "skills": true,
	"environment": true, "delegation": true,
}

var toolGateKeys = map[string]bool{
	"bash": true, "bash_bg": true, "shell_interactive": true, "edit": true,
	"custom": true, "skill": true,
	"media":     true,
	"subagents": true,
}

func (c *LoadedConfig) luaTool(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "tool", toolKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	ct := CustomTool{
		Name:        optStr(opts, "name"),
		Description: optStr(opts, "description"),
		Command:     optStr(opts, "command"),
		Background:  optBool(opts, "background"),
		Timeout:     optInt(opts, "timeout"),
	}
	if ct.Name == "" || ct.Description == "" || ct.Command == "" {
		L.RaiseError("tool: name, description, and command are all required")
	}
	if sec, ok := opts.RawGetString("secrets").(*lua.LTable); ok {
		ct.Secrets = stringList(sec)
	}
	if p, ok := opts.RawGetString("parameters").(*lua.LTable); ok {
		ct.Parameters = tableToMap(p)
		if err := validateParamNames(ct.Name, ct.Parameters); err != nil {
			L.RaiseError("%s", err.Error())
		}
	}
	c.Tools[ct.Name] = ct
	h := L.NewTable()
	h.RawSetString("__tool", lua.LString(ct.Name))
	L.Push(h)
	return 1
}

// paramNameRe constrains custom-tool parameter names to lowercase identifiers.
// Params are exported into the command env by their bare name; secrets and
// standard env vars are uppercase by convention, so a lowercase rule guarantees
// a param can never clobber PATH/HOME/IFS or collide with a declared secret.
var paramNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// validateParamNames rejects any declared parameter property whose name is not
// a lowercase identifier. params is the tool's JSON-schema map.
func validateParamNames(tool string, params map[string]any) error {
	props, _ := params["properties"].(map[string]any)
	for name := range props {
		if !paramNameRe.MatchString(name) {
			return fmt.Errorf("tool %q: parameter %q must be a lowercase identifier ([a-z][a-z0-9_]*)", tool, name)
		}
	}
	return nil
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
		PromptCmd: optStr(opts, "prompt_cmd"),
	}
	if a.Name == "" {
		L.RaiseError("agent: name is required")
	}
	// An empty prompt stays valid for agents (a system prompt is assembled
	// from other sources); only setting BOTH sources is an error.
	if a.Prompt != "" && a.PromptCmd != "" {
		L.RaiseError("agent %q: set exactly one of prompt or prompt_cmd", a.Name)
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
	a.Environment = optBool(opts, "environment")
	a.Delegation = optBool(opts, "delegation")
	c.agents = append(c.agents, a)
	return 0
}

var subagentKeys = map[string]bool{
	"name": true, "description": true, "model": true, "prompt": true, "prompt_cmd": true,
	"tools": true, "skills": true,
	"environment": true, "delegation": true,
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
		PromptCmd:   optStr(opts, "prompt_cmd"),
	}
	if s.Name == "" || s.Description == "" {
		L.RaiseError("subagent: name and description are required")
	}
	// An empty prompt stays valid for subagents; only setting BOTH is an error.
	if s.Prompt != "" && s.PromptCmd != "" {
		L.RaiseError("subagent %q: set exactly one of prompt or prompt_cmd", s.Name)
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
	s.Environment = optBool(opts, "environment")
	s.Delegation = optBool(opts, "delegation")
	c.subagents = append(c.subagents, s)
	h := L.NewTable()
	h.RawSetString("__subagent", lua.LString(s.Name))
	L.Push(h)
	return 1
}
