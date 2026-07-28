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
		b.WriteString("## Parameters\n\n")
		for _, p := range snap.Params {
			value := p.Value
			if value == "" {
				value = p.Default + " (default)"
			}
			field(&b, p.Name, value)
		}
		b.WriteString("\n")
	}

	if len(snap.Tools) > 0 {
		b.WriteString("## Tools\n\n")
		for _, t := range snap.Tools {
			fmt.Fprintf(&b, "- `%s` — %s\n", t.Name, oneLine(t.Description, 120))
		}
		b.WriteString("\n")
	}

	writeList(&b, "Subagents", snap.Subagents)
	writeList(&b, "Skills", snap.Skills)

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

	if rt != nil {
		if jobs := rt.Cron(); len(jobs) > 0 {
			b.WriteString("## Cron\n\n")
			for _, j := range jobs {
				fmt.Fprintf(&b, "- `%s` — %s → %s\n", j.Name, j.Schedule, j.Agent)
			}
			b.WriteString("\n")
		}
		if parts := rt.Parts(); parts != nil {
			if projects := parts.Projects(); len(projects) > 0 {
				b.WriteString("## Projects\n\n")
				for _, p := range projects {
					fmt.Fprintf(&b, "- `%s` — %s\n", p.Name, oneLine(p.Description, 120))
				}
				b.WriteString("\n")
			}
		}
	}

	writeList(&b, "Warnings", snap.Warnings)

	if snap.SystemPrompt != "" {
		b.WriteString("## System prompt\n\n")
		fence(&b, "", snap.SystemPrompt)
	}
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
