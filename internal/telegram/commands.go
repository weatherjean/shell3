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

// BotCommands is the canonical command list, registered with Telegram for the
// "/" autocomplete menu. Kept next to handleCommand so they stay in sync.
// The views (/status, /jobs, /runs) live in the web dash — /dash opens it;
// what remains here are actions.
func BotCommands() []Command {
	return []Command{
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

func (b *Bot) handleCommand(ctx context.Context, m Msg) {
	fields := strings.Fields(m.Text)
	if len(fields) == 0 { // e.g. "/" followed only by whitespace
		return
	}
	arg := strings.TrimSpace(strings.TrimPrefix(m.Text, fields[0]))
	// Telegram appends "@yourbot" to a command typed in a group (and some
	// clients do so after an autocomplete tap), so route on the bare verb.
	cmd, _, _ := strings.Cut(fields[0], "@")
	switch cmd {
	case "/stop":
		// Turn-only: cancel the active main turn. Background jobs are NEVER killed
		// here — a subagent or bash_bg keeps running and will wake its session on
		// completion. /superstop kills those too.
		b.mu.Lock()
		c := b.cancelTurn
		b.mu.Unlock()
		if c != nil {
			c()
			b.sendReply(ctx, "⏹ stopped the turn — background jobs keep running (/superstop kills those too)")
		} else {
			b.sendReply(ctx, "nothing is running")
		}
	case "/superstop":
		b.handleSuperstop(ctx)
	case "/dash":
		b.handleDashCommand(ctx, m, arg)
	case "/run":
		run := b.jobRunner()
		if run == nil {
			b.sendReply(ctx, "no scheduled jobs configured")
			return
		}
		name := strings.TrimSpace(arg)
		if name == "" {
			b.sendReply(ctx, "usage: /run `<job>`")
			return
		}
		if err := run(name); err != nil {
			b.sendReply(ctx, "run failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "▶️ fired job "+name)
	case "/btw":
		// An aside: a one-off turn in its own child session, dispatched to the
		// job runtime. It never touches the conversation — not the transcript,
		// not the queue — so asking "what's the syntax for X" mid-project does
		// not become something the agent carries forward forever.
		//
		// Because it is a background dispatch it also bypasses the turn slot,
		// so it answers WHILE a long turn is still running.
		q := strings.TrimSpace(arg)
		if q == "" {
			b.sendReply(ctx, "usage: /btw `<question>` — answered outside the conversation")
			return
		}
		sess, err := b.mainSession()
		if err != nil {
			b.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		if _, err := sess.Dispatch("", q, shell3.DispatchOpts{
			Description: "btw: " + strutil.Truncate(q, 40),
			Detached:    true,
		}); err != nil {
			b.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		b.sendReply(ctx, "💬 asked on the side — the answer won't enter this conversation")
	case "/reload":
		// runReload takes the turn slot (and Reload fail-fasts on a busy
		// session), so a /reload during a live turn is refused, not raced.
		b.runReload(ctx)
	case "/quiet":
		b.handleQuietCommand(ctx, arg)
	case "/new":
		b.handleNewCommand(ctx)
	default:
		b.sendReply(ctx, "unknown command: "+cmd)
	}
}

// handleSuperstop is the everything-off switch: cancel the running turn, then
// kill every background job — subagents, bash_bg, in-flight cron dispatches —
// with completion routing suppressed. One summary replaces the per-job posts:
// a ⚠️ reply to the user, and the same text queued (unwoken) into the main
// conversation so the agent's next turn knows what happened without spending
// a turn now.
func (b *Bot) handleSuperstop(ctx context.Context) {
	b.mu.Lock()
	c := b.cancelTurn
	main := b.main
	b.mu.Unlock()
	turnStopped := c != nil
	if turnStopped {
		c()
	}
	var killed []shell3.KilledJob
	if sess := b.anyLiveSession(); sess != nil {
		killed = sess.KillAllForStop()
	}
	if !turnStopped && len(killed) == 0 {
		b.sendReply(ctx, "nothing was running")
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
	b.sendReply(ctx, summary)
	if main != nil {
		main.NotifyTextNoWake("[superstop] the user stopped everything. " +
			"Do not resume or retry the killed work unless asked.\n" + summary)
	}
}

// handleDashCommand answers a bare /dash with a freshly tokened dashboard
// URL (host-side, zero tokens spent). /dash with an argument is a request for
// help — most commonly exposing the dash beyond localhost — and becomes a
// normal agent turn pointed at the dash-exposing skill.
func (b *Bot) handleDashCommand(ctx context.Context, m Msg, arg string) {
	if arg != "" {
		prompt := fmt.Sprintf("The user wants help with the web dash: %q. "+
			"If this is about exposing/reaching the dash, read the dash-exposing skill "+
			"(listed under ## Skills) and follow it.", arg)
		b.dispatchMail(ctx, []inMail{{m: m, text: prompt}})
		return
	}
	b.mu.Lock()
	dash := b.dashURL
	b.mu.Unlock()
	if dash == nil {
		b.sendReply(ctx, "the dash is not running (dash_port: 0 disables it, or the listener failed at startup — check the app log)")
		return
	}
	link, err := dash()
	if err != nil {
		b.sendReply(ctx, "⚠️ "+err.Error())
		return
	}
	reply := "🖥 " + link + "\n\nlink valid ~1h — /dash mints a fresh one"
	// The base URL comes from dash_url.txt, which the exposure agent (and thus
	// anything that can prompt-inject it) can rewrite. A fresh valid token is
	// appended to whatever host that file names, so a planted host would be
	// handed a live read-token. Never silently: when the destination is not
	// loopback, name the host and warn, so the operator sees where the token
	// is about to go before tapping.
	if host := urlHost(link); host != "" && !isLoopbackHost(host) {
		reply += "\n\n⚠️ this link points at `" + host + "` (not this machine) — " +
			"the token grants read access to your transcripts and config. " +
			"Only open it if you set up that address yourself."
	}
	b.sendReply(ctx, reply)
}

// urlHost returns the hostname of rawURL, or "" if it doesn't parse.
func urlHost(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// isLoopbackHost reports whether host is the local machine — the only
// destination a /dash link reaches without an operator-set tunnel.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// handleNewCommand starts a fresh conversation: the current main session is
// detached (and Closed when it has no running jobs — jobs keep running and
// their completions re-route to the new conversation), the current-marker is
// cleared, and the next message lazily creates the replacement. Refused while
// a turn is running — /stop first.
func (b *Bot) handleNewCommand(ctx context.Context) {
	b.mu.Lock()
	if b.turnActive {
		b.mu.Unlock()
		b.sendReply(ctx, "⚠️ a turn is running — /stop it first, then /new")
		return
	}
	b.mu.Unlock()
	// Clear the marker BEFORE detaching: a completion's StartFreshTurn racing
	// this would otherwise see main==nil with the old marker still set and
	// resurrect the conversation being detached.
	if err := b.current.SetCurrent(""); err != nil {
		b.log.Warn("current-session marker clear (/new) not persisted", "err", err)
	}
	b.mu.Lock()
	old := b.main
	b.main = nil
	b.mainAnchor = ""
	b.steerAnchor = ""
	b.lastAgentMail = ""
	b.wakePending = false
	b.mu.Unlock()
	// Close only a fully-idle old session: running jobs keep it open (their
	// completions re-route to the new conversation), and undrained agent mail
	// stays inspectable in the dash instead of being torn down undelivered.
	if old != nil && !b.sessionHasRunningJob(old) && !old.HasQueuedInput() {
		_ = old.Close()
	}
	b.sendReply(ctx, "🧵 fresh conversation — the old one stays in the dash and history")
}

// handleQuietCommand reports or sets the /quiet toggle: quiet on sends
// agent-initiated posts (⏰/🔔/✉️) without a notification ping; replies to
// the user's own messages and ⚠️ failures always ring.
func (b *Bot) handleQuietCommand(ctx context.Context, arg string) {
	state := func() string {
		if b.isQuiet() {
			return "🔕 quiet is on — background posts arrive without a ping."
		}
		return "🔔 quiet is off — every post rings."
	}
	switch arg {
	case "":
		b.sendReply(ctx, state())
	case "on", "off":
		b.mu.Lock()
		store := b.quietMode
		b.mu.Unlock()
		if store == nil || store.Path == "" {
			b.sendReply(ctx, "⚠️ quiet mode has nowhere to persist (no store configured).")
			return
		}
		if err := store.Set(arg == "on"); err != nil {
			b.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		b.sendReply(ctx, state())
	default:
		b.sendReply(ctx, "usage: /quiet on|off")
	}
}
