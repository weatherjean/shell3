//go:build unix

package web

import (
	"context"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
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
		// One LLM round-trip, answered synchronously (the web chat has no push
		// channel for a deferred reply). Runs under the turn slot so the Stop
		// button's cancelTurn aborts it, and on the driver's base context so a
		// server shutdown cancels it rather than orphaning a store write.
		turnCtx, ok := d.takeSlot()
		if !ok {
			return shell3.CompactReplyText(0, 0, shell3.ErrBusy)
		}
		before, after, err := d.sess.Compact(turnCtx)
		d.releaseSlot()
		return shell3.CompactReplyText(before, after, err)
	case "/set":
		if arg == "" {
			return shell3.SettableListText(d.sess.Snapshot().Params)
		}
		name, value, ok := shell3.ParseSetArgs(arg)
		if !ok {
			return shell3.SetUsageText
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
/reload — reload the config without restarting`

// stopAll cancels the running turn (if any) and kills every live background
// job — commands and subagents alike — mirroring the bot's /stop.
func (d *Driver) stopAll() string {
	return shell3.StopAll(d.sess, func() context.CancelFunc {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.cancelTurn
	})
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
	return shell3.ReloadReplyText(res, err)
}
