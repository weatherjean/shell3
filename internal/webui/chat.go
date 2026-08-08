//go:build unix

package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// chatRequest is the subset of the AI SDK's POST body we read. The client
// sends the whole visible history; shell3 keeps its own, so only the newest
// user message matters.
type chatRequest struct {
	// ID is the client library's own chat id, which is NOT a stable name for
	// the conversation — assistant-ui mints one per runtime. ThreadID is the
	// browser's, sent explicitly for exactly that reason, and wins when present.
	ID       string        `json:"id"`
	ThreadID string        `json:"threadId"`
	Messages []chatMessage `json:"messages"`
}

// newMessageID names one assistant reply. Unique per turn: a thread's client
// keeps every message it has seen, and a repeated id is a collision it treats
// as a bug — reusing the thread's own name would collide on its second turn.
func newMessageID(thread string) string {
	return "msg-" + thread + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// thread names the conversation this request belongs to.
func (r chatRequest) thread() string {
	if r.ThreadID != "" {
		return r.ThreadID
	}
	return r.ID
}

type chatMessage struct {
	ID    string     `json:"id"`
	Role  string     `json:"role"`
	Parts []chatPart `json:"parts"`
}

type chatPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URL  string `json:"url"`
}

// text joins every text part of a message.
func (m chatMessage) text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == "text" && p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// lastUserMessage returns the newest user message.
func (r chatRequest) lastUserMessage() (chatMessage, bool) {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return r.Messages[i], true
		}
	}
	return chatMessage{}, false
}

// lastUserText returns the newest user message's text.
func (r chatRequest) lastUserText() string {
	msg, ok := r.lastUserMessage()
	if !ok {
		return ""
	}
	return msg.text()
}

// hasFiles reports whether a message carries attachments.
func (m chatMessage) hasFiles() bool {
	for _, p := range m.Parts {
		if p.Type == "file" && p.URL != "" {
			return true
		}
	}
	return false
}

// maxWakeDrains bounds the follow-up turns appended to one response. A job
// finishing during a turn wakes the session, and its note runs as another turn
// whose output belongs in the same reply; a runaway wake loop must not hold the
// HTTP response open forever.
const maxWakeDrains = 8

