// Package shell3 embeds the shell3 coding agent as a library. Run loads a
// shell3.lua config, executes one turn for a prompt, and streams structured
// events back to the caller. It is the entire public surface; pkg/chat,
// pkg/persona, and pkg/llm are internal details.
package shell3

import (
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
