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
	L.SetField(tbl, "telegram", L.NewFunction(c.luaTelegram))
}

// checkPosIntField reads an optional positive-integer field from t. Absent →
// (0, false). Present but not a positive integer → Lua error naming ctx.key.
func checkPosIntField(L *lua.LState, t *lua.LTable, ctx, key string) (int, bool) {
	v := t.RawGetString(key)
	if v == lua.LNil {
		return 0, false
	}
	n, ok := v.(lua.LNumber)
	if !ok || n != lua.LNumber(int(n)) || int(n) <= 0 {
		L.RaiseError("%s.%s must be a positive integer", ctx, key)
	}
	return int(n), true
}

// luaSubagents sets config-global subagent limits: shell3.subagents{ max_depth = N }.
// max_depth caps how deep the subagent nesting may go before spawn attempts are
// blocked. The default (3) is applied at the read site
// (runtime.subagentMaxDepth), not here; 0 is never stored.
func (c *LoadedConfig) luaSubagents(L *lua.LState) int {
	if n, ok := checkPosIntField(L, L.CheckTable(1), "subagents", "max_depth"); ok {
		c.SubagentMaxDepth = n
	}
	return 0
}

// luaBackground sets config-global background job limits: shell3.background{ max_concurrent = N }.
// max_concurrent caps how many background jobs may run simultaneously. The
// default (8) is applied at the read site (newJobManager), not here; 0 is
// never stored.
func (c *LoadedConfig) luaBackground(L *lua.LState) int {
	if n, ok := checkPosIntField(L, L.CheckTable(1), "background", "max_concurrent"); ok {
		c.BackgroundMaxConcurrent = n
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
	mustKeys(L, opts, "model", modelKeys)
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
	mustKeys(L, opts, "skill", skillKeys)
	s := Skill{Name: optStr(opts, "name"), Description: optStr(opts, "description"), Path: optStr(opts, "path")}
	if s.Name == "" || s.Description == "" || s.Path == "" {
		L.RaiseError("skill: name, description, and path are all required")
	}
	c.Skills = append(c.Skills, s)
	return pushHandle(L, "__skill", s.Name)
}

// pushHandle pushes a handle table carrying sentinel → name (the reference
// form agents use in skills={}/tools.custom={}/tools.subagents={}) and returns
// the Lua result count.
func pushHandle(L *lua.LState, sentinel, name string) int {
	h := L.NewTable()
	h.RawSetString(sentinel, lua.LString(name))
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
	"bash": true, "bash_bg": true, "edit": true,
	"custom": true, "skill": true,
	"media":      true,
	"read":       true,
	"list_files": true,
	"subagents":  true,
}

func (c *LoadedConfig) luaTool(L *lua.LState) int {
	opts := L.CheckTable(1)
	mustKeys(L, opts, "tool", toolKeys)
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
	return pushHandle(L, "__tool", ct.Name)
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
	forEachStringPair(L, L.CheckTable(1), "stub_tools", "strings (tool names)", "a string (redirect message)",
		func(name, msg string) { c.StubTools[name] = msg })
	return 0
}

// forEachStringPair iterates a Lua table asserting string keys and string
// values, raising a Lua error (naming ctx and the expected shapes) otherwise.
// fn receives each pair.
func forEachStringPair(L *lua.LState, t *lua.LTable, ctx, keyWant, valWant string, fn func(k, v string)) {
	t.ForEach(func(k, v lua.LValue) {
		name, ok := k.(lua.LString)
		if !ok {
			L.RaiseError("%s: keys must be %s, got %s", ctx, keyWant, k.Type().String())
		}
		val, ok := v.(lua.LString)
		if !ok {
			L.RaiseError("%s[%q]: value must be %s, got %s", ctx, string(name), valWant, v.Type().String())
		}
		fn(string(name), string(val))
	})
}

// handleNamesStrict reads the array part of list as handles carrying sentinel,
// failing fast (via L.RaiseError) on any element that is not a valid handle —
// a bare string, number, or a table missing the sentinel all raise. ctx names
// the list in the error (e.g. `agent "code": tools.subagents`) and want names
// the handle kind. Silent dropping is never acceptable here: a typo'd entry in
// skills={}/tools.custom={}/tools.subagents={} would otherwise load an agent
// quietly missing a grant.
func handleNamesStrict(L *lua.LState, list *lua.LTable, sentinel, ctx, want string) []string {
	var out []string
	for i := 1; i <= list.Len(); i++ {
		ht, ok := list.RawGetInt(i).(*lua.LTable)
		if !ok {
			L.RaiseError("%s[%d] is not a %s handle", ctx, i, want)
		}
		s, ok := ht.RawGetString(sentinel).(lua.LString)
		if !ok {
			L.RaiseError("%s[%d] is not a %s handle", ctx, i, want)
		}
		out = append(out, string(s))
	}
	return out
}

// parseGates reads the boolean tool gates from a tools table.
func parseGates(tt *lua.LTable) ToolGates {
	return ToolGates{
		Bash:      optBool(tt, "bash"),
		BashBg:    optBool(tt, "bash_bg"),
		Edit:      optBool(tt, "edit"),
		Media:     optBool(tt, "media"),
		Read:      optBool(tt, "read"),
		ListFiles: optBool(tt, "list_files"),
	}
}

// toolsBlock is the parsed tools={} table, shared by agents and subagents.
// subagentsRaw carries the raw tools.subagents value (LNil when absent) — the
// two callers give it opposite legality (agents resolve it, subagents forbid
// it), so interpretation stays with them.
type toolsBlock struct {
	gates          ToolGates
	custom         []string
	skillsDisabled bool
	subagentsRaw   lua.LValue
}

// parseToolsBlock reads opts.tools for an agent or subagent declaration. ctx
// names the declaring entity in errors ("agent"/"subagent"). Returns
// ok=false when no tools table was declared.
func parseToolsBlock(L *lua.LState, opts *lua.LTable, ctx string) (toolsBlock, bool) {
	var tb toolsBlock
	tt, isTable := opts.RawGetString("tools").(*lua.LTable)
	if !isTable {
		return tb, false
	}
	mustKeys(L, tt, ctx+".tools", toolGateKeys)
	tb.gates = parseGates(tt)
	if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
		tb.custom = handleNamesStrict(L, cu, "__tool", ctx+".tools.custom", "tool")
	}
	tb.skillsDisabled = tt.RawGetString("skill") == lua.LFalse
	tb.subagentsRaw = tt.RawGetString("subagents")
	return tb, true
}

