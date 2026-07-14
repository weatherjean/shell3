// Package luacfg loads the shell3.lua config. shell3.lua is the entry point;
// it may require()/dofile() sibling .lua modules resolved relative to its dir.
package luacfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"
)

type Model struct {
	Name, BaseURL, APIKey, ModelID string
	ContextWindow                  int
	// CompactAt is the absolute prompt-token threshold at which the host
	// auto-compacts conversation history before the next turn. 0 (unset)
	// disables auto-compaction. See chat.maybeCompact.
	CompactAt int
	// KeepRecent is the verbatim tail (in prompt tokens) preserved across an
	// auto-compaction. 0 (unset) derives a default from CompactAt. See
	// chat.resolveKeepRecent.
	KeepRecent int
	// PruneAt is the lower threshold; stub old tool outputs with no LLM call.
	// 0 disables. Must be below CompactAt (clamped to 0 if not).
	PruneAt     int
	Reasoning   string
	MaxTokens   int
	Temperature *float64
	Extra       map[string]any
	// RunProxy, if set, is a shell command spawned (detached, fire-and-forget)
	// the first time an agent activates this model — used to bring up a local
	// proxy/translation shim in front of BaseURL. See internal/modelproxy.
	RunProxy string
}

type ToolGates struct {
	Bash, BashBg, Edit, Media, Read, ListFiles bool
}

// CustomTool is a declarative bash-command tool. The model supplies typed
// parameters (validated against Parameters); at call time each declared param is
// exported into the command's environment by its (lowercase) name and the
// command (a bash template) runs with that env. Secrets names each .env key to
// also export — kept out of the command string. Background dispatches via
// bash_bg instead of blocking. There is no Lua handler.
type CustomTool struct {
	Name, Description string
	Parameters        map[string]any
	Command           string
	Secrets           []string
	Background        bool
	Timeout           int
}

// Skill is a granted capability surfaced as a one-line entry in the agent's
// ## Skills index (name + description). Body lives in the file at Path; the
// agent reads it with `cat` when the skill applies. Path is stored relative as
// declared, then rewritten to an absolute path during Load (see the skill
// resolution loop) so the index can point the agent at it from any cwd.
type Skill struct{ Name, Description, Path string }

type Agent struct {
	Name, ModelName, Prompt string
	// PromptCmd, if set, is a shell command whose stdout supplies Prompt; it is
	// resolved once at load time (see Load). Empty once resolved or when Prompt
	// was supplied inline.
	PromptCmd      string
	Gates          ToolGates
	CustomTools    []string
	Skills         []string
	SkillsDisabled bool     // true only when tools = { skill = false } is explicitly set
	Subagents      []string // names of registered subagents this agent can spawn
	Environment    bool     // inject the host Environment system-reminder
	Delegation     bool     // inject the host Delegation (spawn-command) system-reminder
}

// Subagent is a delegatable specialist: a non-interactive agent the model can
// spawn as an in-process background job via the task tool. Registered
// separately from agents (never in the Tab rotation). Description is the
// model-facing "when to use".
type Subagent struct {
	Name, Description, ModelName, Prompt string
	// PromptCmd, if set, is a shell command whose stdout supplies Prompt;
	// resolved once at load time (see Load). Empty once resolved or when Prompt
	// was supplied inline.
	PromptCmd      string
	Gates          ToolGates
	CustomTools    []string
	Skills         []string
	SkillsDisabled bool
	Environment    bool // inject the host Environment system-reminder
	Delegation     bool // inject the host Delegation (spawn-command) system-reminder
}

// SkillsActive reports whether skills are enabled: the agent has at least one
// skill listed AND the user has not explicitly disabled them with skill=false.
func (a Agent) SkillsActive() bool {
	return len(a.Skills) > 0 && !a.SkillsDisabled
}

