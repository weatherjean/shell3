//go:build unix

package telegram

import (
	"context"
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
			b.sendReply(ctx, shell3.SettableListText(b.sess.Snapshot().Params))
			return
		}
		name, value, ok := shell3.ParseSetArgs(arg)
		if !ok {
			b.sendReply(ctx, shell3.SetUsageText)
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
		b.sendReply(ctx, shell3.StopAll(b.sess, func() context.CancelFunc {
			b.mu.Lock()
			defer b.mu.Unlock()
			return b.cancelTurn
		}))
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
	case "/voice":
		b.handleVoiceCommand(ctx, arg)
	default:
		b.sendReply(ctx, "unknown command: "+cmd)
	}
}
