// Package luacfg loads the shell3.lua config. shell3.lua is the entry point;
// it may require()/dofile() sibling .lua modules resolved relative to its dir.
package luacfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/bashsafety"
	lua "github.com/yuin/gopher-lua"
)

type Model struct {
	Name, BaseURL, APIKey, ModelID string
	ContextWindow                  int
	// CompactAt is the absolute prompt-token threshold at which the host
	// auto-compacts conversation history before the next turn. 0 (unset)
	// disables auto-compaction. See chat.maybeCompact.
	CompactAt   int
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
	Bash, BashBg, ShellInteractive, Edit, Media bool
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

// CronJob is one parsed cron job entry (shell3.telegram cron list).
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}

// TelegramConfig is the parsed shell3.telegram{...} block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	Agent     string
	WorkDir   string
	Dashboard DashboardConfig
}

// DashboardConfig is the parsed shell3.telegram.dashboard{} block.
type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
}

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
	Description    string   // model-facing "when to use" (unused for top-level agents)
	Environment    bool     // inject the host Environment system-reminder
	Delegation     bool     // inject the host Delegation (spawn-command) system-reminder
}

// Subagent is a delegatable specialist: a non-interactive agent the model can
// spawn as a backgrounded `shell3` process (the bash_bg delegation command).
// Registered separately from agents (never in the Tab rotation). Description is
// the model-facing "when to use".
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
// wrap_bash hook can run; callers MUST call Close when done.
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

	agents    []Agent
	subagents []Subagent
	telegram  TelegramConfig
	cron      []CronJob

	// wrapBash is the registered shell3.wrap_bash hook (nil when none declared):
	// a single Lua function the bash/bash_bg tools pass their command through
	// before execution. See luaWrapBash / WrapBash (lua_bash.go). A second
	// shell3.wrap_bash call replaces it (last writer wins).
	wrapBash *lua.LFunction

	// bashSafety is the parsed shell3.bash_safety policy, or nil when the config
	// declares none. Config-global, like wrapBash.
	bashSafety *bashsafety.Policy

	L  *lua.LState
	mu sync.Mutex
}

func (c *LoadedConfig) Close() {
	if c.L != nil {
		c.L.Close()
	}
}

// Load reads shell3.lua at path; workdir is used for .env + relative paths.
func Load(path, workdir string) (*LoadedConfig, error) {
	env, err := loadDotEnv(filepath.Join(workdir, ".env"))
	if err != nil {
		return nil, err
	}
	c := &LoadedConfig{Tools: map[string]CustomTool{}, StubTools: map[string]string{}, Secrets: env, L: lua.NewState()}
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
		dir := filepath.Dir(path)
		prev := lua.LVAsString(pkg.RawGetString("path"))
		pkg.RawSetString("path", lua.LString(
			filepath.Join(dir, "?.lua")+";"+filepath.Join(dir, "?", "init.lua")+";"+prev))
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
	cfgDir := filepath.Dir(path)
	// Resolve + validate skill file paths against the config dir (cfgDir): each
	// must be a readable, non-empty regular file, caught here at load/reload
	// rather than at turn time. The resolved absolute path is stored back so the
	// ## Skills index can point the agent at it (BuildPersonaFor).
	for i := range c.Skills {
		skillPath := c.Skills[i].Path
		if !filepath.IsAbs(skillPath) {
			skillPath = filepath.Join(cfgDir, skillPath)
		}
		info, err := os.Stat(skillPath)
		if err != nil {
			return nil, fmt.Errorf("config: skill %q path %q: %w", c.Skills[i].Name, c.Skills[i].Path, err)
		}
		if info.IsDir() || info.Size() == 0 {
			return nil, fmt.Errorf("config: skill %q path %q: not a non-empty file", c.Skills[i].Name, c.Skills[i].Path)
		}
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
		if c.agents[i].PromptCmd == "" {
			continue
		}
		out, err := runBodyCmd(cfgDir, c.agents[i].PromptCmd)
		if err != nil {
			return nil, fmt.Errorf("config: agent %q prompt_cmd %q: %w", c.agents[i].Name, c.agents[i].PromptCmd, err)
		}
		c.agents[i].Prompt = out
	}
	for i := range c.subagents {
		if c.subagents[i].PromptCmd == "" {
			continue
		}
		out, err := runBodyCmd(cfgDir, c.subagents[i].PromptCmd)
		if err != nil {
			return nil, fmt.Errorf("config: subagent %q prompt_cmd %q: %w", c.subagents[i].Name, c.subagents[i].PromptCmd, err)
		}
		c.subagents[i].Prompt = out
	}
	if len(c.agents) == 0 {
		return nil, fmt.Errorf("config: no shell3.agent declared")
	}
	for i := range c.agents {
		if c.agents[i].ModelName == "" {
			if len(c.Models) == 0 {
				return nil, fmt.Errorf("config: agent %q has no model and no models are declared", c.agents[i].Name)
			}
			c.agents[i].ModelName = c.Models[0].Name
		}
		if _, ok := c.Model(c.agents[i].ModelName); !ok {
			return nil, fmt.Errorf("config: agent %q references unknown model %q", c.agents[i].Name, c.agents[i].ModelName)
		}
	}
	for i := range c.subagents {
		if c.subagents[i].ModelName == "" {
			if len(c.Models) == 0 {
				return nil, fmt.Errorf("config: subagent %q has no model and no models are declared", c.subagents[i].Name)
			}
			c.subagents[i].ModelName = c.Models[0].Name
		}
		if _, ok := c.Model(c.subagents[i].ModelName); !ok {
			return nil, fmt.Errorf("config: subagent %q references unknown model %q", c.subagents[i].Name, c.subagents[i].ModelName)
		}
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
	for i := range c.cron {
		if c.cron[i].Schedule == "" {
			return nil, fmt.Errorf("config: cron job %q has no schedule", c.cron[i].Name)
		}
		if c.cron[i].Agent == "" {
			return nil, fmt.Errorf("config: cron job %q has no agent", c.cron[i].Name)
		}
		if _, ok := c.SubagentByName(c.cron[i].Agent); !ok {
			return nil, fmt.Errorf("config: cron job %q references unknown subagent %q", c.cron[i].Name, c.cron[i].Agent)
		}
	}
	success = true
	return c, nil
}

// Telegram returns the parsed shell3.telegram{} block (zero value if absent).
func (c *LoadedConfig) Telegram() TelegramConfig { return c.telegram }

// Cron returns the parsed cron jobs from shell3.telegram (nil if absent).
func (c *LoadedConfig) Cron() []CronJob { return c.cron }

// HasWrapBash reports whether a shell3.wrap_bash hook was declared. agentsetup
// uses it to decide whether to wire a WrapBash closure onto chat.Config (nil
// closure = no wrapping = allow all, the unsafe default).
func (c *LoadedConfig) HasWrapBash() bool { return c.wrapBash != nil }

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
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Agent, len(c.agents))
	copy(out, c.agents)
	return out
}

// AgentByName returns the declared agent with the given name. Agent selection
// is the caller's (per-session) state — the config holds only declarations.
func (c *LoadedConfig) AgentByName(name string) (Agent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agents[0]
}

// Subagents returns a copy of the registered subagents in declaration order.
func (c *LoadedConfig) Subagents() []Subagent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Subagent, len(c.subagents))
	copy(out, c.subagents)
	return out
}

// SubagentByName returns the declared subagent with the given name.
func (c *LoadedConfig) SubagentByName(name string) (Subagent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.subagents {
		if s.Name == name {
			return s, true
		}
	}
	return Subagent{}, false
}
