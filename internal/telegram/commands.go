//go:build unix

package telegram

import (
	"context"
	"strings"
)

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
	case "/agents":
		b.sendReply(ctx, "agents: "+strings.Join(b.sess.AgentNames(), ", "))
	case "/agent":
		if err := b.sess.SwitchAgent(arg); err != nil {
			b.sendReply(ctx, "switch failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "🤖 agent → "+b.sess.ActiveAgent())
	case "/set":
		kv := strings.SplitN(arg, " ", 2)
		if len(kv) != 2 {
			b.sendReply(ctx, "usage: /set <name> <value>")
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
	case "/dash":
		if b.dashURL == "" {
			b.sendReply(ctx, "dashboard is disabled")
			return
		}
		// Send a Web App button so tapping opens the dashboard as a Mini App
		// inside Telegram (with initData), not the external browser.
		_, _ = b.client.Send(ctx, b.chatID, "📊 Conversation dashboard",
			[]Button{{Text: "Open dashboard", WebApp: b.dashURL}})
	default:
		b.sendReply(ctx, "unknown command: "+cmd)
	}
}