// LoadedConfig is the parsed result. L stays alive for the session so the
// on_tool_call hooks can run; callers MUST call Close when done.
type LoadedConfig struct {
	Models  []Model
	Tools   map[string]CustomTool
	Skills  []Skill
	Secrets map[string]string
	// StubTools maps a hallucinated tool name (e.g. "read_file", "grep") to a
	// fixed redirect message. Registered config-globally via shell3.stub_tools;
	// when the model calls such a name the chat layer returns the message
	// verbatim (never an error), nudging the model back toward bash/edit_file.
	// See register.go (luaStubTools) and agentsetup.runtimeForAgent for the
	// wiring (StubNames → chat.Config.StubTools).
	StubTools map[string]string
	// Theme holds config-global TUI color overrides (token → "#RRGGBB"), set via
	// shell3.theme{}. luaTheme validates the hex *format* here (a malformed value
	// is dropped with a load warning); unknown token names pass through and are
	// filtered by the front-end that owns the palette vocabulary. The overrides sit
	// atop the sensed light/dark palette in the TUI. Wiring: register.go (luaTheme)
	// → agentsetup SessionConfig (chat.Config.Theme) → internal/shell3 Snapshot.Theme →
	// front-ends.
	Theme map[string]string
	// Welcome, if set, replaces the built-in TUI welcome card verbatim (set via
	// shell3.welcome). Rendered raw and centered, so it may embed ANSI escapes for
	// terminal colors. Same wiring path as Theme (→ Snapshot.Welcome → tui).
	Welcome string
	// SubagentMaxDepth is the maximum allowed subagent nesting depth, set via
	// shell3.subagents{ max_depth = N }. 0 means unset; the runtime applies the
	// default (3) at the read site.
	SubagentMaxDepth int
	// BackgroundMaxConcurrent is the maximum number of concurrent background jobs,
	// set via shell3.background{ max_concurrent = N }. 0 means unset; the runtime
	// applies the default (8) at the read site.
	BackgroundMaxConcurrent int

	agents    []Agent
	subagents []Subagent

	// telegram holds the parsed shell3.telegram{} block (zero value if absent);
	// cron holds the jobs declared under shell3.telegram{ cron = {...} }. Read
	// via Telegram()/Cron(). See telegram.go.
	telegram TelegramConfig
	cron     []CronJob

	// onToolCall is the shell3.on_tool_call handler chain (declaration order): each
	// runs before any tool executes, with the real t.name, and returns a verdict
	// (pass / rewrite / argv / block / ask). command/argv apply to the bash family
	// only; t.command is nil for non-bash tools.
	onToolCall []*lua.LFunction
	// onToolResult is the shell3.on_tool_result chain (declaration order): each
	// may rewrite a tool's output before the model sees it (e.g. redaction).
	onToolResult []*lua.LFunction

	// warnings accumulates non-fatal config issues found at load time (e.g. a
	// removed key that is now silently ignored, or a gate that gates nothing). The
	// caller drains them via Warnings() and decides how to surface them; an empty
	// slice means a clean load.
	warnings []string

	L *lua.LState
	// vmMu serializes access to the Lua VM (gopher-lua is single-threaded):
	// the on_tool_call / on_tool_result chains lock it for each run. It does
	// NOT guard the parsed config data — models/agents/subagents/tools are
	// immutable once Load returns, so the accessors below read them lock-free
	// and can never block behind a slow Lua hook.
	vmMu sync.Mutex
}

// Warnings returns the non-fatal issues collected while loading the config
// (ignored deprecated keys, an enabled gate with no patterns, …). Empty on a
// clean load. Surfacing them is the caller's choice; the config still loaded.
func (c *LoadedConfig) Warnings() []string { return c.warnings }

func (c *LoadedConfig) Close() {
	if c.L != nil {
		c.L.Close()
	}
}

