//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

// BotCommands is the canonical command list, registered with Telegram for the
// "/" autocomplete menu. Kept next to handleCommand so they stay in sync.
func BotCommands() []Command {
	return []Command{
		{"set", "Set a parameter: /set <name> <value>"},
		{"rollback", "Undo the last turn"},
		{"clear", "Reset the conversation"},
		{"compact", "Summarize old context to free tokens"},
		{"stop", "Stop the current turn"},
		{"run", "Run a scheduled job now: /run <name>"},
		{"reload", "Reload shell3.lua config without restarting"},
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
	case "/clear":
		if err := b.sess.Clear(); err != nil {
			b.sendReply(ctx, "clear failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "🧹 cleared")
	case "/compact":
		// The compaction is one LLM round-trip (minutes on a long history), so it
		// must NOT run inline on the update loop. Take the turn slot exactly like
		// a user turn: the loop stays live, messages arriving mid-compaction are
		// Interjected (not bounced with ErrBusy), and /stop's cancelTurn aborts
		// the summarisation call.
		b.mu.Lock()
		if b.turnActive {
			b.mu.Unlock()
			b.sendReply(ctx, shell3.CompactReplyText(0, 0, shell3.ErrBusy))
			return
		}
		cctx, cancel := context.WithCancel(ctx)
		b.cancelTurn = cancel
		b.turnActive = true
		b.mu.Unlock()
		stopTyping := b.keepTyping(ctx)
		go func() {
			before, after, err := b.sess.Compact(cctx)
			stopTyping()
			b.mu.Lock()
			b.cancelTurn = nil
			b.turnActive = false
			b.mu.Unlock()
			cancel()
			b.sendReply(ctx, shell3.CompactReplyText(before, after, err))
		}()
	case "/set":
		if arg == "" {
			b.sendReply(ctx, b.settableList())
			return
		}
		// Split on any whitespace run (double spaces and tabs are easy to
		// type on mobile), keeping the raw remainder as the value.
		name := strings.Fields(arg)[0]
		value := strings.TrimSpace(arg[strings.Index(arg, name)+len(name):])
		if value == "" {
			b.sendReply(ctx, "usage: /set <name> <value>\nsend /set with no arguments to list settable parameters")
			return
		}
		if err := b.sess.SetParam(name, value); err != nil {
			b.sendReply(ctx, "set failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "⚙️ "+name+" = "+value)
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
		// Kill every running background job on the in-process runtime — commands
		// (bash_bg) and model-spawned subagents alike are tracked jobs now.
		killed := 0
		for _, j := range b.sess.Jobs() {
			if !j.Done {
				if err := b.sess.KillJob(j.ID); err == nil {
					killed++
				}
			}
		}
		// Snapshot the cancel func AFTER the kill loop: a turn that ends (and a
		// queued wake turn that starts) mid-loop would leave a pre-loop snapshot
		// cancelling an already-dead context while the fresh turn runs on.
		b.mu.Lock()
		c := b.cancelTurn
		b.mu.Unlock()
		if c != nil {
			c() // cancels turnCtx → synchronous bash/node process groups get SIGTERM→SIGKILL
			msg := "⏹ stopped"
			if killed > 0 {
				msg += fmt.Sprintf(" — killed %d background job(s)", killed)
			}
			b.sendReply(ctx, msg)
			return
		}
		if killed > 0 {
			b.sendReply(ctx, fmt.Sprintf("⏹ no turn running — killed %d background job(s)", killed))
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
		// runReload takes the turn slot (and Reload fail-fasts on a busy
		// session), so a /reload during a live turn is refused, not raced.
		b.runReload(ctx)
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
