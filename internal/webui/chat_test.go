//go:build unix

package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// newTestServer wires a Server onto a runtime whose model always replies with
// reply, with the thread index in a temp dir.
func newTestServer(t *testing.T, reply string) *Server {
	t.Helper()
	dir := t.TempDir()
	rt := shell3test.NewRuntimeForTest(t, reply)

	srv, err := New(Options{
		Runtime:     rt,
		WorkDir:     dir,
		ConfigDir:   dir,
		Version:     "test",
		ThreadsPath: filepath.Join(dir, "threads.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// postChat sends one message to a thread and returns the raw SSE body.
func postChat(t *testing.T, srv *Server, threadID, text string) string {
	t.Helper()
	return postChatAs(t, srv, chatRequest{ThreadID: threadID}, text)
}

// postChatAs sends one message with the request's ids spelled out, for the
// cases where which id names the conversation is the thing under test.
func postChatAs(t *testing.T, srv *Server, req chatRequest, text string) string {
	t.Helper()
	req.Messages = []chatMessage{
		{ID: "u1", Role: "user", Parts: []chatPart{{Type: "text", Text: text}}},
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, httpReq)
	return rec.Body.String()
}

// replyText concatenates every text delta in an SSE body.
func replyText(t *testing.T, body string) string {
	t.Helper()
	var out strings.Builder
	for _, chunk := range chunks(t, body) {
		if chunk["type"] == "text-delta" {
			out.WriteString(chunk["delta"].(string))
		}
	}
	return out.String()
}

func TestChatStreamsATurn(t *testing.T) {
	srv := newTestServer(t, "hello from the agent")

	body := postChat(t, srv, "t1", "hi")
	if got := replyText(t, body); got != "hello from the agent" {
		t.Errorf("reply = %q, want the model's text", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Error("the stream must terminate with [DONE]")
	}
}

func TestChatRejectsNonPost(t *testing.T) {
	srv := newTestServer(t, "hi")
	rec := httptest.NewRecorder()
	srv.handleChat(rec, httptest.NewRequest(http.MethodGet, "/api/chat", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestChatRejectsEmptyMessage(t *testing.T) {
	srv := newTestServer(t, "hi")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat",
		strings.NewReader(`{"id":"t1","messages":[{"role":"user","parts":[{"type":"text","text":"  "}]}]}`))
	srv.handleChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// A thread must keep its session across messages — that mapping is what makes
// a conversation continue rather than restart.
func TestChatReusesAThreadsSession(t *testing.T) {
	srv := newTestServer(t, "ok")

	postChat(t, srv, "t1", "first")
	first := srv.live["t1"]
	if first == nil {
		t.Fatal("the thread's session should be live after a turn")
	}

	postChat(t, srv, "t1", "second")
	if srv.live["t1"] != first {
		t.Error("a second message on the same thread opened a different session")
	}

	postChat(t, srv, "t2", "other")
	if srv.live["t2"] == first {
		t.Error("a different thread must get its own session")
	}
}

// The client library mints its own chat id per runtime and puts it in `id`,
// so `id` cannot name the conversation: honouring it opened a new session —
// a conversation with no memory of itself — for every single message. The
// browser sends the thread it means as `threadId`, and that is what counts.
func TestChatKeepsAThreadTogetherAcrossClientIDs(t *testing.T) {
	srv := newTestServer(t, "ok")

	postChatAs(t, srv, chatRequest{ID: "__LOCALID_one", ThreadID: "t1"}, "first")
	first := srv.live["t1"]
	if first == nil {
		t.Fatal("the session should be filed under the browser's thread id")
	}

	postChatAs(t, srv, chatRequest{ID: "__LOCALID_two", ThreadID: "t1"}, "second")
	if srv.live["t1"] != first {
		t.Error("a new client-side id started a new session; the conversation restarted")
	}
	if len(srv.live) != 1 {
		t.Errorf("live sessions = %d, want 1: the client id must not name a thread", len(srv.live))
	}
}

// The thread→session mapping must be recorded durably, or a restart loses
// every conversation. A session with no id (no runs dir to resume from) is
// deliberately not recorded, so the index never fills with junk.
func TestChatRecordsThreadMapping(t *testing.T) {
	srv := newTestServer(t, "ok")
	postChat(t, srv, "t1", "hi")

	sess := srv.live["t1"]
	recorded, ok := srv.threads.lookup("t1")

	if sess.ID() == "" {
		if ok {
			t.Errorf("recorded %q for a session with no id", recorded)
		}
		return
	}
	if !ok || recorded != sess.ID() {
		t.Errorf("thread index has %q (found=%v), want the session id %q", recorded, ok, sess.ID())
	}
}

// One main-agent turn at a time: a second concurrent request is refused with
// an in-band error rather than queued behind the first.
func TestChatRefusesConcurrentTurns(t *testing.T) {
	srv := newTestServer(t, "ok")

	// Hold the turn slot as a running turn would.
	_, cancel, taken := srv.takeTurn(t.Context())
	if !taken {
		t.Fatal("the slot should be free")
	}
	defer cancel()

	body := postChat(t, srv, "t1", "hi")
	srv.releaseTurn()

	found := false
	for _, chunk := range chunks(t, body) {
		if chunk["type"] == "error" &&
			strings.Contains(chunk["errorText"].(string), "already running") {
			found = true
		}
	}
	if !found {
		t.Errorf("a concurrent message should be refused in-band, got %q", body)
	}
}

// The slot must be released even when a turn ends, or the interface deadlocks
// after the first message.
func TestChatReleasesTheTurnSlot(t *testing.T) {
	srv := newTestServer(t, "ok")
	postChat(t, srv, "t1", "hi")

	srv.mu.Lock()
	active, sess := srv.turnActive, srv.turnSession
	srv.mu.Unlock()
	if active {
		t.Error("the turn slot is still held after the turn ended")
	}
	if sess != nil {
		t.Error("the turn's session reference should be cleared")
	}
}

func TestStopReportsWhenNoTurnIsRunning(t *testing.T) {
	srv := newTestServer(t, "ok")
	if got := srv.stopTurn(); got == "" {
		t.Error("stopTurn should always report something")
	}
}

// Sessions past the cap retire so a long-lived process does not hold every
// thread it has ever seen; the newest stays resident.
func TestSessionsRetirePastTheCap(t *testing.T) {
	srv := newTestServer(t, "ok")

	for i := range keepLiveSessions + 3 {
		if _, err := srv.sessionFor(threadName(i)); err != nil {
			t.Fatal(err)
		}
	}

	srv.mu.Lock()
	live := len(srv.live)
	srv.mu.Unlock()
	if live > keepLiveSessions {
		t.Errorf("%d sessions live, want at most %d", live, keepLiveSessions)
	}

	newest := threadName(keepLiveSessions + 2)
	srv.mu.Lock()
	_, stillLive := srv.live[newest]
	srv.mu.Unlock()
	if !stillLive {
		t.Error("the most recent thread must stay resident")
	}
}

func threadName(i int) string {
	return "t" + string(rune('a'+i))
}

// Concurrent requests hit the same shared maps; the turn gate must keep them
// safe and let exactly one through at a time.
func TestConcurrentChatRequestsAreSafe(t *testing.T) {
	srv := newTestServer(t, "ok")

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			postChat(t, srv, threadName(i), "hi")
		}()
	}
	wg.Wait()

	srv.mu.Lock()
	active := srv.turnActive
	srv.mu.Unlock()
	if active {
		t.Error("the turn slot leaked after concurrent requests")
	}
}

// A completion the notifier chose to post must reach the browser's event
// stream as a notification.
func TestPostCompletionPublishesANotification(t *testing.T) {
	srv := newTestServer(t, "ok")
	events, cancel := srv.hub.subscribe()
	defer cancel()

	srv.PostCompletion("nightly", "", "3 PRs need review")

	select {
	case ev := <-events:
		if ev.Name != "notification" {
			t.Fatalf("event name = %q, want notification", ev.Name)
		}
		var n notification
		if err := json.Unmarshal(ev.Data, &n); err != nil {
			t.Fatal(err)
		}
		if n.Kind != "cron" || n.Title != "nightly" {
			t.Errorf("notification = %+v, want a cron entry titled nightly", n)
		}
		if n.ID == "" || n.At == "" {
			t.Error("a notification needs an id and a timestamp")
		}
	default:
		t.Fatal("no notification was published")
	}
}

// A failed job carries the runtime's own marker; it must surface as an alert,
// not a routine note.
func TestPostCompletionMarksFailuresAsAlerts(t *testing.T) {
	srv := newTestServer(t, "ok")
	events, cancel := srv.hub.subscribe()
	defer cancel()

	srv.PostCompletion("", "", "⚠️ backup.sh failed: exit 2")

	ev := <-events
	var n notification
	if err := json.Unmarshal(ev.Data, &n); err != nil {
		t.Fatal(err)
	}
	if n.Kind != "alert" {
		t.Errorf("kind = %q, want alert", n.Kind)
	}
	if strings.Contains(n.Body, "⚠️") {
		t.Errorf("body still carries the marker: %q", n.Body)
	}
}

func TestWakeOwnerReportsAMissingSession(t *testing.T) {
	srv := newTestServer(t, "ok")
	if srv.WakeOwner("no-such-session", "a note") {
		t.Error("waking an unknown session must report failure so the runtime falls back")
	}
}

func TestWakeOwnerQueuesForALiveSession(t *testing.T) {
	srv := newTestServer(t, "ok")
	sess, err := srv.sessionFor("t1")
	if err != nil {
		t.Fatal(err)
	}
	if !srv.WakeOwner(sess.ID(), "a job finished") {
		t.Fatal("a live session should accept the note")
	}
	if !sess.HasQueuedInput() {
		t.Error("the note should be queued on the session")
	}
}

func TestCollectReplyKeepsTheFinalSegment(t *testing.T) {
	events := make(chan shell3.Event, 6)
	events <- shell3.Event{Kind: shell3.Token, Text: "let me check"}
	events <- shell3.Event{Kind: shell3.ToolCall, ToolName: "bash"}
	events <- shell3.Event{Kind: shell3.ToolResult, ToolOutput: "done"}
	events <- shell3.Event{Kind: shell3.Token, Text: "all clear"}
	close(events)

	if got := collectReply(events); got != "all clear" {
		t.Errorf("collectReply() = %q, want only the segment after the last tool call", got)
	}
}

func TestCollectReplySurfacesErrors(t *testing.T) {
	events := make(chan shell3.Event, 2)
	events <- shell3.Event{Kind: shell3.Error, Err: errTest}
	close(events)

	if got := collectReply(events); !strings.Contains(got, "boom") {
		t.Errorf("collectReply() = %q, want the error text", got)
	}
}

var errTest = errTestType("boom")

type errTestType string

func (e errTestType) Error() string { return string(e) }

// A client that auto-submits after tool results must not make us replay the
// previous user message. This is the loop that shipped once: shell3 runs tools
// server-side inside a turn, so the AI SDK's "assistant message complete with
// tool results" condition holds at the end of EVERY tool-using turn, and a
// re-submitted request would run the same prompt again, forever.
func TestChatIgnoresContinuationAfterToolResults(t *testing.T) {
	srv := newTestServer(t, "ok")

	// A first, real turn.
	postChat(t, srv, "t1", "do ls")

	// Now the shape a re-submitting client sends: same user message, with the
	// assistant's reply appended — so the LAST message is not the user's.
	body, _ := json.Marshal(chatRequest{
		ID: "t1",
		Messages: []chatMessage{
			{ID: "u1", Role: "user", Parts: []chatPart{{Type: "text", Text: "do ls"}}},
			{ID: "a1", Role: "assistant", Parts: []chatPart{{Type: "text", Text: "ok"}}},
		},
	})
	rec := httptest.NewRecorder()
	srv.handleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat",
		strings.NewReader(string(body))))

	// The response must be a well-formed but empty turn: no text, no rerun.
	for _, chunk := range chunks(t, rec.Body.String()) {
		if chunk["type"] == "text-delta" {
			t.Fatalf("a continuation request ran a turn: %s", rec.Body.String())
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(rec.Body.String()), "data: [DONE]") {
		t.Error("the continuation should still close the stream cleanly")
	}
}

// The guard must not swallow a genuine message that merely arrives with
// history attached — which is every message after the first.
func TestChatRunsWhenHistoryPrecedesTheUserMessage(t *testing.T) {
	srv := newTestServer(t, "second answer")

	body, _ := json.Marshal(chatRequest{
		ID: "t1",
		Messages: []chatMessage{
			{ID: "u1", Role: "user", Parts: []chatPart{{Type: "text", Text: "first"}}},
			{ID: "a1", Role: "assistant", Parts: []chatPart{{Type: "text", Text: "ok"}}},
			{ID: "u2", Role: "user", Parts: []chatPart{{Type: "text", Text: "second"}}},
		},
	})
	rec := httptest.NewRecorder()
	srv.handleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat",
		strings.NewReader(string(body))))

	if got := replyText(t, rec.Body.String()); got != "second answer" {
		t.Errorf("reply = %q, want the turn to have run", got)
	}
}
