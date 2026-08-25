package shell3

import (
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/strutil"
)

// isClosing reports whether cancelAll has begun runtime teardown.
func (m *jobManager) isClosing() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closing
}

// Completion delivery is mail: every finished background task becomes a
// CompletionEvent and routes deterministically — no triage turn, no judge:
//
//   - failed: the ⚠️ floor post always reaches the user, and a live owner is
//     additionally mailed so the agent can react. An ownerless failure (cron)
//     starts NO fresh turn — a broken schedule must not burn a main-model
//     turn per tick, and the floor post IS the delivery.
//   - report:"raw": the raw result posts straight to the user, and the owner
//     gets the notice queued WITHOUT a wake, so the next turn has it for free.
//   - default (report:"auto"): mail TO THE AGENT — the owning session is woken
//     with it, or a fresh main-agent session runs it when no owner is live.
//     That turn's reply posts as ✉️ mail; NO_REPLY keeps it silent.
//   - report:"always": the same mail, but the report turn is BOUND to answer.
//     The mail carries the raw result as Mail.Fallback, and a front-end that
//     ends the turn with nothing posted posts that instead — the spawner said
//     the user is waiting, so silence is not an outcome the model may pick.

// CompletionKind discriminates what finished.
type CompletionKind int

const (
	EvBashBg   CompletionKind = iota // a bash_bg command job
	EvSubagent                       // a task-tool subagent's main turn
	EvFollowUp                       // a lingering subagent's follow-up turn
	EvCron                           // a cron-dispatched subagent
)

// String returns the model-facing kind label.
func (k CompletionKind) String() string {
	switch k {
	case EvBashBg:
		return "bash_bg command"
	case EvSubagent:
		return "subagent"
	case EvFollowUp:
		return "subagent follow-up"
	case EvCron:
		return "cron job"
	}
	return fmt.Sprintf("CompletionKind(%d)", int(k))
}

// CompletionEvent is one finished background task, as routed by
// dispatchCompletion.
type CompletionEvent struct {
	Kind    CompletionKind
	JobID   string // bg3 / sub2
	Title   string // command text or task description
	Agent   string // subagent/cron: the spawned agent's name
	CronJob string // cron: job name ("" otherwise)
	Exit    *int   // bash_bg: exit code
	ErrText string // subagent/cron: run error ("" = clean)
	Tail    string // capped output tail or result summary
	Note    string // spawner-provided intent note ("" = none)
	Detail  string // on-disk path with the full output ("" = none)
	Elapsed time.Duration
	OwnerID string // owning root session's store id ("" when gone/cron)
	RunID   string // subagent/cron: the child session's store id ("" for commands)
	// Report is the single axis for what this finish does to the chat.
	Report notify.ReportMode
	// Detached is an aside (/btw): deliver to the user and tell the owning
	// session nothing. ReportRaw still queues a notice; a detached job must
	// leave no trace, which is the point of asking outside the conversation.
	Detached bool

	// notice is the pre-built raw notification for the direct and host-nil
	// paths.
	notice notify.Notification
	// owner is the owning root session, nil when gone, for the host-nil
	// fallback and the direct no-wake queue. Host-mediated mail goes through
	// WakeOwner instead, so the front-end checks liveness under its own lock.
	owner *Session
}

// post builds the user-facing post for this event carrying its provenance.
func (e CompletionEvent) post(text string) CompletionPost {
	return CompletionPost{
		CronJob: e.CronJob, OwnerID: e.OwnerID,
		JobID: e.JobID, RunID: e.RunID, Text: text,
	}
}

// Failed reports a nonzero exit or run error. Such an event is never silent.
func (e CompletionEvent) Failed() bool {
	return e.ErrText != "" || (e.Exit != nil && *e.Exit != 0)
}

