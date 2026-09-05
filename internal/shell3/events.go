package shell3

import (
	"errors"
	"fmt"

	"github.com/weatherjean/shell3/internal/chat"
)

// EventKind discriminates a streamed Event.
type EventKind int

const (
	Token      EventKind = iota // assistant text         → Text
	Reasoning                   // thinking text           → Text
	ToolCall                    // tool started            → ToolName, ToolInput
	ToolResult                  // tool finished           → ToolName, ToolOutput
	Compacted                   // auto-compaction occurred → Text + post-compaction PromptTokens/TotalTokens estimate
	Usage                       // per-roundtrip tokens    → PromptTokens/CompletionTokens/TotalTokens
	Retry                       // transient retry         → Text
	Error                       // turn error              → Err
	Done                        // turn end (normal)       → token fields (final totals)
)

// Event is one item streamed on a Send/Run channel. Only the fields named for a
// given Kind are populated.
type Event struct {
	Kind             EventKind
	Text             string // Token, Reasoning, Retry
	ToolName         string // ToolCall, ToolResult
	ToolInput        string // ToolCall (raw JSON args)
	ToolOutput       string // ToolResult
	ToolError        bool   // ToolResult — the tool reported an error (a tool-call hook denial, a dispatch/validation failure, or a host tool failure; bash builtin exit codes are not classified)
	PromptTokens     int    // Usage, Done
	CompletionTokens int    // Usage, Done
	TotalTokens      int    // Usage, Done
	Err              error  // Error
}

// translate maps the closed internal chat event set to the front-end event
// set. An unknown kind becomes a typed error instead of disappearing.
func translate(ev chat.Event) Event {
	switch ev.Kind {
	case chat.EventAssistantToken:
		return Event{Kind: Token, Text: ev.Text}
	case chat.EventAssistantReasoning:
		return Event{Kind: Reasoning, Text: ev.Text}
	case chat.EventToolCall:
		return Event{Kind: ToolCall, ToolName: ev.ToolName, ToolInput: ev.ToolInput}
	case chat.EventToolResult:
		return Event{Kind: ToolResult, ToolName: ev.ToolName, ToolOutput: ev.ToolOutput, ToolError: ev.ToolError}
	case chat.EventCompacted:
		e := usageEvent(Compacted, ev)
		e.Text = ev.Text
		return e
	case chat.EventUsage:
		return usageEvent(Usage, ev)
	case chat.EventTurnDone:
		return usageEvent(Done, ev)
	case chat.EventRetry:
		return Event{Kind: Retry, Text: ev.Text}
	case chat.EventError:
		err := ev.Err
		if err == nil { // defensive: some emitters may set only Text
			err = errors.New(ev.Text)
		}
		return Event{Kind: Error, Err: err}
	default:
		return Event{Kind: Error, Err: fmt.Errorf("shell3: unknown chat event kind %d", ev.Kind)}
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
