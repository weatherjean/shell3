//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strconv"
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
		{"inbox", "Show queued mail (yours and the agent's)"},
		{"jobs", "List running background tasks"},
		{"job", "Show one job's detail: /job <id>"},
		{"cancel", "Cancel a background task: /cancel <id>"},
		{"cron", "List scheduled cron jobs"},
		{"runs", "List runs (tap /run_N to replay): /runs [page|id]"},
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
	// /run_N taps (rendered by the /runs listing) are dynamic commands, routed
	// by prefix before the static switch.
	if n, ok := strings.CutPrefix(cmd, "/run_"); ok {
		b.handleRunTap(ctx, n)
		return
	}
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
	case "/inbox":
		b.sendReply(ctx, b.renderInbox())
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
		a := strings.TrimSpace(arg)
		if page, isPage := pageArg(a); isPage {
			// The page goes out as an inline message, never a document:
			// Telegram only linkifies /run_N commands in message text.
			md, index, total, err := render.RunsPage(b.runsRoot, page, runsPageSize)
			if err != nil {
				b.sendReply(ctx, "⚠️ "+err.Error())
				return
			}
			if md == "" {
				b.sendReply(ctx, fmt.Sprintf("page %d of %d — /runs %d is the last", page, total, total))
				return
			}
			b.mu.Lock()
			b.runIndex = index
			b.mu.Unlock()
			b.sendReply(ctx, md)
			return
		}
		out, err := render.RunReplay(b.runsRoot, a)
		if err != nil {
			b.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		b.sendMarkdownDoc(ctx, "run-"+a+".md", out)
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

// runsPageSize is how many runs one /runs page lists — small enough that the
// page always fits one inline message even with 60-rune prompt snippets.
const runsPageSize = 8

// pageArg reports whether a /runs argument selects a page: "" is page 1 and a
// positive integer is that page. Anything else (run ids contain 'T', '.', '-')
// is not a page and falls through to the replay path.
func pageArg(s string) (int, bool) {
	if s == "" {
		return 1, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// handleRunTap resolves a tapped /run_N against the map the last /runs render
// stored — never against a re-derived listing, so a stale tap (bot restarted,
// listing since re-rendered) errors instead of opening the wrong run.
func (b *Bot) handleRunTap(ctx context.Context, arg string) {
	nStr, _, _ := strings.Cut(arg, "@") // handleCommand cut fields[0] at "@" already; belt and braces
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 1 {
		b.sendReply(ctx, "unknown command: /run_"+nStr)
		return
	}
	b.mu.Lock()
	id, ok := b.runIndex[n]
	b.mu.Unlock()
	if !ok {
		b.sendReply(ctx, "index not found — run /runs again")
		return
	}
	out, err := render.RunReplay(b.runsRoot, id)
	if err != nil {
		b.sendReply(ctx, "⚠️ "+err.Error())
		return
	}
	b.sendMarkdownDoc(ctx, "run-"+id+".md", out)
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
