//go:build unix

package telegram

import (
	"context"
	"fmt"
	"net"
	neturl "net/url"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// KitCommand is a verb the bot answers by running a shell function, with no
// model turn.
type KitCommand struct {
	Name, Desc string
}

// SetKitCommands installs the kit's commands. A nil run advertises them
// without making them answerable, for health checks and menu tests.
func (b *Bot) SetKitCommands(cmds []KitCommand, run func(ctx context.Context, name, arg string) (string, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.kitCommands, b.kitCommandRun = cmds, run
}

// BotCommands is the built-ins plus the kit's, in that order — what is
// registered as Telegram's "/" autocomplete menu.
func (b *Bot) BotCommands() []Command {
	out := BotCommands()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.kitCommands {
		out = append(out, Command{c.Name, c.Desc})
	}
	return out
}

// BotCommands is the canonical BUILT-IN list, next to handleCommand so the
// two stay in sync. The views live in the web dash; these are actions.
func BotCommands() []Command {
	return []Command{
		{"ask", "Talk to the agent in a group: /ask <message>"},
		{"help", "How this bot works — chats, groups, commands"},
		{"dash", "Open the web dashboard (link valid ~1h)"},
		{"stop", "Stop the current turn"},
		{"superstop", "Stop the turn AND every background job"},
		{"new", "Start a fresh conversation (the old one stays in the dash)"},
		{"run", "Run a scheduled job now: /run <name>"},
		{"btw", "Ask something outside this conversation: /btw <question>"},
		{"reload", "Reload the config without restarting"},
		{"quiet", "Hush background posts: /quiet on|off"},
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
	case "/dash":
		c.handleDashCommand(ctx, m, arg)
	case "/run":
		run := c.b.jobRunner()
		if run == nil {
			c.sendReply(ctx, "no scheduled jobs configured")
			return
		}
		name := strings.TrimSpace(arg)
		if name == "" {
			c.sendReply(ctx, "usage: /run `<job>`")
			return
		}
		if err := run(name); err != nil {
			c.sendReply(ctx, "run failed: "+err.Error())
			return
		}
		c.sendReply(ctx, "▶️ fired job "+name)
	case "/btw":
		// An aside: a one-off turn in its own child session. It touches
		// neither the transcript nor the queue, so "what's the syntax for X"
		// mid-project is not carried forward forever — and being a background
		// dispatch it bypasses the turn slot, answering while a long turn runs.
		q := strings.TrimSpace(arg)
		if q == "" {
			c.sendReply(ctx, "usage: /btw `<question>` — answered outside the conversation")
			return
		}
		sess, err := c.mainSession()
		if err != nil {
			c.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		if _, err := sess.Dispatch("", q, shell3.DispatchOpts{
			Description: "btw: " + strutil.Truncate(q, 40),
			Detached:    true,
		}); err != nil {
			c.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		c.sendReply(ctx, "💬 asked on the side — the answer won't enter this conversation")
	case "/reload":
		// runReload takes the turn slot, so a /reload during a live turn is
		// refused rather than raced.
		c.runReload(ctx)
	case "/quiet":
		c.handleQuietCommand(ctx, arg)
	case "/new":
		c.handleNewCommand(ctx)
	default:
		// Built-ins matched above always win; a kit command named after one is
		// rejected at load rather than shadowing here.
		if c.runKitCommand(ctx, strings.TrimPrefix(cmd, "/"), arg) {
			return
		}
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

// handleDashCommand answers a bare /dash with a freshly tokened URL, host-side
// and free. /dash with an argument is a request for help — usually exposing
// the dash — and becomes an agent turn pointed at the exposing skill.
func (c *conversation) handleDashCommand(ctx context.Context, m Msg, arg string) {
	if arg != "" {
		prompt := fmt.Sprintf("The user wants help with the web dash: %q. "+
			"If this is about exposing/reaching the dash, read the dash-exposing skill "+
			"(listed under ## Skills) and follow it.", arg)
		c.dispatchMail(ctx, []inMail{{m: m, text: prompt}})
		return
	}
	c.mu.Lock()
	dash := c.b.dashURL
	c.mu.Unlock()
	if dash == nil {
		c.sendReply(ctx, "the dash is not running (dash_port: 0 disables it, or the listener failed at startup — check the app log)")
		return
	}
	link, err := dash()
	if err != nil {
		c.sendReply(ctx, "⚠️ "+err.Error())
		return
	}
	reply := "🖥 " + link + "\n\nlink valid ~1h — /dash mints a fresh one"
	// The base URL comes from dash_url.txt, which the exposure agent — and so
	// anything that can prompt-inject it — can rewrite, and a fresh token is
	// appended to whatever host it names. So a non-loopback destination is
	// named and warned about, never handed over silently.
	if host := urlHost(link); host != "" && !isLoopbackHost(host) {
		reply += "\n\n⚠️ this link points at `" + host + "` (not this machine) — " +
			"the token grants read access to your transcripts and config. " +
			"Only open it if you set up that address yourself."
	}
	c.sendReply(ctx, reply)
}

// urlHost returns the hostname of rawURL, or "" if it doesn't parse.
func urlHost(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// isLoopbackHost: the only destination a /dash link reaches without a tunnel.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
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
	if err := c.index.SetCurrent(""); err != nil {
		c.b.log.Warn("current-session marker clear (/new) not persisted", "err", err)
	}
	c.mu.Lock()
	old := c.main
	c.main = nil
	c.mainAnchor = ""
	c.steerAnchor = ""
	c.lastAgentMail = ""
	c.wakePending = false
	c.mu.Unlock()
	// Close only a fully-idle session: running jobs keep it open, and undrained
	// mail stays inspectable in the dash rather than torn down undelivered.
	if old != nil && !c.b.sessionHasRunningJob(old) && !old.HasQueuedInput() {
		_ = old.Close()
	}
	// The turn that would have answered the room's report:"always" binds has
	// just been detached with the session, so post their results now: a
	// completion the spawner marked as awaited must not be what /new discards.
	c.flushRequired()
	c.sendReply(ctx, "🧵 fresh conversation — the old one stays in the dash and history")
}

// handleQuietCommand reports or sets /quiet: on, agent-initiated posts arrive
// without a ping. Replies to the user and ⚠️ failures always ring.
func (c *conversation) handleQuietCommand(ctx context.Context, arg string) {
	state := func() string {
		if c.b.isQuiet() {
			return "🔕 quiet is on — background posts arrive without a ping."
		}
		return "🔔 quiet is off — every post rings."
	}
	switch arg {
	case "":
		c.sendReply(ctx, state())
	case "on", "off":
		c.mu.Lock()
		store := c.b.quietMode
		c.mu.Unlock()
		if store == nil || store.Path == "" {
			c.sendReply(ctx, "⚠️ quiet mode has nowhere to persist (no store configured).")
			return
		}
		if err := store.Set(arg == "on"); err != nil {
			c.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		c.sendReply(ctx, state())
	default:
		c.sendReply(ctx, "usage: /quiet on|off")
	}
}

// runKitCommand answers one kit command, reporting whether it claimed the
// verb. Stdout is the reply and empty posts nothing, so an idempotent command
// stays silent. A failure is reported, not swallowed: a command grants and
// blocks nothing, so there is no fail-closed question.
func (c *conversation) runKitCommand(ctx context.Context, name, arg string) bool {
	c.mu.Lock()
	run := c.b.kitCommandRun
	declared := false
	for _, kc := range c.b.kitCommands {
		if kc.Name == name {
			declared = true
			break
		}
	}
	c.mu.Unlock()
	if !declared || run == nil {
		return false
	}
	out, err := run(ctx, name, arg)
	switch {
	case err != nil:
		c.sendReply(ctx, "/"+name+" failed: "+err.Error())
	case strings.TrimSpace(out) != "":
		c.sendReply(ctx, out)
	}
	return true
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
		sb.WriteString("**Talking to me here**\n" +
			"Start with `/ask <message>`, then just REPLY to my answers to keep going — " +
			"no prefix needed after the first one. An @mention works too, if my operator " +
			"has enabled it (see below).\n\n" +
			"Everything else said in this group is dropped: not stored, not read, never " +
			"sent to a model. Being in this group is not permission either — only the " +
			"accounts my operator listed can drive me.\n\n" +
			"**If an @mention seems ignored**, that is Telegram, not me: a bot with privacy " +
			"mode ON is never delivered plain @mentions, so I never saw it. `/ask` and " +
			"replies always reach me. To enable @mentions, my operator can promote me to " +
			"admin here, or turn Group Privacy off in @BotFather and re-add me.\n\n" +
			"**This group's description** becomes standing context for this room — edit " +
			"it in Telegram and my instructions here change, no config edit needed. " +
			"Telegram only shares it with a bot that can see group info, so if I seem " +
			"unaware of it, promote me to admin here and it arrives within a few minutes " +
			"(or right away after `/reload`).\n\n")
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