// handleChat runs one turn and streams it. The response is an SSE body in the
// UI message stream dialect, ending when the turn's event channel closes.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Only the newest message can start a turn. A request whose last message
	// is an assistant message is a client auto-submitting after tool results —
	// meaningful when the BROWSER runs tools, but shell3 runs them server-side
	// within the turn, so there is nothing left to continue. Running anyway
	// would re-execute the previous user message, forever.
	if len(req.Messages) > 0 && req.Messages[len(req.Messages)-1].Role != "user" {
		stream, ok := newStreamWriter(w)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		stream.start(newMessageID(req.thread()))
		stream.finish()
		return
	}

	msg, hasUser := req.lastUserMessage()
	prompt := msg.text()
	if !hasUser || (strings.TrimSpace(prompt) == "" && !msg.hasFiles()) {
		http.Error(w, "empty message", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	messageID := newMessageID(req.thread())

	// One main-agent turn at a time, matching the runtime's own contract. A
	// message that arrives mid-turn is refused rather than queued: the running
	// turn is never steered from here.
	//
	// The turn's context is its own, NOT the request's: a phone that locks its
	// screen kills the connection within seconds, and that must not kill the
	// turn. What keeps a detached turn accountable: /api/stop still cancels
	// it, Status shows the slot taken, and a client that comes back
	// re-attaches to the live stream (attach.go).
	turnCtx, cancel, taken := s.takeTurn(context.Background())
	if !taken {
		stream, ok := newStreamWriter(w)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		stream.start(messageID)
		stream.errorText("A turn is already running. Stop it before sending another message.")
		stream.finish()
		return
	}

	// The turn writes into a broker; this response is merely its first
	// reader. The goroutine owns the turn end to end — it must not touch the
	// ResponseWriter, which dies with the request.
	broker := newTurnBroker()
	s.setAttachable(req.thread(), broker)
	stream := newBrokeredStream(broker)

	go func() {
		// Deferred first so it runs after everything below — a reload the
		// agent queued mid-turn needs the turn slot free before it applies.
		defer s.runPendingReload()
		defer s.clearAttachable(broker)
		defer broker.done() // idempotent backstop under stream.finish
		defer s.releaseTurn()
		defer cancel()
		defer stream.finish()
		stream.start(messageID)

		sess, err := s.sessionFor(req.thread())
		if err != nil {
			stream.errorText("Could not open a session: " + err.Error())
			return
		}
		// Recorded here rather than in sessionFor: a thread exists once
		// something is said in it. Opening Status, which needs a session to
		// read the agent's shape, must not put an empty conversation in the
		// list.
		s.recordThread(req.thread(), sess, previewOf(prompt))
		s.setTurnSession(sess)

		// A slash command acts on the conversation instead of continuing it,
		// and is answered here — it never reaches the model.
		if !msg.hasFiles() && s.runCommand(turnCtx, stream, sess, prompt) {
			return
		}

		// Attachments are stored before the turn so they keep a durable path,
		// then captioned if a describe model is configured. What the model
		// actually receives is decided in sessionParts.
		uploads, notes := s.saveUploads(msg.Parts)
		uploads = s.describeUploads(turnCtx, uploads)
		prompt = promptWithUploads(prompt, uploads, notes)

		usage, errText := pumpUsage(stream,
			sess.SendParts(turnCtx, prompt, sessionParts(uploads)), s.turnNotice)
		s.recordUsage(usage)
		if errText != "" {
			s.alertTurnFailure(req.thread(), errText)
			return
		}

		// A background job that finished during the turn queued a note and
		// woke the session; run those follow-ups into the same reply so the
		// user sees them without sending another message.
		for i := 0; i < maxWakeDrains; i++ {
			if turnCtx.Err() != nil || !sess.HasQueuedInput() {
				return
			}
			usage, errText := pumpUsage(stream, sess.RunQueued(turnCtx), s.turnNotice)
			s.recordUsage(usage)
			if errText != "" {
				s.alertTurnFailure(req.thread(), errText)
				return
			}
		}
	}()

	writeSSEHeaders(w, flusher)
	broker.serve(r.Context(), w, flusher)
}

// takeTurn claims the single turn slot. The returned context is cancelled by
// stopTurn (the /api/stop route) as well as by the request ending.
func (s *Server) takeTurn(parent context.Context) (context.Context, context.CancelFunc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnActive {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(parent)
	s.turnActive = true
	s.cancelTurn = cancel
	return ctx, cancel, true
}

func (s *Server) releaseTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnActive = false
	s.cancelTurn = nil
	s.turnSession = nil
}

// setTurnSession records which session the running turn belongs to. Tracked
// separately from s.recentSess, which any request can move — a Status view opened
// mid-turn must not redirect a later /api/stop at the wrong session's jobs.
func (s *Server) setTurnSession(sess *shell3.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnSession = sess
}

// stopTurn cancels the running turn, after killing the background jobs the
// session started. Reports what happened, in the shape /stop always used.
func (s *Server) stopTurn() string {
	s.mu.Lock()
	cancel := s.cancelTurn
	sess := s.turnSession
	s.mu.Unlock()

	killed := 0
	if sess != nil {
		killed = sess.KillRunningJobs()
	}
	if cancel != nil {
		cancel()
	}
	return shell3.StopReplyText(cancel != nil, killed)
}

// sessionFor resolves the session backing a browser thread: the live one if we
// still hold it, the recorded one resumed from disk if not, and a fresh
// session for a thread we have never seen.
//
// Sessions stay live between turns so background jobs keep their parent; the
// oldest idle one is retired once there are more than keepLiveSessions.
func (s *Server) sessionFor(threadID string) (*shell3.Session, error) {
	if threadID == "" {
		threadID = "default"
	}

	s.mu.Lock()
	if sess, ok := s.live[threadID]; ok {
		s.recentSess = sess
		s.mu.Unlock()
		return sess, nil
	}
	s.mu.Unlock()

	opts := shell3.SessionOpts{
		Name:     "web-" + threadID,
		WorkDir:  s.workDir,
		Asker:    s.asks.Ask,
		ResumeID: "",
	}
	if id, ok := s.threads.lookup(threadID); ok {
		opts.ResumeID = id
	}

	sess, err := s.rt.Session(opts)
	if err != nil && opts.ResumeID != "" {
		// The recorded session is gone (janitor swept it, or the store moved).
		// Start a clean one rather than failing the message.
		opts.ResumeID = ""
		sess, err = s.rt.Session(opts)
	}
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.live[threadID] = sess
	s.order = append(s.order, threadID)
	s.recentSess = sess
	victims := s.retireOverflowLocked()
	s.mu.Unlock()

	for _, victim := range victims {
		_ = victim.Close()
	}
	return sess, nil
}

// recordThread maps a browser thread to its session, naming it by what was
// first asked. A session with no id has no durable runs dir to resume from, so
// recording it would only put junk in the index.
func (s *Server) recordThread(threadID string, sess *shell3.Session, preview string) {
	if threadID == "" {
		threadID = "default"
	}
	if id := sess.ID(); id != "" {
		s.threads.record(threadID, id, preview)
	}
}

// previewOf shortens a prompt into something that names a conversation. Long
// enough to recognise, short enough for a sidebar row.
func previewOf(prompt string) string {
	fields := strings.Fields(prompt)
	joined := strings.Join(fields, " ")
	const max = 60
	if len(joined) <= max {
		return joined
	}
	// Cut at a word boundary so the ellipsis follows a whole word.
	cut := joined[:max]
	if space := strings.LastIndex(cut, " "); space > max/2 {
		cut = cut[:space]
	}
	return cut + "…"
}

// keepLiveSessions caps how many threads stay resident. Older threads are
// resumed from disk on their next message.
const keepLiveSessions = 8

// retireOverflowLocked picks idle sessions to close, oldest first, and returns
// them for the caller to close outside the lock. Never retires the session a
// turn is running in, nor one with running jobs — those jobs would lose their
// parent, and a completion needs a live owner to wake. Caller holds s.mu.
func (s *Server) retireOverflowLocked() []*shell3.Session {
	var victims []*shell3.Session
	var kept []string

	over := len(s.order) - keepLiveSessions
	for _, threadID := range s.order {
		sess, ok := s.live[threadID]
		if !ok {
			continue
		}
		if over > 0 && sess != s.recentSess && sess != s.turnSession && !hasRunningJobs(sess) {
			delete(s.live, threadID)
			victims = append(victims, sess)
			over--
			continue
		}
		kept = append(kept, threadID)
	}
	s.order = kept
	return victims
}

// hasRunningJobs reports whether this session in particular still owns work.
// Session.Jobs() lists the whole job runtime, so the parent has to be matched
// explicitly — JobInfo.ParentID carries the owning session's registry name.
func hasRunningJobs(sess *shell3.Session) bool {
	for _, job := range sess.Jobs() {
		if job.ParentID != sess.Name() {
			continue
		}
		if !job.Done || job.ChildOpen {
			return true
		}
	}
	return false
}