// requireOnePromptSource raises unless at most one of prompt/prompt_cmd is
// set. An empty prompt stays valid (a system prompt is assembled from other
// sources); only setting BOTH is an error.
func requireOnePromptSource(L *lua.LState, kind, name, prompt, promptCmd string) {
	if prompt != "" && promptCmd != "" {
		L.RaiseError("%s %q: set exactly one of prompt or prompt_cmd", kind, name)
	}
}

// agentCommon is the declaration surface agents and subagents share: skills,
// the tools block, and the host-reminder toggles. subagentsRaw carries the raw
// tools.subagents value (LNil when absent) for the caller to interpret —
// agents resolve it, subagents forbid it.
type agentCommon struct {
	skills         []string
	gates          ToolGates
	custom         []string
	skillsDisabled bool
	subagentsRaw   lua.LValue
	environment    bool
	delegation     bool
}

// parseAgentCommon validates and extracts the fields agents and subagents
// declare identically. kind/name label errors; name must already be deduped.
func (c *LoadedConfig) parseAgentCommon(L *lua.LState, opts *lua.LTable, kind, name, prompt, promptCmd string) agentCommon {
	requireOnePromptSource(L, kind, name, prompt, promptCmd)
	ac := agentCommon{subagentsRaw: lua.LNil}
	if sk, ok := opts.RawGetString("skills").(*lua.LTable); ok {
		ac.skills = handleNamesStrict(L, sk, "__skill", fmt.Sprintf("%s %q: skills", kind, name), "skill")
	}
	if tb, ok := parseToolsBlock(L, opts, kind); ok {
		ac.gates, ac.custom, ac.skillsDisabled = tb.gates, tb.custom, tb.skillsDisabled
		ac.subagentsRaw = tb.subagentsRaw
	}
	ac.environment = optBool(opts, "environment")
	ac.delegation = optBool(opts, "delegation")
	return ac
}

// luaAgent parses name/model/prompt, skills, and the tools struct (gates + custom).
func (c *LoadedConfig) luaAgent(L *lua.LState) int {
	opts := L.CheckTable(1)
	mustKeys(L, opts, "agent", agentKeys)
	a := Agent{
		Name:      optStr(opts, "name"),
		ModelName: optStr(opts, "model"),
		Prompt:    optStr(opts, "prompt"),
		PromptCmd: optStr(opts, "prompt_cmd"),
	}
	if a.Name == "" {
		L.RaiseError("agent: name is required")
	}
	// Duplicate names auto-suffix rather than failing the load (the first
	// declaration keeps its bare name; later collisions become name2, name3…).
	a.Name = c.uniqueName(a.Name)
	ac := c.parseAgentCommon(L, opts, "agent", a.Name, a.Prompt, a.PromptCmd)
	a.Skills, a.Gates, a.CustomTools, a.SkillsDisabled = ac.skills, ac.gates, ac.custom, ac.skillsDisabled
	a.Environment, a.Delegation = ac.environment, ac.delegation
	if ac.subagentsRaw != lua.LNil {
		sg, ok := ac.subagentsRaw.(*lua.LTable)
		if !ok {
			L.RaiseError("agent %q: tools.subagents must be a list of subagent handles, got %s", a.Name, ac.subagentsRaw.Type().String())
		}
		a.Subagents = handleNamesStrict(L, sg, "__subagent", fmt.Sprintf("agent %q: tools.subagents", a.Name), "subagent")
	}
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
	mustKeys(L, opts, "subagent", subagentKeys)
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
	// Duplicate names (against any agent or subagent) auto-suffix rather than
	// failing the load. The returned handle below carries the deduped name, so
	// tools.subagents={handle} references still resolve.
	s.Name = c.uniqueName(s.Name)
	ac := c.parseAgentCommon(L, opts, "subagent", s.Name, s.Prompt, s.PromptCmd)
	if _, isTable := ac.subagentsRaw.(*lua.LTable); isTable {
		L.RaiseError("subagent %q: a subagent may not declare its own subagents (depth limit 1)", s.Name)
	}
	s.Skills, s.Gates, s.CustomTools, s.SkillsDisabled = ac.skills, ac.gates, ac.custom, ac.skillsDisabled
	s.Environment, s.Delegation = ac.environment, ac.delegation
	c.subagents = append(c.subagents, s)
	return pushHandle(L, "__subagent", s.Name)
}
