//go:build unix

package telegram

import (
	"context"
	"strings"
)

// BotCommands is the canonical command list, registered with Telegram for the
// "/" autocomplete menu. Kept next to handleCommand so they stay in sync.
func BotCommands() []Command {
	return []Command{
		{"stop", "Stop the current turn"},
		{"run", "Run a scheduled job now: /run <name>"},
		{"jobs", "List running background tasks"},
		{"cancel", "Cancel a background task: /cancel <id>"},
		{"reload", "Reload the config without restarting"},
		{"voice", "Voice replies: /voice off|inbound|always"},
	}
}

func (b *Bot) handleCommand(ctx context.Context, m Msg) {
	fields := strings.Fields(m.Text)
	if len(fields) == 0 { // e.g. "/" followed only by whitespace
		return
	}
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(m.Text, cmd))
	switch cmd {
	case "/stop":
		// Turn-only: cancel the active main turn. Background jobs are NEVER killed
		// here — a subagent or bash_bg keeps running and will wake its session on
		// completion. Cancel those with /jobs and /cancel <id>.
		b.mu.Lock()
		c := b.cancelTurn
		b.mu.Unlock()
		if c != nil {
			c()
			b.sendReply(ctx, "⏹ stopped the turn — background jobs keep running (list them with /jobs, cancel one with /cancel `<id>`)")
		} else {
			b.sendReply(ctx, "nothing is running")
		}
	case "/run":
		if b.runJob == nil {
			b.sendReply(ctx, "no scheduled jobs configured")
			return
		}
		name := strings.TrimSpace(arg)
		if name == "" {
			b.sendReply(ctx, "usage: /run `<job>`")
			return
		}
		if err := b.runJob(name); err != nil {
			b.sendReply(ctx, "run failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "▶️ fired job "+name)
	case "/jobs":
		// Deterministic, zero-token job control from the phone: lists running
		// subagents and bash_bg commands. Natural-language "what's running?"
		// still works via the agent's task_list tool; this is the direct path.
		if b.listJobs == nil {
			b.sendReply(ctx, "job control not available")
			return
		}
		if out := b.listJobs(); out != "" {
			b.sendReply(ctx, out)
		} else {
			b.sendReply(ctx, "no background jobs running")
		}
	case "/cancel":
		if b.cancelJob == nil {
			b.sendReply(ctx, "job control not available")
			return
		}
		id := strings.TrimSpace(arg)
		if id == "" {
			b.sendReply(ctx, "usage: /cancel `<id>`")
			return
		}
		if err := b.cancelJob(id); err != nil {
			b.sendReply(ctx, err.Error())
			return
		}
		b.sendReply(ctx, "🛑 cancelled task "+id)
	case "/reload":
		// runReload takes the turn slot (and Reload fail-fasts on a busy
		// session), so a /reload during a live turn is refused, not raced.
		b.runReload(ctx)
	case "/voice":
		b.handleVoiceCommand(ctx, arg)
	default:
		b.sendReply(ctx, "unknown command: "+cmd)
	}
}
