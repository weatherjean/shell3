//go:build unix

package web

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// SetJobRunner wires /run to the host's cron scheduler; nil means no jobs.
// Also called by the reload coordinator when a reload adds or removes jobs.
func (d *Driver) SetJobRunner(fn func(name string) error) {
	d.mu.Lock()
	d.runJob = fn
	d.mu.Unlock()
}

// SetReloader wires /reload to the host's reload coordinator. Call before the
// server starts handling requests.
func (d *Driver) SetReloader(fn func() (shell3.ReloadResult, error)) {
	d.mu.Lock()
	d.reload = fn
	d.mu.Unlock()
}

// Command executes one slash command and returns the reply shown as a system
// notice in the web chat. It mirrors telegram.Bot.handleCommand; /help is
// web-only because the browser has no "/" autocomplete menu. Replies are
// ephemeral by design — they are not part of session history.
func (d *Driver) Command(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 { // "/" followed only by whitespace
		return helpText
	}
	cmd := fields[0]
	arg := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), cmd))
	switch cmd {
	case "/help":
		return helpText
	case "/clear":
		if err := d.sess.Clear(); err != nil {
			return "clear failed: " + err.Error()
		}
		return "🧹 cleared"
	case "/compact":
		// One synchronous LLM round-trip; Session.Compact holds the busy gate so
		// an overlapping send is refused, not raced.
		before, after, err := d.sess.Compact(context.Background())
		switch {
		case errors.Is(err, chat.ErrNothingToCompact):
			return "nothing to compact"
		case errors.Is(err, shell3.ErrBusy):
			return "a turn is in flight — try /compact when it finishes"
		case err != nil:
			return "compact failed: " + err.Error()
		}
		return fmt.Sprintf("🗜 compacted: %s → %s tokens",
			strutil.FormatTokens(before), strutil.FormatTokens(after))
	case "/set":
		if arg == "" {
			return d.settableList()
		}
		// Split on any whitespace run (double spaces and tabs are easy to
		// type on mobile), keeping the raw remainder as the value.
		name := strings.Fields(arg)[0]
		value := strings.TrimSpace(arg[strings.Index(arg, name)+len(name):])
		if value == "" {
			return "usage: /set <name> <value>\nsend /set with no arguments to list settable parameters"
		}
		if err := d.sess.SetParam(name, value); err != nil {
			return "set failed: " + err.Error()
		}
		return "⚙️ " + name + " = " + value
	case "/rollback":
		ok, err := d.sess.Rollback()
		if err != nil {
			return "rollback failed: " + err.Error()
		}
		if !ok {
			return "nothing to roll back"
		}
		return "↩️ rolled back"
	case "/stop":
		return d.stopAll()
	case "/run":
		d.mu.Lock()
		run := d.runJob
		d.mu.Unlock()
		if run == nil {
			return "no scheduled jobs configured"
		}
		if arg == "" {
			return "usage: /run <job>"
		}
		if err := run(arg); err != nil {
			return "run failed: " + err.Error()
		}
		return "▶️ fired job " + arg
	case "/reload":
		return d.runReload()
	default:
		return "unknown command: " + cmd + "\n\n" + helpText
	}
}

const helpText = `commands:
/help — this list
/clear — reset the conversation
/compact — summarize old context to free tokens
/stop — stop the current turn and kill background jobs
/set <name> <value> — set a model parameter (bare /set lists them)
/rollback — undo the last turn
/run <job> — fire a scheduled job now
/reload — reload shell3.lua without restarting`

// stopAll cancels the running turn (if any) and kills every live background
// job — commands and subagents alike — mirroring the bot's /stop.
func (d *Driver) stopAll() string {
	killed := 0
	for _, j := range d.sess.Jobs() {
		if !j.Done {
			if err := d.sess.KillJob(j.ID); err == nil {
				killed++
			}
		}
	}
	// Snapshot the cancel func AFTER the kill loop: a turn that ends (and a
	// queued wake turn that starts) while jobs are being killed would leave a
	// pre-loop snapshot cancelling an already-dead context — reporting
	// "stopped" while the fresh turn keeps running.
	d.mu.Lock()
	c := d.cancelTurn
	d.mu.Unlock()
	if c != nil {
		c()
		msg := "⏹ stopped"
		if killed > 0 {
			msg += fmt.Sprintf(" — killed %d background job(s)", killed)
		}
		return msg
	}
	if killed > 0 {
		return fmt.Sprintf("⏹ no turn running — killed %d background job(s)", killed)
	}
	return "nothing running"
}

// runReload performs a config reload while holding the turn slot, so no send
// or wake turn can start mid-swap; Runtime.Reload itself also fail-fasts on a
// busy session. Mirrors telegram.Bot.runReload, including running any input
// queued while the slot was held.
func (d *Driver) runReload() string {
	d.mu.Lock()
	reload := d.reload
	d.mu.Unlock()
	if reload == nil {
		return "reload not available"
	}
	if _, ok := d.takeSlot(); !ok {
		return "a turn is in flight — try /reload again when it finishes"
	}
	res, err := reload()
	d.releaseSlot()
	// A message that arrived during the reload was queued (Interject) against
	// the held slot; run it now rather than stranding it.
	if d.sess.HasQueuedInput() {
		if turnCtx, ok := d.takeSlot(); ok {
			go func() {
				d.drain(d.sess.RunQueued(turnCtx))
				d.releaseSlot()
			}()
		}
	}
	if err != nil {
		return "❌ reload failed: " + err.Error()
	}
	msg := fmt.Sprintf("✅ reloaded — %d agents, %d models, %d jobs", res.Agents, res.Models, res.Jobs)
	if len(res.Notes) > 0 {
		msg += "\n• " + strings.Join(res.Notes, "\n• ")
	}
	return msg
}

// settableList renders the agent's tunable parameters with their current value
// (falling back to the provider default) and allowed values, for a bare /set.
func (d *Driver) settableList() string {
	params := d.sess.Snapshot().Params
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
