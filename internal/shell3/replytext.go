package shell3

import (
	"fmt"
	"strings"
)

// Shared chat-reply rendering for the host front-ends. Like CompactReplyText,
// both front-ends (Telegram, web) send exactly these strings, so the wording
// lives in one place next to the APIs it describes.

// SettableListText renders the agent's tunable parameters with their current
// value (falling back to the provider default) and allowed values, for a bare
// /set.
func SettableListText(params []ParamValue) string {
	if len(params) == 0 {
		return "no settable parameters for this model"
	}
	var sb strings.Builder
	sb.WriteString("⚙️ settable parameters — /set <name> <value>:\n")
	for _, p := range params {
		val := p.Value
		switch {
		case val == "" && p.Default != "":
			val = p.Default + " (default)"
		case val == "":
			val = "unset"
		}
		sb.WriteString("• " + p.Name + " = " + val)
		if len(p.Enum) > 0 {
			sb.WriteString(" [" + strings.Join(p.Enum, " | ") + "]")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ParseSetArgs splits a /set argument string into the parameter name and the
// raw remainder as the value. Split on any whitespace run (double spaces and
// tabs are easy to type on mobile). ok is false — and both parts empty — when
// either part is missing.
func ParseSetArgs(arg string) (name, value string, ok bool) {
	arg = strings.TrimSpace(arg)
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return "", "", false
	}
	name = fields[0] // arg is trimmed, so it starts with name
	value = strings.TrimSpace(arg[len(name):])
	if value == "" {
		return "", "", false
	}
	return name, value, true
}

// ReloadReplyText renders a reload coordinator's result as the chat reply.
func ReloadReplyText(res ReloadResult, err error) string {
	if err != nil {
		return "❌ reload failed: " + err.Error()
	}
	msg := fmt.Sprintf("✅ reloaded — %d agents, %d models, %d jobs", res.Agents, res.Models, res.Jobs)
	if len(res.Notes) > 0 {
		msg += "\n• " + strings.Join(res.Notes, "\n• ")
	}
	return msg
}

// SetUsageText is the reply for a /set whose arguments don't parse — the
// wording documents ParseSetArgs's contract, so it lives beside it.
const SetUsageText = "usage: /set <name> <value>\nsend /set with no arguments to list settable parameters"

// StopReplyText renders a /stop outcome: whether a running turn was cancelled
// and how many background jobs were killed.
func StopReplyText(cancelled bool, killed int) string {
	switch {
	case cancelled && killed > 0:
		return fmt.Sprintf("⏹ stopped — killed %d background job(s)", killed)
	case cancelled:
		return "⏹ stopped"
	case killed > 0:
		return fmt.Sprintf("⏹ no turn running — killed %d background job(s)", killed)
	default:
		return "nothing running"
	}
}
