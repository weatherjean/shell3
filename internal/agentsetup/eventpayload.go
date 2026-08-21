package agentsetup

import (
	"encoding/json"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/strutil"
)

// eventTextCap bounds the text and tool-output fields in one event payload.
// A subscriber gets a summary line, not an unbounded assistant message or a
// megabyte of tool output re-serialised and piped into a shell on every turn —
// the full content is already in the runs store, which is where a hook that
// genuinely needs it should look.
const eventTextCap = 4096

// eventJSON is the stable wire shape an `event:` subscriber reads on stdin.
// Field names are snake_case and deliberately short: they are read by shell
// scripts through jq, not by Go.
type eventJSON struct {
	Event   string `json:"event"`
	Agent   string `json:"agent"`
	Session string `json:"session,omitempty"`
	Time    string `json:"time"`

	Text string `json:"text,omitempty"`
	Role string `json:"role,omitempty"`

	Tool      string `json:"tool,omitempty"`
	ToolInput string `json:"tool_input,omitempty"`
	Output    string `json:"output,omitempty"`
	ToolError bool   `json:"tool_error,omitempty"`
	ToolCall  string `json:"tool_call_id,omitempty"`

	Usage *eventUsageJSON `json:"usage,omitempty"`

	Meta map[string]string `json:"meta,omitempty"`
}

type eventUsageJSON struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// eventPayload renders one event for its subscriber. Never returns an error:
// every field is a plain string or int, so the marshal cannot fail, and an
// observer must not be able to break a turn by receiving something odd.
func eventPayload(agent string, ev chat.Event) []byte {
	t := ev.Time
	if t.IsZero() {
		t = time.Now()
	}
	p := eventJSON{
		Event:     ev.Kind.String(),
		Agent:     agent,
		Session:   ev.SessionID,
		Time:      t.UTC().Format(time.RFC3339),
		Text:      strutil.Truncate(ev.Text, eventTextCap),
		Role:      ev.Role,
		Tool:      ev.ToolName,
		ToolInput: strutil.Truncate(ev.ToolInput, eventTextCap),
		Output:    strutil.Truncate(ev.ToolOutput, eventTextCap),
		ToolError: ev.ToolError,
		ToolCall:  ev.ToolCallID,
		Meta:      ev.Meta,
	}
	if ev.Usage != nil {
		p.Usage = &eventUsageJSON{
			PromptTokens:     ev.Usage.PromptTokens,
			CompletionTokens: ev.Usage.CompletionTokens,
			TotalTokens:      ev.Usage.TotalTokens,
			CachedTokens:     ev.Usage.CachedTokens,
		}
	}
	b, err := json.Marshal(p)
	if err != nil { // unreachable: every field is a plain scalar
		return []byte(`{"event":"` + ev.Kind.String() + `"}`)
	}
	return b
}
