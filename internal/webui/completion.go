//go:build unix

package webui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// The completion funnel's front-end half. Every finished background job — a
// bash_bg command, a subagent, a cron run — reaches the notifier, which either
// posts to the user or wakes the agent. Those two verdicts land here.
//
// A post becomes a notification in the bell. A wake runs another turn: the
// owning session if it is still live, a fresh one otherwise. Turns triggered
// this way go through the same single-turn gate as the browser's, so a cron
// result can never run concurrently with someone typing.

// notification is one entry in the bell, matching the browser's shape.
type notification struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "note" | "cron" | "alert"
	Title    string `json:"title"`
	Body     string `json:"body"`
	At       string `json:"at"`
	ThreadID string `json:"threadId,omitempty"`
}

// task is one unit of out-of-turn work for the background worker.
type task struct {
	fresh   bool // run note in a new session
	note    string
	session *shell3.Session // drain this session's queued input
}

// PostCompletion surfaces a completion the notifier chose to show the user.
// Never runs a turn.
func (s *Server) PostCompletion(cronJob, ownerID, text string) {
	if strings.TrimSpace(text) == "" {
		text = "(no output)"
	}

	kind, title := "note", "agent"
	switch {
	case cronJob != "":
		kind, title = "cron", cronJob
	case strings.HasPrefix(text, "⚠️"):
		// The runtime's failure floor already marked this one.
		kind, title = "alert", "job failed"
		text = strings.TrimSpace(strings.TrimPrefix(text, "⚠️"))
	}

	s.publishNotification(notification{
		Kind:     kind,
		Title:    title,
		Body:     text,
		ThreadID: s.threadForSession(ownerID),
	})
}

// WakeOwner hands a note to a live session. Returns false when the owner is
// gone, which makes the runtime fall back to StartFreshTurn.
//
// The liveness check and the delivery happen under one lock, paired with
// session retirement: a note can land in a session or that session can retire,
// never both — otherwise a completion would be delivered into a closing
// session and lost.
func (s *Server) WakeOwner(ownerID, note string) bool {
	s.mu.Lock()
	var owner *shell3.Session
	for _, sess := range s.live {
		if sess.ID() == ownerID {
			owner = sess
			break
		}
	}
	if owner == nil {
		s.mu.Unlock()
		return false
	}
	owner.NotifyText(note) // queue + wake; never blocks
	busy := s.turnActive
	s.mu.Unlock()

	// A turn already in flight drains its own queue before responding. Idle
	// sessions need the worker to run the queued note.
	if !busy {
		s.enqueue(task{session: owner})
	}
	return true
}

// StartFreshTurn runs a note with no surviving owner — a cron result, or a job
// whose session has been retired — as a new conversation the user can reply to.
func (s *Server) StartFreshTurn(note string) {
	s.enqueue(task{fresh: true, note: note})
}

// enqueue hands work to the background worker, dropping it (loudly) if the
// queue is full rather than blocking a job-runtime goroutine.
func (s *Server) enqueue(t task) {
	select {
	case s.tasks <- t:
	default:
		s.log.Warn("webui: task queue full, dropped a completion")
		if t.note != "" {
			s.publishNotification(notification{
				Kind: "alert", Title: "dropped", Body: t.note,
			})
		}
	}
}

// turnNotice surfaces a host-level event from inside a turn. Compaction is the
// one that matters: the conversation was summarized and part of it is gone, so
// a later "why did you forget that" has an answer the user can find.
func (s *Server) turnNotice(kind, text string) {
	switch kind {
	case "compacted":
		body := "The conversation was summarized to stay within the context window."
		if text != "" {
			body = text
		}
		s.publishNotification(notification{
			Kind: "note", Title: "context compacted", Body: body,
		})
	case "retry":
		// Transient by nature and often several in a row; logging is enough.
		s.log.Warn("webui: provider retry during a turn", "detail", text)
	}
}

// jobProgress is one write from a running background job, pushed to the
// browser so a job can be watched rather than waited on.
type jobProgress struct {
	JobID   string `json:"jobId"`
	Kind    string `json:"kind"`
	Title   string `json:"title,omitempty"`
	Chunk   string `json:"chunk,omitempty"`
	Done    bool   `json:"done"`
	Summary string `json:"summary,omitempty"`
}