// Load reads shell3.lua at path. Everything the config references — .env,
// required sibling modules, skill files, prompt_cmd working dir — resolves
// relative to path's directory, so there is no separate workdir to drift from.
func Load(path string) (*LoadedConfig, error) {
	cfgDir := filepath.Dir(path)
	env, err := loadDotEnv(filepath.Join(cfgDir, ".env"))
	if err != nil {
		return nil, err
	}
	c := &LoadedConfig{Tools: map[string]CustomTool{}, StubTools: map[string]string{}, Theme: map[string]string{}, Secrets: env, L: lua.NewState()}
	// The returned config owns c.L; close it only if we error out below.
	var success bool
	defer func() {
		if !success {
			c.L.Close()
		}
	}()
	registerShell3(c)
	// Let shell3.lua require()/dofile() sibling modules by name. gopher-lua's
	// default package.path resolves relative to the process CWD, which is
	// wherever shell3 was invoked from — not the config dir. Prepend the config
	// dir so `require("foo")` finds foo.lua next to shell3.lua.
	if pkg, ok := c.L.GetGlobal("package").(*lua.LTable); ok {
		prev := lua.LVAsString(pkg.RawGetString("path"))
		pkg.RawSetString("path", lua.LString(
			filepath.Join(cfgDir, "?.lua")+";"+filepath.Join(cfgDir, "?", "init.lua")+";"+prev))
	}
	if err := c.L.DoFile(path); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	// Resolve command-backed bodies/prompts before any cross-reference
	// validation. Each command runs with cwd = the config dir (same dir as
	// .env and the lib/ modules), so relative paths like `cat lib/skills/x.md`
	// resolve as authors expect. A failing or empty command fails the whole
	// Load (fail-closed); since reload goes through Load, a bad prompt file can
	// never silently swap in an empty prompt at runtime.
	// Resolve + validate skill file paths against the config dir (cfgDir): each
	// must be a readable, non-empty regular file, caught here at load/reload
	// rather than at turn time. The resolved absolute path is stored back so the
	// ## Skills index can point the agent at it (BuildPersonaFor).
	for i := range c.Skills {
		skillPath := c.Skills[i].Path
		if !filepath.IsAbs(skillPath) {
			skillPath = filepath.Join(cfgDir, skillPath)
		}
		// One read covers every failure mode: a missing path and a directory
		// both error here, and whitespace-only content is treated as empty.
		data, err := os.ReadFile(skillPath)
		if err != nil {
			return nil, fmt.Errorf("config: skill %q path %q: %w", c.Skills[i].Name, c.Skills[i].Path, err)
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			return nil, fmt.Errorf("config: skill %q path %q: file is empty", c.Skills[i].Name, c.Skills[i].Path)
		}
		c.Skills[i].Path = skillPath
	}
	for i := range c.agents {
		a := &c.agents[i]
		if err := resolvePromptCmd(cfgDir, "agent", a.Name, a.PromptCmd, &a.Prompt); err != nil {
			return nil, err
		}
	}
	for i := range c.subagents {
		sa := &c.subagents[i]
		if err := resolvePromptCmd(cfgDir, "subagent", sa.Name, sa.PromptCmd, &sa.Prompt); err != nil {
			return nil, err
		}
	}
	if len(c.agents) == 0 {
		return nil, fmt.Errorf("config: no shell3.agent declared")
	}
	for i := range c.agents {
		if err := c.resolveModelName("agent", c.agents[i].Name, &c.agents[i].ModelName); err != nil {
			return nil, err
		}
	}
	for i := range c.subagents {
		if err := c.resolveModelName("subagent", c.subagents[i].Name, &c.subagents[i].ModelName); err != nil {
			return nil, err
		}
	}
	// Validate cron jobs (every job needs a schedule + a declared subagent).
	if err := c.validateCron(); err != nil {
		return nil, err
	}
	// Validate cross-references: agent.Subagents must all resolve.
	for _, a := range c.agents {
		for _, name := range a.Subagents {
			found := false
			for _, sa := range c.subagents {
				if sa.Name == name {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("config: agent %q references unknown subagent %q", a.Name, name)
			}
		}
	}
	success = true
	return c, nil
}

// resolvePromptCmd runs a declaration's prompt_cmd (when set) with cwd=cfgDir
// and stores its output as the prompt. kind/name label errors ("agent"/"subagent").
func resolvePromptCmd(cfgDir, kind, name, promptCmd string, prompt *string) error {
	if promptCmd == "" {
		return nil
	}
	out, err := runBodyCmd(cfgDir, promptCmd)
	if err != nil {
		return fmt.Errorf("config: %s %q prompt_cmd %q: %w", kind, name, promptCmd, err)
	}
	*prompt = out
	return nil
}

// resolveModelName defaults an empty model reference to the first declared
// model and validates that the reference resolves. kind labels errors
// ("agent"/"subagent").
func (c *LoadedConfig) resolveModelName(kind, name string, modelName *string) error {
	if *modelName == "" {
		if len(c.Models) == 0 {
			return fmt.Errorf("config: %s %q has no model and no models are declared", kind, name)
		}
		*modelName = c.Models[0].Name
	}
	if _, ok := c.Model(*modelName); !ok {
		return fmt.Errorf("config: %s %q references unknown model %q", kind, name, *modelName)
	}
	return nil
}

// StubNames returns the registered stub-tool names with their redirect
// messages. The map is config-global (not per-agent); agentsetup appends one
// minimal tool def per entry to every agent's schema. The returned map is the
// live config map — read-only by convention (callers never mutate it).
func (c *LoadedConfig) StubNames() map[string]string { return c.StubTools }

func (c *LoadedConfig) Model(name string) (Model, bool) {
	for _, m := range c.Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// Agents returns a copy of the registered agents in declaration order.
func (c *LoadedConfig) Agents() []Agent {
	out := make([]Agent, len(c.agents))
	copy(out, c.agents)
	return out
}

// AgentByName returns the declared agent with the given name. Agent selection
// is the caller's (per-session) state — the config holds only declarations.
func (c *LoadedConfig) AgentByName(name string) (Agent, bool) {
	for _, a := range c.agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// FirstAgent returns the first declared agent (the default when a caller
// doesn't name one). Load guarantees at least one agent exists.
func (c *LoadedConfig) FirstAgent() Agent {
	return c.agents[0]
}

// Subagents returns a copy of the registered subagents in declaration order.
// Production reads go through SubagentDescription; this accessor exists for
// tests asserting on declaration/dedup behavior.
func (c *LoadedConfig) Subagents() []Subagent {
	out := make([]Subagent, len(c.subagents))
	copy(out, c.subagents)
	return out
}

// SubagentByName returns the declared subagent with the given name.
func (c *LoadedConfig) SubagentByName(name string) (Subagent, bool) {
	for _, s := range c.subagents {
		if s.Name == name {
			return s, true
		}
	}
	return Subagent{}, false
}