// label names the event in floor posts: the cron job name, or "bg3 (title)".
// A command title collapses to one line — a heredoc script must never dump
// its body into a chat post.
func (e CompletionEvent) label() string {
	if e.CronJob != "" {
		return e.CronJob
	}
	title := strutil.Truncate(strings.Join(strings.Fields(e.Title), " "), 80)
	if title == "" {
		return e.JobID
	}
	return fmt.Sprintf("%s (%s)", e.JobID, title)
}

// traceStatus is the one-word outcome in mailText's summary line, and so in
// the persisted trace: enough to recall later whether a report was a failure.
func (e CompletionEvent) traceStatus() string {
	if e.Failed() {
		return "FAILED"
	}
	return "clean"
}

// CompletionPost is one user-facing post plus its provenance, so the
// front-end can offer a way INTO the work rather than a bare notice.
type CompletionPost struct {
	CronJob string // cron job name ("" for non-cron)
	OwnerID string // owning root session's store id ("" when gone/cron)
	JobID   string // background job id (bg3/sub2; "" when unknown)
	RunID   string // stored child-session id ("" for bash_bg commands)
	Text    string
	// Aside is a /btw answer — a reply, not a job report — so the host
	// renders it plainly rather than as "sub1 (…) finished:".
	Aside bool
}

// CompletionHost is the front-end delivery surface a Runtime host plugs in via
// SetCompletionHost. All methods may be called from job-runtime goroutines.
type CompletionHost interface {
	// PostCompletion posts p.Text and reports whether it reached the
	// transport: a non-nil error means the user did NOT see it, and the
	// router keeps the outbox row for redelivery. The send is synchronous on
	// the job-runtime goroutine — milliseconds normally, seconds of retry in
	// an outage — which never stalls a conversation turn.
	//
	// p.CronJob marks a cron origin (host prefix "⏰ <job>:", else "🔔");
	// p.OwnerID threads the post onto a live session; JobID/RunID let the
	// host link into the job and its stored run.
	PostCompletion(p CompletionPost) error
	// WakeOwner queues and wakes the owning session iff the host still
	// considers m.OwnerID live, false when it is gone and the caller falls back
	// to StartFreshTurn. Hosts do the liveness check and delivery under their
	// own lock. That turn's reply posts as ✉️ mail unless it is NO_REPLY.
	WakeOwner(m Mail) bool
	// StartFreshTurn runs a fresh main-agent turn over the mail, for a
	// completion with no live owner. Implementations serialize on their
	// single-turn gate and never drop it. Quiet, like WakeOwner.
	StartFreshTurn(m Mail)
}

// Mail is one task report on its way to an agent turn. It carries more than
// the note because a report:"always" job binds that turn to answer, and the
// front-end enforcing the bind needs the text to post when the model does not:
// asking the model a second time would spend another turn to re-run the
// judgement that just failed.
type Mail struct {
	// OwnerID is the owning root session's store id ("" for cron/orphans).
	OwnerID string
	// Note is the agent-facing report (mailText).
	Note string
	// Required binds the turn to answer the user (report:"always").
	Required bool
	// Fallback is what the front-end posts when a Required turn says nothing
	// — the same text report:"raw" would have posted. Empty unless Required.
	Fallback string
	// Post carries the event's provenance for that fallback post, so it
	// threads and links like any other completion post.
	Post CompletionPost
}

// mail builds the Mail for this event: the agent-facing report plus, when the
// spawner bound the turn to answer, the raw text to post if it does not.
func (e CompletionEvent) mail() Mail {
	m := Mail{OwnerID: e.OwnerID, Note: mailText(e), Required: e.Report == notify.ReportAlways}
	if m.Required {
		m.Fallback = directText(e)
		m.Post = e.post(m.Fallback)
	}
	return m
}

// CronOutcome is one cron run's REAL result, on its way back to the
// scheduler. The scheduler only ever sees Dispatch's return, which reports
// that the subagent was ACCEPTED — so without this its counters describe the
// dispatch and not the run, and a job that dispatches cleanly every night and
// fails its work every night reads as runs=N fail=0 forever.
type CronOutcome struct {
	Job     string        // cron job name (JobStatus key)
	SubID   string        // the dispatched job id, matching JobStatus.LastSubID
	OK      bool          // the run itself succeeded
	ErrText string        // failure reason ("" when OK)
	Elapsed time.Duration // how long the run actually took
}

