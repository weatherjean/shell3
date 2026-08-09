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

// Completion delivery is mail. Every finished background task (bash_bg,
// subagent, lingering follow-up, cron) becomes a CompletionEvent and routes
// deterministically — no triage turn, no judge model:
//
//   - failed: the ⚠️ floor post always reaches the user, and a live owning
//     session is additionally mailed (woken) so the agent can react. An
//     ownerless failure (cron) does NOT start a fresh turn: a broken schedule
//     firing every tick must not burn a main-model turn per tick — the floor
//     post IS the delivery.
//   - direct: the raw result posts straight to the user (the spawner said the
//     user is waiting). The owning session gets the notice queued WITHOUT a
//     wake, so the next turn has it in context without spending one now.
//   - default: the completion is mail TO THE AGENT — the owning session is
//     woken with it, or a fresh main-agent session runs it when no owner is
//     live (cron, orphans). These turns are quiet: reaching the user from one
//     takes the mail_user tool.

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
	// Direct marks a job spawned with direct:true (task/bash_bg arg, cron
	// frontmatter): the raw result goes straight to the user, no agent turn.
	Direct bool

	// notice is the pre-built raw notification for the direct and host-nil
	// delivery paths.
	notice notify.Notification
	// owner is the owning root session handle (nil when gone). Used by the
	// host-nil fallback and the direct no-wake queue; host-mediated agent mail
	// goes through CompletionHost.WakeOwner so the front-end can check
	// liveness under its own lock.
	owner *Session
}

// post builds the user-facing post for this event carrying its provenance.
func (e CompletionEvent) post(text string) CompletionPost {
	return CompletionPost{
		CronJob: e.CronJob, OwnerID: e.OwnerID,
		JobID: e.JobID, RunID: e.RunID, Text: text,
	}
}

// Failed reports whether the underlying job failed (nonzero exit or a run
// error) — such an event may never end silent (hard rule 1).
func (e CompletionEvent) Failed() bool {
	return e.ErrText != "" || (e.Exit != nil && *e.Exit != 0)
}

// label names the event in floor posts: the cron job name, or "bg3 (title)".
func (e CompletionEvent) label() string {
	if e.CronJob != "" {
		return e.CronJob
	}
	title := strutil.Truncate(e.Title, 80)
	if title == "" {
		return e.JobID
	}
	return fmt.Sprintf("%s (%s)", e.JobID, title)
}

// CompletionPost is one user-facing completion post: the text plus where it
// came from, so the front-end can offer a way INTO the work — the job, the
// stored run — instead of a bare notice.
type CompletionPost struct {
	CronJob string // cron job name ("" for non-cron)
	OwnerID string // owning root session's store id ("" when gone/cron)
	JobID   string // background job id (bg3/sub2; "" when unknown)
	RunID   string // stored child-session id ("" for bash_bg commands)
	Text    string
}

// CompletionHost is the front-end delivery surface a Runtime host plugs in via
// SetCompletionHost. All methods may be called from job-runtime goroutines.
type CompletionHost interface {
	// PostCompletion posts p.Text to the user. p.CronJob != "" marks a cron
	// origin (the host prefixes "⏰ <cronJob>:"), otherwise "🔔". p.OwnerID,
	// when it names a live threaded session, lets the host thread+anchor the
	// post; "" (or an unknown id) posts standalone. JobID/RunID, when set,
	// let the host link the post to the job and its stored run.
	PostCompletion(p CompletionPost)
	// WakeOwner delivers mail into the owning session (queue + wake) iff the
	// host still considers ownerID live, returning false when it is gone —
	// the caller then falls back to StartFreshTurn. Hosts implement the
	// liveness check and delivery under their own lock (Session.NotifyText is
	// the delivery primitive). The resulting turn is QUIET — its reply is not
	// posted; the agent reaches the user via mail_user.
	WakeOwner(ownerID, note string) bool
	// StartFreshTurn runs a fresh main-agent turn over note (a completion
	// with no live owner — cron, orphans). Implementations must serialize on
	// their single-turn gate and never drop the note. Quiet, like WakeOwner.
	StartFreshTurn(note string)
}

// SetCompletionHost installs the front-end delivery surface for background
// completions. nil (the default) keeps the library fallback: raw notices are
// delivered straight to the owning session.
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

// NotifyText queues a host notice on this session and wakes it if idle — the
// delivery primitive a CompletionHost's WakeOwner uses.
func (s *Session) NotifyText(text string) {
	s.sess.InterjectNotice(text)
	if !s.isBusy() {
		s.wake()
	}
}

