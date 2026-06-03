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
	Agent   Agent
	Tools   map[string]CustomTool
	Skills  []Skill
	Secrets map[string]string

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
	c := &LoadedConfig{Tools: map[string]CustomTool{}, Secrets: env, L: lua.NewState()}
	registerShell3(c)
	if err := c.L.DoFile(path); err != nil {
		c.L.Close()
		return nil, fmt.Errorf("config: %w", err)
	}
	if c.Agent.Name == "" {
		c.L.Close()
		return nil, fmt.Errorf("config: no shell3.agent declared")
	}
	if _, ok := c.Model(c.Agent.ModelName); !ok {
		c.L.Close()
		return nil, fmt.Errorf("config: agent references unknown model %q", c.Agent.ModelName)
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
