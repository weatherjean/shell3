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
	"github.com/weatherjean/shell3/internal/strutil"
)

// BotCommands is the canonical command list, registered with Telegram for the
// "/" autocomplete menu. Kept next to handleCommand so they stay in sync.
func BotCommands() []Command {
	return []Command{
		{"stop", "Stop the current turn"},
		{"new", "Start a fresh conversation (the old one stays in /runs)"},
		{"run", "Run a scheduled job now: /run <name>"},
		{"btw", "Ask something outside this conversation: /btw <question>"},
		{"status", "Runtime, jobs, cron and queue (tap /job_N, /cancel_N)"},
		{"job", "Show one job's detail: /job <id>"},
		{"cancel", "Cancel a background task: /cancel <id>"},
		{"runs", "List runs (tap /run_N to replay): /runs [page|id]"},
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
	// /run_N taps (rendered by the /runs listing) are dynamic commands, routed
	// by prefix before the static switch.
	if n, ok := strings.CutPrefix(cmd, "/run_"); ok {
		b.handleRunTap(ctx, n)
		return
	}
	if n, ok := strings.CutPrefix(cmd, "/job_"); ok {
		b.handleJobTap(ctx, n, false)
		return
	}
	if n, ok := strings.CutPrefix(cmd, "/cancel_"); ok {
		b.handleJobTap(ctx, n, true)
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
	case "/status":
		// One view answers "what is going on": runtime, then jobs, cron and
		// the queue. They were four commands and four round trips for four
		// short sections that fit on one screen together.
		status := render.Status(b.anyLiveSession(), b.rt, b.version)
		if b.isQuiet() {
			status += "\n- quiet: on (background posts don't ping)"
		}
		if b.jobsList != nil {
			jobs := b.jobsList()
			b.mu.Lock()
			b.jobIndex = indexJobs(jobs)
			b.mu.Unlock()
			status += "\n\n" + render.JobsTappable(jobs)
		}
		if cron := b.rt.Cron(); len(cron) > 0 {
			var lastRuns map[string]time.Time
			if b.cronLastRuns != nil {
				lastRuns = b.cronLastRuns()
			}
			status += "\n\n" + render.CronBrief(cron, lastRuns)
		}
		if inbox := strings.TrimSpace(b.renderInbox()); inbox != "" && !strings.Contains(inbox, "nothing queued") {
			status += "\n\n## Inbox\n\n" + inbox
		}
		// Always inline: the tappable /job_N and /cancel_N commands only
		// linkify in message text, never inside a document.
		b.sendReply(ctx, status)
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
		b.showJob(ctx, id)
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
		page, err := render.RunReplayHTML(b.runsRoot, a)
		if err != nil {
			b.sendReply(ctx, "⚠️ "+err.Error())
			return
		}
		b.sendRunDoc(ctx, a, page)
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
		b.cancelJobByID(ctx, id)
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
	b.current.SetCurrent("")
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
	// stays inspectable in /runs instead of being torn down undelivered.
	if old != nil && !b.sessionHasRunningJob(old) && !old.HasQueuedInput() {
		_ = old.Close()
	}
	b.sendReply(ctx, "🧵 fresh conversation — the old one stays in /runs and history")
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
	page, err := render.RunReplayHTML(b.runsRoot, id)
	if err != nil {
		b.sendReply(ctx, "⚠️ "+err.Error())
		return
	}
	b.sendRunDoc(ctx, id, page)
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

// indexJobs numbers the jobs exactly as JobsTappable renders them, so a tap
// and the listing that produced it agree.
func indexJobs(jobs []shell3.JobInfo) map[int]string {
	idx := make(map[int]string, len(jobs))
	for i, j := range jobs {
		idx[i+1] = j.ID
	}
	return idx
}

// handleJobTap resolves a tapped /job_N or /cancel_N against the map the last
// /status render stored. Same contract as /run_N: never re-derive the listing,
// because a job finishing in between would shift every number and the tap
// would act on its neighbour.
func (b *Bot) handleJobTap(ctx context.Context, arg string, cancel bool) {
	verb := "/job_"
	if cancel {
		verb = "/cancel_"
	}
	nStr, _, _ := strings.Cut(arg, "@")
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 1 {
		b.sendReply(ctx, "unknown command: "+verb+nStr)
		return
	}
	b.mu.Lock()
	id, ok := b.jobIndex[n]
	b.mu.Unlock()
	if !ok {
		b.sendReply(ctx, "index not found — run /status again")
		return
	}
	if cancel {
		b.cancelJobByID(ctx, id)
		return
	}
	b.showJob(ctx, id)
}

// showJob renders one job, whether reached by /job <id> or a /job_N tap.
func (b *Bot) showJob(ctx context.Context, id string) {
	if b.jobsList == nil {
		b.sendReply(ctx, "job control not available")
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
}

// cancelJobByID cancels one job, whether reached by /cancel <id> or a tap.
func (b *Bot) cancelJobByID(ctx context.Context, id string) {
	if b.cancelJob == nil {
		b.sendReply(ctx, "job control not available")
		return
	}
	if err := b.cancelJob(id); err != nil {
		b.sendReply(ctx, err.Error())
		return
	}
	b.sendReply(ctx, "🛑 cancelled task "+id)
}
