// Package luacfg loads a strict single-file shell3.lua config.
package luacfg

import (
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
	prompt  bool
}

type Agent struct {
	Name, ModelName, Prompt string
	Gates                   ToolGates
	CustomTools             []string
	Skills                  []string
	Guard                   []GuardEntry
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

func (c *LoadedConfig) Model(name string) (Model, bool) {
	for _, m := range c.Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}
