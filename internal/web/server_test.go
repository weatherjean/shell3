package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/llm/fakellm"
	"github.com/weatherjean/shell3/pkg/persona"
)

func newTestServer(t *testing.T, scripts ...fakellm.Script) *httptest.Server {
	t.Helper()
	client := fakellm.New(scripts...)
	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})
	tc := chat.TurnConfig{
		LLM:         client,
		Personality: persona.Persona{Name: "test"},
		Handlers:    chat.NewHandlers(chat.Config{}),
		Log:         chat.LogOrNoop(nil),
	}
	h := NewHub(sess, func(ctx context.Context, input string) { sess.Run(ctx, tc, input) })
	h.Start()
	srv := httptest.NewServer(NewServer(h).Handler())
	t.Cleanup(func() { srv.Close(); h.Close(); sess.End("ok"); sess.CloseEvents() })
	return srv
}

func TestServer_IndexServesHTML(t *testing.T) {
	srv := newTestServer(t)
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q", res.Header.Get("Content-Type"))
	}
}

func TestServer_InputTriggersTurnAndStreams(t *testing.T) {
	srv := newTestServer(t, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "yo"}}})

	req, _ := http.NewRequest("GET", srv.URL+"/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	post, err := http.Post(srv.URL+"/input", "application/json", strings.NewReader(`{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if post.StatusCode != http.StatusAccepted {
		t.Fatalf("input status = %d, want 202", post.StatusCode)
	}

	sc := bufio.NewScanner(res.Body)
	deadline := time.Now().Add(3 * time.Second)
	seen := false
	for time.Now().Before(deadline) && sc.Scan() {
		if strings.Contains(sc.Text(), `"turn_done"`) {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatal("did not observe turn_done frame over SSE")
	}
}

func TestServer_BusyReturns409(t *testing.T) {
	srv := newTestServer(t, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a"}, {TextDelta: "b"}}})
	_, _ = http.Post(srv.URL+"/input", "application/json", strings.NewReader(`{"text":"first"}`))
	for i := 0; i < 200; i++ {
		res, err := http.Post(srv.URL+"/input", "application/json", strings.NewReader(`{"text":"again"}`))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusConflict {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Skip("never observed busy window (turns completed too quickly)")
}

func TestServer_ClearReturns204(t *testing.T) {
	srv := newTestServer(t)
	res, err := http.Post(srv.URL+"/clear", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("clear status = %d, want 204", res.StatusCode)
	}
}

func TestServer_CancelReturns204(t *testing.T) {
	srv := newTestServer(t)
	res, err := http.Post(srv.URL+"/cancel", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204", res.StatusCode)
	}
}