// commandEvent builds the completion event for a finished bash_bg job. owner
// is the root session the event threads at (nil when gone).
func commandEvent(j *bgJob, n notify.Notification, exit int, owner *Session) CompletionEvent {
	e := exit
	ev := CompletionEvent{
		Kind: EvBashBg, JobID: j.id, Title: j.title, Exit: &e,
		Tail: n.Preview, Note: j.note, Detail: j.logPath,
		Elapsed: time.Since(j.startedAt),
		Direct:  j.direct,
		notice:  n,
	}
	if owner != nil {
		ev.owner, ev.OwnerID = owner, owner.ID()
	}
	return ev
}

// subagentEvent builds the completion event for a finished subagent (task tool
// or cron dispatch). Cron events carry no owner: the pinned cron parent never
// runs turns, so agent mail starts a fresh main-agent turn instead.
func subagentEvent(j *bgJob, summary, errText string) CompletionEvent {
	tail, _ := strutil.CutRunes(summary, agentDoneResultCap)
	ev := CompletionEvent{
		Kind: EvSubagent, JobID: j.id, Title: j.title, Agent: j.agent,
		CronJob: j.cronJob, ErrText: errText, Tail: tail, Note: j.note,
		RunID:   j.childID,
		Elapsed: time.Since(j.startedAt),
		Direct:  j.direct,
		notice:  notifyAgentDone(j.id, summary, errText),
	}
	if j.cronJob != "" {
		ev.Kind = EvCron
	} else if j.parent != nil {
		ev.owner, ev.OwnerID = j.parent, j.parent.ID()
	}
	return ev
}

// followUpEvent builds the completion event for one lingering-subagent
// follow-up turn's result. Same owner rule as subagentEvent.
func followUpEvent(sub *bgJob, n notify.Notification, summary, errText string) CompletionEvent {
	tail, _ := strutil.CutRunes(summary, agentDoneResultCap)
	ev := CompletionEvent{
		Kind: EvFollowUp, JobID: sub.id, Title: sub.title, Agent: sub.agent,
		CronJob: sub.cronJob, ErrText: errText, Tail: tail, Note: sub.note,
		RunID:   sub.childID,
		Elapsed: time.Since(sub.startedAt),
		Direct:  sub.direct,
		notice:  n,
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

// dispatchCompletion routes one finished background task deterministically —
// see the package comment at the top of this file. Called from the finish
// sites (outside jobManager.mu) for EVERY completed job, direct included.
func (m *jobManager) dispatchCompletion(ev CompletionEvent) {
	if m.rt == nil {
		// Bare unit-test job manager: no host, no runtime. Keep the direct
		// contract at least queueing on the owner so nothing is lost.
		if ev.Direct && ev.owner != nil {
			ev.owner.injectNoticeNoWake(ev.notice)
		}
		return
	}
	if m.isClosing() {
		return // shutdown: cancelled completions are noise
	}
	host := m.rt.completionHost()
	if host == nil {
		// Library fallback (no front-end host — shell3 ask, tests): raw notice
		// straight to the owner, waking it. ask's verbose view sees everything.
		if ev.owner != nil {
			ev.owner.injectNotification(m.rt, ev.notice)
		}
		return
	}
	if ev.Failed() {
		// Hard floor: a failure is never silent. The owner, if still live, is
		// additionally mailed so the agent can react; an ownerless failure
		// (cron) stops at the post — no fresh turn per broken tick.
		host.PostCompletion(ev.post(floorText(ev)))
		if ev.OwnerID != "" {
			host.WakeOwner(ev.OwnerID, mailText(ev))
		}
		return
	}
	if ev.Direct {
		// The spawner said the user is waiting: post the raw result now, and
		// queue it on the owning session (no wake) so the agent's next turn
		// has it in context without spending one here. The post uses the
		// user-facing rendering — the notice text is written FOR the agent
		// ("relay this", "call task_status") and must not leak into the chat.
		host.PostCompletion(ev.post(directText(ev)))
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

// directText renders a direct completion for the USER's chat: what ran and
// its output tail — none of the agent-facing instructions the queued notice
// carries. Cron posts get the job name from the host's ⏰ prefix, so they
// lead with the result itself.
func directText(ev CompletionEvent) string {
	tail := strings.TrimSpace(ev.Tail)
	if ev.CronJob != "" {
		if tail == "" {
			return "done (no output)"
		}
		return strutil.Tail(tail, 1500)
	}
	head := ev.label() + " finished"
	if tail == "" {
		return head + " (no output)"
	}
	return head + ":\n" + strutil.Tail(tail, 1500)
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
func mailText(ev CompletionEvent) string {
	var b strings.Builder
	b.WriteString("Background mail: a task finished.\n")
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
	b.WriteString("\nThis turn is quiet — its reply is not shown to anyone. " +
		"If the user should hear about this, send it with mail_user; " +
		"a routine result nobody is waiting on needs no mail at all.")
	return b.String()
}
