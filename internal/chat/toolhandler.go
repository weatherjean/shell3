package chat

import (
	"context"
	"encoding/json"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/usertools"
)

// ToolHandler is the interface for built-in tool implementations.
// Each built-in tool (bash, edit_file, prune_tool_result, etc.) implements this.
// User tools (YAML-defined) use a separate dispatch path and do not implement this interface.
type ToolHandler interface {
	Name() string
	Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}

// ToolConfig holds per-invocation state passed to ToolHandler.Execute.
// Mutations to AllMsgs and SessMsgs elements propagate to the caller's slices.
type ToolConfig struct {
	Store    *store.Store
	WorkDir  string
	Secrets  map[string]string
	AllMsgs  []llm.Message
	SessMsgs []llm.Message
}

// TurnConfig holds all dependencies needed for one user→assistant turn.
// It is constructed from Config in RunInteractive/RunOnce and passed to runTurn.
type TurnConfig struct {
	LLM         LLMClient
	Hooks       *hooks.Runner
	Personality persona.Persona
	StatusLine  string
	WorkDir     string
	Store       *store.Store
	UserTools   map[string]usertools.Tool
	Secrets     map[string]string
	Truncate    bool
	Handlers    map[string]ToolHandler
	Log         applog.Logger
	// Headless is true when shell3 runs as a subprocess (no human at the
	// keyboard). turn.go drops shell_interactive and injects a system
	// reminder when this is set.
	Headless bool
}
