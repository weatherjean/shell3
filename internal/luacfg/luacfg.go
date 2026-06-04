// Package luacfg loads a strict single-file shell3.lua config.
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
}

type ToolGates struct {
	Bash, BashBg, ShellInteractive, Edit, Memory, History, Docs bool
}

type CustomTool struct {
	Name, Description string
	Parameters        map[string]any
	handler           *lua.LFunction
}

type Skill struct{ Name, Description, Body string }

// GuardEntry is one middleware in the on_tool_call chain: either a Lua
// function or a built-in guard identified by Builtin.
type GuardEntry struct {
	fn      *lua.LFunction
	Builtin string // "" unless a shell3.guards.* handle
}

type Agent struct {
	Name, ModelName, Prompt string
	Gates                   ToolGates
	CustomTools             []string
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
	Models  []Model
	Tools   map[string]CustomTool
	Skills  []Skill
	Secrets map[string]string

	agents    []Agent
	activeIdx int

	L  *lua.LState
	mu sync.Mutex
	// vmLockHeld is true while c.mu is held by CallTool/runLuaGuard driving the VM
	// on this (single) goroutine. withIOUnlock reads it to decide whether it
	// must release+reacquire c.mu around blocking IO. Only ever touched by the
	// goroutine holding c.mu (or at top-level Load, where it is false), per the
	// single-agent VM invariant — so it needs no separate synchronization.
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
	c := &LoadedConfig{Tools: map[string]CustomTool{}, Secrets: env, L: lua.NewState()}
	registerShell3(c)
	if err := c.L.DoFile(path); err != nil {
		c.L.Close()
		return nil, fmt.Errorf("config: %w", err)
	}
	if len(c.agents) == 0 {
		c.L.Close()
		return nil, fmt.Errorf("config: no shell3.agent declared")
	}
	for i := range c.agents {
		if c.agents[i].ModelName == "" {
			if len(c.Models) == 0 {
				c.L.Close()
				return nil, fmt.Errorf("config: agent %q has no model and no models are declared", c.agents[i].Name)
			}
			c.agents[i].ModelName = c.Models[0].Name
		}
		if _, ok := c.Model(c.agents[i].ModelName); !ok {
			c.L.Close()
			return nil, fmt.Errorf("config: agent %q references unknown model %q", c.agents[i].Name, c.agents[i].ModelName)
		}
	}
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

// Active/Agents/SwitchAgent guard activeIdx with c.mu for visibility, but
// correctness also relies on the front-end busy-gate: SwitchAgent is only
// called when no turn is in flight, so a tool call's guard chain (OnToolCall,
// which snapshots the active agent) never races a switch mid-turn.

// Active returns the currently selected agent.
func (c *LoadedConfig) Active() Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agents[c.activeIdx]
}

// Agents returns a copy of the registered agents in declaration order.
func (c *LoadedConfig) Agents() []Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Agent, len(c.agents))
	copy(out, c.agents)
	return out
}

// SwitchAgent sets the active agent by name. An unknown name returns an error
// and leaves the active agent unchanged.
func (c *LoadedConfig) SwitchAgent(name string) (Agent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, a := range c.agents {
		if a.Name == name {
			c.activeIdx = i
			return c.agents[i], nil
		}
	}
	return Agent{}, fmt.Errorf("unknown agent %q", name)
}
