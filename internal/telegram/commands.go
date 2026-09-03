//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// BotCommands is shell3's complete Telegram command menu.
func (b *Bot) BotCommands() []Command {
	return []Command{
		{"ask", "Talk to shell3 in a group: /ask <message>"},
		{"help", "How this remote control works"},
		{"stop", "Stop the current turn"},
		{"superstop", "Stop the turn and background commands"},
		{"new", "Start a fresh conversation"},
		{"reload", "Validate and reload shell3.lisp"},
	}
}

func (c *conversation) handleCommand(ctx context.Context, m Msg) {
	fields := strings.Fields(m.Text)
	if len(fields) == 0 { // e.g. "/" followed only by whitespace
		return
	}
	arg := strings.TrimSpace(strings.TrimPrefix(m.Text, fields[0]))
	// Telegram appends "@yourbot" in a group, so route on the bare verb.
	cmd, _, _ := strings.Cut(fields[0], "@")
	switch cmd {
	case "/ask":
		// The group entry point. Privacy mode never delivers a plain
		// "@bot do X", but DOES deliver "/ask@thisbot …" and every reply to
		// the bot's own messages — so /ask opens a thread and replies
		// continue it, with no BotFather toggle and no admin rights. Where
		// the bot already hears everything it is just a longer way to type.
		q := strings.TrimSpace(arg)
		if q == "" {
			c.sendReply(ctx, "usage: `/ask <message>` — then just reply to my messages to continue")
			return
		}
		ask := m
		ask.Text = q
		c.dispatchMail(ctx, []inMail{{m: ask, text: q}})
	case "/help":
		c.sendReply(ctx, c.helpText())
	case "/stop":
		// Turn-only. Background jobs are NEVER killed here: they keep running
		// and wake their session on completion. /superstop kills those too.
		c.mu.Lock()
		cancel := c.cancelTurn
		c.mu.Unlock()
		if cancel != nil {
			cancel()
			c.sendReply(ctx, "⏹ stopped the turn — background jobs keep running (/superstop kills those too)")
		} else {
			c.sendReply(ctx, "nothing is running")
		}
	case "/superstop":
		c.handleSuperstop(ctx)
	case "/new":
		c.handleNewCommand(ctx)
	case "/reload":
		c.b.mu.Lock()
		reload := c.b.reload
		c.b.mu.Unlock()
		if reload == nil {
			c.sendReply(ctx, "reload is unavailable")
		} else if err := reload(); err != nil {
			c.b.log.Warn("config reload failed", "error", err)
			c.sendReply(ctx, "reload failed: "+err.Error())
		} else {
			c.sendReply(ctx, "config reloaded")
		}
	default:
		c.sendReply(ctx, "unknown command: "+cmd)
	}
}

// handleSuperstop is the everything-off switch: cancel the turn, then kill
// every background job with completion routing suppressed. One summary
// replaces the per-job posts — a ⚠️ reply, plus the same text queued unwoken
// so the next turn knows without spending one now.
func (c *conversation) handleSuperstop(ctx context.Context) {
	c.mu.Lock()
	cancel := c.cancelTurn
	main := c.main
	c.mu.Unlock()
	turnStopped := cancel != nil
	if turnStopped {
		cancel()
	}
	var killed []shell3.KilledJob
	if sess := c.b.LiveSession(); sess != nil {
		killed = sess.KillAllForStop()
	}
	if !turnStopped && len(killed) == 0 {
		c.sendReply(ctx, "nothing was running")
		return
	}
	var sb strings.Builder
	sb.WriteString("⚠️ superstop —")
	if turnStopped {
		sb.WriteString(" turn stopped;")
	}
	fmt.Fprintf(&sb, " killed %d job(s)", len(killed))
	for _, k := range killed {
		fmt.Fprintf(&sb, "\n- `%s` %s — %s (ran %s)", k.ID, k.Kind, strutil.Truncate(k.Title, 80), k.Runtime)
	}
	summary := sb.String()
	c.sendReply(ctx, summary)
	if main != nil {
		main.NotifyTextNoWake("[superstop] the user stopped everything. " +
			"Do not resume or retry the killed work unless asked.\n" + summary)
	}
}

