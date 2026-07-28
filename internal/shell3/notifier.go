package shell3

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// hasActiveTriage reports whether any notifier triage turn is in flight —
// drainParkedClosers must keep old-generation Parts alive until they finish.
func (m *jobManager) hasActiveTriage() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.triages > 0
}

// The notifier is the background-completion triage funnel: every completion
// (bash_bg, subagent, lingering follow-up, cron) that was not started with
// direct:true runs one small headless turn of the reserved "notifier" persona
// (notifier.md), whose verdict is a tool call — send (post to the user), wake
// (hand the result to the main agent), or neither (stay silent). Hard rules
// live HERE, not in the persona prompt: failures always surface, at most one
// wake per completion, a timed-out/errored triage degrades to a raw post, and
// with no notifier.md configured every completion posts raw.

// notifierTurnTimeout bounds one triage turn so a hung provider call can never
// clog job-runtime slots; on expiry the completion degrades to the raw-post
// floor.
const notifierTurnTimeout = 60 * time.Second

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

// CompletionEvent is one finished background task, as handed to the notifier
// (or to the raw-post fallbacks when triage is unavailable).
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

	// notice is the pre-built raw notification for the non-triage paths:
	// host-nil owner delivery, degraded (no notifier.md) posts, and the
	// notifier-errored floor.
	notice notify.Notification
	// owner is the owning root session handle (nil when gone). Used only by
	// the host-nil fallback; host-mediated delivery goes through
	// CompletionHost.WakeOwner so the front-end can check liveness under its
	// own lock.
	owner *Session
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

// CompletionHost is the front-end delivery surface a Runtime host plugs in via
// SetCompletionHost. All methods may be called from job-runtime goroutines.
type CompletionHost interface {
	// PostCompletion posts text to the user. cronJob != "" marks a cron
	// origin (the host prefixes "⏰ <cronJob>:"), otherwise "🔔". ownerID,
	// when it names a live threaded session, lets the host thread+anchor the
	// post; "" (or an unknown id) posts standalone.
	PostCompletion(cronJob, ownerID, text string)
	// WakeOwner delivers note into the owning session (queue + wake) iff the
	// host still considers ownerID live, returning false when it is gone —
	// the caller then falls back to StartFreshTurn. Hosts implement the
	// liveness check and delivery under their own lock (Session.NotifyText is
	// the delivery primitive).
	WakeOwner(ownerID, note string) bool
	// StartFreshTurn runs a fresh main-agent turn seeded with note (a
	// completion with no live owner — cron, orphans). Implementations must
	// serialize on their single-turn gate and never drop the note.
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

// notifierAvailable reports whether a notifier.md persona is configured (or a
// test forced one).
func (rt *Runtime) notifierAvailable() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.forceNotifier {
		return true
	}
	return rt.parts != nil && rt.parts.HasNotifier()
}

// NotifyText queues a host notice on this session and wakes it if idle — the
// delivery primitive a CompletionHost's WakeOwner uses.
func (s *Session) NotifyText(text string) {
	s.sess.InterjectNotice(text)
	if !s.isBusy() {
		s.wake()
	}
}

// commandEvent builds the notifier event for a finished bash_bg job. owner is
// the root session the event threads at (nil when gone).
func commandEvent(j *bgJob, n notify.Notification, exit int, owner *Session) CompletionEvent {
	e := exit
	ev := CompletionEvent{
		Kind: EvBashBg, JobID: j.id, Title: j.title, Exit: &e,
		Tail: n.Preview, Note: j.note, Detail: j.logPath,
		Elapsed: time.Since(j.startedAt),
		notice:  n,
	}
	if owner != nil {
		ev.owner, ev.OwnerID = owner, owner.ID()
	}
	return ev
}

