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
//   - direct: the raw result posts straight to the user, and the owner gets
//     the notice queued WITHOUT a wake, so the next turn has it for free.
//   - default: mail TO THE AGENT — the owning session is woken with it, or a
//     fresh main-agent session runs it when no owner is live. That turn's
//     reply posts as ✉️ mail; NO_REPLY keeps it silent.

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
	// Direct sends the raw result straight to the user, with no agent turn.
	Direct bool
	// Detached is an aside (/btw): deliver to the user and tell the owning
	// session nothing. Direct still queues a notice; a detached job must
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
	// considers ownerID live, false when it is gone and the caller falls back
	// to StartFreshTurn. Hosts do the liveness check and delivery under their
	// own lock. That turn's reply posts as ✉️ mail unless it is NO_REPLY.
	WakeOwner(ownerID, note string) bool
	// StartFreshTurn runs a fresh main-agent turn over note, for a completion
	// with no live owner. Implementations serialize on their single-turn gate
	// and never drop the note. Quiet, like WakeOwner.
	StartFreshTurn(note string)
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
		Direct:   j.direct,
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
		Direct:   j.direct,
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
		Direct:   sub.direct,
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
	if m.suppressed(ev.JobID) {
		return // killed by superstop: the summary already told everyone
	}
	if m.rt == nil {
		// Bare unit-test manager: no host, no runtime. Keep the direct
		// contract queueing on the owner so nothing is lost.
		if ev.Direct && !ev.Detached && ev.owner != nil {
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
		undelivered = host.PostCompletion(ev.post(floorText(ev))) != nil
		if ev.OwnerID != "" {
			host.WakeOwner(ev.OwnerID, mailText(ev))
		}
		return
	}
	if ev.Direct {
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
	if t := strings.TrimSpace(ev.Tail); t != "" && strutil.IsNoReply(t) {
		if ev.owner != nil {
			ev.owner.injectNoticeNoWake(ev.notice)
		}
		return
	}
	// Default: mail the agent.
	note := mailText(ev)
	if ev.OwnerID == "" || !host.WakeOwner(ev.OwnerID, note) {
		host.StartFreshTurn(note)
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
	b.WriteString("\nYou are now speaking TO THE USER: every word of your reply is posted to " +
		"their chat as an ✉️ update. If they need nothing from this report — a routine " +
		"result nobody is waiting on, or the conversation above shows they already have " +
		"the information — reply with exactly NO_REPLY and nothing else. Never narrate " +
		"staying silent (\"routine tick, no message needed\" would itself be posted).")
	return b.String()
}
