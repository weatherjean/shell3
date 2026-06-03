// Package shell3 embeds the shell3 coding agent as a library. Run loads a
// shell3.lua config, executes one turn for a prompt, and streams structured
// events back to the caller. It is the entire public surface; pkg/chat,
// pkg/persona, and pkg/llm are internal details.
package shell3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/persona"
)

// Spec configures a single Run. Prompt is required; the rest default.
type Spec struct {
	// Prompt is the user input for the single turn. Required.
	Prompt string
	// ConfigPath is the path to shell3.lua. Defaults to
	// ~/.shell3/shell3.lua when empty.
	ConfigPath string
	// WorkDir is the working directory for tool execution. Defaults to
	// os.Getwd() when empty.
	WorkDir string
}

// Kind discriminates a streamed Event.
type Kind int

const (
	// Token is a chunk of streamed assistant text. Text is set.
	Token Kind = iota
	// ToolResult reports a completed tool call. ToolName and ToolOutput are set.
	ToolResult
	// Error is a non-fatal turn error. Err is set. The turn still drains to Done.
	Error
	// Done marks the end of the turn. The channel closes immediately after.
	Done
)

// Event is one item streamed on the Run channel. Only the fields named for a
// given Kind are populated.
type Event struct {
	Kind       Kind
	Text       string // Kind == Token
	ToolName   string // Kind == ToolResult
	ToolOutput string // Kind == ToolResult
	Err        error  // Kind == Error
}

// runConfig runs one turn against an already-built chat.Config and streams
// translated public Events. The returned channel is closed exactly once after
// a final Done event; cleanup runs after teardown (used by Run to close the
// Lua state). cfg.LLM is injectable, which is what makes this testable with
// fakellm.
func runConfig(ctx context.Context, cfg chat.Config, prompt string, cleanup func()) <-chan Event {
	out := make(chan Event)

	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})
	tc := chat.TurnConfig{
		LLM:             cfg.LLM,
		Personality:     cfg.Personality,
		StatusLine:      cfg.StatusLine,
		WorkDir:         cfg.WorkDir,
		Truncate:        cfg.Truncate,
		Handlers:        chat.NewHandlers(cfg),
		Log:             chat.LogOrNoop(cfg.Log),
		Headless:        true,
		CustomTool:      cfg.CustomTool,
		CustomToolNames: cfg.CustomToolNames,
		ToolGuard:       cfg.ToolGuard,
		ShellInteractive: func(ctx context.Context, cmd, workdir string) string {
			return "error: interactive TTY not available in headless mode"
		},
	}

	go func() {
		sess.Run(ctx, tc, prompt)
		sess.CloseEvents()
	}()

	go func() {
		defer close(out)
		defer cleanup()
		for ev := range sess.Events() {
			if pub, ok := translate(ev); ok {
				out <- pub
			}
		}
	}()

	return out
}

// Run loads the config at spec.ConfigPath, runs one turn for spec.Prompt in
// spec.WorkDir, and streams translated Events on the returned channel.
//
// A non-nil error means Run failed to START (missing/invalid config, unknown
// model, missing key): nothing ran and the channel is nil. A nil error means
// the turn is underway; per-turn failures arrive as Event{Kind: Error}. The
// channel is closed exactly once after a final Done event. The caller's only
// obligation is to drain the channel until it closes.
func Run(ctx context.Context, spec Spec) (<-chan Event, error) {
	cfg, closeLua, err := buildConfig(spec)
	if err != nil {
		return nil, err
	}
	return runConfig(ctx, cfg, spec.Prompt, closeLua), nil
}

// buildConfig loads shell3.lua and assembles the minimal chat.Config for an
// embedded single-turn run: OpenAI-compatible adapter, persona system prompt,
// tool defs, custom-tool dispatch, and the guard chain. The returned cleanup
// closes the Lua state and is invoked by runConfig after the turn.
func buildConfig(spec Spec) (chat.Config, func(), error) {
	noop := func() {}

	workDir := spec.WorkDir
	if workDir == "" {
		w, err := os.Getwd()
		if err != nil {
			return chat.Config{}, noop, fmt.Errorf("get working directory: %w", err)
		}
		workDir = w
	}

	configPath := spec.ConfigPath
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return chat.Config{}, noop, fmt.Errorf("get home directory: %w", err)
		}
		configPath = filepath.Join(home, ".shell3", "shell3.lua")
	}

	lc, err := luacfg.Load(configPath, filepath.Dir(configPath))
	if err != nil {
		return chat.Config{}, noop, fmt.Errorf("load config: %w", err)
	}
	cleanup := func() { lc.Close() }

	m, ok := lc.Model(lc.Agent.ModelName)
	if !ok {
		cleanup()
		return chat.Config{}, noop, fmt.Errorf("agent references unknown model %q", lc.Agent.ModelName)
	}

	client := openai.NewClient(m.BaseURL, m.APIKey, m.ModelID)
	rp := llm.RequestParams{
		ReasoningEffort: m.Reasoning,
		MaxTokens:       m.MaxTokens,
		Temperature:     m.Temperature,
	}
	client.SetParams(rp)
	if m.Extra != nil {
		client.SetExtra(m.Extra)
	}

	sysPrompt := lc.BuildPersona(luacfg.RuntimeData{CWD: workDir, Model: m.ModelID})

	customDefs := lc.CustomToolsFor(lc.Agent.CustomTools)
	hasSkills := lc.Agent.SkillsActive()
	toolDefs := luacfg.ToolDefs(lc.Agent.Gates, customDefs, hasSkills)

	pers := persona.Persona{
		Name:         lc.Agent.Name,
		SystemPrompt: sysPrompt,
		Tools:        toolDefs,
		Parameters:   rp,
	}

	customNames := make(map[string]bool, len(lc.Agent.CustomTools))
	for _, n := range lc.Agent.CustomTools {
		customNames[n] = true
	}
	if hasSkills {
		customNames["skill"] = true
	}

	cfg := chat.Config{
		LLM:             client,
		Personality:     pers,
		WorkDir:         workDir,
		StatusLine:      lc.Agent.Name + " │ " + m.ModelID,
		ModeLabel:       lc.Agent.Name,
		ContextWindow:   m.ContextWindow,
		ActiveSkills:    lc.Agent.Skills,
		CustomTool:      lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		Headless: true,
		Params:   rp,
	}

	return cfg, cleanup, nil
}

// translate maps an internal chat.Event to a public Event. The second return
// is false when the internal event has no public equivalent and should be
// dropped (reasoning, tool-call, usage, session, user/assistant-message,
// system-reminder, retry).
func translate(ev chat.Event) (Event, bool) {
	switch ev.Kind {
	case chat.EventAssistantToken:
		return Event{Kind: Token, Text: ev.Text}, true
	case chat.EventToolResult:
		return Event{Kind: ToolResult, ToolName: ev.ToolName, ToolOutput: ev.ToolOutput}, true
	case chat.EventError:
		return Event{Kind: Error, Err: errors.New(ev.Text)}, true
	case chat.EventTurnDone:
		return Event{Kind: Done}, true
	default:
		return Event{}, false
	}
}