// handleNewCommand starts a fresh conversation: detach the current session,
// closing it when no jobs are running (they keep going and re-route to the new
// one), clear the marker, and let the next message create the replacement.
// Refused mid-turn — /stop first.
func (c *conversation) handleNewCommand(ctx context.Context) {
	c.mu.Lock()
	if c.turnActive {
		c.mu.Unlock()
		c.sendReply(ctx, "⚠️ a turn is running — /stop it first, then /new")
		return
	}
	c.mu.Unlock()
	// Clear the marker BEFORE detaching, or a StartFreshTurn racing this sees
	// main==nil with the old marker set and resurrects what is being detached.
	if err := c.setCurrentThread(""); err != nil {
		c.b.log.Warn("current-session marker clear (/new) not persisted", "err", err)
	}
	c.mu.Lock()
	old := c.main
	c.main = nil
	c.mainAnchor = ""
	c.steerAnchor = ""
	c.lastAgentMail = ""
	c.contextMilestone = 0
	c.wakePending = false
	c.mu.Unlock()
	// Close only a fully-idle session: running jobs keep it open, and undrained
	// mail stays durable and searchable rather than torn down undelivered.
	if old != nil && !c.b.sessionHasRunningJob(old) && !old.HasQueuedInput() {
		_ = old.Close()
	}
	// The turn that would have answered the room's report:"always" binds has
	// just been detached with the session, so post their results now: a
	// completion the spawner marked as awaited must not be what /new discards.
	c.flushRequired()
	c.sendReply(ctx, "🧵 fresh conversation — the old one stays searchable in history")
}

// helpText covers what the command list cannot show: that each chat is its own
// conversation, and what a group needs before the bot hears anything. Both are
// configured once and forgotten, so the answer lives one /help away.
func (c *conversation) helpText() string {
	var sb strings.Builder
	sb.WriteString("**shell3** — your agent, one conversation per chat.\n\n")

	c.mu.Lock()
	group := c.isGroup
	c.mu.Unlock()
	if group {
		sb.WriteString("**Talking to me here**\n")
		if c.b.answersAllGroupMessages() {
			sb.WriteString("Every message from an account my operator listed continues this " +
				"conversation; no `/ask`, @mention, or reply is needed. Messages from everyone " +
				"else are still ignored. Telegram must deliver the messages, so I need to be an " +
				"admin here or Group Privacy must be off in @BotFather.\n\n")
		} else {
			sb.WriteString("Start with `/ask <message>`, then just REPLY to my answers to keep going — " +
				"no prefix needed after the first one. An @mention works too, if my operator " +
				"has enabled it (see below).\n\n" +
				"Everything else said in this group is dropped: not stored, not read, never " +
				"sent to a model. Being in this group is not permission either — only the " +
				"accounts my operator listed can drive me.\n\n" +
				"**If an @mention seems ignored**, that is Telegram, not me: a bot with privacy " +
				"mode ON is never delivered plain @mentions, so I never saw it. `/ask` and " +
				"replies always reach me. To enable @mentions, my operator can promote me to " +
				"admin here, or turn Group Privacy off in @BotFather and re-add me.\n\n")
		}
		sb.WriteString(
			"**This group's description** becomes standing context for this room — edit " +
				"it in Telegram and my instructions here change, no config edit needed. " +
				"Telegram only shares it with a bot that can see group info, so if I seem " +
				"unaware of it, promote me to admin here and it arrives within a few minutes " +
				"after the next refresh.\n\n")
	} else {
		sb.WriteString("**In this chat**\nJust type — every message continues our conversation here.\n\n" +
			"**In a group**, start with `/ask <message>` and then reply to my answers to " +
			"keep going. Plain @mentions only reach me if I am an admin there or Group " +
			"Privacy is off in @BotFather — otherwise Telegram never delivers them, and " +
			"`/ask` is the way in. A group's description becomes standing context for " +
			"that room — Telegram shares it only with a bot that can see group info, so " +
			"promote me to admin there if I seem unaware of it.\n\n")
	}

	sb.WriteString("**Separate conversations**\n" +
		"Each chat keeps its own memory of what was said. Nothing here is visible in another " +
		"chat, and `/new` starts fresh only where you type it. That is the point: one room per " +
		"topic keeps each one short and cheap.\n\n")

	sb.WriteString("**Who can use me**\n" +
		"Only the Telegram accounts my operator listed. Anyone else is ignored everywhere, " +
		"whatever they type — being in this group is not permission.\n\n")

	sb.WriteString("**Commands**\n")
	for _, cmd := range c.b.BotCommands() {
		fmt.Fprintf(&sb, "/%s — %s\n", cmd.Command, cmd.Description)
	}
	sb.WriteString("\nIn a group, tack my name on if Telegram asks for it: `/help@yourbot`.")
	return sb.String()
}
