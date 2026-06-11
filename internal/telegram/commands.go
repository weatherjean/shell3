//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// BotCommands is the canonical command list, registered with Telegram for the
// "/" autocomplete menu. Kept next to handleCommand so they stay in sync.
func BotCommands() []Command {
	return []Command{
		{"set", "Set a parameter: /set <name> <value>"},
		{"rollback", "Undo the last turn"},
		{"clear", "Reset the conversation"},
		{"stop", "Stop the current turn"},
		{"run", "Run a scheduled job now: /run <name>"},
		{"reload", "Reload shell3.lua config without restarting"},
	}
}

func (b *Bot) handleCommand(ctx context.Context, m Msg) {
	fields := strings.Fields(m.Text)
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(m.Text, cmd))
	switch cmd {
	case "/clear":
		if err := b.sess.Clear(); err != nil {
			b.sendReply(ctx, "clear failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "🧹 cleared")
	case "/set":
		if arg == "" {
			b.sendReply(ctx, b.settableList())
			return
		}
		kv := strings.SplitN(arg, " ", 2)
		if len(kv) != 2 {
			b.sendReply(ctx, "usage: /set <name> <value>\nsend /set with no arguments to list settable parameters")
			return
		}
		if err := b.sess.SetParam(kv[0], kv[1]); err != nil {
			b.sendReply(ctx, "set failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "⚙️ "+kv[0]+" = "+kv[1])
	case "/rollback":
		ok, err := b.sess.Rollback()
		if err != nil {
			b.sendReply(ctx, "rollback failed: "+err.Error())
			return
		}
		if !ok {
			b.sendReply(ctx, "nothing to roll back")
			return
		}
		b.sendReply(ctx, "↩️ rolled back")
	case "/stop":
		if c := b.cancelTurn; c != nil {
			c()
			b.sendReply(ctx, "⏹ stopped")
			return
		}
		b.sendReply(ctx, "nothing running")
	case "/run":
		if b.runJob == nil {
			b.sendReply(ctx, "no scheduled jobs configured")
			return
		}
		name := strings.TrimSpace(arg)
		if name == "" {
			b.sendReply(ctx, "usage: /run <job>")
			return
		}
		if err := b.runJob(name); err != nil {
			b.sendReply(ctx, "run failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "▶️ fired job "+name)
	case "/reload":
		if b.reload == nil {
			b.sendReply(ctx, "reload not available")
			return
		}
		res, err := b.reload()
		if err != nil {
			b.sendReply(ctx, "❌ reload failed: "+err.Error())
			return
		}
		b.sendReply(ctx, formatReload(res))
	default:
		b.sendReply(ctx, "unknown command: "+cmd)
	}
}

// formatReload renders a ReloadResult as a chat reply.
func formatReload(r shell3.ReloadResult) string {
	msg := fmt.Sprintf("✅ reloaded — %d agents, %d models, %d jobs", r.Agents, r.Models, r.Jobs)
	if len(r.Notes) > 0 {
		msg += "\n• " + strings.Join(r.Notes, "\n• ")
	}
	return msg
}

// settableList renders the agent's tunable parameters with their current value
// (falling back to the provider default) and allowed values, for a bare /set.
func (b *Bot) settableList() string {
	params := b.sess.Snapshot().Params
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
