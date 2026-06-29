package shell3

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/runs"
)

// TestRenderNotification_AgentDone verifies the agent_done branch of the LIVE
// renderNotification renders a short POINTER (id, status, preview, transcript
// path) and never inlines the transcript — the preview is the result to act on,
// and the transcript carries the schema-aware extraction one-liner for when it
// isn't enough.
func TestRenderNotification_AgentDone(t *testing.T) {
	got := renderNotification(notify.Notification{
		Kind: "agent_done", ID: "explore1", Status: "ok",
		Transcript: ".shell3/agents/explore1.jsonl",
		Preview:    "Found 3 call sites in pkg/foo.",
	})
	for _, want := range []string{
		"explore1", "Found 3 call sites", ".shell3/agents/explore1.jsonl",
		"relay it to the user", // the agent must surface the result, not sit on it
		"jq -rs 'map(select(.kind==\"assistant_message\"))[-1].text'", // exact extraction recipe
	} {
		if !strings.Contains(got, want) {
			t.Errorf("agent_done pointer %q missing %q", got, want)
		}
	}
	// The old wording told the agent it was already done ("act on it directly;
	// you do NOT need to read anything else") — which let it stay silent instead
	// of relaying. Guard against that regressing.
	if strings.Contains(got, "you do NOT need to read anything else") {
		t.Errorf("agent_done pointer still discourages relaying: %q", got)
	}
	// With no preview, the message points straight at the transcript extraction
	// and still tells the agent to relay what it finds.
	noPrev := renderNotification(notify.Notification{
		Kind: "agent_done", ID: "x", Status: "ok",
		Transcript: ".shell3/agents/x.jsonl",
	})
	if !strings.Contains(noPrev, "Read its result from the transcript") || !strings.Contains(noPrev, "relay it to the user") {
		t.Errorf("preview-less agent_done = %q, want transcript-read + relay instruction", noPrev)
	}
	// A status-less notification still renders a sane default.
	if g := renderNotification(notify.Notification{Kind: "agent_done", ID: "x"}); !strings.Contains(g, "subagent x finished (done)") {
		t.Errorf("status-less agent_done = %q, want default 'done' status", g)
	}
}

// readInbox returns the parsed pointer lines of a store's inbox.jsonl (nil if
// the file does not exist yet).
func readInbox(t *testing.T, root string) []runs.Pointer {
	t.Helper()
	f, err := os.Open(filepath.Join(root, "inbox.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []runs.Pointer
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var p runs.Pointer
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			t.Fatalf("inbox line not a pointer: %q (%v)", sc.Text(), err)
		}
		out = append(out, p)
	}
	return out
}

// TestReport_SubagentAppendsOnePointer asserts that a subagent session (one with
// a non-empty ParentSession) appends exactly one well-formed pointer line to the
// project inbox on Close, and a root session (no parent) appends nothing. This is
// the entire completion path after the socket/revive removal.
func TestReport_SubagentAppendsOnePointer(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	// Subagent: ParentSession set → exactly one pointer on Close.
	rt := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "all green"}}}))
	sub, err := rt.Session(SessionOpts{Name: "sub", WorkDir: t.TempDir(), ParentSession: "parent-123"})
	if err != nil {
		t.Fatal(err)
	}
	for range sub.Send(context.Background(), "do the thing") {
	}
	subID := sub.sess.ID()
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}

	ptrs := readInbox(t, root)
	if len(ptrs) != 1 {
		t.Fatalf("subagent Close: got %d inbox pointers, want exactly 1", len(ptrs))
	}
	p := ptrs[0]
	if p.RunID != subID {
		t.Errorf("pointer RunID = %q, want subagent id %q", p.RunID, subID)
	}
	if p.Kind != notify.KindAgentDone {
		t.Errorf("pointer Kind = %q, want %q", p.Kind, notify.KindAgentDone)
	}
	if p.Summary != "all green" {
		t.Errorf("pointer Summary = %q, want the assistant preview", p.Summary)
	}
	if p.TS == "" {
		t.Error("pointer TS is empty, want an RFC3339 timestamp")
	}

	// Root: no ParentSession → no pointer appended.
	rtR := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}}))
	rootSess, err := rtR.Session(SessionOpts{Name: "root", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for range rootSess.Send(context.Background(), "hi") {
	}
	if err := rootSess.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(readInbox(t, root)); got != 1 {
		t.Fatalf("root Close appended a pointer: inbox now has %d, want still 1", got)
	}
}

// TestInboxWatcher_DeliversPointerToLiveSession is the end-to-end completion
// path: a pointer appended to the project inbox is tailed by the runtime's
// watcher and injected as a short notification that Wakes an idle session, then
// surfaces as a system-reminder on the next turn. The Phase-6 analogue of the
// retired socket E2E.
func TestInboxWatcher_DeliversPointerToLiveSession(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	rt := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}}))
	// Wire the watcher onto the test runtime (newTestRuntime doesn't run NewRuntime).
	rt.store = st
	go func() { _ = st.Watch(rt.ctx, rt.injectPointer) }()

	s, err := rt.Session(SessionOpts{Name: "live", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	exit := 0
	if err := st.AppendInbox(runs.Pointer{
		TS: time.Now().UTC().Format(time.RFC3339), RunID: "bg_9c", Kind: notify.KindBgDone,
		Path: "/tmp/x.log", Summary: "npx tsc",
	}); err != nil {
		t.Fatal(err)
	}
	_ = exit

	// Idle session: the watcher must Wake it.
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "live" {
			t.Fatalf("got %+v, want Wake for live", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inbox watcher did not Wake the session")
	}

	// The pointer was Interjected into the inbox; the next turn drains it as a
	// system-reminder naming the job.
	var reminder string
	for ev := range s.Send(context.Background(), "next") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "bg_9c") {
			reminder = ev.Text
		}
	}
	if reminder == "" {
		t.Fatal("bg_done pointer not delivered to inbox")
	}
}