// SetCronOutcomeHook installs where finished cron runs report their outcome;
// wireHost points it at Scheduler.RecordOutcome. nil disables reporting (the
// library default, and every front-end without a scheduler). Survives Reload,
// like the completion host — Reload swaps parts, not wiring.
func (rt *Runtime) SetCronOutcomeHook(fn func(CronOutcome)) {
	rt.mu.Lock()
	rt.cronOutcome = fn
	rt.mu.Unlock()
}

func (rt *Runtime) cronOutcomeHook() func(CronOutcome) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.cronOutcome
}

// reportCronOutcome hands a finished cron run's outcome to the scheduler,
// from the one place that sees every terminal state — clean, failed, killed
// by /superstop, or a dead-PID marker recovered at boot.
//
// Called FIRST in dispatchCompletion, before the suppressed and closing
// returns, because bookkeeping is not a chat post: /superstop replaces N
// floor posts with one summary, and that is no reason for the run to vanish
// from its job's history. Delivery is at-least-once, so the same run arrives
// here repeatedly; RecordOutcome dedupes on SubID rather than the router
// trying to guess which pass is the first.
func (m *jobManager) reportCronOutcome(ev CompletionEvent) {
	// Follow-up turns of a lingering cron subagent are the SAME run
	// continuing, not a new one: the run's outcome is its main turn's.
	if ev.CronJob == "" || ev.Kind != EvCron || m.rt == nil {
		return
	}
	// A job cancelAll killed reports at the next boot from its running
	// marker; the "context canceled" failure it carries now is manufactured
	// by the restart and must not count against the job, exactly as its
	// outbox row is dropped rather than redelivered.
	if m.isClosing() && m.shutdownCancelled(ev.JobID) {
		return
	}
	fn := m.rt.cronOutcomeHook()
	if fn == nil {
		return
	}
	o := CronOutcome{Job: ev.CronJob, SubID: ev.JobID, OK: !ev.Failed(), Elapsed: ev.Elapsed}
	if !o.OK {
		o.ErrText = ev.ErrText
		if o.ErrText == "" && ev.Exit != nil {
			o.ErrText = fmt.Sprintf("exit %d", *ev.Exit)
		}
	}
	fn(o)
}

// SetCompletionHost installs the front-end delivery surface. nil keeps the
// library fallback: raw notices straight to the owning session.
func (rt *Runtime) SetCompletionHost(h CompletionHost) {
	rt.mu.Lock()
	rt.completionH = h
	rt.mu.Unlock()
}

func (rt *Runtime) completionHost() CompletionHost {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.completionH
}

// NotifyText queues a host notice and wakes the session if idle — the
// primitive WakeOwner is built on.
func (s *Session) NotifyText(text string) {
	s.sess.InterjectNotice(text)
	if !s.isBusy() {
		s.wake()
	}
}

// NotifyTextNoWake queues a notice WITHOUT waking, so the next turn sees it
// without spending one now.
func (s *Session) NotifyTextNoWake(text string) {
	s.sess.InterjectNotice(text)
}

// commandEvent builds a finished bash_bg job's event; owner is the root
// session it threads at, nil when gone.
func commandEvent(j *bgJob, n notify.Notification, exit int, owner *Session) CompletionEvent {
	e := exit
	ev := CompletionEvent{
		Kind: EvBashBg, JobID: j.id, Title: j.title, Exit: &e,
		Tail: n.Preview, Note: j.note, Detail: j.logPath,
		Elapsed:  time.Since(j.startedAt),
		Report:   j.report,
		Detached: j.detached,
		notice:   n,
	}
	if owner != nil {
		ev.owner, ev.OwnerID = owner, owner.ID()
	}
	return ev
}