// watchJobs forwards the runtime's job-progress bus onto the event stream.
// The bus drops on a full buffer by design, so a browser that misses a chunk
// simply sees less tail — the job's stored output remains the record.
func (s *Server) watchJobs(ctx context.Context) {
	events := s.rt.JobEvents()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			s.hub.publish("job", jobProgress{
				JobID: ev.JobID, Kind: ev.Kind.String(), Title: ev.Title,
				Chunk: ev.Chunk, Done: ev.Done, Summary: ev.Summary,
			})
		}
	}
}

// runWorker serializes out-of-turn work. One at a time, and never while the
// browser holds the turn slot.
func (s *Server) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-s.tasks:
			s.runTask(ctx, t)
		}
	}
}

// runTask waits for the turn slot, runs the work, and publishes whatever the
// agent said as a notification.
func (s *Server) runTask(ctx context.Context, t task) {
	turnCtx, cancel, ok := s.waitForTurn(ctx)
	if !ok {
		return
	}
	defer s.releaseTurn()
	defer cancel()

	sess := t.session
	threadID := ""
	if t.fresh {
		threadID = "bg-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		var err error
		sess, err = s.sessionFor(threadID)
		if err != nil {
			s.publishNotification(notification{
				Kind: "alert", Title: "session failed", Body: err.Error(),
			})
			return
		}
		// Background work that starts its own conversation is a real thread.
		s.recordThread(threadID, sess, previewOf(t.note))
		sess.NotifyText(t.note)
	} else {
		threadID = s.threadForSession(sess.ID())
	}

	reply := collectReply(sess.RunQueued(turnCtx))
	if strings.TrimSpace(reply) == "" {
		return // the agent had nothing to say; the job's own post already landed
	}
	s.publishNotification(notification{
		Kind:     "note",
		Title:    "agent",
		Body:     strutil.Truncate(reply, 2000),
		ThreadID: threadID,
	})
}

// waitForTurn blocks until the single turn slot is free.
func (s *Server) waitForTurn(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	for {
		if turnCtx, cancel, ok := s.takeTurn(ctx); ok {
			return turnCtx, cancel, true
		}
		select {
		case <-ctx.Done():
			return nil, nil, false
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// collectReply drains a turn and returns its assistant text. The reply is the
// FINAL assistant segment, so text written before a tool call is scaffolding,
// not the answer.
func collectReply(events <-chan shell3.Event) string {
	var segment strings.Builder
	for ev := range events {
		switch ev.Kind {
		case shell3.Token:
			segment.WriteString(ev.Text)
		case shell3.ToolCall:
			segment.Reset()
		case shell3.Error:
			if ev.Err != nil {
				return "⚠️ " + ev.Err.Error()
			}
		}
	}
	return strings.TrimSpace(segment.String())
}

// recentNotifications caps the replay buffer. Enough that a reload shows the
// evening's background work, small enough to stay a footnote in memory.
const recentNotifications = 50

// publishNotification stamps an id, remembers it, and pushes it to every open
// event stream. The buffer is what makes a reload keep the bell's contents:
// a completion that arrived while the tab was closed is still worth seeing.
func (s *Server) publishNotification(n notification) {
	s.mu.Lock()
	if n.ID == "" {
		s.notifySeq++
		n.ID = fmt.Sprintf("n%d-%d", time.Now().Unix(), s.notifySeq)
	}
	if n.At == "" {
		n.At = time.Now().UTC().Format(time.RFC3339)
	}
	s.recent = append(s.recent, n)
	if len(s.recent) > recentNotifications {
		s.recent = s.recent[len(s.recent)-recentNotifications:]
	}
	s.mu.Unlock()

	s.hub.publish("notification", n)
	// Also out to any subscribed browser, so background work reaches a phone
	// with the tab closed.
	s.pushNotification(n)
}

// recentNotices returns the replay buffer, oldest first.
func (s *Server) recentNotices() []notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]notification, len(s.recent))
	copy(out, s.recent)
	return out
}

// threadForSession reverse-maps a session id to the browser thread showing it.
func (s *Server) threadForSession(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for threadID, sess := range s.live {
		if sess.ID() == sessionID {
			return threadID
		}
	}
	return ""
}
