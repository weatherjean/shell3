// Package shell3 embeds the shell3 coding agent as a library. Run loads a
// shell3.lua config, executes one turn for a prompt, and streams structured
// events back to the caller. It is the entire public surface; pkg/chat,
// pkg/persona, and pkg/llm are internal details.
package shell3

import (
	"context"
	"errors"

	"github.com/weatherjean/shell3/pkg/chat"
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
