//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/shell3"
)

// BotCommands is the canonical command list, registered with Telegram for the
// "/" autocomplete menu. Kept next to handleCommand so they stay in sync.
func BotCommands() []Command {
	return []Command{
		{"stop", "Stop the current turn"},
		{"run", "Run a scheduled job now: /run <name>"},
		{"status", "Show runtime status"},
		{"jobs", "List running background tasks"},
		{"job", "Show one job's detail: /job <id>"},
		{"cancel", "Cancel a background task: /cancel <id>"},
		{"cron", "List scheduled cron jobs"},
		{"runs", "List stored runs, or replay one: /runs [id]"},
		{"reload", "Reload the config without restarting"},
		{"voice", "Voice replies: /voice off|inbound|always"},
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
	case "/status":
		b.sendMarkdownDoc(ctx, "status.md", render.Status(b.anyLiveSession(), b.rt, b.version))
	case "/jobs":
		// Deterministic, zero-token job control from the phone: lists running
		// subagents and bash_bg commands. Natural-language "what's running?"
		// still works via the agent's task_list tool; this is the direct path.
		if b.jobsList == nil {
			b.sendReply(ctx, "job control not available")
			return
		}
		b.sendMarkdownDoc(ctx, "jobs.md", render.Jobs(b.jobsList()))
	case "/job":
		if b.jobsList == nil {
			b.sendReply(ctx, "job control not available")
			return
		}
		id := strings.TrimSpace(arg)
		if id == "" {
			b.sendReply(ctx, "usage: /job `<id>`")
			return
		}
		info, ok := findJob(b.jobsList(), id)
		if !ok {
			b.sendReply(ctx, fmt.Sprintf("no such job %q", id))
			return
		}
		var transcript string
		if b.jobTranscript != nil {
			transcript = b.jobTranscript(id)
		}
		b.sendMarkdownDoc(ctx, "job-"+id+".md", render.JobDetail(info, transcript))
	case "/cron":
		var lastRuns map[string]time.Time
		if b.cronLastRuns != nil {
			lastRuns = b.cronLastRuns()
		}
		b.sendMarkdownDoc(ctx, "cron.md", render.Cron(b.rt.Cron(), lastRuns))
	case "/runs":
		if b.runsRoot == "" {
			b.sendReply(ctx, "runs not available")
			return
		}
		id := strings.TrimSpace(arg)
		if id == "" {
			out, err := render.RunsList(b.runsRoot, 20)
			if err != nil {
				b.sendReply(ctx, "⚠️ "+err.Error())
				return
			}
			b.sendMarkdownDoc(ctx, "runs.md", out)
			return
		}
		out, err := render.RunReplay(b.runsRoot, id)
		if err != nil {
			b.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		b.sendMarkdownDoc(ctx, "run-"+id+".md", out)
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

// findJob returns the job matching id from jobs, if any.
func findJob(jobs []shell3.JobInfo, id string) (shell3.JobInfo, bool) {
	for _, j := range jobs {
		if j.ID == id {
			return j, true
		}
	}
	return shell3.JobInfo{}, false
}