// capSummary head-caps an agent-written summary, marking the cut so it never
// ends mid-word unsignalled; the full result stays in task_status.
func capSummary(summary string) string {
	return strutil.Ellipsize(summary, agentDoneResultCap)
}

// subagentEvent builds a finished subagent's event. Cron events carry no
// owner — the pinned parent runs no turns — so mail starts a fresh turn.
func subagentEvent(j *bgJob, summary, errText string) CompletionEvent {
	tail := capSummary(summary)
	ev := CompletionEvent{
		Kind: EvSubagent, JobID: j.id, Title: j.title, Agent: j.agent,
		CronJob: j.cronJob, ErrText: errText, Tail: tail, Note: j.note,
		RunID:    j.childID,
		Elapsed:  time.Since(j.startedAt),
		Report:   j.report,
		Detached: j.detached,
		notice:   notifyAgentDone(j.id, summary, errText),
	}
	if j.cronJob != "" {
		ev.Kind = EvCron
	} else if j.parent != nil {
		ev.owner, ev.OwnerID = j.parent, j.parent.ID()
	}
	return ev
}

// followUpEvent builds one follow-up turn's event. Owner rule as above.
func followUpEvent(sub *bgJob, n notify.Notification, summary, errText string) CompletionEvent {
	tail := capSummary(summary)
	ev := CompletionEvent{
		Kind: EvFollowUp, JobID: sub.id, Title: sub.title, Agent: sub.agent,
		CronJob: sub.cronJob, ErrText: errText, Tail: tail, Note: sub.note,
		RunID:    sub.childID,
		Elapsed:  time.Since(sub.startedAt),
		Report:   sub.report,
		Detached: sub.detached,
		notice:   n,
	}
	if sub.cronJob == "" && sub.parent != nil {
		ev.owner, ev.OwnerID = sub.parent, sub.parent.ID()
	}
	return ev
}

