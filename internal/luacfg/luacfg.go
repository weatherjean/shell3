// Package luacfg loads the shell3.lua config. shell3.lua is the entry point;
// it may require()/dofile() sibling .lua modules resolved relative to its dir.
package luacfg

import (
	"fmt"
	"path/filepath"
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
	Bash, BashBg, Edit, Media bool
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

// Skill is one resolved *.md from an agent's skills dirs (see skills.go),
// surfaced as a one-line entry in the ## Skills index; the agent reads the
// body at Path (absolute) with `cat` when the skill applies.
type Skill struct{ Name, Description, Path string }

// AgentCommon holds the declaration fields agents and subagents share. Both
// embed it, so load-time resolution (skills, prompt_cmd, model) runs over one
// shape and downstream copiers can copy the whole core at once.
type AgentCommon struct {
	Name, ModelName, Prompt string
	// PromptCmd, if set, is a shell command whose stdout supplies Prompt; it is
	// resolved once at load time (see Load) and kept as declared.
	PromptCmd   string
	Gates       ToolGates
	CustomTools []string
	// SkillDirs is the skills = { "dir", ... } list as declared; Skills is the
	// result of scanning those dirs at Load (see resolveSkillDirs).
	SkillDirs   []string
	Skills      []Skill
	Environment bool // inject the host Environment system-reminder
	Delegation  bool // advertise the task/task_* tools (with a non-empty tools.subagents)
	// Prune toggles the cheap tool-output-stubbing tier for this agent/subagent.
	// nil (unset) inherits the model's prune_at; false skips pruning entirely
	// (context management goes straight to compact_at); true is an explicit
	// inherit — it cannot invent a threshold the model doesn't declare. See
	// agentsetup.runtimeForAgent for the overlay.
	Prune *bool
}

type Agent struct {
	AgentCommon
	Subagents []string // names of registered subagents this agent can spawn
}

// Subagent is a delegatable specialist: a non-interactive agent the model can
// spawn as an in-process background job via the task tool. Registered
// separately from agents (never user-facing directly). Description is the
// model-facing "when to use". No Subagents field: delegation is single-level
// by construction.
type Subagent struct {
	AgentCommon
	Description string
}

// LoadedConfig is the parsed result. L stays alive for the session so the
// on_tool_call hooks can run; callers MUST call Close when done.
type LoadedConfig struct {
	Models  []Model
	Tools   map[string]CustomTool
	Secrets map[string]string
	// BackgroundMaxConcurrent is the maximum number of concurrent background jobs,
	// set via shell3.background{ max_concurrent = N }. 0 means unset; the runtime
	// applies the default (8) at the read site.
	BackgroundMaxConcurrent int

	agents    []Agent
	subagents []Subagent

	// telegram holds the parsed shell3.telegram{} block (zero value if absent);
	// cron holds the jobs declared via top-level shell3.cron{...}. Read via
	// Telegram()/Cron(). See telegram.go / cron.go.
	telegram TelegramConfig
	cron     []CronJob
	// web holds the parsed top-level shell3.web{} block (zero value if
	// absent). Read via Web(). See web.go.
	web WebConfig
	// heartbeat holds the parsed shell3.heartbeat{} block (nil if absent).
	// Read via Heartbeat(). See heartbeat.go.
	heartbeat *Heartbeat

	// stt/tts/describe/imagegen hold the parsed media config blocks (nil if
	// absent). Read via STT()/TTS()/Describe()/Imagegen(). See media.go.
	stt      *STTConfig
	tts      *TTSConfig
	describe *DescribeConfig
	imagegen *ImagegenConfig

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
// Every error names the config file: with named configs (~/.shell3/<name>.lua)
// a bare "config: ..." wouldn't say which file failed.
func Load(path string) (*LoadedConfig, error) {
	c, err := load(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func load(path string) (*LoadedConfig, error) {
	cfgDir := filepath.Dir(path)
	env, err := loadDotEnv(filepath.Join(cfgDir, ".env"))
	if err != nil {
		return nil, err
	}
	c := &LoadedConfig{Tools: map[string]CustomTool{}, Secrets: env, L: lua.NewState()}
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
	// Resolve each agent's/subagent's skills dirs (skills.go): a missing dir
	// fails the load here at load/reload time rather than at turn time; invalid
	// skill files are skipped with a warning.
	if len(c.agents) == 0 {
		return nil, fmt.Errorf("config: no shell3.agent declared")
	}
	sc := newSkillScanner(cfgDir, func(w string) { c.warnings = append(c.warnings, w) })
	// Agents and subagents share AgentCommon, so one loop resolves both.
	cores := make([]*AgentCommon, 0, len(c.agents)+len(c.subagents))
	kinds := make([]string, 0, cap(cores))
	for i := range c.agents {
		cores, kinds = append(cores, &c.agents[i].AgentCommon), append(kinds, "agent")
	}
	for i := range c.subagents {
		cores, kinds = append(cores, &c.subagents[i].AgentCommon), append(kinds, "subagent")
	}
	for i, core := range cores {
		kind := kinds[i]
		sk, err := sc.resolve(core.SkillDirs, fmt.Sprintf("%s %q", kind, core.Name))
		if err != nil {
			return nil, err
		}
		core.Skills = sk
		if err := resolvePromptCmd(cfgDir, kind, core.Name, core.PromptCmd, &core.Prompt); err != nil {
			return nil, err
		}
		if err := c.resolveModelName(kind, core.Name, &core.ModelName); err != nil {
			return nil, err
		}
	}
	// Validate media block model references (stt/tts/describe/imagegen): each
	// must name a declared shell3.model. Runs post-parse so declaration order
	// between a media block and its model never matters.
	if err := c.validateMediaRefs(); err != nil {
		return nil, err
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

// AgentByName returns the declared agent with the given name. Only one agent
// can be declared; this lookup exists for internal plumbing that resolves an
// agent reference by name.
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
