package luacfg

import (
	"fmt"
	"regexp"
	"strconv"

	lua "github.com/yuin/gopher-lua"
)

// nameTaken reports whether any already-registered agent or subagent uses name.
// Agents and subagents share one namespace so AgentByName/SubagentByName stay
// unambiguous. Called only during single-threaded config load, so no lock.
func (c *LoadedConfig) nameTaken(name string) bool {
	for _, a := range c.agents {
		if a.Name == name {
			return true
		}
	}
	for _, s := range c.subagents {
		if s.Name == name {
			return true
		}
	}
	return false
}

// uniqueName returns base when it is free, otherwise the lowest base<N>
// (N counting from 2) that no agent or subagent already claims. This makes a
// duplicate agent/subagent declaration auto-suffix ("code", "code2", "code3")
// instead of failing the whole config load — the first declaration keeps its
// bare name, so existing --agent references to it stay valid.
func (c *LoadedConfig) uniqueName(base string) string {
	if !c.nameTaken(base) {
		return base
	}
	for n := 2; ; n++ {
		if cand := base + strconv.Itoa(n); !c.nameTaken(cand) {
			return cand
		}
	}
}

// luaSecret binds shell3.env.secret(key): look up a secret from the .env file
// loaded alongside shell3.lua. Raises a Lua error if the key is not found so a
// missing secret is a hard load-time failure, not a silent empty string.
func (c *LoadedConfig) luaSecret(L *lua.LState) int {
	key := L.CheckString(1)
	v, ok := c.Secrets[key]
	if !ok {
		L.RaiseError("config: secret %q not found in .env", key)
	}
	L.Push(lua.LString(v))
	return 1
}

func registerShell3(c *LoadedConfig) {
	L := c.L
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	L.SetField(tbl, "model", L.NewFunction(c.luaModel))
	L.SetField(tbl, "skill", L.NewFunction(c.luaSkill))
	L.SetField(tbl, "tool", L.NewFunction(c.luaTool))
	L.SetField(tbl, "stub_tools", L.NewFunction(c.luaStubTools))
	L.SetField(tbl, "theme", L.NewFunction(c.luaTheme))
	L.SetField(tbl, "welcome", L.NewFunction(c.luaWelcome))
	L.SetField(tbl, "agent", L.NewFunction(c.luaAgent))
	L.SetField(tbl, "subagent", L.NewFunction(c.luaSubagent))
	env := L.NewTable()
	L.SetField(env, "secret", L.NewFunction(c.luaSecret))
	L.SetField(tbl, "env", env)
	registerRegex(L)
	L.SetField(tbl, "regex", L.NewFunction(c.luaRegex))
	L.SetField(tbl, "on_tool_call", L.NewFunction(c.luaOnToolCall))
	L.SetField(tbl, "on_tool_result", L.NewFunction(c.luaOnToolResult))
	L.SetField(tbl, "subagents", L.NewFunction(c.luaSubagents))
	L.SetField(tbl, "background", L.NewFunction(c.luaBackground))
}

// luaSubagents sets config-global subagent limits: shell3.subagents{ max_depth = N }.
// max_depth must be a positive integer; it caps how deep the subagent nesting
// may go before spawn attempts are blocked. The default (3) is applied at the
// read site (runtime.subagentMaxDepth), not here; 0 is never stored.
func (c *LoadedConfig) luaSubagents(L *lua.LState) int {
	t := L.CheckTable(1)
	if v := t.RawGetString("max_depth"); v != lua.LNil {
		n, ok := v.(lua.LNumber)
		if !ok || int(n) <= 0 {
			L.RaiseError("subagents.max_depth must be a positive integer")
		}
		c.SubagentMaxDepth = int(n)
	}
	return 0
}

// luaBackground sets config-global background job limits: shell3.background{ max_concurrent = N }.
// max_concurrent must be a positive integer; it caps how many background jobs
// may run simultaneously. The default (8) is applied at the read site
// (newJobManager), not here; 0 is never stored.
func (c *LoadedConfig) luaBackground(L *lua.LState) int {
	t := L.CheckTable(1)
	if v := t.RawGetString("max_concurrent"); v != lua.LNil {
		n, ok := v.(lua.LNumber)
		if !ok || int(n) <= 0 {
			L.RaiseError("background.max_concurrent must be a positive integer")
		}
		c.BackgroundMaxConcurrent = int(n)
	}
	return 0
}

