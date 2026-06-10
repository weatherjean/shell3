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
	Reasoning                      string
	MaxTokens                      int
	Temperature                    *float64
	Extra                          map[string]any
	// RunProxy, if set, is a shell command spawned (detached, fire-and-forget)
	// the first time an agent activates this model — used to bring up a local
	// proxy/translation shim in front of BaseURL. See internal/modelproxy.
	RunProxy string
}

type ToolGates struct {
	Bash, BashBg, ShellInteractive, Edit, History, Prune, Compact, Media, Subagents bool
}

type CustomTool struct {
	Name, Description string
	Parameters        map[string]any
	handler           *lua.LFunction
}

type Skill struct{ Name, Description, Body string }

// MCPServer is a declared external MCP server (stdio transport).
type MCPServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Tools   []string // optional allowlist
}

// GuardEntry is one middleware in the on_tool_call chain: a Lua function that
// inspects a tool call and returns an allow/block/cancel decision.
type GuardEntry struct {
	fn *lua.LFunction
}

type Agent struct {
	Name, ModelName, Prompt string
	Gates                   ToolGates
	CustomTools             []string
	MCPServerNames          []string
	Skills                  []string
	SkillsDisabled          bool // true only when tools = { skill = false } is explicitly set
	Guard                   []GuardEntry
}

// SkillsActive reports whether skills are enabled: the agent has at least one
// skill listed AND the user has not explicitly disabled them with skill=false.
func (a Agent) SkillsActive() bool {
	return len(a.Skills) > 0 && !a.SkillsDisabled
}

// LoadedConfig is the parsed result. L stays alive for the session so custom
// tool handlers and guards can run; callers MUST call Close when done.
type LoadedConfig struct {
	Models     []Model
	Tools      map[string]CustomTool
	MCPServers map[string]MCPServer
	Skills     []Skill
	Secrets    map[string]string

	agents []Agent

	L  *lua.LState
	mu sync.Mutex
	// vmLockHeld is true while c.mu is held by CallTool/runLuaGuard driving the
	// VM. See withIOUnlock (lua_bash.go) for the locking model.
	vmLockHeld bool
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
	c := &LoadedConfig{Tools: map[string]CustomTool{}, MCPServers: map[string]MCPServer{}, Secrets: env, L: lua.NewState()}
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
	success = true
	return c, nil
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