// subagentEvent builds the notifier event for a finished subagent (task tool
// or cron dispatch). Cron events carry no owner: the pinned cron parent never
// runs turns, so a wake verdict starts a fresh main-agent turn instead.
func subagentEvent(j *bgJob, summary, errText string) CompletionEvent {
	tail, _ := strutil.CutRunes(summary, agentDoneResultCap)
	ev := CompletionEvent{
		Kind: EvSubagent, JobID: j.id, Title: j.title, Agent: j.agent,
		CronJob: j.cronJob, ErrText: errText, Tail: tail, Note: j.note,
		Elapsed: time.Since(j.startedAt),
		notice:  notifyAgentDone(j.id, summary, errText),
	}
	if j.cronJob != "" {
		ev.Kind = EvCron
	} else if j.parent != nil {
		ev.owner, ev.OwnerID = j.parent, j.parent.ID()
	}
	return ev
}

// followUpEvent builds the notifier event for one lingering-subagent follow-up
// turn's result. Same owner rule as subagentEvent.
func followUpEvent(sub *bgJob, n notify.Notification, summary, errText string) CompletionEvent {
	tail, _ := strutil.CutRunes(summary, agentDoneResultCap)
	ev := CompletionEvent{
		Kind: EvFollowUp, JobID: sub.id, Title: sub.title, Agent: sub.agent,
		CronJob: sub.cronJob, ErrText: errText, Tail: tail, Note: sub.note,
		Elapsed: time.Since(sub.startedAt),
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

// dispatchCompletion routes one finished background task: to the notifier for
// triage, or straight to the raw fallbacks when no host/persona is available.
// Called from the finish sites (outside jobManager.mu) for every job not
// started with direct:true.
func (m *jobManager) dispatchCompletion(ev CompletionEvent) {
	if m.rt == nil {
		return // bare unit-test job manager: nothing to deliver to
	}
	m.mu.Lock()
	closing := m.closing
	m.mu.Unlock()
	if closing {
		return // shutdown: cancelled completions are noise
	}
	host := m.rt.completionHost()
	if host == nil {
		// Library fallback (no front-end host): raw notice straight to the
		// owner, waking it — the pre-notifier behavior.
		if ev.owner != nil {
			ev.owner.injectNotification(m.rt, ev.notice)
		}
		return
	}
	if !m.rt.notifierAvailable() {
		// Degraded mode: no notifier.md — post everything raw, zero tokens.
		host.PostCompletion(ev.CronJob, ev.OwnerID, renderNotification(ev.notice))
		return
	}
	m.mu.Lock()
	m.triages++
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer func() {
			m.mu.Lock()
			m.triages--
			m.mu.Unlock()
			m.wg.Done()
			// A parked old-generation Parts teardown may have been waiting on
			// this triage turn.
			m.rt.drainParkedClosers()
		}()
		m.runTriage(host, ev)
	}()
}

// runTriage runs one notifier turn over ev and applies the hard floors.
func (m *jobManager) runTriage(host CompletionHost, ev CompletionEvent) {
	var mu sync.Mutex
	var sent, woke bool

	parentID := ev.OwnerID
	if parentID == "" {
		parentID = "notifier" // any non-empty marker keeps resume-latest away
	}
	child, err := m.rt.Session(SessionOpts{Agent: "notifier", Headless: true, ParentID: parentID})
	if err != nil {
		m.applyTriageFloor(host, ev, false, err)
		return
	}
	_ = child.RegisterHostTool(HostTool{
		Name:        "send",
		Description: "Post a short message to the user about this completion (one or two sentences). Call at most once.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "The message shown to the user, verbatim."},
			},
			"required": []string{"text"},
		},
		Handler: func(_ context.Context, argsJSON string) (string, error) {
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &p); err != nil || strings.TrimSpace(p.Text) == "" {
				return "", fmt.Errorf("send needs a non-empty text")
			}
			mu.Lock()
			already := sent
			sent = true
			mu.Unlock()
			if already {
				return "already sent — at most one send per completion", nil
			}
			host.PostCompletion(ev.CronJob, ev.OwnerID, p.Text)
			return "sent", nil
		},
	})
	_ = child.RegisterHostTool(HostTool{
		Name:        "wake",
		Description: "Hand this completion to the main agent to act on or relay (it wakes with the result and your note). Call at most once; use only when the result demands agent action or a threaded reply.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"note": map[string]any{"type": "string", "description": "Optional context for the agent (why this needs attention)."},
			},
		},
		Handler: func(_ context.Context, argsJSON string) (string, error) {
			var p struct {
				Note string `json:"note"`
			}
			_ = json.Unmarshal([]byte(argsJSON), &p)
			mu.Lock()
			already := woke
			woke = true
			mu.Unlock()
			if already {
				return "already woken — at most one wake per completion", nil
			}
			note := wakeText(ev, p.Note)
			if ev.OwnerID == "" || !host.WakeOwner(ev.OwnerID, note) {
				host.StartFreshTurn(note)
			}
			return "delivered to the agent", nil
		},
	})

	ctx, cancel := context.WithTimeout(m.driverCtx, notifierTurnTimeout)
	defer cancel()
	var runErr error
	for e := range child.Send(ctx, renderCompletionEvent(ev)) {
		if e.Kind == Error && e.Err != nil {
			runErr = e.Err
		}
	}
	if runErr == nil && ctx.Err() != nil {
		runErr = ctx.Err()
	}
	_ = child.Close()
	mu.Lock()
	delivered := sent || woke
	mu.Unlock()
	m.applyTriageFloor(host, ev, delivered, runErr)
}

