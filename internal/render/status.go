package render

import (
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

// Status renders the agent's effective state: model, tools, subagents, skills,
// config warnings, the command gate, and the system prompt in force. Either
// argument may be nil — a front-end reporting status before the runtime is up
// still gets a document.
func Status(sess *shell3.Session, rt *shell3.Runtime, version string) string {
	var b strings.Builder
	b.WriteString("# shell3 status\n\n")
	field(&b, "version", version)

	if rt != nil {
		if dir, err := rt.ConfigDir(); err == nil {
			field(&b, "config", dir)
		}
	}
	if sess == nil {
		b.WriteString("\n_No live session._\n")
		return b.String()
	}

	snap := sess.Snapshot()
	field(&b, "agent", snap.Agent)
	field(&b, "model", snap.Model)
	field(&b, "status line", snap.StatusLine)
	if snap.ContextWindow > 0 {
		field(&b, "context window", fmt.Sprintf("%d tokens", snap.ContextWindow))
	}
	field(&b, "messages in context", fmt.Sprintf("%d", sess.MessageCount()))
	gate := "not armed (no tool-call hook)"
	if snap.ToolHooksOn {
		gate = "armed"
	}
	field(&b, "command gate", gate)
	b.WriteString("\n")

	if len(snap.Params) > 0 {
		parts := make([]string, 0, len(snap.Params))
		for _, p := range snap.Params {
			value := p.Value
			if value == "" {
				value = p.Default
			}
			if value == "" {
				continue
			}
			parts = append(parts, p.Name+"="+value)
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "**params:** %s\n\n", strings.Join(parts, ", "))
		}
	}

	// Names, not descriptions. /status is a glance at what is going on, and a
	// full tool manifest pushed it past Telegram's message cap — which forced
	// it into a document, which is where tappable commands stop working. The
	// descriptions are the model's business; `shell3 health` prints them.
	if len(snap.Tools) > 0 {
		names := make([]string, 0, len(snap.Tools))
		for _, t := range snap.Tools {
			names = append(names, "`"+t.Name+"`")
		}
		fmt.Fprintf(&b, "**tools** (%d): %s\n\n", len(names), strings.Join(names, ", "))
	}

	if len(snap.Subagents) > 0 {
		fmt.Fprintf(&b, "**employees:** %s\n\n", strings.Join(snap.Subagents, ", "))
	}
	if len(snap.Skills) > 0 {
		fmt.Fprintf(&b, "**skills** (%d): %s\n\n", len(snap.Skills), strings.Join(snap.Skills, ", "))
	}

	if len(snap.MCP) > 0 {
		b.WriteString("## MCP servers\n\n")
		for _, s := range snap.MCP {
			state := fmt.Sprintf("%d tools", s.ToolCount)
			if !s.Up {
				state = "down"
				if s.Err != "" {
					state += " — " + oneLine(s.Err, 120)
				}
			}
			fmt.Fprintf(&b, "- `%s` — %s\n", s.Name, state)
		}
		b.WriteString("\n")
	}

	writeList(&b, "Warnings", snap.Warnings)

	// The system prompt is deliberately NOT here. It is thousands of tokens of
	// text the operator wrote and can read in shell3.sh, and dumping it made
	// every /status a document.
	return b.String()
}

func writeList(b *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", heading)
	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", it)
	}
	b.WriteString("\n")
}
