package shell3

import (
	"errors"
	"fmt"

	"github.com/weatherjean/shell3/internal/chat"
)

// EventKind discriminates a streamed Event.
type EventKind int

const (
	Token          EventKind = iota // assistant text         → Text
	Reasoning                       // thinking text           → Text
	ToolCall                        // tool started            → ToolName, ToolCallID, ToolInput, IsHostTool
	ToolResult                      // tool finished           → ToolName, ToolCallID, ToolOutput
	SystemReminder                  // injected reminder block → Text
	Compacted                       // auto-compaction occurred → Text + post-compaction PromptTokens/TotalTokens estimate
	Usage                           // per-roundtrip tokens    → PromptTokens/CompletionTokens/TotalTokens
	Retry                           // transient retry         → Text
	Error                           // turn error              → Err
	Done                            // turn end (normal)       → token fields (final totals)
)

// String returns the event name (e.g. "token", "tool_call") for logs and
// diagnostics.
func (k EventKind) String() string {
	switch k {
	case Token:
		return "token"
	case Reasoning:
		return "reasoning"
	case ToolCall:
		return "tool_call"
	case ToolResult:
		return "tool_result"
	case SystemReminder:
		return "system_reminder"
	case Compacted:
		return "compacted"
	case Usage:
		return "usage"
	case Retry:
		return "retry"
	case Error:
		return "error"
	case Done:
		return "done"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Event is one item streamed on a Send/Run channel. Only the fields named for a
// given Kind are populated.
type Event struct {
	Kind             EventKind
	Text             string // Token, Reasoning, Retry, SystemReminder
	ToolName         string // ToolCall, ToolResult
	ToolCallID       string // ToolCall, ToolResult (links a call to its result)
	ToolInput        string // ToolCall (raw JSON args)
	ToolOutput       string // ToolResult
	ToolError        bool   // ToolResult — the tool reported an error (a tool-call hook denial, a dispatch/validation failure, or a host tool failure; bash builtin exit codes are not classified)
	IsHostTool       bool   // ToolCall (resolved against the active agent's host-tool set)
	PromptTokens     int    // Usage, Done
	CompletionTokens int    // Usage, Done
	TotalTokens      int    // Usage, Done
	Err              error  // Error
}

// translate maps an internal chat.Event to a public Event. ok is false when the
// internal event has no public equivalent (session lifecycle, echoed user
// message, post-stream assistant message).
//
// translate is pure: it does NOT resolve Event.IsHostTool, which depends on
// the session's current agent config. route sets that field after translate
// (see route), so this stays a config-free, table-testable mapping.
func translate(ev chat.Event) (Event, bool) {
	switch ev.Kind {
	case chat.EventAssistantToken:
		return Event{Kind: Token, Text: ev.Text}, true
	case chat.EventAssistantReasoning:
		return Event{Kind: Reasoning, Text: ev.Text}, true
	case chat.EventToolCall:
		return Event{Kind: ToolCall, ToolName: ev.ToolName, ToolCallID: ev.ToolCallID, ToolInput: ev.ToolInput}, true
	case chat.EventToolResult:
		return Event{Kind: ToolResult, ToolName: ev.ToolName, ToolCallID: ev.ToolCallID, ToolOutput: ev.ToolOutput, ToolError: ev.ToolError}, true
	case chat.EventSystemReminder:
		return Event{Kind: SystemReminder, Text: ev.Text}, true
	case chat.EventCompacted:
		e := usageEvent(Compacted, ev)
		e.Text = ev.Text
		return e, true
	case chat.EventUsage:
		return usageEvent(Usage, ev), true
	case chat.EventTurnDone:
		return usageEvent(Done, ev), true
	case chat.EventRetry:
		return Event{Kind: Retry, Text: ev.Text}, true
	case chat.EventError:
		err := ev.Err
		if err == nil { // defensive: some emitters may set only Text
			err = errors.New(ev.Text)
		}
		return Event{Kind: Error, Err: err}, true
	default:
		return Event{}, false
	}
}

func usageEvent(k EventKind, ev chat.Event) Event {
	e := Event{Kind: k}
	if ev.Usage != nil {
		e.PromptTokens = ev.Usage.PromptTokens
		e.CompletionTokens = ev.Usage.CompletionTokens
		e.TotalTokens = ev.Usage.TotalTokens
	}
	return e
}