var modelKeys = map[string]bool{
	"base_url": true, "api_key": true, "model": true, "context_window": true,
	"compact_at": true, "keep_recent": true, "prune_at": true,
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
		CompactAt:     optInt(opts, "compact_at"),
		KeepRecent:    optInt(opts, "keep_recent"),
		PruneAt:       optInt(opts, "prune_at"),
		Reasoning:     optStr(opts, "reasoning"),
		MaxTokens:     optInt(opts, "max_tokens"),
		Temperature:   optFloatPtr(opts, "temperature"),
	}
	// Clamp keep_recent below compact_at: a tail >= the trigger threshold is
	// nonsensical (compaction would never reduce context). Clamp to half of
	// compact_at so the head always has room to summarise.
	if m.CompactAt > 0 && m.KeepRecent >= m.CompactAt {
		m.KeepRecent = m.CompactAt / 2 // round(compact_at*0.5); tail must stay below trigger
	}
	// prune_at defaults to round(compact_at*0.6) so the cheap-prune tier is on by
	// default, sitting below the compaction trigger. An explicit 0 disables it; an
	// explicit value at or above compact_at is pointless (compaction supersedes
	// it) and also disables it. Lua can't distinguish an unset key from an explicit
	// 0, so presence is checked against the raw table.
	if m.CompactAt > 0 {
		if opts.RawGetString("prune_at") == lua.LNil {
			m.PruneAt = m.CompactAt * 60 / 100
		} else if m.PruneAt >= m.CompactAt {
			m.PruneAt = 0
		}
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
	"media":      true,
	"read":       true,
	"list_files": true,
	"subagents":  true,
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

// luaWelcome sets a custom TUI welcome card: shell3.welcome("...text..."). The
// string replaces the built-in card verbatim — centered but otherwise unstyled —
// so it may embed ANSI escapes (e.g. "\27[38;5;208m...\27[0m") for terminal
// colors. Config-global; a later call replaces an earlier one.
func (c *LoadedConfig) luaWelcome(L *lua.LState) int {
	c.Welcome = L.CheckString(1)
	return 0
}

var themeHex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// luaTheme registers config-global TUI color overrides from a token→hex table:
// shell3.theme{ primary = "#EAB308", fg = "#E5E7EB" }. Overrides sit atop the
// terminal-sensed light/dark palette. It validates only the hex *format* here (a
// malformed value is a typo — warn and skip, don't fail the load); which token
// names are meaningful is the front-end's business — the TUI owns the palette
// vocabulary and warns about unknown tokens when it applies them — so an
// unrecognized name passes through untouched. Non-string keys/values are a hard
// error (a type mistake, not a typo).
func (c *LoadedConfig) luaTheme(L *lua.LState) int {
	t := L.CheckTable(1)
	t.ForEach(func(k, v lua.LValue) {
		name, ok := k.(lua.LString)
		if !ok {
			L.RaiseError("theme: keys must be strings (color tokens), got %s", k.Type().String())
		}
		hex, ok := v.(lua.LString)
		if !ok {
			L.RaiseError("theme[%q]: value must be a hex string like \"#RRGGBB\", got %s", string(name), v.Type().String())
		}
		if !themeHex.MatchString(string(hex)) {
			c.warnings = append(c.warnings, fmt.Sprintf("theme[%q]: %q is not a #RRGGBB hex color, ignored", string(name), string(hex)))
			return
		}
		c.Theme[string(name)] = string(hex)
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
		Read:             optBool(tt, "read"),
		ListFiles:        optBool(tt, "list_files"),
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
	// Duplicate names auto-suffix rather than failing the load (the first
	// declaration keeps its bare name; later collisions become name2, name3…).
	a.Name = c.uniqueName(a.Name)
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
	// Duplicate names (against any agent or subagent) auto-suffix rather than
	// failing the load. The returned handle below carries the deduped name, so
	// tools.subagents={handle} references still resolve.
	s.Name = c.uniqueName(s.Name)
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