// applyTriageFloor enforces the deterministic floors after a triage turn:
// a failed job the notifier left silent (or failed to judge) always posts;
// a clean job whose triage errored posts raw; a clean job judged silent stays
// silent. Suppressed entirely while the runtime is closing.
func (m *jobManager) applyTriageFloor(host CompletionHost, ev CompletionEvent, delivered bool, runErr error) {
	m.mu.Lock()
	closing := m.closing
	m.mu.Unlock()
	if closing || delivered {
		return
	}
	switch {
	case ev.Failed():
		host.PostCompletion(ev.CronJob, ev.OwnerID, floorText(ev))
	case runErr != nil:
		host.PostCompletion(ev.CronJob, ev.OwnerID, renderNotification(ev.notice))
	}
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

// wakeText renders the notice delivered to the (woken or fresh) main agent:
// the raw completion notice plus the notifier's note.
func wakeText(ev CompletionEvent, note string) string {
	text := renderNotification(ev.notice)
	if n := strings.TrimSpace(note); n != "" {
		text += "\nNotifier note: " + strutil.NeutralizeReminderTags(strutil.Truncate(n, 500))
	}
	return text
}

// renderCompletionEvent renders the triage turn's input. Untrusted fields
// (output, titles, notes) are defanged so a job can't forge a reminder
// envelope at the notifier.
func renderCompletionEvent(ev CompletionEvent) string {
	var b strings.Builder
	b.WriteString("A background task finished. Decide what, if anything, the user or agent should see.\n\n")
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
		fmt.Fprintf(&b, "note from the requesting agent: %s\n", strutil.NeutralizeReminderTags(strutil.Truncate(n, 500)))
	}
	if t := strings.TrimSpace(ev.Tail); t != "" {
		fmt.Fprintf(&b, "output tail:\n%s\n", strutil.NeutralizeReminderTags(t))
	}
	if ev.Detail != "" {
		fmt.Fprintf(&b, "full output: %s (inspect with the read tool if the tail is not enough)\n", ev.Detail)
	}
	b.WriteString("\nRespond by calling AT MOST one of:\n" +
		"- send {text}: post a short message to the user now.\n" +
		"- wake {note}: hand the result to the main agent to act on or relay.\n" +
		"Or call neither to stay silent (a routine result nobody is waiting on). " +
		"Failures you leave silent are posted automatically, so never send a bare failure restatement.")
	return b.String()
}