// joinNote concatenates two optional note fragments.
func joinNote(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// dispatchCompletion routes one finished task as the file comment describes.
// Called from the finish sites, outside jobManager.mu, for EVERY job.
func (m *jobManager) dispatchCompletion(ev CompletionEvent) {
	// Before every early return below: a cron run's history is bookkeeping,
	// not delivery, and must not depend on whether the post was wanted.
	m.reportCronOutcome(ev)
	if m.suppressed(ev.JobID) {
		return // killed by superstop: the summary already told everyone
	}
	if m.rt == nil {
		// Bare unit-test manager: no host, no runtime. Keep the direct
		// contract queueing on the owner so nothing is lost.
		if ev.Report == notify.ReportRaw && !ev.Detached && ev.owner != nil {
			ev.owner.injectNoticeNoWake(ev.notice)
		}
		return
	}
	// Persist before routing, and delete only after the hand-off returns:
	// at-least-once, so a crash inside that window duplicates the report at
	// the next boot rather than losing it.
	rowID := m.persistEvent(ev)
	if m.isClosing() {
		// Shutdown. A job cancelAll killed reports at the next boot from its
		// running marker, so its manufactured "context canceled" failure is
		// noise and its event row goes too. A real completion that raced
		// SIGTERM keeps its row and is redelivered.
		if m.shutdownCancelled(ev.JobID) {
			m.deleteOutboxRow(rowID)
		}
		return
	}
	// A rejected post keeps its row for the redelivery pass — the user never
	// saw it. In-process delivery cannot fail this way, so those paths delete.
	undelivered := false
	defer func() {
		if undelivered {
			m.rememberUndelivered(rowID)
			return
		}
		m.deleteOutboxRow(rowID)
	}()
	host := m.rt.completionHost()
	if host == nil {
		// No front-end host (shell3 ask, tests): raw notice straight to the
		// owner, waking it — ask's verbose view sees everything.
		if ev.owner != nil && !ev.Detached {
			ev.owner.injectNotification(m.rt, ev.notice)
		}
		return
	}
	if ev.Detached {
		// Asked outside the conversation, so the answer stops at the user. A
		// failure still surfaces — silence would look like the question was
		// swallowed — but is never mailed, since nothing waits on it.
		text := strings.TrimSpace(ev.Tail)
		if ev.Failed() {
			text = floorText(ev)
		}
		p := ev.post(text)
		p.Aside = !ev.Failed()
		undelivered = host.PostCompletion(p) != nil
		return
	}
	if ev.Failed() {
		// A failure is never silent. A live owner is additionally mailed;
		// an ownerless one stops at the post, no fresh turn per broken tick.
		// A failed ⚠️ post keeps its row even when the mail landed, so
		// redelivery may show the owner the mail twice — at-least-once, and
		// the floor post is what must never be lost.
		// The floor post already told the user, so the mail is never Required
		// here whatever the spawner asked for: binding this turn to speak
		// would only duplicate the ⚠️ they can already see.
		undelivered = host.PostCompletion(ev.post(floorText(ev))) != nil
		if ev.OwnerID != "" {
			ml := ev.mail()
			ml.Required, ml.Fallback, ml.Post = false, "", CompletionPost{}
			host.WakeOwner(ml)
		}
		return
	}
	if ev.Report == notify.ReportRaw {
		// The user is waiting: post the raw result now and queue it unwoken,
		// so the next turn has it for free. The post uses the user-facing
		// rendering — the notice is written FOR the agent ("call
		// task_status") and must not leak into the chat.
		undelivered = host.PostCompletion(ev.post(directText(ev))) != nil
		if ev.owner != nil {
			ev.owner.injectNoticeNoWake(ev.notice)
		}
		return
	}
	// A clean run whose whole result is the sentinel has nothing to judge.
	// Mailing it buys a main-agent turn at full context to read "NO_REPLY"
	// and answer "NO_REPLY" — the dominant cost of a frequent idempotent job.
	//
	// The empty check is load-bearing: IsNoReply("") is true, but an empty
	// ev.Tail means "no output captured", the normal shape of a successful
	// bash_bg, and a build that prints nothing must still wake its owner.
	// report:"always" is exempt: the spawner said the user is waiting, and a
	// job whose own output happens to be the sentinel must not be the thing
	// that decides they are not.
	if t := strings.TrimSpace(ev.Tail); t != "" && strutil.IsNoReply(t) && ev.Report != notify.ReportAlways {
		if ev.owner != nil {
			ev.owner.injectNoticeNoWake(ev.notice)
		}
		return
	}
	// Default: mail the agent.
	ml := ev.mail()
	if ev.OwnerID == "" || !host.WakeOwner(ml) {
		host.StartFreshTurn(ml)
	}
}

// directBodyCap bounds a direct post's result: runes for agent summaries,
// bytes for command tails. Above agentDoneResultCap, so a summary that
// survived capture posts whole; the front-end chunks long messages anyway.
const directBodyCap = 2500

// directText renders a direct completion for the USER's chat: what ran and
// its result, none of the agent-facing instructions. Cron posts get the job
// name from the ⏰ prefix, so they lead with the result.
//
// Truncation direction follows what produced the text: a command's output
// from the END, where the exit lines are, an agent summary from the START,
// where the model leads with the point. Getting this wrong, stacked on the
// head-capped capture, posted a middle window with BOTH ends amputated.
func directText(ev CompletionEvent) string {
	body := directBody(ev)
	if ev.CronJob != "" {
		if body == "" {
			return "done (no output)"
		}
		return body
	}
	head := ev.label() + " finished"
	if body == "" {
		return head + " (no output)"
	}
	return head + ":\n" + body
}

// directBody applies the kind-appropriate cap to the event's result text.
func directBody(ev CompletionEvent) string {
	tail := strings.TrimSpace(ev.Tail)
	if tail == "" {
		return ""
	}
	if ev.Kind == EvBashBg {
		return strutil.Tail(tail, directBodyCap)
	}
	return strutil.Ellipsize(tail, directBodyCap)
}

// floorErrCap bounds the error text carried by a failure-floor post.
const floorErrCap = 1500

// floorText renders the deterministic failure post.
func floorText(ev CompletionEvent) string {
	reason := ev.ErrText
	if reason == "" && ev.Exit != nil {
		reason = fmt.Sprintf("exit %d", *ev.Exit)
	}
	text := fmt.Sprintf("⚠️ %s failed: %s", ev.label(), strutil.Truncate(reason, floorErrCap))
	if tail := strings.TrimSpace(ev.Tail); tail != "" {
		text += "\n" + strutil.Tail(tail, 500)
	}
	return text
}

// mailText renders the mail delivered to the (woken or fresh) main agent.
// Untrusted fields (output, titles, notes) are defanged so a job cannot forge
// a reminder envelope at the agent.
//
// The FIRST line is a self-contained summary naming the job and its outcome:
// chat.reportTrace lifts exactly that line as the durable record of this
// delivery, so it has to identify the report on its own.
func mailText(ev CompletionEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TASK REPORT — %s (%s), system-generated, for you only; the user has NOT seen any of this:\n",
		strutil.NeutralizeReminderTags(strutil.Truncate(ev.label(), 120)), ev.traceStatus())
	fmt.Fprintf(&b, "kind: %s\n", ev.Kind)
	fmt.Fprintf(&b, "job: %s — %s\n", ev.JobID, strutil.NeutralizeReminderTags(strutil.Truncate(ev.Title, 200)))
	if ev.Agent != "" {
		fmt.Fprintf(&b, "agent: %s\n", ev.Agent)
	}
	if ev.CronJob != "" {
		fmt.Fprintf(&b, "cron job: %s\n", ev.CronJob)
	}
	switch {
	case ev.ErrText != "":
		fmt.Fprintf(&b, "status: FAILED — %s\n", strutil.NeutralizeReminderTags(strutil.Truncate(ev.ErrText, 500)))
	case ev.Exit != nil && *ev.Exit != 0:
		fmt.Fprintf(&b, "status: FAILED — exit %d\n", *ev.Exit)
	default:
		b.WriteString("status: clean\n")
	}
	if ev.Elapsed > 0 {
		fmt.Fprintf(&b, "elapsed: %s\n", ev.Elapsed.Round(time.Second))
	}
	if n := strings.TrimSpace(ev.Note); n != "" {
		fmt.Fprintf(&b, "note from the spawner: %s\n", strutil.NeutralizeReminderTags(strutil.Truncate(n, 500)))
	}
	if t := strings.TrimSpace(ev.Tail); t != "" {
		fmt.Fprintf(&b, "output tail:\n%s\n", strutil.NeutralizeReminderTags(t))
	}
	if ev.Detail != "" {
		fmt.Fprintf(&b, "full output: %s\n", ev.Detail)
	}
	b.WriteString("\nThis is your work continuing, not an interruption: you have your tools " +
		"and this turn. If the job was a step in something unfinished — a check you were " +
		"going to run on its output, a file you were going to update, the next step of a " +
		"plan — DO IT NOW rather than waiting to be asked again.\n")
	if ev.Report == notify.ReportAlways {
		b.WriteString("Then answer the user: this job was started with report:\"always\", " +
			"so a reply is REQUIRED. NO_REPLY is not available here — if you send it, the " +
			"raw output above is posted to them in your place, unexplained. Tell them what " +
			"happened yourself.")
		return b.String()
	}
	b.WriteString("Whatever else you do this turn, your final message is posted to the user's " +
		"chat as an ✉️ update. Reply with exactly NO_REPLY, and nothing else, ONLY if there " +
		"is genuinely nothing for them here — a routine tick nobody is waiting on. " +
		"Telling them earlier that a job had STARTED is not the same as telling them how it " +
		"ENDED, and does not make this report redundant: if they asked for this result, or " +
		"you said you would report back, you owe them the answer now. Never narrate staying " +
		"silent (\"routine tick, no message needed\" would itself be posted).")
	return b.String()
}
