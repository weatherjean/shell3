package shell3

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/sink"
)

func intp(i int) *int { return &i }

// TestSinkWatcher_DeliversBgDonePointer is the end-to-end host-watcher path: a
// producer appends a bg_done line to a session's sink; the watcher tails it,
// injects a short POINTER (not the log contents) into the session inbox, and
// Wakes the idle session. Mirrors the deliverSubagentResult delivery test.
func TestSinkWatcher_DeliversBgDonePointer(t *testing.T) {
	// Speed the poll up for the test (restored after).
	old := sinkPollInterval
	sinkPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { sinkPollInterval = old })

	rt := newTestRuntime(t, fakeCfg("ok"))
	wd := t.TempDir()
	s, err := rt.Session(SessionOpts{Name: "tg:1", WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}

	// The watcher derives the same path the producer writes to.
	if err := sink.Append(s.sinkPath(), sink.Notification{
		Kind: "bg_done", ID: "bg_9c", Exit: intp(0), Log: "/tmp/x.log", Cmd: "npx tsc",
	}); err != nil {
		t.Fatal(err)
	}

	// Idle session: the watcher must Wake it.
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "tg:1" {
			t.Fatalf("got %+v, want Wake for tg:1", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not Wake the session")
	}

	// The pointer was Interjected into the inbox; the next turn drains it as a
	// system-reminder. It must name the log/cmd but NOT inline log contents.
	var reminder string
	for ev := range s.Send(context.Background(), "next") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "bg_9c") {
			reminder = ev.Text
		}
	}
	if reminder == "" {
		t.Fatal("bg_done pointer not delivered to inbox")
	}
	if !strings.Contains(reminder, "/tmp/x.log") || !strings.Contains(reminder, "npx tsc") {
		t.Fatalf("pointer missing log/cmd: %q", reminder)
	}
}

// TestFormatNotification_AgentDone verifies the agent_done branch renders a
// short POINTER (id, status, preview, transcript path) and never inlines the
// transcript — the agent reads it on demand.
func TestFormatNotification_AgentDone(t *testing.T) {
	got := formatNotification(sink.Notification{
		Kind: "agent_done", ID: "explore1", Status: "ok",
		Transcript: ".shell3/agents/explore1.jsonl",
		Preview:    "Found 3 call sites in pkg/foo.",
	})
	for _, want := range []string{"explore1", "(ok)", "Found 3 call sites", ".shell3/agents/explore1.jsonl", "read it for detail"} {
		if !strings.Contains(got, want) {
			t.Errorf("agent_done pointer %q missing %q", got, want)
		}
	}
	// A status-less notification still renders a sane default.
	if g := formatNotification(sink.Notification{Kind: "agent_done", ID: "x"}); !strings.Contains(g, "subagent x finished (done)") {
		t.Errorf("status-less agent_done = %q, want default 'done' status", g)
	}
}

// TestSinkWatcher_DeliversAgentDonePointer is the end-to-end agent_done path: a
// child self-report appends an agent_done line; the watcher injects the pointer
// (transcript path + preview) and Wakes the idle session.
func TestSinkWatcher_DeliversAgentDonePointer(t *testing.T) {
	old := sinkPollInterval
	sinkPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { sinkPollInterval = old })

	rt := newTestRuntime(t, fakeCfg("ok"))
	wd := t.TempDir()
	s, err := rt.Session(SessionOpts{Name: "tg:ad", WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Append(s.sinkPath(), sink.Notification{
		Kind: "agent_done", ID: "explore1", Status: "ok",
		Transcript: ".shell3/agents/explore1.jsonl", Preview: "did the thing",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "tg:ad" {
			t.Fatalf("got %+v, want Wake for tg:ad", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not Wake the session")
	}
	var reminder string
	for ev := range s.Send(context.Background(), "next") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "explore1") {
			reminder = ev.Text
		}
	}
	if reminder == "" {
		t.Fatal("agent_done pointer not delivered to inbox")
	}
	if !strings.Contains(reminder, ".shell3/agents/explore1.jsonl") || !strings.Contains(reminder, "did the thing") {
		t.Fatalf("pointer missing transcript/preview: %q", reminder)
	}
}

// TestSinkWatcher_PartialLineNotConsumed verifies the watcher only advances
// past complete (newline-terminated) lines: a half-written trailing line is not
// decoded until its newline arrives, and the offset discipline means a complete
// line that follows is still delivered exactly once.
func TestSinkWatcher_PartialLineNotConsumed(t *testing.T) {
	old := sinkPollInterval
	sinkPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { sinkPollInterval = old })

	rt := newTestRuntime(t, fakeCfg("ok"))
	wd := t.TempDir()
	s, err := rt.Session(SessionOpts{Name: "tg:2", WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	path := s.sinkPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a complete line followed by a partial (no trailing newline) line.
	partial := `{"kind":"bg_done","id":"bg_complete","exit":0}` + "\n" + `{"kind":"bg_done","id":"bg_partial"`
	if err := os.WriteFile(path, []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}

	// The complete line should Wake; the partial must NOT be delivered.
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake {
			t.Fatalf("got %+v, want Wake", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("complete line not delivered")
	}

	// Drain the first turn (consumes the complete pointer).
	var seen []string
	for ev := range s.Send(context.Background(), "a") {
		if ev.Kind == SystemReminder {
			seen = append(seen, ev.Text)
		}
	}
	if !containsAny(seen, "bg_complete") {
		t.Fatalf("complete line not in inbox: %v", seen)
	}
	if containsAny(seen, "bg_partial") {
		t.Fatalf("partial line was consumed before its newline: %v", seen)
	}

	// Now terminate the partial line; it must be delivered exactly once.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(",\"exit\":0}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	deadline := time.After(2 * time.Second)
	for {
		// Drain the Wake bus so the next Send isn't gated by a stale event.
		select {
		case <-rt.Events():
		case <-deadline:
			t.Fatal("partial line not delivered after newline arrived")
		case <-time.After(20 * time.Millisecond):
		}
		var got []string
		for ev := range s.Send(context.Background(), "b") {
			if ev.Kind == SystemReminder {
				got = append(got, ev.Text)
			}
		}
		if containsAny(got, "bg_partial") {
			if containsAny(got, "bg_complete") {
				t.Fatalf("complete line re-delivered (offset not advanced): %v", got)
			}
			return
		}
	}
}

func containsAny(texts []string, sub string) bool {
	for _, s := range texts {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestSinkWatcher_RemovesFileOnClose verifies Close stops the watcher and
// removes the sink file.
func TestSinkWatcher_RemovesFileOnClose(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	wd := t.TempDir()
	s, err := rt.Session(SessionOpts{Name: "tg:3", WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	path := s.sinkPath()
	if err := sink.Append(path, sink.Notification{Kind: "bg_done", ID: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sink file should exist: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("sink file should be removed on Close, stat err: %v", err)
	}
}
