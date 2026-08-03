//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// jsonlEvents decodes every complete JSONL line the transport has written.
func jsonlEvents(t *testing.T, out *syncBuffer) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for _, ln := range strings.Split(out.String(), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			continue // torn tail mid-write; complete lines only
		}
		evs = append(evs, m)
	}
	return evs
}

// eventWhere returns the first event matching pred.
func eventWhere(evs []map[string]any, pred func(map[string]any) bool) (map[string]any, bool) {
	for _, e := range evs {
		if pred(e) {
			return e, true
		}
	}
	return nil, false
}

// TestJSONLIntegrationDrivesBotLoop wires the REAL Bot loop over the JSONL
// transport: a message event runs a turn whose reply arrives as a send event
// threaded to the inbound id; replying to that send's id resumes the thread;
// EOF stops Bot.Run.
func TestJSONLIntegrationDrivesBotLoop(t *testing.T) {
	rt, _ := newFakeRuntime(t, "hello from agent")
	pr, pw := io.Pipe()
	out := &syncBuffer{}
	jc := NewJSONLClient(pr, out, ConsoleChatID, t.TempDir())
	b := NewBot(jc, rt, ConsoleChatID, mkThreads(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()

	// Fresh message with a client-chosen id → a send event replying to it,
	// carrying the agent's markdown.
	if _, err := io.WriteString(pw, `{"type":"message","id":"disc-100","text":"hi"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	waitFor(t, func() bool {
		var ok bool
		reply, ok = eventWhere(jsonlEvents(t, out), func(e map[string]any) bool {
			return e["type"] == "send" && e["reply_to_id"] == "disc-100"
		})
		return ok
	})
	if got, _ := reply["text"].(string); !strings.Contains(got, "hello from agent") {
		t.Fatalf("reply text = %q", got)
	}
	agentID, _ := reply["id"].(string)
	if agentID == "" {
		t.Fatal("reply send has no id")
	}

	// Replying to the agent's send id resumes the thread — a second turn runs,
	// no can't-continue notice.
	if _, err := io.WriteString(pw, `{"type":"message","id":"disc-101","reply_to_id":"`+agentID+`","text":"more"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		n := 0
		for _, e := range jsonlEvents(t, out) {
			if e["type"] == "send" {
				if s, _ := e["text"].(string); strings.Contains(s, "hello from agent") {
					n++
				}
			}
		}
		return n >= 2
	})
	for _, e := range jsonlEvents(t, out) {
		if s, _ := e["text"].(string); strings.Contains(s, "can't continue") {
			t.Fatalf("thread continuation hit the can't-continue notice:\n%s", out.String())
		}
	}

	// EOF stops the loop cleanly.
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Bot.Run did not return after EOF")
	}
}

// TestJSONLIntegrationCompletionPosts exercises the completion-post path: a
// cron completion arrives as a ⏰-prefixed send, a plain one as 🔔.
func TestJSONLIntegrationCompletionPosts(t *testing.T) {
	rt, _ := newFakeRuntime(t, "unused")
	out := &syncBuffer{}
	jc := NewJSONLClient(strings.NewReader(""), out, ConsoleChatID, t.TempDir())
	b := NewBot(jc, rt, ConsoleChatID, mkThreads(t))

	b.PostCompletion("nightly", "", "backup complete")
	waitFor(t, func() bool {
		_, ok := eventWhere(jsonlEvents(t, out), func(e map[string]any) bool {
			s, _ := e["text"].(string)
			return e["type"] == "send" && strings.Contains(s, "⏰ nightly: backup complete")
		})
		return ok
	})

	b.PostCompletion("", "", "fetch finished")
	waitFor(t, func() bool {
		_, ok := eventWhere(jsonlEvents(t, out), func(e map[string]any) bool {
			s, _ := e["text"].(string)
			return e["type"] == "send" && strings.Contains(s, "🔔 fetch finished")
		})
		return ok
	})
}

// TestJSONLIntegrationConfirmRoundTrip exercises the approval flow over the
// wire end to end: a hook-style Ask parks its goroutine, the confirm event
// appears on stdout, the front-end answers by writing a callback line to
// stdin, and the running bot loop's callback consumer unblocks the Ask —
// followed by the ack (spinner-stop) and the edit replacing the confirm text.
func TestJSONLIntegrationConfirmRoundTrip(t *testing.T) {
	rt, _ := newFakeRuntime(t, "unused")
	pr, pw := io.Pipe()
	out := &syncBuffer{}
	jc := NewJSONLClient(pr, out, ConsoleChatID, t.TempDir())
	b := NewBot(jc, rt, ConsoleChatID, mkThreads(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()

	// The session Asker calls exactly this under a hook {ask: …} verdict.
	resCh := make(chan bool, 1)
	go func() { resCh <- b.Ask(ctx, "rm -rf ./scratch", "hook wants approval") }()

	// The confirm event reaches the wire with the command, the reason, and
	// distinct yes/no callback data.
	var confirm map[string]any
	waitFor(t, func() bool {
		var ok bool
		confirm, ok = eventWhere(jsonlEvents(t, out), func(e map[string]any) bool {
			return e["type"] == "confirm"
		})
		return ok
	})
	text, _ := confirm["text"].(string)
	if !strings.Contains(text, "rm -rf ./scratch") || !strings.Contains(text, "hook wants approval") {
		t.Fatalf("confirm text = %q, want command and reason", text)
	}
	yes, _ := confirm["yes"].(string)
	no, _ := confirm["no"].(string)
	if yes == "" || no == "" || yes == no {
		t.Fatalf("confirm callback data malformed: yes=%q no=%q", yes, no)
	}

	// The front-end presses Allow: a callback line on stdin.
	if _, err := io.WriteString(pw, `{"type":"callback","id":"press-1","data":"`+yes+`"}`+"\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case allowed := <-resCh:
		if !allowed {
			t.Fatal("Ask returned false after the Allow press")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not unblock from the wire callback")
	}

	// The press is acknowledged and the confirm is edited (buttons gone).
	waitFor(t, func() bool {
		evs := jsonlEvents(t, out)
		_, acked := eventWhere(evs, func(e map[string]any) bool {
			return e["type"] == "ack" && e["callback_id"] == "press-1"
		})
		_, edited := eventWhere(evs, func(e map[string]any) bool {
			s, _ := e["text"].(string)
			return e["type"] == "edit" && e["id"] == confirm["id"] && strings.Contains(s, "allowed")
		})
		return acked && edited
	})

	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Bot.Run did not return after EOF")
	}
}
