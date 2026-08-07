//go:build unix

package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// gatedClient streams "part one ", signals Started, waits for Release, then
// streams "part two" and finishes. It ignores turn cancellation between the
// halves on purpose: whether "part two" appears is how a test observes whether
// the turn outlived the request that started it.
type gatedClient struct {
	Started chan struct{}
	Release chan struct{}
	once    sync.Once
}

func newGatedClient() *gatedClient {
	return &gatedClient{Started: make(chan struct{}), Release: make(chan struct{})}
}

func (g *gatedClient) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamEvent)) error {
	emit(llm.StreamEvent{TextDelta: "part one "})
	g.once.Do(func() { close(g.Started) })
	<-g.Release
	if ctx.Err() != nil {
		return ctx.Err()
	}
	emit(llm.StreamEvent{TextDelta: "part two"})
	return nil
}

func newGatedServer(t *testing.T) (*Server, *gatedClient) {
	t.Helper()
	dir := t.TempDir()
	client := newGatedClient()
	rt := shell3test.NewRuntimeForTestClient(t, client)
	srv, err := New(Options{Runtime: rt, WorkDir: dir, ConfigDir: dir, Version: "test", StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, client
}

// startTurn posts one message on its own goroutine with a cancellable request
// context and returns the cancel plus a channel closed when the handler
// returns (its recorder is only safe to read after that).
func startTurn(t *testing.T, srv *Server, thread string) (cancelReq context.CancelFunc, handlerDone chan string) {
	t.Helper()
	reqCtx, cancel := context.WithCancel(context.Background())
	handlerDone = make(chan string, 1)
	body := `{"threadId":"` + thread + `","messages":[{"id":"u1","role":"user","parts":[{"type":"text","text":"hi"}]}]}`
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body)).WithContext(reqCtx)
		rec := httptest.NewRecorder()
		srv.handleChat(rec, req)
		handlerDone <- rec.Body.String()
	}()
	return cancel, handlerDone
}

// attach runs the re-attach GET and returns its body once the stream ends.
func attach(t *testing.T, srv *Server, thread string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/chat/stream?thread="+thread, nil)
	rec := httptest.NewRecorder()
	srv.handleChatAttach(rec, req)
	return rec
}

func waitStarted(t *testing.T, ch chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never reached the model")
	}
}

// The heart of reconnect-and-replay: a client that vanishes mid-turn (phone
// locked, tab navigated) must not take the turn with it.
func TestTurnSurvivesClientDisconnect(t *testing.T) {
	srv, client := newGatedServer(t)
	cancelReq, handlerDone := startTurn(t, srv, "t1")

	waitStarted(t, client.Started)
	cancelReq() // the phone locked
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after its client left")
	}

	close(client.Release)
	rec := attach(t, srv, "t1")
	body := rec.Body.String()
	if !strings.Contains(body, "part two") {
		t.Errorf("the turn should have finished after the disconnect; attach saw:\n%s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("attach stream should end with [DONE]:\n%s", body)
	}
}

// Re-attaching mid-turn replays the whole turn so far, then follows it live.
func TestAttachReplaysTheWholeTurn(t *testing.T) {
	srv, client := newGatedServer(t)
	_, handlerDone := startTurn(t, srv, "t1")
	waitStarted(t, client.Started)

	attached := make(chan string, 1)
	go func() {
		rec := attach(t, srv, "t1")
		attached <- rec.Body.String()
	}()
	// Give the attach a moment to subscribe before the turn finishes, so the
	// test exercises replay + live follow rather than replay-after-close.
	time.Sleep(50 * time.Millisecond)
	close(client.Release)

	select {
	case body := <-attached:
		for _, want := range []string{`"type":"start"`, "part one", "part two", `"type":"finish"`, "[DONE]"} {
			if !strings.Contains(body, want) {
				t.Errorf("attach body missing %q:\n%s", want, body)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach never completed")
	}
	<-handlerDone
}

// No running turn — nothing to attach to. 204 is the AI SDK's "already done".
func TestAttachWithoutATurnIsNoContent(t *testing.T) {
	srv := newTestServer(t, "ok")
	if rec := attach(t, srv, "t1"); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

// A finished turn must stop being attachable, or a reload after it would
// replay a stale stream over the freshly loaded transcript.
func TestAttachAfterTheTurnEndsIsNoContent(t *testing.T) {
	srv, client := newGatedServer(t)
	cancelReq, handlerDone := startTurn(t, srv, "t1")
	defer cancelReq()
	waitStarted(t, client.Started)
	close(client.Release)
	<-handlerDone

	if rec := attach(t, srv, "t1"); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

// The stream belongs to its thread: another thread's client sees nothing.
func TestAttachForAnotherThreadIsNoContent(t *testing.T) {
	srv, client := newGatedServer(t)
	cancelReq, handlerDone := startTurn(t, srv, "t1")
	defer cancelReq()
	waitStarted(t, client.Started)

	if rec := attach(t, srv, "t2"); rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}

	close(client.Release)
	<-handlerDone
}

// The original POST subscriber still sees the whole stream when nobody
// disconnects — the common case must stay exactly as it was.
func TestPostStillStreamsTheWholeTurn(t *testing.T) {
	srv, client := newGatedServer(t)
	_, handlerDone := startTurn(t, srv, "t1")
	waitStarted(t, client.Started)
	close(client.Release)

	select {
	case body := <-handlerDone:
		for _, want := range []string{"part one", "part two", "[DONE]"} {
			if !strings.Contains(body, want) {
				t.Errorf("POST body missing %q:\n%s", want, body)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("POST never completed")
	}
}
